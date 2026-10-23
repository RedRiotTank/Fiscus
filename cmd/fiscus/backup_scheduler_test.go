package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/config"
	"github.com/RedRiotTank/Fiscus/internal/storage"
)

type fakeDocumentSender struct {
	filename string
	data     []byte
	calls    int
	err      error
}

func (f *fakeDocumentSender) SendDocument(_ context.Context, filename string, data []byte) error {
	f.calls++
	f.filename = filename
	f.data = data
	return f.err
}

func TestWriteAndSendBackup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer func() { _ = store.Close() }()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	cfg := config.Default()
	cfg.Backups.Directory = t.TempDir()
	cfg.Attachments.Directory = t.TempDir()
	if err := os.WriteFile(filepath.Join(cfg.Attachments.Directory, "receipt.pdf"), []byte("receipt"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	sender := &fakeDocumentSender{}
	now := time.Date(2026, 10, 22, 9, 8, 7, 0, time.UTC)
	if err := writeAndSendBackup(ctx, cfg, store, sender, now); err != nil {
		t.Fatalf("writeAndSendBackup: %v", err)
	}
	if sender.calls != 1 || sender.filename != "fiscus-20261022-090807.tar.gz" {
		t.Fatalf("sender = %+v", sender)
	}
	reader, err := gzip.NewReader(bytes.NewReader(sender.data))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	tarReader := tar.NewReader(reader)
	entries := make(map[string]bool)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		entries[header.Name] = true
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if !entries["fiscus.db"] || !entries["attachments/receipt.pdf"] {
		t.Fatalf("archive entries = %v", entries)
	}
}
