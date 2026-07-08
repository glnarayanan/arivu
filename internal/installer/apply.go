package installer

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type ApplyOptions struct {
	Root              string
	AdminPassword     string
	AdminPasswordFile string
	DryRun            bool
	Quiet             bool
	ArtifactURL       string
	ChecksumsURL      string
}

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
		if err := installArivuBinary(ctx, opts, plan); err != nil {
			return err
		}
	}
	if root == "/" && shouldBootstrapAdmin(plan, opts) {
		if err := bootstrapAdmin(ctx, plan, opts); err != nil {
			return err
		}
	}
	if root == "/" {
		if err := run(ctx, "systemctl", "daemon-reload"); err != nil {
			return err
		}
		if err := run(ctx, "systemctl", "enable", "--now", "arivu.service"); err != nil {
			return err
		}
		if plan.Options.BackupEnabled {
			if err := run(ctx, "systemctl", "enable", "--now", "arivu-backup.timer"); err != nil {
				return err
			}
		}
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
	if err := run(ctx, "apt-get", "update"); err != nil {
		return err
	}
	args := append([]string{"install", "-y", "--no-install-recommends"}, packages...)
	return run(ctx, "apt-get", args...)
}

func ensureSystemUser(ctx context.Context, plan Plan) error {
	if plan.Facts.UserExists {
		return nil
	}
	if _, err := exec.LookPath("useradd"); err != nil {
		return nil
	}
	return run(ctx, "useradd", "--system", "--home-dir", "/var/lib/arivu", "--create-home", "--shell", "/usr/sbin/nologin", "arivu")
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
	cmd := exec.CommandContext(ctx, "/usr/local/bin/arivu", "admin", "bootstrap", "--db", "/var/lib/arivu/arivu.sqlite3", "--email", plan.Options.AdminEmail, "--password-stdin")
	cmd.Stdin = strings.NewReader(password + "\n")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func Backup(root string) (string, error) {
	if root == "" {
		root = "/"
	}
	source := rootPath(root, "/var/lib/arivu/arivu.sqlite3")
	targetDir := rootPath(root, "/var/backups/arivu/"+time.Now().UTC().Format("20060102T150405Z"))
	if err := os.MkdirAll(targetDir, 0o750); err != nil {
		return "", err
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := copyIfExists(source+suffix, filepath.Join(targetDir, "arivu.sqlite3"+suffix)); err != nil {
			return "", err
		}
	}
	return targetDir, nil
}

func Restore(root string, backupDir string) error {
	if root == "" {
		root = "/"
	}
	if backupDir == "" {
		return errors.New("backup directory is required")
	}
	target := rootPath(root, "/var/lib/arivu/arivu.sqlite3")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := copyIfExists(filepath.Join(backupDir, "arivu.sqlite3"+suffix), target+suffix); err != nil {
			return err
		}
	}
	return nil
}

func Upgrade(ctx context.Context, facts HostFacts, opts ApplyOptions, version string) error {
	plan := Plan{Facts: facts, Options: Options{Version: strings.TrimSpace(version)}}
	if err := installArivuBinary(ctx, opts, plan); err != nil {
		return err
	}
	return run(ctx, "systemctl", "restart", "arivu.service")
}

func Uninstall(ctx context.Context, purge bool) error {
	_ = run(ctx, "systemctl", "disable", "--now", "arivu-backup.timer")
	_ = run(ctx, "systemctl", "disable", "--now", "arivu.service")
	for _, path := range []string{"/etc/systemd/system/arivu.service", "/etc/systemd/system/arivu-backup.service", "/etc/systemd/system/arivu-backup.timer", "/usr/local/bin/arivu"} {
		_ = os.Remove(path)
	}
	if purge {
		_ = os.RemoveAll("/etc/arivu")
		_ = os.RemoveAll("/var/lib/arivu")
		_ = os.RemoveAll("/var/backups/arivu")
	}
	return run(ctx, "systemctl", "daemon-reload")
}

func copyIfExists(source string, target string) error {
	in, err := os.Open(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
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
