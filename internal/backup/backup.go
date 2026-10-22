// Package backup creates consistent gzipped snapshots of the live SQLite
// database using SQLite's online backup API.
package backup

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/storage"
)

// Result describes a completed gzipped backup.
type Result struct {
	Path            string
	CompressedBytes int64
	Pages           int
}

// DefaultPath returns a timestamped backup filename inside dir.
func DefaultPath(dir string, now time.Time) string {
	if strings.TrimSpace(dir) == "" {
		dir = "backups"
	}
	return filepath.Join(dir, "fiscus-"+now.UTC().Format("20060102-150405")+".sqlite.gz")
}

// CreateGzip writes a gzipped online backup to out. The temporary SQLite file
// is removed after compression, and partial output is removed on failure.
func CreateGzip(ctx context.Context, store *storage.Store, out string) (Result, error) {
	if store == nil {
		return Result{}, errors.New("backup: store is required")
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return Result{}, errors.New("backup: output path is required")
	}
	dir := filepath.Dir(out)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return Result{}, fmt.Errorf("backup: create output directory: %w", err)
	}
	tempFile, err := os.CreateTemp(dir, ".fiscus-backup-*.sqlite")
	if err != nil {
		return Result{}, fmt.Errorf("backup: create temporary database: %w", err)
	}
	tempPath := tempFile.Name()
	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempPath)
		return Result{}, fmt.Errorf("backup: close temporary database: %w", err)
	}
	defer func() {
		_ = os.Remove(tempPath)
		_ = os.Remove(tempPath + "-journal")
		_ = os.Remove(tempPath + "-wal")
		_ = os.Remove(tempPath + "-shm")
	}()

	info, err := store.OnlineBackup(ctx, tempPath)
	if err != nil {
		return Result{}, err
	}

	success := false
	defer func() {
		if !success {
			_ = os.Remove(out)
		}
	}()
	// #nosec G304 -- the operator selects the backup destination path.
	outFile, err := os.OpenFile(out, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return Result{}, fmt.Errorf("backup: create output file: %w", err)
	}
	gzipWriter := gzip.NewWriter(outFile)
	// #nosec G304 -- tempPath is created by this function in the output directory.
	source, err := os.Open(tempPath)
	if err != nil {
		_ = gzipWriter.Close()
		_ = outFile.Close()
		return Result{}, fmt.Errorf("backup: open temporary database: %w", err)
	}
	if _, err := io.Copy(gzipWriter, source); err != nil {
		_ = source.Close()
		_ = gzipWriter.Close()
		_ = outFile.Close()
		return Result{}, fmt.Errorf("backup: compress database: %w", err)
	}
	if err := source.Close(); err != nil {
		_ = gzipWriter.Close()
		_ = outFile.Close()
		return Result{}, fmt.Errorf("backup: close temporary database: %w", err)
	}
	if err := gzipWriter.Close(); err != nil {
		_ = outFile.Close()
		return Result{}, fmt.Errorf("backup: finish compression: %w", err)
	}
	if err := outFile.Close(); err != nil {
		return Result{}, fmt.Errorf("backup: close output file: %w", err)
	}
	stat, err := os.Stat(out)
	if err != nil {
		return Result{}, fmt.Errorf("backup: stat output file: %w", err)
	}
	success = true
	return Result{Path: out, CompressedBytes: stat.Size(), Pages: info.Pages}, nil
}
