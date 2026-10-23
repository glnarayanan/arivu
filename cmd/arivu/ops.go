package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glnarayanan/arivu/internal/backup"
)

// runBackup writes a verified backup (SQLite snapshot, assets, checksum
// manifest) in the same format as `arivu-installer backup`. It is safe while
// the server is running, so containers can use it with `docker exec`, and its
// output directory satisfies `arivu reprocess --apply --backup`.
func runBackup(args []string) {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	dbPath := fs.String("db", envDefault("ARIVU_DB", "arivu.sqlite3"), "SQLite database path")
	out := fs.String("out", os.Getenv("ARIVU_BACKUP_DIR"), "Directory that receives the timestamped backup (default: backups/ next to the database)")
	_ = fs.Parse(args)
	parent := *out
	if parent == "" {
		parent = filepath.Join(filepath.Dir(*dbPath), "backups")
	}
	dir, err := backup.Create(*dbPath, parent)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(dir)
}

// runRestore replaces the database and its assets with a backup made by
// `arivu backup` or `arivu-installer backup`. The server must be stopped; the
// command refuses while another process has the database open.
func runRestore(args []string) {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	dbPath := fs.String("db", envDefault("ARIVU_DB", "arivu.sqlite3"), "SQLite database path")
	backupDir := fs.String("backup", "", "Backup directory to restore (holds arivu.sqlite3 and usually manifest.json)")
	_ = fs.Parse(args)
	if strings.TrimSpace(*backupDir) == "" {
		log.Fatal("usage: arivu restore --backup /path/to/backup [--db arivu.sqlite3]")
	}
	if err := restoreDatabase(*dbPath, *backupDir); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("restored %s from %s\n", *dbPath, *backupDir)
}

func restoreDatabase(dbPath, backupDir string) error {
	if err := backup.EnsureNotInUse(dbPath); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o750); err != nil {
		return err
	}
	return backup.Restore(dbPath, backupDir, backup.RestoreHooks{
		Activate: func() error { return quickCheck(dbPath) },
	})
}

func quickCheck(dbPath string) error {
	db, err := sql.Open("sqlite3", dbPath+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	var result string
	if err := db.QueryRow("PRAGMA quick_check").Scan(&result); err != nil {
		return fmt.Errorf("restored database check: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("restored database check: %s", result)
	}
	return nil
}

// runHealthcheck requests /api/health on the local server and exits non-zero
// unless it answers 200. Container images have no curl, so their health check
// runs this.
func runHealthcheck(args []string) {
	fs := flag.NewFlagSet("healthcheck", flag.ExitOnError)
	target := fs.String("url", "", "Health URL (default: http://127.0.0.1:<port of ARIVU_ADDR>/api/health)")
	timeout := fs.Duration("timeout", 3*time.Second, "Request timeout")
	_ = fs.Parse(args)
	url := *target
	if url == "" {
		url = localHealthURL(envDefault("ARIVU_ADDR", ":8080"))
	}
	if err := checkHealth(url, *timeout); err != nil {
		fmt.Fprintln(os.Stderr, "unhealthy:", err)
		os.Exit(1)
	}
}

func localHealthURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		port = "8080"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/api/health"
}

func checkHealth(url string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return errors.New(resp.Status)
	}
	return nil
}
