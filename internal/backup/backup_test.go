package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/storage"
)

func TestCreateArchiveProducesRestorableDatabaseAndAttachments(t *testing.T) {
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

	attachmentsDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(attachmentsDir, "2026", "march"), 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	attachmentPath := filepath.Join(attachmentsDir, "2026", "march", "receipt.txt")
	if err := os.WriteFile(attachmentPath, []byte("receipt-content"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	out := filepath.Join(t.TempDir(), "snapshot.tar.gz")
	result, err := CreateArchive(ctx, source, attachmentsDir, out)
	if err != nil {
		t.Fatalf("CreateArchive: %v", err)
	}
	if result.Path != out || result.CompressedBytes <= 0 || result.Pages <= 0 || result.AttachmentFiles != 1 {
		t.Fatalf("result = %+v", result)
	}
	stat, err := os.Stat(out)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if stat.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", stat.Mode().Perm())
	}

	extracted := t.TempDir()
	entries := extractArchive(t, out, extracted)
	if _, ok := entries["fiscus.db"]; !ok {
		t.Fatalf("archive entries = %v, missing fiscus.db", entries)
	}
	if _, ok := entries["attachments/2026/march/receipt.txt"]; !ok {
		t.Fatalf("archive entries = %v, missing nested attachment", entries)
	}
	if got := string(entries["attachments/2026/march/receipt.txt"]); got != "receipt-content" {
		t.Fatalf("attachment content = %q", got)
	}

	restored, err := storage.Open(ctx, filepath.Join(extracted, "fiscus.db"))
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

func TestCreateArchiveToleratesMissingAttachmentDirectory(t *testing.T) {
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
	out := filepath.Join(t.TempDir(), "snapshot.tar.gz")
	result, err := CreateArchive(ctx, source, filepath.Join(t.TempDir(), "missing"), out)
	if err != nil {
		t.Fatalf("CreateArchive: %v", err)
	}
	if result.AttachmentFiles != 0 {
		t.Fatalf("attachment files = %d, want 0", result.AttachmentFiles)
	}
}

func TestCreateArchiveRejectsInvalidArguments(t *testing.T) {
	t.Parallel()
	if _, err := CreateArchive(context.Background(), nil, "", "out.tar.gz"); err == nil {
		t.Fatal("expected nil store error")
	}
	ctx := context.Background()
	source, err := storage.Open(ctx, filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer func() { _ = source.Close() }()
	if _, err := CreateArchive(ctx, source, "", "  "); err == nil {
		t.Fatal("expected empty output error")
	}
}

func TestDefaultPath(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 22, 9, 8, 7, 0, time.UTC)
	got := DefaultPath("snapshots", now)
	want := filepath.Join("snapshots", "fiscus-20261022-090807.tar.gz")
	if got != want {
		t.Fatalf("DefaultPath = %q, want %q", got, want)
	}
}

func extractArchive(t *testing.T, path, destination string) map[string][]byte {
	t.Helper()
	compressed, err := os.Open(path)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer func() { _ = compressed.Close() }()
	gzipReader, err := gzip.NewReader(compressed)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer func() { _ = gzipReader.Close() }()
	tarReader := tar.NewReader(gzipReader)
	entries := make(map[string][]byte)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		clean := filepath.Clean(header.Name)
		if clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
			t.Fatalf("unsafe archive entry %q", header.Name)
		}
		data, err := io.ReadAll(tarReader)
		if err != nil {
			t.Fatalf("read %s: %v", header.Name, err)
		}
		entries[header.Name] = data
		target := filepath.Join(destination, clean)
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			t.Fatalf("WriteFile %s: %v", target, err)
		}
	}
	return entries
}
