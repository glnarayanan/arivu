package database

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenInitializesSchema(t *testing.T) {
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	var name string
	if err := db.QueryRowContext(context.Background(), `SELECT name FROM sqlite_master WHERE type='table' AND name='users'`).Scan(&name); err != nil {
		t.Fatalf("users table missing: %v", err)
	}
}
