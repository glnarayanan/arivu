package installer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestRootRestoreChecksHealthBeforeBackupTimer(t *testing.T) {
	root := restoreFixture(t, "ARIVU_ADDR=127.0.0.1:8123\nARIVU_BACKUPS_ENABLED=true\n")
	backupDir := filepath.Join(t.TempDir(), "backup")
	if err := os.MkdirAll(backupDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "arivu.sqlite3"), []byte("backup"), 0o640); err != nil {
		t.Fatal(err)
	}

	var commands []string
	oldRun := runCommand
	oldHealth := healthCheckFunc
	defer func() {
		runCommand = oldRun
		healthCheckFunc = oldHealth
	}()
	healthIndex := -1
	runCommand = func(_ context.Context, name string, args ...string) error {
		commands = append(commands, name+" "+strings.Join(args, " "))
		return nil
	}
	healthCheckFunc = func(_ context.Context, port int) error {
		if port != 8123 {
			t.Fatalf("health check port = %d, want 8123", port)
		}
		healthIndex = len(commands)
		return nil
	}

	if err := restore(root, backupDir, true); err != nil {
		t.Fatal(err)
	}
	timerIndex := indexCommand(commands, "systemctl start arivu-backup.timer")
	if healthIndex < 0 || timerIndex < 0 || timerIndex < healthIndex {
		t.Fatalf("backup timer did not start after health check: healthIndex=%d commands=%#v", healthIndex, commands)
	}
}

func TestRootRestoreHealthFailureSkipsBackupTimer(t *testing.T) {
	root := restoreFixture(t, "ARIVU_ADDR=127.0.0.1:8123\nARIVU_BACKUPS_ENABLED=true\n")
	backupDir := filepath.Join(t.TempDir(), "backup")
	if err := os.MkdirAll(backupDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "arivu.sqlite3"), []byte("backup"), 0o640); err != nil {
		t.Fatal(err)
	}

	var commands []string
	oldRun := runCommand
	oldHealth := healthCheckFunc
	defer func() {
		runCommand = oldRun
		healthCheckFunc = oldHealth
	}()
	runCommand = func(_ context.Context, name string, args ...string) error {
		commands = append(commands, name+" "+strings.Join(args, " "))
		return nil
	}
	healthCheckFunc = func(context.Context, int) error {
		return errors.New("service unhealthy")
	}

	err := restore(root, backupDir, true)
	if err == nil || !strings.Contains(err.Error(), "restore health check failed") {
		t.Fatalf("restore error = %v, want health failure", err)
	}
	if indexCommand(commands, "systemctl start arivu-backup.timer") >= 0 {
		t.Fatalf("backup timer started despite health failure: %#v", commands)
	}
}

func restoreFixture(t *testing.T, env string) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"etc/arivu", "var/lib/arivu"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "etc/arivu/arivu.env"), []byte(env), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "var/lib/arivu/arivu.sqlite3"), []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	return root
}

func indexCommand(commands []string, needle string) int {
	for i, command := range commands {
		if command == needle {
			return i
		}
	}
	return -1
}
