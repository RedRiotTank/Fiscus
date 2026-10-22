package webhooks

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type capturedRequest struct {
	body      []byte
	event     string
	signature string
	timestamp string
}

func TestDispatcherSignsAndDelivers(t *testing.T) {
	t.Parallel()
	var (
		mu       sync.Mutex
		captured capturedRequest
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		captured = capturedRequest{
			body: body, event: r.Header.Get("X-Fiscus-Event"),
			signature: r.Header.Get("X-Fiscus-Signature"), timestamp: r.Header.Get("X-Fiscus-Timestamp"),
		}
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	dispatcher, err := New([]Config{{
		URL: server.URL, Secret: "top-secret", Events: []string{"transaction.created"},
	}}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	dispatcher.Emit(context.Background(), "transaction.created", map[string]any{"id": int64(7)})
	dispatcher.Wait()

	mu.Lock()
	defer mu.Unlock()
	if captured.event != "transaction.created" {
		t.Fatalf("event header = %q", captured.event)
	}
	wantSignature := "sha256=" + Signature("top-secret", captured.body)
	if captured.signature != wantSignature {
		t.Fatalf("signature = %q, want %q", captured.signature, wantSignature)
	}
	if captured.timestamp == "" {
		t.Fatal("timestamp header is empty")
	}
	var envelope Envelope
	if err := json.Unmarshal(captured.body, &envelope); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if envelope.Event != "transaction.created" || envelope.Timestamp.IsZero() {
		t.Fatalf("envelope = %+v", envelope)
	}
}

func TestDispatcherRetriesTransientFailures(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	dispatcher, err := New([]Config{{URL: server.URL, Events: []string{"sync.completed"}}}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	dispatcher.baseDelay = time.Millisecond
	dispatcher.Emit(context.Background(), "sync.completed", map[string]any{"success": true})
	dispatcher.Wait()
	if got := attempts.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

func TestDispatcherDoesNotRetryClientErrors(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	dispatcher, err := New([]Config{{URL: server.URL, Events: []string{"alert.triggered"}}}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	dispatcher.baseDelay = time.Millisecond
	dispatcher.Emit(context.Background(), "alert.triggered", map[string]any{"id": int64(1)})
	dispatcher.Wait()
	if got := attempts.Load(); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
}

func TestDispatcherFiltersEvents(t *testing.T) {
	t.Parallel()
	var first, second atomic.Int32
	serverOne := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		first.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer serverOne.Close()
	serverTwo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		second.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer serverTwo.Close()

	dispatcher, err := New([]Config{
		{URL: serverOne.URL, Events: []string{"transaction.created"}},
		{URL: serverTwo.URL, Events: []string{"alert.triggered"}},
	}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	dispatcher.Emit(context.Background(), "transaction.created", map[string]any{"id": int64(1)})
	dispatcher.Wait()
	if first.Load() != 1 || second.Load() != 0 {
		t.Fatalf("deliveries = first:%d second:%d", first.Load(), second.Load())
	}
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	t.Parallel()
	tests := []Config{
		{URL: "ftp://example.test", Events: []string{"sync.completed"}},
		{URL: "https://example.test", Events: nil},
		{URL: "https://example.test", Events: []string{" "}},
	}
	for _, cfg := range tests {
		if _, err := New([]Config{cfg}, nil); err == nil {
			t.Fatalf("New(%+v) succeeded", cfg)
		}
	}
}
