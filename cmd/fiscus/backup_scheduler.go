package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/backup"
	"github.com/RedRiotTank/Fiscus/internal/config"
	"github.com/RedRiotTank/Fiscus/internal/storage"
)

type documentSender interface {
	SendDocument(ctx context.Context, filename string, data []byte) error
}

func startBackupScheduler(ctx context.Context, cfg *config.Config, store *storage.Store, sender documentSender, logger *slog.Logger) {
	if !cfg.Backups.Enabled || !cfg.Backups.Telegram {
		return
	}
	interval := cfg.Backups.Interval.Std()
	if interval <= 0 {
		logger.Error("scheduled backups disabled: interval must be positive")
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := writeAndSendBackup(ctx, cfg, store, sender, time.Now().UTC()); err != nil {
					logger.Error("scheduled backup failed", "error", err)
				}
			}
		}
	}()
	logger.Info("scheduled Telegram backups enabled", "interval", interval.String(), "directory", cfg.Backups.Directory)
}

func writeAndSendBackup(ctx context.Context, cfg *config.Config, store *storage.Store, sender documentSender, now time.Time) error {
	if sender == nil {
		return fmt.Errorf("backup: document sender is required")
	}
	path := backup.DefaultPath(cfg.Backups.Directory, now)
	result, err := backup.CreateArchive(ctx, store, cfg.Attachments.Directory, path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(result.Path)
	if err != nil {
		return fmt.Errorf("backup: read generated archive: %w", err)
	}
	if err := sender.SendDocument(ctx, filepath.Base(result.Path), data); err != nil {
		return fmt.Errorf("backup: send Telegram document: %w", err)
	}
	return nil
}
