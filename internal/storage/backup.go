package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"modernc.org/sqlite"
)

// BackupInfo summarizes an online SQLite backup.
type BackupInfo struct {
	Pages int
}

// OnlineBackup copies the live database to dest using SQLite's online backup
// API. Readers and writers may continue while pages are copied.
func (s *Store) OnlineBackup(ctx context.Context, dest string) (BackupInfo, error) {
	if strings.TrimSpace(dest) == "" {
		return BackupInfo{}, errors.New("storage: backup destination is required")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return BackupInfo{}, fmt.Errorf("storage: reserve backup connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	info := BackupInfo{}
	err = conn.Raw(func(driverConn any) error {
		backuper, ok := driverConn.(interface {
			NewBackup(dstURI string) (*sqlite.Backup, error)
		})
		if !ok {
			return errors.New("storage: sqlite driver does not expose online backup")
		}
		backup, err := backuper.NewBackup(dest)
		if err != nil {
			return fmt.Errorf("storage: initialize online backup: %w", err)
		}
		for {
			if err := ctx.Err(); err != nil {
				_ = backup.Finish()
				return fmt.Errorf("storage: online backup canceled: %w", err)
			}
			more, err := backup.Step(128)
			if err != nil {
				_ = backup.Finish()
				return fmt.Errorf("storage: copy online backup pages: %w", err)
			}
			if !more {
				break
			}
		}
		info.Pages = backup.PageCount()
		if err := backup.Finish(); err != nil {
			return fmt.Errorf("storage: finish online backup: %w", err)
		}
		return nil
	})
	return info, err
}
