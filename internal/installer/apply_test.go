package installer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/glnarayanan/arivu/internal/database"
)

func TestValidateDownloadURLRejectsPlainHTTP(t *testing.T) {
	if err := validateDownloadURL("http://example.com/arivu"); err == nil {
		t.Fatal("expected http URL to fail")
	}
	if err := validateDownloadURL("https://example.com/arivu"); err != nil {
		t.Fatal(err)
	}
}

func TestBackupRequiresPrimaryDatabase(t *testing.T) {
	if _, err := Backup(t.TempDir()); err == nil {
		t.Fatal("expected missing primary database to fail")
	}
}

func TestBackupCreatesConsistentSQLiteSnapshot(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "var/lib/arivu")
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(context.Background(), filepath.Join(dataDir, "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), `CREATE TABLE backup_probe(value TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), `INSERT INTO backup_probe(value) VALUES('ok')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	backupDir, err := Backup(root)
	if err != nil {
		t.Fatal(err)
	}
	backupDB, err := database.Open(context.Background(), filepath.Join(backupDir, "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer backupDB.Close()
	var value string
	if err := backupDB.QueryRowContext(context.Background(), `SELECT value FROM backup_probe`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "ok" {
		t.Fatalf("backup value = %q", value)
	}
}

func TestRestoreRequiresPrimaryBackupDatabase(t *testing.T) {
	if err := Restore(t.TempDir(), t.TempDir()); err == nil {
		t.Fatal("expected missing primary backup database to fail")
	}
}
