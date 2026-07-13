package installer

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
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
	Root                 string
	AdminPassword        string
	AdminPasswordFile    string
	DryRun               bool
	ArtifactURL          string
	InstallerArtifactURL string
	ChecksumsURL         string
	InstallBinary        bool
}

var (
	runCommand      = run
	healthCheckFunc = healthCheck
	downloadFunc    = download
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
		defaultURL, _, defaultSums := ReleaseArtifactURLs("https://github.com/glnarayanan/arivu", plan.Options.Version, plan.Facts.Arch)
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
	binary, err := downloadFunc(ctx, url)
	if err != nil {
		return err
	}
	sums, err := downloadFunc(ctx, sumsURL)
	if err != nil {
		return err
	}
	name := pathBase(url)
	if err := VerifyChecksum(binary, sums, name); err != nil {
		return err
	}
	return installExecutableBinary("/usr/local/bin/arivu", binary)
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
	manifest := backupManifest{Version: 1}
	if err := addManifestFile(targetDir, target, "arivu.sqlite3", &manifest); err != nil {
		return "", err
	}
	assetSource := source + ".assets"
	if _, err := os.Stat(assetSource); err == nil {
		err = filepath.Walk(assetSource, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil || info.IsDir() {
				return walkErr
			}
			rel, err := filepath.Rel(assetSource, path)
			if err != nil {
				return err
			}
			dst := filepath.Join(targetDir, "arivu.sqlite3.assets", rel)
			if strings.HasPrefix(filepath.ToSlash(rel), ".staging/") {
				return nil
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
				return err
			}
			if err := copyRequired(path, dst); err != nil {
				return err
			}
			return addManifestFile(targetDir, dst, filepath.ToSlash(filepath.Join("arivu.sqlite3.assets", rel)), &manifest)
		})
		if err != nil {
			return "", err
		}
	}
	raw, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(filepath.Join(targetDir, "manifest.json"), raw, 0o640); err != nil {
		return "", err
	}
	return targetDir, nil
}

type backupManifest struct {
	Version int                  `json:"version"`
	Files   []backupManifestFile `json:"files"`
}
type backupManifestFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func addManifestFile(root, path, name string, m *backupManifest) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	m.Files = append(m.Files, backupManifestFile{name, size, hex.EncodeToString(h.Sum(nil))})
	return nil
}
func verifyManifest(dir string) error {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var m backupManifest
	if json.Unmarshal(raw, &m) != nil || m.Version != 1 {
		return errors.New("unsupported backup manifest")
	}
	for _, f := range m.Files {
		if filepath.IsAbs(f.Path) || strings.Contains(f.Path, "..") {
			return errors.New("invalid backup manifest path")
		}
		file, err := os.Open(filepath.Join(dir, filepath.FromSlash(f.Path)))
		if err != nil {
			return fmt.Errorf("backup integrity: %s: %w", f.Path, err)
		}
		h := sha256.New()
		size, copyErr := io.Copy(h, file)
		file.Close()
		if copyErr != nil || size != f.Size || hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
			return fmt.Errorf("backup integrity check failed for %s", f.Path)
		}
	}
	return nil
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
	if err := verifyManifest(backupDir); err != nil {
		return err
	}
	source := filepath.Join(backupDir, "arivu.sqlite3")
	if _, err := os.Stat(source); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("primary backup database %s is missing", source)
		}
		return err
	}
	ctx := context.Background()
	target := rootPath(root, "/var/lib/arivu/arivu.sqlite3")
	tmp := target + ".restore-tmp"
	if err := copyRequired(source, tmp); err != nil {
		return err
	}
	defer os.Remove(tmp)
	assetBackup := filepath.Join(backupDir, "arivu.sqlite3.assets")
	assetTmp := target + ".assets.restore-tmp"
	hasAssets := false
	if info, err := os.Stat(assetBackup); err == nil && info.IsDir() {
		hasAssets = true
		_ = os.RemoveAll(assetTmp)
		if err := copyTree(assetBackup, assetTmp); err != nil {
			return err
		}
		defer os.RemoveAll(assetTmp)
	}
	if err := verifyArtifactRows(tmp, assetTmp); err != nil {
		return err
	}
	if rootInstall {
		_ = runCommand(ctx, "systemctl", "stop", "arivu-backup.timer")
		_ = runCommand(ctx, "systemctl", "stop", "arivu.service")
	}
	oldDB, oldAssets := target+".restore-previous", target+".assets.restore-previous"
	_ = os.Remove(oldDB)
	_ = os.RemoveAll(oldAssets)
	if _, err := os.Stat(target); err == nil {
		if err = os.Rename(target, oldDB); err != nil {
			return err
		}
	}
	rollback := func(cause error) error {
		var rollbackErrors []string
		if rootInstall {
			if e := runCommand(ctx, "systemctl", "stop", "arivu.service"); e != nil {
				rollbackErrors = append(rollbackErrors, e.Error())
			}
		}
		_ = os.Remove(target)
		if e := os.Rename(oldDB, target); e != nil {
			rollbackErrors = append(rollbackErrors, e.Error())
		}
		if _, e := os.Stat(oldAssets); e == nil {
			_ = os.RemoveAll(target + ".assets")
			if e = os.Rename(oldAssets, target+".assets"); e != nil {
				rollbackErrors = append(rollbackErrors, e.Error())
			}
		}
		if rootInstall {
			if e := runCommand(ctx, "systemctl", "start", "arivu.service"); e != nil {
				rollbackErrors = append(rollbackErrors, e.Error())
			}
			if e := runCommand(ctx, "systemctl", "start", "arivu-backup.timer"); e != nil {
				rollbackErrors = append(rollbackErrors, e.Error())
			}
		}
		if len(rollbackErrors) > 0 {
			return fmt.Errorf("%w; rollback errors: %s", cause, strings.Join(rollbackErrors, "; "))
		}
		return cause
	}
	if hasAssets {
		if _, err := os.Stat(target + ".assets"); err == nil {
			if err = os.Rename(target+".assets", oldAssets); err != nil {
				return rollback(err)
			}
		}
	}
	if err := os.Rename(tmp, target); err != nil {
		return rollback(err)
	}
	if hasAssets {
		if err := os.Rename(assetTmp, target+".assets"); err != nil {
			return rollback(err)
		}
	}
	_ = os.Remove(target + "-wal")
	_ = os.Remove(target + "-shm")
	if rootInstall {
		if err := repairOwnership(ctx); err != nil {
			return rollback(err)
		}
		if err := runCommand(ctx, "systemctl", "start", "arivu.service"); err != nil {
			return rollback(err)
		}
		opts, _ := OptionsFromEnvFile(rootPath(root, "/etc/arivu/arivu.env"))
		port := opts.BindPort
		if port == 0 {
			port = 8080
		}
		if err := healthCheckFunc(ctx, port); err != nil {
			return rollback(fmt.Errorf("restore health check failed after restarting arivu.service: %w", err))
		}
		if opts.BackupEnabled {
			_ = runCommand(ctx, "systemctl", "start", "arivu-backup.timer")
		}
	}
	_ = os.Remove(oldDB)
	_ = os.RemoveAll(oldAssets)
	return nil
}

func verifyArtifactRows(dbPath, assetRoot string) error {
	db, err := sql.Open("sqlite3", dbPath+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT storage_key,byte_size,sha256 FROM artifacts WHERE deleted_at IS NULL`)
	if err != nil { // Legacy databases predate artifacts.
		if strings.Contains(err.Error(), "no such table") {
			return nil
		}
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key, digest string
		var expected int64
		if err = rows.Scan(&key, &expected, &digest); err != nil {
			return err
		}
		if strings.Contains(key, "..") || filepath.IsAbs(key) {
			return errors.New("invalid artifact storage key")
		}
		f, e := os.Open(filepath.Join(assetRoot, "objects", filepath.FromSlash(key)))
		if e != nil {
			return fmt.Errorf("live artifact %s missing: %w", key, e)
		}
		h := sha256.New()
		n, e := io.Copy(h, f)
		f.Close()
		if e != nil || n != expected || hex.EncodeToString(h.Sum(nil)) != digest {
			return fmt.Errorf("live artifact %s integrity check failed", key)
		}
	}
	return rows.Err()
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, e := filepath.Rel(src, path)
		if e != nil {
			return e
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		return copyRequired(path, target)
	})
}

func Upgrade(ctx context.Context, facts HostFacts, opts ApplyOptions, version string) error {
	return upgrade(ctx, facts, opts, version, "/", activateUpgrade)
}

func upgrade(ctx context.Context, facts HostFacts, opts ApplyOptions, version, root string, activate func(context.Context, string) error) error {
	plan := Plan{Facts: facts, Options: Options{Version: strings.TrimSpace(version)}}
	appURL := opts.ArtifactURL
	installerURL := opts.InstallerArtifactURL
	sumsURL := opts.ChecksumsURL
	defaultAppURL, defaultInstallerURL, defaultSumsURL := ReleaseArtifactURLs("https://github.com/glnarayanan/arivu", plan.Options.Version, facts.Arch)
	customAppArtifact := appURL != ""
	if appURL == "" {
		appURL = defaultAppURL
	}
	if installerURL == "" && !customAppArtifact {
		installerURL = defaultInstallerURL
	}
	if sumsURL == "" {
		sumsURL = defaultSumsURL
	}
	targets := []string{appURL, sumsURL}
	if installerURL != "" {
		targets = append(targets, installerURL)
	}
	for _, target := range targets {
		if err := validateDownloadURL(target); err != nil {
			return err
		}
	}
	sums, err := downloadFunc(ctx, sumsURL)
	if err != nil {
		return err
	}
	app, err := downloadVerifiedArtifact(ctx, appURL, sums)
	if err != nil {
		return err
	}
	replacements := []*binaryReplacement{
		{path: rootPath(root, "/usr/local/bin/arivu"), data: app},
	}
	if installerURL != "" {
		installerBinary, err := downloadVerifiedArtifact(ctx, installerURL, sums)
		if err != nil {
			return err
		}
		replacements = append(replacements, &binaryReplacement{path: rootPath(root, "/usr/local/bin/arivu-installer"), data: installerBinary})
	}
	for _, replacement := range replacements {
		if err := replacement.prepare(); err != nil {
			for _, item := range replacements {
				item.cleanup()
			}
			return err
		}
	}
	defer func() {
		for _, replacement := range replacements {
			replacement.cleanup()
		}
	}()
	for index, replacement := range replacements {
		if err := replacement.commit(); err != nil {
			for rollbackIndex := index - 1; rollbackIndex >= 0; rollbackIndex-- {
				_ = replacements[rollbackIndex].rollback()
			}
			return err
		}
	}
	if err := activate(ctx, root); err != nil {
		var rollbackErr error
		for index := len(replacements) - 1; index >= 0; index-- {
			rollbackErr = errors.Join(rollbackErr, replacements[index].rollback())
		}
		rollbackErr = errors.Join(rollbackErr, activate(ctx, root))
		if rollbackErr != nil {
			return fmt.Errorf("activate upgraded Arivu: %w (rollback: %v)", err, rollbackErr)
		}
		return fmt.Errorf("activate upgraded Arivu: %w", err)
	}
	return nil
}

func activateUpgrade(ctx context.Context, root string) error {
	if err := runCommand(ctx, "systemctl", "restart", "arivu.service"); err != nil {
		return fmt.Errorf("%w%s", err, serviceFailureDetail(ctx, root))
	}
	if err := runCommand(ctx, "systemctl", "is-active", "--quiet", "arivu.service"); err != nil {
		return fmt.Errorf("%w%s", err, serviceFailureDetail(ctx, root))
	}
	port := 8080
	if opts, err := OptionsFromEnvFile(rootPath(root, "/etc/arivu/arivu.env")); err == nil && opts.BindPort != 0 {
		port = opts.BindPort
	}
	if err := healthCheckFunc(ctx, port); err != nil {
		return fmt.Errorf("%w%s", err, serviceFailureDetail(ctx, root))
	}
	return nil
}

// installExecutableBinary writes a release binary with explicit execute bits.
// os.WriteFile applies the process umask, so chmod is required for hosts with a
// restrictive root umask (for example 0117 → 0640 without chmod).
func installExecutableBinary(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// MkdirAll is also umask-affected; directories need +x to be traversable.
	if err := os.Chmod(dir, 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	_ = os.Remove(tmp)
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Chmod(path, 0o755); err != nil {
		return err
	}
	return nil
}

func serviceFailureDetail(ctx context.Context, root string) string {
	if root != "/" && root != "" {
		return ""
	}
	var b strings.Builder
	appendCommandOutput(&b, ctx, "systemctl", "status", "arivu.service", "--no-pager", "-l")
	appendCommandOutput(&b, ctx, "journalctl", "-u", "arivu.service", "-n", "20", "--no-pager")
	if b.Len() == 0 {
		return ""
	}
	return "\n" + strings.TrimSpace(b.String())
}

func appendCommandOutput(b *strings.Builder, ctx context.Context, name string, args ...string) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if len(out) == 0 && err != nil {
		return
	}
	if b.Len() > 0 {
		b.WriteByte('\n')
	}
	b.Write(bytes.TrimSpace(out))
}

func downloadVerifiedArtifact(ctx context.Context, target string, sums []byte) ([]byte, error) {
	data, err := downloadFunc(ctx, target)
	if err != nil {
		return nil, err
	}
	if err := VerifyChecksum(data, sums, pathBase(target)); err != nil {
		return nil, err
	}
	return data, nil
}

type binaryReplacement struct {
	path        string
	data        []byte
	tmp         string
	previous    string
	hadOriginal bool
	applied     bool
}

func (r *binaryReplacement) prepare() error {
	r.tmp = r.path + ".tmp"
	r.previous = r.path + ".previous"
	if _, err := os.Stat(r.path); errors.Is(err, os.ErrNotExist) {
		if _, previousErr := os.Stat(r.previous); previousErr == nil {
			if err := os.Rename(r.previous, r.path); err != nil {
				return err
			}
		}
	} else if err != nil {
		return err
	}
	dir := filepath.Dir(r.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		return err
	}
	_ = os.Remove(r.tmp)
	if err := os.WriteFile(r.tmp, r.data, 0o755); err != nil {
		return err
	}
	if err := os.Chmod(r.tmp, 0o755); err != nil {
		_ = os.Remove(r.tmp)
		return err
	}
	return nil
}

func (r *binaryReplacement) commit() error {
	if _, err := os.Stat(r.path); err == nil {
		_ = os.Remove(r.previous)
		if err := os.Rename(r.path, r.previous); err != nil {
			return err
		}
		r.hadOriginal = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(r.tmp, r.path); err != nil {
		if r.hadOriginal {
			return errors.Join(err, os.Rename(r.previous, r.path))
		}
		return err
	}
	if err := os.Chmod(r.path, 0o755); err != nil {
		if r.hadOriginal {
			_ = os.Remove(r.path)
			_ = os.Rename(r.previous, r.path)
		}
		return err
	}
	r.applied = true
	return nil
}

func (r *binaryReplacement) rollback() error {
	if !r.applied {
		return nil
	}
	r.applied = false
	if err := os.Remove(r.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if r.hadOriginal {
		return os.Rename(r.previous, r.path)
	}
	return nil
}

func (r *binaryReplacement) cleanup() {
	_ = os.Remove(r.tmp)
	if r.applied {
		_ = os.Remove(r.previous)
	}
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
