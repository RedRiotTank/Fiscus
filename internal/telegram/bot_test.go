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

	"github.com/RedRiotTank/Fiscus/internal/banking/ratelimit"
	"github.com/RedRiotTank/Fiscus/internal/budgets"
	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/storage"
)

type categoryUpdate struct {
	ID       int64
	Category string
}

type noteUpdate struct {
	ID    int64
	Notes string
}

type fakeStore struct {
	accounts     []domain.Account
	transactions []domain.Transaction
	between      []domain.Transaction
	spending     []storage.CategorySpending
	updates      []categoryUpdate
	notes        []noteUpdate
	updateErr    error
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

func (f *fakeStore) ListTransactionsBetween(context.Context, time.Time, time.Time) ([]domain.Transaction, error) {
	return f.between, f.err
}

func (f *fakeStore) SpendingByCategory(context.Context, time.Time, time.Time) ([]storage.CategorySpending, error) {
	return f.spending, f.err
}

func (f *fakeStore) UpdateTransactionCategory(_ context.Context, id int64, category string) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	f.updates = append(f.updates, categoryUpdate{ID: id, Category: category})
	return nil
}

func (f *fakeStore) UpdateTransactionNotes(_ context.Context, id int64, notes string) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	f.notes = append(f.notes, noteUpdate{ID: id, Notes: notes})
	return nil
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
	ChatID      string                `json:"chat_id"`
	Text        string                `json:"text"`
	ReplyMarkup *inlineKeyboardMarkup `json:"reply_markup"`
}

type answeredCallback struct {
	ID   string `json:"callback_query_id"`
	Text string `json:"text"`
}

type sentDocument struct {
	ChatID   string
	Filename string
	Data     []byte
}

type fakeAPI struct {
	mu        sync.Mutex
	updates   []update
	sent      []sentMessage
	answered  []answeredCallback
	documents []sentDocument
	firstPoll chan struct{}
	polls     int
	fileData  []byte
}

type fakeSearcher struct {
	query   string
	limit   int
	results []domain.Transaction
	err     error
}

func (f *fakeSearcher) Search(_ context.Context, query string, limit int) ([]domain.Transaction, error) {
	f.query = query
	f.limit = limit
	return f.results, f.err
}

type fakeIngestor struct {
	filename    string
	contentType string
	caption     string
	data        []byte
	result      domain.Attachment
	err         error
}

func (f *fakeIngestor) SaveTelegram(_ context.Context, filename, contentType, caption string, body io.Reader) (domain.Attachment, error) {
	f.filename = filename
	f.contentType = contentType
	f.caption = caption
	f.data, _ = io.ReadAll(body)
	return f.result, f.err
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
		case strings.HasSuffix(r.URL.Path, "/answerCallbackQuery"):
			var answer answeredCallback
			if err := json.NewDecoder(r.Body).Decode(&answer); err != nil {
				t.Errorf("decode answerCallbackQuery: %v", err)
			}
			api.answered = append(api.answered, answer)
			writeJSON(t, w, map[string]any{"ok": true, "result": true})
		case strings.HasSuffix(r.URL.Path, "/getFile"):
			writeJSON(t, w, map[string]any{"ok": true, "result": map[string]any{
				"file_id": "file-1", "file_path": "receipts/invoice.pdf", "file_size": len(api.fileData),
			}})
		case strings.Contains(r.URL.Path, "/file/bot"):
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write(api.fileData)
		case strings.HasSuffix(r.URL.Path, "/sendDocument"):
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse sendDocument: %v", err)
				writeJSON(t, w, map[string]any{"ok": false})
				return
			}
			file, header, err := r.FormFile("document")
			if err != nil {
				t.Errorf("sendDocument file: %v", err)
				writeJSON(t, w, map[string]any{"ok": false})
				return
			}
			defer func() { _ = file.Close() }()
			data, _ := io.ReadAll(file)
			api.documents = append(api.documents, sentDocument{
				ChatID: r.FormValue("chat_id"), Filename: header.Filename, Data: data,
			})
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
		Categories: []string{"Dining", "Utilities", "Dining", "Uncategorized"},
		Budgets: []budgets.Budget{{
			Category: "Dining", LimitMinor: 2500, Currency: "EUR", NotifyAtPercent: []int{80, 100},
		}},
		Now: func() time.Time { return time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC) },
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

func TestDocumentAttachmentIngestion(t *testing.T) {
	t.Parallel()
	api, server := newFakeAPI(t)
	api.fileData = []byte("pdf-bytes")
	txID := int64(17)
	ingestor := &fakeIngestor{result: domain.Attachment{ID: 3, TransactionID: &txID, Filename: "invoice.pdf"}}
	bot := newTestBot(t, api, server, &fakeStore{}, nil)
	bot.attachments = ingestor
	update := update{
		UpdateID: 9,
		Message: &message{
			MessageID: 11, Caption: "Total 12.34 EUR 2026-03-05", Chat: chat{ID: 42}, From: &user{ID: 7},
			Document: &document{FileID: "file-1", FileName: "invoice.pdf", MimeType: "application/pdf"},
		},
	}
	bot.handleUpdate(context.Background(), update)
	if ingestor.filename != "invoice.pdf" || ingestor.contentType != "application/pdf" ||
		ingestor.caption != "Total 12.34 EUR 2026-03-05" || string(ingestor.data) != "pdf-bytes" {
		t.Fatalf("ingestor = %+v data=%q", ingestor, ingestor.data)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 1 || !strings.Contains(api.sent[0].Text, "vinculado") {
		t.Fatalf("sent = %+v", api.sent)
	}
}

func TestPhotoAttachmentIngestion(t *testing.T) {
	t.Parallel()
	api, server := newFakeAPI(t)
	api.fileData = []byte("jpg-bytes")
	ingestor := &fakeIngestor{result: domain.Attachment{ID: 4}}
	bot := newTestBot(t, api, server, &fakeStore{}, nil)
	bot.attachments = ingestor
	update := update{
		UpdateID: 10,
		Message: &message{
			MessageID: 12, Chat: chat{ID: 42}, From: &user{ID: 7},
			Photo: []photoSize{
				{FileID: "small", Width: 10, Height: 10},
				{FileID: "large", Width: 400, Height: 300},
			},
		},
	}
	bot.handleUpdate(context.Background(), update)
	if ingestor.filename != "telegram-photo-12.jpg" || ingestor.contentType != "image/jpeg" ||
		string(ingestor.data) != "jpg-bytes" {
		t.Fatalf("ingestor = %+v data=%q", ingestor, ingestor.data)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 1 || !strings.Contains(api.sent[0].Text, "bandeja") {
		t.Fatalf("sent = %+v", api.sent)
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

func TestBuscarCommand(t *testing.T) {
	t.Parallel()
	api, server := newFakeAPI(t)
	searcher := &fakeSearcher{results: []domain.Transaction{{
		AccountID: "a1", Counterparty: "Coffee Shop", AmountMinor: -1250, Currency: "EUR",
		BookingDate: time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC), Category: "Dining",
	}}}
	bot := newTestBot(t, api, server, &fakeStore{}, nil)
	bot.search = searcher
	bot.handleUpdate(context.Background(), messageUpdate(42, 7, "/buscar coffee amount:>10"))
	api.mu.Lock()
	defer api.mu.Unlock()
	if searcher.query != "coffee amount:>10" || searcher.limit != 10 {
		t.Fatalf("searcher = %+v", searcher)
	}
	if len(api.sent) != 1 || !strings.Contains(api.sent[0].Text, "Resultados (1)") ||
		!strings.Contains(api.sent[0].Text, "Coffee Shop") {
		t.Fatalf("reply = %+v", api.sent)
	}
}

func TestBuscarRequiresQuery(t *testing.T) {
	t.Parallel()
	api, server := newFakeAPI(t)
	bot := newTestBot(t, api, server, &fakeStore{}, nil)
	bot.search = &fakeSearcher{}
	bot.handleUpdate(context.Background(), messageUpdate(42, 7, "/buscar"))
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 1 || !strings.Contains(api.sent[0].Text, "Uso: /buscar") {
		t.Fatalf("reply = %+v", api.sent)
	}
}

func TestNotaCommandSavesNotes(t *testing.T) {
	t.Parallel()
	api, server := newFakeAPI(t)
	store := &fakeStore{}
	bot := newTestBot(t, api, server, store, nil)
	bot.handleUpdate(context.Background(), messageUpdate(42, 7, "/nota 123 client reimbursed lunch"))
	if len(store.notes) != 1 || store.notes[0].ID != 123 || store.notes[0].Notes != "client reimbursed lunch" {
		t.Fatalf("notes = %+v", store.notes)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 1 || !strings.Contains(api.sent[0].Text, "Nota guardada") {
		t.Fatalf("reply = %+v", api.sent)
	}
}

func TestNotaCommandValidatesInput(t *testing.T) {
	t.Parallel()
	api, server := newFakeAPI(t)
	bot := newTestBot(t, api, server, &fakeStore{}, nil)
	bot.handleUpdate(context.Background(), messageUpdate(42, 7, "/nota nope text"))
	bot.handleUpdate(context.Background(), messageUpdate(42, 7, "/nota 123"))
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 2 || !strings.Contains(api.sent[0].Text, "ID") || !strings.Contains(api.sent[1].Text, "Uso: /nota") {
		t.Fatalf("replies = %+v", api.sent)
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

func TestSyncCommandReportsRateLimitCooldown(t *testing.T) {
	t.Parallel()
	api, server := newFakeAPI(t)
	until := time.Date(2026, 3, 10, 18, 0, 0, 0, time.UTC)
	syncer := &fakeSyncer{err: &ratelimit.Error{
		Reason: ratelimit.CodeASPSPDailyLimit,
		Until:  until,
	}}
	bot := newTestBot(t, api, server, &fakeStore{}, syncer)
	bot.handleUpdate(context.Background(), messageUpdate(42, 7, "/sync"))
	if syncer.calls != 1 {
		t.Fatalf("sync calls = %d, want 1", syncer.calls)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 1 {
		t.Fatalf("sent = %+v", api.sent)
	}
	text := api.sent[0].Text
	if !strings.Contains(text, "Límite de acceso bancario alcanzado (máximo diario PSD2).") ||
		!strings.Contains(text, "Mostrando datos en caché.") ||
		!strings.Contains(text, "Próxima sincronización disponible: 2026-03-10 18:00 UTC.") {
		t.Fatalf("reply = %q", text)
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

func TestNotifyUncategorizedInlineKeyboard(t *testing.T) {
	t.Parallel()
	api, server := newFakeAPI(t)
	store := &fakeStore{}
	bot := newTestBot(t, api, server, store, nil)
	tx := domain.Transaction{
		ID: 9, AccountID: "acc-1", ExternalID: "tx-9", AmountMinor: -1234, Currency: "EUR",
		Counterparty: "Corner Cafe", Description: "Card payment",
		BookingDate: time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC), Category: "Uncategorized",
	}
	if err := bot.NotifyUncategorized(context.Background(), tx); err != nil {
		t.Fatalf("NotifyUncategorized: %v", err)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(api.sent))
	}
	if !strings.Contains(api.sent[0].Text, "Movimiento sin categoría") || !strings.Contains(api.sent[0].Text, "Corner Cafe") {
		t.Fatalf("notification text = %q", api.sent[0].Text)
	}
	markup := api.sent[0].ReplyMarkup
	if markup == nil || len(markup.InlineKeyboard) != 2 {
		t.Fatalf("markup = %+v", markup)
	}
	firstRow := markup.InlineKeyboard[0]
	if len(firstRow) != 2 || firstRow[0].CallbackData != "tx:9:cat:0" || firstRow[1].CallbackData != "tx:9:cat:1" {
		t.Fatalf("category buttons = %+v", firstRow)
	}
	ignore := markup.InlineKeyboard[1]
	if len(ignore) != 1 || ignore[0].Text != "Ignorar" || ignore[0].CallbackData != "tx:9:ignore" {
		t.Fatalf("ignore button = %+v", ignore)
	}
}

func callbackUpdate(chatID, userID int64, data string) update {
	return update{
		UpdateID: 2,
		CallbackQuery: &callbackQuery{
			ID: "callback-1", From: &user{ID: userID}, Data: data,
			Message: &message{MessageID: 2, Chat: chat{ID: chatID}},
		},
	}
}

func TestCallbackCategorizesTransaction(t *testing.T) {
	t.Parallel()
	api, server := newFakeAPI(t)
	store := &fakeStore{}
	bot := newTestBot(t, api, server, store, nil)
	bot.handleUpdate(context.Background(), callbackUpdate(42, 7, "tx:9:cat:1"))
	bot.handleUpdate(context.Background(), callbackUpdate(42, 7, "tx:9:ignore"))

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(store.updates) != 2 {
		t.Fatalf("updates = %+v, want 2", store.updates)
	}
	if store.updates[0] != (categoryUpdate{ID: 9, Category: "Utilities"}) {
		t.Fatalf("first update = %+v", store.updates[0])
	}
	if store.updates[1] != (categoryUpdate{ID: 9, Category: "Ignored"}) {
		t.Fatalf("second update = %+v", store.updates[1])
	}
	if len(api.answered) != 2 || !strings.Contains(api.answered[0].Text, "Utilities") || !strings.Contains(api.answered[1].Text, "ignorado") {
		t.Fatalf("answers = %+v", api.answered)
	}
}

func TestCallbackRejectsInvalidData(t *testing.T) {
	t.Parallel()
	api, server := newFakeAPI(t)
	store := &fakeStore{}
	bot := newTestBot(t, api, server, store, nil)
	bot.handleUpdate(context.Background(), callbackUpdate(42, 7, "tx:9:cat:99"))
	bot.handleUpdate(context.Background(), callbackUpdate(99, 7, "tx:9:ignore"))

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(store.updates) != 0 {
		t.Fatalf("updates = %+v, want none", store.updates)
	}
	if len(api.answered) != 1 || !strings.Contains(api.answered[0].Text, "no válida") {
		t.Fatalf("answers = %+v", api.answered)
	}
}

func TestRunwayCommand(t *testing.T) {
	t.Parallel()
	api, server := newFakeAPI(t)
	store := &fakeStore{
		accounts: []domain.Account{{ID: "a1", Currency: "EUR", BalanceMinor: 300000}},
		between: []domain.Transaction{{
			AccountID: "a1", AmountMinor: -90000, Currency: "EUR",
			BookingDate: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
			Status:      domain.TransactionStatusBooked,
		}},
	}
	bot := newTestBot(t, api, server, store, nil)
	bot.handleUpdate(context.Background(), messageUpdate(42, 7, "/runway"))
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 1 {
		t.Fatalf("sent = %d", len(api.sent))
	}
	text := api.sent[0].Text
	for _, want := range []string{"Autonomía financiera", "liquidez 3,000.00", "consumo mensual 300.00", "10.0 meses"} {
		if !strings.Contains(text, want) {
			t.Errorf("runway missing %q: %q", want, text)
		}
	}
}

func TestPresupuestoCommand(t *testing.T) {
	t.Parallel()
	api, server := newFakeAPI(t)
	store := &fakeStore{spending: []storage.CategorySpending{
		{Category: "Dining", Currency: "EUR", Minor: 2000},
	}}
	bot := newTestBot(t, api, server, store, nil)
	bot.handleUpdate(context.Background(), messageUpdate(42, 7, "/presupuesto"))
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 1 {
		t.Fatalf("sent = %d", len(api.sent))
	}
	text := api.sent[0].Text
	for _, want := range []string{"Presupuesto de March 2026", "Dining: 20.00 de 25.00 EUR (80%)", "Mes transcurrido: 32%"} {
		if !strings.Contains(text, want) {
			t.Errorf("budget missing %q: %q", want, text)
		}
	}
}

func TestSendDocument(t *testing.T) {
	t.Parallel()
	api, server := newFakeAPI(t)
	bot := newTestBot(t, api, server, &fakeStore{}, nil)
	if err := bot.SendDocument(context.Background(), "backup.sqlite.gz", []byte("gzip-data")); err != nil {
		t.Fatalf("SendDocument: %v", err)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.documents) != 1 {
		t.Fatalf("documents = %+v", api.documents)
	}
	got := api.documents[0]
	if got.ChatID != "42" || got.Filename != "backup.sqlite.gz" || string(got.Data) != "gzip-data" {
		t.Fatalf("document = %+v", got)
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
