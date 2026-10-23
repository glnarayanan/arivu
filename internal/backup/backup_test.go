package backup

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glnarayanan/arivu/internal/database"
)

func createDB(t *testing.T, path string) {
	t.Helper()
	db, err := database.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
}

func TestCreateAndRestoreRoundTrip(t *testing.T) {
	src := t.TempDir()
	dbPath := filepath.Join(src, DatabaseName)
	createDB(t, dbPath)
	writeFile(t, dbPath+".assets/objects/ab/one", "asset")
	writeFile(t, dbPath+".assets/.staging/partial", "skip me")

	dir, err := Create(dbPath, filepath.Join(src, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyManifest(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, AssetsName, ".staging")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging files were copied: %v", err)
	}

	dst := t.TempDir()
	target := filepath.Join(dst, DatabaseName)
	writeFile(t, target+".assets/objects/old", "previous")
	activated := false
	if err := Restore(target, dir, RestoreHooks{Activate: func() error { activated = true; return nil }}); err != nil {
		t.Fatal(err)
	}
	if !activated {
		t.Fatal("Activate hook did not run")
	}
	if raw, err := os.ReadFile(target + ".assets/objects/ab/one"); err != nil || string(raw) != "asset" {
		t.Fatalf("restored asset = %q, %v", raw, err)
	}
	for _, leftover := range []string{target + ".restore-previous", target + ".assets.restore-previous", target + ".restore-tmp"} {
		if _, err := os.Stat(leftover); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s left behind: %v", leftover, err)
		}
	}
}

func TestRestoreRollsBackWhenActivateFails(t *testing.T) {
	src := t.TempDir()
	dbPath := filepath.Join(src, DatabaseName)
	createDB(t, dbPath)
	writeFile(t, dbPath+".assets/objects/new", "new")
	dir, err := Create(dbPath, filepath.Join(src, "backups"))
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(t.TempDir(), DatabaseName)
	writeFile(t, target, "previous database")
	writeFile(t, target+".assets/objects/old", "old")
	var calls []string
	err = Restore(target, dir, RestoreHooks{
		Stop:           func() { calls = append(calls, "stop") },
		Activate:       func() error { calls = append(calls, "activate"); return errors.New("health failed") },
		BeforeRollback: func() error { calls = append(calls, "before"); return nil },
		AfterRollback:  func() []error { calls = append(calls, "after"); return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "health failed") {
		t.Fatalf("Restore error = %v", err)
	}
	if got := strings.Join(calls, ","); got != "stop,activate,before,after" {
		t.Fatalf("hook order = %s", got)
	}
	if raw, _ := os.ReadFile(target); string(raw) != "previous database" {
		t.Fatalf("database not rolled back: %q", raw)
	}
	if raw, _ := os.ReadFile(target + ".assets/objects/old"); string(raw) != "old" {
		t.Fatalf("assets not rolled back: %q", raw)
	}
}

func TestRestoreRejectsTamperedBackupBeforeTouchingLiveFiles(t *testing.T) {
	src := t.TempDir()
	dbPath := filepath.Join(src, DatabaseName)
	createDB(t, dbPath)
	writeFile(t, dbPath+".assets/objects/a", "asset")
	dir, err := Create(dbPath, filepath.Join(src, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, AssetsName, "objects/a"), "tampered")

	target := filepath.Join(t.TempDir(), DatabaseName)
	writeFile(t, target, "live")
	stopped := false
	err = Restore(target, dir, RestoreHooks{Stop: func() { stopped = true }})
	if err == nil || !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("Restore error = %v", err)
	}
	if stopped {
		t.Fatal("Stop ran for a backup that failed verification")
	}
	if raw, _ := os.ReadFile(target); string(raw) != "live" {
		t.Fatalf("live database changed: %q", raw)
	}
}

func TestEnsureNotInUse(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), DatabaseName)
	if err := EnsureNotInUse(dbPath); err != nil {
		t.Fatalf("missing database: %v", err)
	}
	createDB(t, dbPath)
	live, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	live.SetMaxOpenConns(1)
	var n int
	if err := live.QueryRow("SELECT count(*) FROM sqlite_master").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if err := EnsureNotInUse(dbPath); !errors.Is(err, ErrDatabaseInUse) {
		t.Fatalf("open database: %v", err)
	}
	if err := live.Close(); err != nil {
		t.Fatal(err)
	}
	if err := EnsureNotInUse(dbPath); err != nil {
		t.Fatalf("closed database: %v", err)
	}
}
