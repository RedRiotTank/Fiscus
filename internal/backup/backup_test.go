package backup

import (
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/storage"
)

func TestCreateGzipProducesRestorableDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	source, err := storage.Open(ctx, filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer func() { _ = source.Close() }()
	if err := source.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	connection := domain.Connection{
		RequisitionID: "req-backup", InstitutionID: "INST", InstitutionName: "Backup Bank", Status: "LN",
	}
	if err := source.UpsertConnection(ctx, &connection); err != nil {
		t.Fatalf("UpsertConnection: %v", err)
	}

	out := filepath.Join(t.TempDir(), "snapshot.sqlite.gz")
	result, err := CreateGzip(ctx, source, out)
	if err != nil {
		t.Fatalf("CreateGzip: %v", err)
	}
	if result.Path != out || result.CompressedBytes <= 0 || result.Pages <= 0 {
		t.Fatalf("result = %+v", result)
	}
	stat, err := os.Stat(out)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if stat.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", stat.Mode().Perm())
	}

	compressed, err := os.Open(out)
	if err != nil {
		t.Fatalf("open gzip: %v", err)
	}
	defer func() { _ = compressed.Close() }()
	reader, err := gzip.NewReader(compressed)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	restoredPath := filepath.Join(t.TempDir(), "restored.db")
	restoredFile, err := os.OpenFile(restoredPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("create restored: %v", err)
	}
	if _, err := io.Copy(restoredFile, reader); err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if err := restoredFile.Close(); err != nil {
		t.Fatalf("close restored: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close gzip reader: %v", err)
	}

	restored, err := storage.Open(ctx, restoredPath)
	if err != nil {
		t.Fatalf("open restored: %v", err)
	}
	defer func() { _ = restored.Close() }()
	connections, err := restored.ListConnections(ctx)
	if err != nil {
		t.Fatalf("ListConnections: %v", err)
	}
	if len(connections) != 1 || connections[0].RequisitionID != "req-backup" {
		t.Fatalf("connections = %+v", connections)
	}
}

func TestCreateGzipRejectsInvalidArguments(t *testing.T) {
	t.Parallel()
	if _, err := CreateGzip(context.Background(), nil, "out.gz"); err == nil {
		t.Fatal("expected nil store error")
	}
	ctx := context.Background()
	source, err := storage.Open(ctx, filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer func() { _ = source.Close() }()
	if _, err := CreateGzip(ctx, source, "  "); err == nil {
		t.Fatal("expected empty output error")
	}
}

func TestDefaultPath(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 22, 9, 8, 7, 0, time.UTC)
	got := DefaultPath("snapshots", now)
	want := filepath.Join("snapshots", "fiscus-20261022-090807.sqlite.gz")
	if got != want {
		t.Fatalf("DefaultPath = %q, want %q", got, want)
	}
}
