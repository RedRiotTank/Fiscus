// Package backup creates consistent compressed archives containing a live
// SQLite snapshot and the managed attachment directory.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/storage"
)

const databaseEntryName = "fiscus.db"

// Result describes a completed archive.
type Result struct {
	Path            string
	CompressedBytes int64
	Pages           int
	AttachmentFiles int
}

// DefaultPath returns a timestamped backup archive filename inside dir.
func DefaultPath(dir string, now time.Time) string {
	if strings.TrimSpace(dir) == "" {
		dir = "backups"
	}
	return filepath.Join(dir, "fiscus-"+now.UTC().Format("20060102-150405")+".tar.gz")
}

// CreateArchive writes a gzipped tar archive containing an online SQLite
// snapshot named fiscus.db and every regular file below attachmentsDir under
// an attachments/ prefix. The temporary SQLite file is removed after the
// archive is written, and partial output is removed on failure.
func CreateArchive(ctx context.Context, store *storage.Store, attachmentsDir, out string) (Result, error) {
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
	tarWriter := tar.NewWriter(gzipWriter)
	closeWriters := func() error {
		if err := tarWriter.Close(); err != nil {
			_ = gzipWriter.Close()
			_ = outFile.Close()
			return fmt.Errorf("backup: finish tar archive: %w", err)
		}
		if err := gzipWriter.Close(); err != nil {
			_ = outFile.Close()
			return fmt.Errorf("backup: finish compression: %w", err)
		}
		if err := outFile.Close(); err != nil {
			return fmt.Errorf("backup: close output file: %w", err)
		}
		return nil
	}

	if err := appendFile(tarWriter, tempPath, databaseEntryName); err != nil {
		_ = tarWriter.Close()
		_ = gzipWriter.Close()
		_ = outFile.Close()
		return Result{}, err
	}
	attachmentFiles, err := appendAttachments(tarWriter, attachmentsDir, out, tempPath)
	if err != nil {
		_ = tarWriter.Close()
		_ = gzipWriter.Close()
		_ = outFile.Close()
		return Result{}, err
	}
	if err := closeWriters(); err != nil {
		return Result{}, err
	}
	stat, err := os.Stat(out)
	if err != nil {
		return Result{}, fmt.Errorf("backup: stat output file: %w", err)
	}
	success = true
	return Result{
		Path:            out,
		CompressedBytes: stat.Size(),
		Pages:           info.Pages,
		AttachmentFiles: attachmentFiles,
	}, nil
}

func appendFile(writer *tar.Writer, path, name string) error {
	// #nosec G304 -- path is either the temporary database created above or a
	// file discovered below the configured attachment directory.
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("backup: open %s: %w", name, err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("backup: stat %s: %w", name, err)
	}
	header := &tar.Header{
		Name:    name,
		Mode:    0o600,
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}
	if err := writer.WriteHeader(header); err != nil {
		return fmt.Errorf("backup: write header for %s: %w", name, err)
	}
	if _, err := io.Copy(writer, file); err != nil {
		return fmt.Errorf("backup: write %s: %w", name, err)
	}
	return nil
}

func appendAttachments(writer *tar.Writer, attachmentsDir, out, tempPath string) (int, error) {
	attachmentsDir = strings.TrimSpace(attachmentsDir)
	if attachmentsDir == "" {
		return 0, nil
	}
	info, err := os.Stat(attachmentsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("backup: stat attachment directory: %w", err)
	}
	if !info.IsDir() {
		return 0, fmt.Errorf("backup: attachment path %q is not a directory", attachmentsDir)
	}

	out = filepath.Clean(out)
	tempPath = filepath.Clean(tempPath)
	written := 0
	err = filepath.WalkDir(attachmentsDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		clean := filepath.Clean(path)
		if clean == out || clean == tempPath {
			return nil
		}
		relative, err := filepath.Rel(attachmentsDir, path)
		if err != nil {
			return fmt.Errorf("backup: relative attachment path: %w", err)
		}
		if relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || relative == ".." {
			return nil
		}
		name := filepath.ToSlash(filepath.Join("attachments", relative))
		if err := appendFile(writer, path, name); err != nil {
			return err
		}
		written++
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("backup: archive attachments: %w", err)
	}
	return written, nil
}
