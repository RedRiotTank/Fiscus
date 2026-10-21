// Package storage implements SQLite persistence for Fiscus using the pure-Go
// modernc.org/sqlite driver.
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// Store wraps a SQLite database connection pool.
type Store struct {
	db *sql.DB
}

var memoryDatabaseID atomic.Uint64

// Open opens (and creates if necessary) a SQLite database. For file databases,
// parent directories are created automatically and WAL mode is enabled.
func Open(ctx context.Context, path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("storage: database path is required")
	}

	dsn := path
	maxConns := 8
	if path == ":memory:" {
		dsn = fmt.Sprintf("file:fiscus-%d?mode=memory&cache=shared", memoryDatabaseID.Add(1))
		maxConns = 1
	} else if !strings.HasPrefix(path, "file:") {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("storage: create database directory: %w", err)
		}
		dsn = "file:" + path
	}

	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	var pragmas []string
	pragmas = append(pragmas, "_pragma=busy_timeout(5000)", "_pragma=foreign_keys(1)")
	if path != ":memory:" {
		pragmas = append(pragmas, "_pragma=journal_mode(WAL)", "_pragma=synchronous(NORMAL)")
	}
	dsn += sep + strings.Join(pragmas, "&")

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("storage: open database: %w", err)
	}
	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns)
	db.SetConnMaxIdleTime(5 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("storage: ping database: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the underlying database pool.
func (s *Store) Close() error {
	return s.db.Close()
}

// Ping verifies that the database is reachable.
func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// DB exposes the underlying pool for diagnostics. Callers should prefer the
// typed repository methods.
func (s *Store) DB() *sql.DB {
	return s.db
}
