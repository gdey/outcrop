// Package store handles the SQLite-backed persistent state for outcrop:
// configuration (token, listen address, default vault), the vault registry,
// and the routing history.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gdey/goose/v3"
	"github.com/gdey/outcrop/store/migrations"

	_ "modernc.org/sqlite"
)

// Store wraps a *sql.DB connection to the outcrop SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if necessary) the outcrop SQLite database at path,
// applies PRAGMAs, and runs any pending goose migrations.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}

	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	// Verify the connection by executing a trivial round-trip.
	if err := db.PingContext(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}

	if err := runMigrations(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return &Store{db: db}, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB returns the underlying *sql.DB. Tests and callers needing to run
// arbitrary SQL outside the typed wrappers may use it.
func (s *Store) DB() *sql.DB {
	return s.db
}

func runMigrations(db *sql.DB) error {
	if err := migrations.Up(db, goose.WithNoOutput()); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}
