package installer

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type ApplyOptions struct {
	Root              string
	AdminPassword     string
	AdminPasswordFile string
	DryRun            bool
	ArtifactURL       string
	ChecksumsURL      string
	InstallBinary     bool
}

var (
	runCommand      = run
	healthCheckFunc = healthCheck
)

func Apply(ctx context.Context, plan Plan, opts ApplyOptions) error {
	root := opts.Root
	if root == "" {
		root = "/"
	}
	if opts.DryRun {
		return nil
	}
	if root == "/" && os.Geteuid() != 0 {
		return errors.New("install requires root; rerun with sudo")
	}
	if root == "/" {
		if err := installPackages(ctx, plan); err != nil {
			return err
		}
		if err := ensureSystemUser(ctx, plan); err != nil {
			return err
		}
	}
	for _, dir := range []string{"/etc/arivu", "/etc/arivu/proxy", "/var/lib/arivu", "/var/backups/arivu"} {
		if err := os.MkdirAll(rootPath(root, dir), 0o750); err != nil {
			return err
		}
	}
	secret, err := existingOrNewSecret(rootPath(root, "/etc/arivu/arivu.env"))
	if err != nil {
		return err
	}
	for _, file := range plan.Files {
		content := strings.ReplaceAll(file.Content, "GENERATED-BY-INSTALLER", secret)
		if err := writeManagedFile(rootPath(root, file.Path), file.Mode, content); err != nil {
			return err
		}
	}
	if root == "/" {
		if err := repairOwnership(ctx); err != nil {
			return err
		}
	}
	if root == "/" && (!plan.Options.Reconfigure || opts.InstallBinary) {
		if err := installArivuBinary(ctx, opts, plan); err != nil {
			return err
		}
	}
	if root == "/" && shouldBootstrapAdmin(plan, opts) {
		if err := bootstrapAdmin(ctx, plan, opts); err != nil {
			return err
		}
		if err := repairOwnership(ctx); err != nil {
			return err
		}
	}
	if root == "/" {
		if err := runCommand(ctx, "systemctl", "daemon-reload"); err != nil {
			return err
		}
		if err := activateProxy(ctx, plan); err != nil {
			return err
		}
		if err := activateRootServices(ctx, plan); err != nil {
			return err
		}
	}
	return nil
}

func activateRootServices(ctx context.Context, plan Plan) error {
	if err := runCommand(ctx, "systemctl", "enable", "--now", "arivu.service"); err != nil {
		return err
	}
	if plan.Options.BackupEnabled {
		if err := runCommand(ctx, "systemctl", "enable", "--now", "arivu-backup.timer"); err != nil {
			return err
		}
	} else if plan.Options.Reconfigure {
		_ = runCommand(ctx, "systemctl", "disable", "--now", "arivu-backup.timer")
	}
	if err := healthCheckFunc(ctx, plan.BindPort); err != nil {
		return err
	}
	return nil
}

func shouldBootstrapAdmin(plan Plan, opts ApplyOptions) bool {
	return !plan.Options.Reconfigure || opts.AdminPassword != "" || opts.AdminPasswordFile != ""
}

func installPackages(ctx context.Context, plan Plan) error {
	if plan.Facts.Commands["apt-get"] == "" {
		return nil
	}
	packages := []string{"ca-certificates", "curl", "sqlite3"}
	if plan.ProxyMode == ProxyManagedCaddy || (plan.ProxyMode == ProxyExistingProxy && plan.Facts.Commands["caddy"] != "") {
		packages = append(packages, "caddy")
	}
	if err := runCommand(ctx, "apt-get", "update"); err != nil {
		return err
	}
	args := append([]string{"install", "-y", "--no-install-recommends"}, packages...)
	return runCommand(ctx, "apt-get", args...)
}

func ensureSystemUser(ctx context.Context, plan Plan) error {
	if plan.Facts.UserExists {
		return nil
	}
	if _, err := exec.LookPath("useradd"); err != nil {
		return nil
	}
	return runCommand(ctx, "useradd", "--system", "--home-dir", "/var/lib/arivu", "--create-home", "--shell", "/usr/sbin/nologin", "arivu")
}

func existingOrNewSecret(envPath string) (string, error) {
	raw, err := os.ReadFile(envPath)
	if err == nil {
		scanner := bufio.NewScanner(bytes.NewReader(raw))
		for scanner.Scan() {
			key, value, ok := strings.Cut(scanner.Text(), "=")
			if ok && key == "SECRET_KEY" && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value), nil
			}
		}
	}
	return GenerateSecret()
}

func writeManagedFile(path string, mode string, content string) error {
	perm := os.FileMode(0o644)
	if mode == "0640" {
		perm = 0o640
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), perm)
}

func installArivuBinary(ctx context.Context, opts ApplyOptions, plan Plan) error {
	url := opts.ArtifactURL
	sumsURL := opts.ChecksumsURL
	if url == "" || sumsURL == "" {
		defaultURL, defaultSums := ReleaseArtifactURLs("https://github.com/glnarayanan/arivu", plan.Options.Version, plan.Facts.Arch)
		if url == "" {
			url = defaultURL
		}
		if sumsURL == "" {
			sumsURL = defaultSums
		}
	}
	if err := validateDownloadURL(url); err != nil {
		return err
	}
	if err := validateDownloadURL(sumsURL); err != nil {
		return err
	}
	binary, err := download(ctx, url)
	if err != nil {
		return err
	}
	sums, err := download(ctx, sumsURL)
	if err != nil {
		return err
	}
	name := pathBase(url)
	if err := VerifyChecksum(binary, sums, name); err != nil {
		return err
	}
	tmp := "/usr/local/bin/arivu.tmp"
	if err := os.WriteFile(tmp, binary, 0o755); err != nil {
		return err
	}
	if err := verifyProvenance(ctx, tmp, url); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, "/usr/local/bin/arivu")
}

func bootstrapAdmin(ctx context.Context, plan Plan, opts ApplyOptions) error {
	password := opts.AdminPassword
	if password == "" && opts.AdminPasswordFile != "" {
		raw, err := os.ReadFile(opts.AdminPasswordFile)
		if err != nil {
			return err
		}
		password = strings.TrimRight(string(raw), "\r\n")
	}
	if password == "" {
		return errors.New("admin password is required")
	}
	args := []string{"/usr/local/bin/arivu", "admin", "bootstrap", "--db", "/var/lib/arivu/arivu.sqlite3", "--email", plan.Options.AdminEmail, "--password-stdin"}
	name := args[0]
	if _, err := exec.LookPath("runuser"); err == nil && os.Geteuid() == 0 {
		name = "runuser"
		args = append([]string{"-u", "arivu", "--"}, args...)
	} else {
		args = args[1:]
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(password + "\n")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "ADMIN_EMAILS="+plan.Options.AdminEmail, "ARIVU_DB=/var/lib/arivu/arivu.sqlite3")
	return cmd.Run()
}

func repairOwnership(ctx context.Context) error {
	if err := runCommand(ctx, "chown", "-R", "arivu:arivu", "/var/lib/arivu", "/var/backups/arivu"); err != nil {
		return err
	}
	_ = runCommand(ctx, "chown", "root:arivu", "/etc/arivu/arivu.env")
	return runCommand(ctx, "chmod", "0640", "/etc/arivu/arivu.env")
}

func activateProxy(ctx context.Context, plan Plan) error {
	switch plan.ProxyMode {
	case ProxyManagedCaddy:
		restore, err := caddyImportRollback("/etc/caddy/Caddyfile")
		if err != nil {
			return err
		}
		if err := ensureCaddyImport("/etc/caddy/Caddyfile"); err != nil {
			return err
		}
		if err := runCommand(ctx, "caddy", "validate", "--config", "/etc/caddy/Caddyfile"); err != nil {
			restore()
			return err
		}
		if err := runCommand(ctx, "systemctl", "enable", "--now", "caddy"); err != nil {
			restore()
			return err
		}
		if err := runCommand(ctx, "systemctl", "reload", "caddy"); err != nil {
			restore()
			return err
		}
		return nil
	case ProxyExistingProxy:
		if plan.Facts.Commands["caddy"] != "" {
			return runCommand(ctx, "caddy", "validate", "--config", "/etc/arivu/proxy/Caddyfile.arivu")
		}
		if plan.Facts.Commands["nginx"] != "" {
			return runCommand(ctx, "nginx", "-t")
		}
		if plan.Facts.Commands["apache2"] != "" || plan.Facts.Commands["httpd"] != "" {
			if plan.Facts.Commands["apache2ctl"] != "" {
				return runCommand(ctx, "apache2ctl", "configtest")
			}
		}
	}
	return nil
}

func caddyImportRollback(path string) (func(), error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return func() { _ = os.Remove(path) }, nil
	}
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	return func() { _ = os.WriteFile(path, raw, info.Mode().Perm()) }, nil
}

func ensureCaddyImport(path string) error {
	const importLine = "import /etc/caddy/conf.d/*.caddy"
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return os.WriteFile(path, []byte(importLine+"\n"), 0o644)
	}
	if err != nil {
		return err
	}
	if strings.Contains(string(raw), importLine) {
		return nil
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = fmt.Fprintln(file, "\n# Arivu managed site blocks\n"+importLine)
	return err
}

func Backup(root string) (string, error) {
	if root == "" {
		root = "/"
	}
	source := rootPath(root, "/var/lib/arivu/arivu.sqlite3")
	if _, err := os.Stat(source); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("primary database %s is missing", source)
		}
		return "", err
	}
	targetDir := rootPath(root, "/var/backups/arivu/"+time.Now().UTC().Format("20060102T150405Z"))
	if err := os.MkdirAll(targetDir, 0o750); err != nil {
		return "", err
	}
	target := filepath.Join(targetDir, "arivu.sqlite3")
	if err := sqliteBackup(source, target); err != nil {
		return "", err
	}
	return targetDir, nil
}

func Restore(root string, backupDir string) error {
	return restore(root, backupDir, root == "/" || root == "")
}

func restore(root string, backupDir string, rootInstall bool) error {
	if root == "" {
		root = "/"
	}
	if backupDir == "" {
		return errors.New("backup directory is required")
	}
	source := filepath.Join(backupDir, "arivu.sqlite3")
	if _, err := os.Stat(source); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("primary backup database %s is missing", source)
		}
		return err
	}
	ctx := context.Background()
	if rootInstall {
		_ = runCommand(ctx, "systemctl", "stop", "arivu-backup.timer")
		_ = runCommand(ctx, "systemctl", "stop", "arivu.service")
	}
	target := rootPath(root, "/var/lib/arivu/arivu.sqlite3")
	tmp := target + ".restore-tmp"
	if err := copyRequired(source, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		return err
	}
	_ = os.Remove(target + "-wal")
	_ = os.Remove(target + "-shm")
	if rootInstall {
		if err := repairOwnership(ctx); err != nil {
			return err
		}
		if err := runCommand(ctx, "systemctl", "start", "arivu.service"); err != nil {
			return err
		}
		opts, _ := OptionsFromEnvFile(rootPath(root, "/etc/arivu/arivu.env"))
		port := opts.BindPort
		if port == 0 {
			port = 8080
		}
		if err := healthCheckFunc(ctx, port); err != nil {
			return fmt.Errorf("restore health check failed after restarting arivu.service: %w", err)
		}
		if opts.BackupEnabled {
			_ = runCommand(ctx, "systemctl", "start", "arivu-backup.timer")
		}
	}
	return nil
}

func Upgrade(ctx context.Context, facts HostFacts, opts ApplyOptions, version string) error {
	plan := Plan{Facts: facts, Options: Options{Version: strings.TrimSpace(version)}}
	previous := "/usr/local/bin/arivu.previous"
	if err := copyIfExists("/usr/local/bin/arivu", previous); err != nil {
		return err
	}
	if err := installArivuBinary(ctx, opts, plan); err != nil {
		return err
	}
	if err := runCommand(ctx, "systemctl", "restart", "arivu.service"); err != nil {
		_ = rollbackBinary(ctx, previous)
		return err
	}
	if err := runCommand(ctx, "systemctl", "is-active", "--quiet", "arivu.service"); err != nil {
		_ = rollbackBinary(ctx, previous)
		return err
	}
	port := 8080
	if opts, err := OptionsFromEnvFile("/etc/arivu/arivu.env"); err == nil && opts.BindPort != 0 {
		port = opts.BindPort
	}
	if err := healthCheckFunc(ctx, port); err != nil {
		_ = rollbackBinary(ctx, previous)
		return err
	}
	_ = os.Remove(previous)
	return nil
}

func Uninstall(ctx context.Context, purge bool) error {
	_ = runCommand(ctx, "systemctl", "disable", "--now", "arivu-backup.timer")
	_ = runCommand(ctx, "systemctl", "disable", "--now", "arivu.service")
	for _, path := range []string{"/etc/systemd/system/arivu.service", "/etc/systemd/system/arivu-backup.service", "/etc/systemd/system/arivu-backup.timer", "/usr/local/bin/arivu"} {
		_ = os.Remove(path)
	}
	if purge {
		_ = os.RemoveAll("/etc/arivu")
		_ = os.RemoveAll("/var/lib/arivu")
		_ = os.RemoveAll("/var/backups/arivu")
	}
	return runCommand(ctx, "systemctl", "daemon-reload")
}

func validateDownloadURL(target string) error {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme == "" {
		return fmt.Errorf("download URL %q is invalid", target)
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("download URL %q must use https", target)
	}
	return nil
}

func verifyProvenance(ctx context.Context, artifactPath string, artifactURL string) error {
	parsed, err := url.Parse(artifactURL)
	if err != nil {
		return err
	}
	if parsed.Host != "github.com" {
		return fmt.Errorf("unsupported artifact provenance host %q", parsed.Host)
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 2 {
		return fmt.Errorf("cannot infer GitHub repository from %s", artifactURL)
	}
	if _, err := exec.LookPath("gh"); err != nil {
		return errors.New("gh is required to verify GitHub artifact attestations")
	}
	return runCommand(ctx, "gh", "attestation", "verify", artifactPath, "-R", parts[0]+"/"+parts[1])
}

func sqliteBackup(source string, target string) error {
	tmp := target + ".tmp"
	_ = os.Remove(tmp)
	db, err := sql.Open("sqlite3", source+"?_busy_timeout=5000")
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec("VACUUM INTO " + sqliteQuote(tmp)); err != nil {
		return err
	}
	check, err := sql.Open("sqlite3", tmp+"?_query_only=true")
	if err != nil {
		return err
	}
	defer check.Close()
	var result string
	if err := check.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("backup integrity check failed: %s", result)
	}
	return os.Rename(tmp, target)
}

func sqliteQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func copyIfExists(source string, target string) error {
	err := copyRequired(source, target)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func copyRequired(source string, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func rollbackBinary(ctx context.Context, previous string) error {
	if _, err := os.Stat(previous); err != nil {
		return err
	}
	if err := os.Rename(previous, "/usr/local/bin/arivu"); err != nil {
		return err
	}
	return runCommand(ctx, "systemctl", "restart", "arivu.service")
}

func healthCheck(ctx context.Context, port int) error {
	deadline := time.Now().Add(20 * time.Second)
	var lastErr error
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/api/health", port), nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode < 400 {
				return nil
			}
			lastErr = fmt.Errorf("health check returned status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			return lastErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func VerifyChecksum(data []byte, sums []byte, name string) error {
	sum := sha256.Sum256(data)
	actual := hex.EncodeToString(sum[:])
	scanner := bufio.NewScanner(bytes.NewReader(sums))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && strings.TrimPrefix(fields[1], "*") == name {
			if fields[0] == actual {
				return nil
			}
			return fmt.Errorf("checksum mismatch for %s", name)
		}
	}
	return fmt.Errorf("checksum for %s not found", name)
}

func download(ctx context.Context, target string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("download %s: status %d", target, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 200<<20))
}

func run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func pathBase(value string) string {
	if idx := strings.LastIndex(value, "/"); idx >= 0 {
		return value[idx+1:]
	}
	return value
}

func rootPath(root string, path string) string {
	if root == "" || root == "/" {
		return path
	}
	return filepath.Join(root, strings.TrimPrefix(path, "/"))
}
