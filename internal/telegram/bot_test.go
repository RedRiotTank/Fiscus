package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

type fakeStore struct {
	accounts     []domain.Account
	transactions []domain.Transaction
	limit        int
	err          error
}

func (f *fakeStore) ListAccounts(context.Context) ([]domain.Account, error) {
	return f.accounts, f.err
}

func (f *fakeStore) RecentBookedTransactions(_ context.Context, limit int) ([]domain.Transaction, error) {
	f.limit = limit
	if len(f.transactions) > limit {
		return f.transactions[:limit], f.err
	}
	return f.transactions, f.err
}

type fakeSyncer struct {
	err   error
	calls int
}

func (f *fakeSyncer) SyncAll(context.Context) error {
	f.calls++
	return f.err
}

type sentMessage struct {
	ChatID string `json:"chat_id"`
	Text   string `json:"text"`
}

type fakeAPI struct {
	mu        sync.Mutex
	updates   []update
	sent      []sentMessage
	firstPoll chan struct{}
	polls     int
}

func newFakeAPI(t *testing.T) (*fakeAPI, *httptest.Server) {
	t.Helper()
	api := &fakeAPI{firstPoll: make(chan struct{})}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.mu.Lock()
		defer api.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			api.polls++
			if api.polls == 1 {
				close(api.firstPoll)
			}
			writeJSON(t, w, map[string]any{"ok": true, "result": api.updates})
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			var msg sentMessage
			if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
				t.Errorf("decode sendMessage: %v", err)
			}
			api.sent = append(api.sent, msg)
			writeJSON(t, w, map[string]any{"ok": true, "result": map[string]any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return api, server
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func newTestBot(t *testing.T, _ *fakeAPI, server *httptest.Server, store Store, syncer Syncer) *Bot {
	t.Helper()
	bot, err := New(Config{
		BotToken: "test-token", ChatID: "42", UserID: "7", BaseURL: server.URL,
		HTTPClient: server.Client(),
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Store:      store,
		Syncer:     syncer,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	bot.retryDelay = 10 * time.Millisecond
	return bot
}

func messageUpdate(chatID, userID int64, text string) update {
	return update{
		UpdateID: 1,
		Message: &message{
			MessageID: 1, Text: text, Chat: chat{ID: chatID}, From: &user{ID: userID},
		},
	}
}

func TestAuthorizedHelp(t *testing.T) {
	t.Parallel()
	api, server := newFakeAPI(t)
	bot := newTestBot(t, api, server, &fakeStore{}, nil)
	bot.handleUpdate(context.Background(), messageUpdate(42, 7, "/help"))
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(api.sent))
	}
	if api.sent[0].ChatID != "42" || !strings.Contains(api.sent[0].Text, "/saldo") || !strings.Contains(api.sent[0].Text, "/ultimos") {
		t.Fatalf("reply = %+v", api.sent[0])
	}
}

func TestUnauthorizedUpdatesAreIgnored(t *testing.T) {
	t.Parallel()
	api, server := newFakeAPI(t)
	bot := newTestBot(t, api, server, &fakeStore{}, nil)
	bot.handleUpdate(context.Background(), messageUpdate(99, 7, "/help"))
	bot.handleUpdate(context.Background(), messageUpdate(42, 8, "/help"))
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 0 {
		t.Fatalf("sent = %+v, want none", api.sent)
	}
}

func TestSaldoCommand(t *testing.T) {
	t.Parallel()
	api, server := newFakeAPI(t)
	store := &fakeStore{accounts: []domain.Account{
		{ID: "a1", Name: "Everyday", Currency: "EUR", BalanceMinor: 125050},
		{ID: "a2", Name: "Savings", Currency: "EUR", BalanceMinor: 50000},
		{ID: "a3", Name: "Dollars", Currency: "USD", BalanceMinor: 1234},
	}}
	bot := newTestBot(t, api, server, store, nil)
	bot.handleUpdate(context.Background(), messageUpdate(42, 7, "/saldo"))
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 1 {
		t.Fatalf("sent = %d", len(api.sent))
	}
	text := api.sent[0].Text
	for _, want := range []string{"Saldo consolidado", "EUR: 1,750.50", "USD: 12.34", "Everyday", "Savings"} {
		if !strings.Contains(text, want) {
			t.Errorf("reply missing %q: %q", want, text)
		}
	}
}

func TestUltimosCommandUsesConfirmedTransactions(t *testing.T) {
	t.Parallel()
	api, server := newFakeAPI(t)
	store := &fakeStore{transactions: []domain.Transaction{
		{ID: 1, AccountID: "a1", Counterparty: "Streaming", AmountMinor: -999, Currency: "EUR", Category: "Subscriptions", BookingDate: time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC)},
		{ID: 2, AccountID: "a1", Counterparty: "Employer", AmountMinor: 300000, Currency: "EUR", Category: "Payroll", BookingDate: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)},
	}}
	bot := newTestBot(t, api, server, store, nil)
	bot.handleUpdate(context.Background(), messageUpdate(42, 7, "/ultimos 2"))
	api.mu.Lock()
	defer api.mu.Unlock()
	if store.limit != 2 {
		t.Fatalf("limit = %d, want 2", store.limit)
	}
	if len(api.sent) != 1 {
		t.Fatalf("sent = %d", len(api.sent))
	}
	text := api.sent[0].Text
	for _, want := range []string{"Últimos 2 movimientos", "Streaming", "-9.99", "[Subscriptions]", "Employer", "3,000.00", "[Payroll]"} {
		if !strings.Contains(text, want) {
			t.Errorf("reply missing %q: %q", want, text)
		}
	}
}

func TestUltimosRejectsInvalidLimit(t *testing.T) {
	t.Parallel()
	api, server := newFakeAPI(t)
	bot := newTestBot(t, api, server, &fakeStore{}, nil)
	bot.handleUpdate(context.Background(), messageUpdate(42, 7, "/ultimos 500"))
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 1 || !strings.Contains(api.sent[0].Text, "/ultimos [1-50]") {
		t.Fatalf("reply = %+v", api.sent)
	}
}

func TestSyncCommandReportsOutcome(t *testing.T) {
	t.Parallel()
	api, server := newFakeAPI(t)
	syncer := &fakeSyncer{}
	bot := newTestBot(t, api, server, &fakeStore{}, syncer)
	bot.handleUpdate(context.Background(), messageUpdate(42, 7, "/sync"))
	if syncer.calls != 1 {
		t.Fatalf("sync calls = %d, want 1", syncer.calls)
	}
	api.mu.Lock()
	if len(api.sent) != 1 || !strings.Contains(api.sent[0].Text, "completada") {
		t.Fatalf("reply = %+v", api.sent)
	}
	api.mu.Unlock()

	syncer.err = errors.New("bank unavailable")
	bot.handleUpdate(context.Background(), messageUpdate(42, 7, "/sync"))
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 2 || !strings.Contains(api.sent[1].Text, "falló") {
		t.Fatalf("reply = %+v", api.sent)
	}
}

func TestUnknownCommand(t *testing.T) {
	t.Parallel()
	api, server := newFakeAPI(t)
	bot := newTestBot(t, api, server, &fakeStore{}, nil)
	bot.handleUpdate(context.Background(), messageUpdate(42, 7, "/something"))
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 1 || !strings.Contains(api.sent[0].Text, "Instrucción desconocida") {
		t.Fatalf("reply = %+v", api.sent)
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	t.Parallel()
	api, server := newFakeAPI(t)
	bot := newTestBot(t, api, server, &fakeStore{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		bot.Run(ctx)
	}()
	select {
	case <-api.firstPoll:
	case <-time.After(2 * time.Second):
		t.Fatal("bot did not poll")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("bot did not stop after context cancellation")
	}
}
