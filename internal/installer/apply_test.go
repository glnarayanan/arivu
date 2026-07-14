package installer

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/glnarayanan/arivu/internal/database"
)

func createBackupDB(t *testing.T, path string) {
	t.Helper()
	db, err := database.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

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
	assetPath := filepath.Join(dataDir, "arivu.sqlite3.assets", "objects", "ab", "artifact")
	if err := os.MkdirAll(filepath.Dir(assetPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(assetPath, []byte("preserved"), 0o600); err != nil {
		t.Fatal(err)
	}

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
	artifact, err := os.ReadFile(filepath.Join(backupDir, "arivu.sqlite3.assets", "objects", "ab", "artifact"))
	if err != nil {
		t.Fatalf("read backed-up artifact: %v", err)
	}
	if string(artifact) != "preserved" {
		t.Fatalf("backed-up artifact = %q", artifact)
	}
}

func TestRestoreRequiresPrimaryBackupDatabase(t *testing.T) {
	if err := Restore(t.TempDir(), t.TempDir()); err == nil {
		t.Fatal("expected missing primary backup database to fail")
	}
}

func TestRestoreRejectsCorruptManifestAndAcceptsLegacyBackup(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "var/lib/arivu"), 0o750); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(t.TempDir(), "legacy")
	if err := os.MkdirAll(legacy, 0o750); err != nil {
		t.Fatal(err)
	}
	createBackupDB(t, filepath.Join(legacy, "arivu.sqlite3"))
	if err := Restore(root, legacy); err != nil {
		t.Fatalf("legacy restore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "manifest.json"), []byte(`{"version":1,"files":[{"path":"arivu.sqlite3","size":6,"sha256":"bad"}]}`), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := Restore(root, legacy); err == nil || !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("corrupt manifest error=%v", err)
	}
}

func TestRootRestoreChecksHealthBeforeBackupTimer(t *testing.T) {
	root := restoreFixture(t, "ARIVU_ADDR=127.0.0.1:8123\nARIVU_BACKUPS_ENABLED=true\n")
	backupDir := filepath.Join(t.TempDir(), "backup")
	if err := os.MkdirAll(backupDir, 0o750); err != nil {
		t.Fatal(err)
	}
	createBackupDB(t, filepath.Join(backupDir, "arivu.sqlite3"))

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
	createBackupDB(t, filepath.Join(backupDir, "arivu.sqlite3"))

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
	if indexCommand(commands, "systemctl stop arivu.service") < 0 || indexCommand(commands, "systemctl start arivu-backup.timer") < 0 {
		t.Fatalf("old service and timer were not restored after health failure: %#v", commands)
	}
}

func TestRootReconfigureDisablesBackupTimerWhenBackupsDisabled(t *testing.T) {
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
	healthCheckFunc = func(context.Context, int) error { return nil }

	err := activateRootServices(context.Background(), Plan{
		Options:     Options{Reconfigure: true, BackupEnabled: false},
		BindPort:    8080,
		ProxyMode:   ProxyAppOnly,
		BindAddress: "127.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if indexCommand(commands, "systemctl disable --now arivu-backup.timer") < 0 {
		t.Fatalf("disabled reconfigure did not stop backup timer: %#v", commands)
	}
	if indexCommand(commands, "systemctl enable --now arivu-backup.timer") >= 0 {
		t.Fatalf("disabled reconfigure enabled backup timer: %#v", commands)
	}
}

func TestRootFreshInstallWithBackupsDisabledDoesNotManageBackupTimer(t *testing.T) {
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
	healthCheckFunc = func(context.Context, int) error { return nil }

	err := activateRootServices(context.Background(), Plan{
		Options:     Options{Reconfigure: false, BackupEnabled: false},
		BindPort:    8080,
		ProxyMode:   ProxyAppOnly,
		BindAddress: "127.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if indexCommand(commands, "systemctl disable --now arivu-backup.timer") >= 0 || indexCommand(commands, "systemctl enable --now arivu-backup.timer") >= 0 {
		t.Fatalf("fresh install with disabled backups managed backup timer: %#v", commands)
	}
}

func TestCaptureServiceStartsBeforeArivu(t *testing.T) {
	var commands []string
	oldRun := runCommand
	oldHealth := healthCheckFunc
	t.Cleanup(func() {
		runCommand = oldRun
		healthCheckFunc = oldHealth
	})
	runCommand = func(_ context.Context, name string, args ...string) error {
		commands = append(commands, name+" "+strings.Join(args, " "))
		return nil
	}
	healthCheckFunc = func(context.Context, int) error { return nil }

	err := activateRootServices(context.Background(), Plan{Options: Options{CaptureEnabled: true}, BindPort: 8080})
	if err != nil {
		t.Fatal(err)
	}
	captureIndex := indexCommand(commands, "systemctl enable --now arivu-capture.service")
	appIndex := indexCommand(commands, "systemctl enable --now arivu.service")
	if captureIndex < 0 || appIndex < 0 || captureIndex > appIndex {
		t.Fatalf("capture did not start before app: %#v", commands)
	}
}

func TestCaptureArchiveExtractionRejectsTraversalAndLinks(t *testing.T) {
	for name, entry := range map[string]tar.Header{
		"traversal": {Name: "../outside", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg},
		"symlink":   {Name: "node", Mode: 0o777, Typeflag: tar.TypeSymlink, Linkname: "/bin/sh"},
	} {
		t.Run(name, func(t *testing.T) {
			archive := writeCaptureTar(t, []tar.Header{entry}, [][]byte{[]byte("x")})
			if err := extractCaptureArchive(archive, t.TempDir()); err == nil {
				t.Fatal("expected unsafe capture archive to fail")
			}
		})
	}
}

func TestCaptureReleaseArchive(t *testing.T) {
	archive := os.Getenv("ARIVU_CAPTURE_RELEASE_ARCHIVE")
	if archive == "" {
		t.Skip("set ARIVU_CAPTURE_RELEASE_ARCHIVE to validate a packaged runtime")
	}
	destination := t.TempDir()
	if err := extractCaptureArchive(archive, destination); err != nil {
		t.Fatal(err)
	}
	if err := validateCaptureLayout(destination); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureArchiveDownloadTimesOutBeforeResponseHeaders(t *testing.T) {
	oldClient := captureHTTPClientFunc
	captureHTTPClientFunc = func() *http.Client {
		return &http.Client{
			Timeout: 25 * time.Millisecond,
			Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				<-request.Context().Done()
				return nil, request.Context().Err()
			}),
		}
	}
	t.Cleanup(func() { captureHTTPClientFunc = oldClient })
	sum := sha256.Sum256(nil)
	sums := []byte(fmt.Sprintf("%x  archive.tar.gz\n", sum))

	_, cleanup, err := downloadCaptureArchive(context.Background(), "https://release.example/archive.tar.gz", sums, t.TempDir())
	cleanup()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("download error = %v", err)
	}
}

func TestCaptureHTTPClientBoundsHeadersAndTotalDownload(t *testing.T) {
	client := newCaptureHTTPClient()
	if client.Timeout != 8*time.Minute {
		t.Fatalf("client timeout = %s", client.Timeout)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.ResponseHeaderTimeout != 30*time.Second {
		t.Fatalf("response header timeout = %#v", client.Transport)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestCaptureDirectoryReplacementRollsBack(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "arivu-capture")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "version"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	archive := validCaptureArchive(t, "new")
	replacement := &directoryReplacement{path: target}
	if err := replacement.prepare(archive); err != nil {
		t.Fatal(err)
	}
	defer replacement.cleanup()
	if err := replacement.commit(); err != nil {
		t.Fatal(err)
	}
	assertFileContentMode(t, filepath.Join(target, "version"), "new", 0o644)
	if err := replacement.rollback(); err != nil {
		t.Fatal(err)
	}
	assertFileContentMode(t, filepath.Join(target, "version"), "old", 0o644)
}

func TestUpgradeReplacesAppAndInstallerFromSameVerifiedRelease(t *testing.T) {
	root := upgradeFixture(t)
	restoreClient := upgradeReleaseDownloads(t, []byte("new app"), []byte("new installer"))
	defer restoreClient()

	err := upgrade(context.Background(), HostFacts{Arch: "amd64"}, ApplyOptions{
		ArtifactURL:          "https://release.example/arivu-linux-amd64",
		InstallerArtifactURL: "https://release.example/arivu-installer-linux-amd64",
		ChecksumsURL:         "https://release.example/SHA256SUMS",
	}, "v1.2.3", root, func(context.Context, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filepath.Join(root, "usr/local/bin/arivu"), "new app")
	assertFileContent(t, filepath.Join(root, "usr/local/bin/arivu-installer"), "new installer")
}

func TestUpgradeRollsBackBothBinariesWhenActivationFails(t *testing.T) {
	root := upgradeFixture(t)
	restoreClient := upgradeReleaseDownloads(t, []byte("new app"), []byte("new installer"))
	defer restoreClient()

	err := upgrade(context.Background(), HostFacts{Arch: "amd64"}, ApplyOptions{
		ArtifactURL:          "https://release.example/arivu-linux-amd64",
		InstallerArtifactURL: "https://release.example/arivu-installer-linux-amd64",
		ChecksumsURL:         "https://release.example/SHA256SUMS",
	}, "v1.2.3", root, func(context.Context, string) error { return errors.New("service unhealthy") })
	if err == nil || !strings.Contains(err.Error(), "service unhealthy") {
		t.Fatalf("upgrade error = %v", err)
	}
	assertFileContent(t, filepath.Join(root, "usr/local/bin/arivu"), "old app")
	assertFileContent(t, filepath.Join(root, "usr/local/bin/arivu-installer"), "old installer")
}

func TestUpgradeWithCustomAppArtifactPreservesInstaller(t *testing.T) {
	root := upgradeFixture(t)
	restoreClient := upgradeReleaseDownloads(t, []byte("custom app"), []byte("unused installer"))
	defer restoreClient()

	err := upgrade(context.Background(), HostFacts{Arch: "amd64"}, ApplyOptions{
		ArtifactURL:  "https://release.example/arivu-linux-amd64",
		ChecksumsURL: "https://release.example/SHA256SUMS",
	}, "", root, func(context.Context, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filepath.Join(root, "usr/local/bin/arivu"), "custom app")
	assertFileContent(t, filepath.Join(root, "usr/local/bin/arivu-installer"), "old installer")
}

func TestUpgradeReplacesAndRollsBackConfiguredCaptureRuntime(t *testing.T) {
	for _, activationFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("activation_fails_%v", activationFails), func(t *testing.T) {
			root := upgradeFixture(t)
			if err := os.MkdirAll(filepath.Join(root, "etc/arivu"), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "etc/arivu/arivu.env"), []byte("ARIVU_BROWSER_CAPTURE_ENABLED=true\n"), 0o640); err != nil {
				t.Fatal(err)
			}
			captureDir := filepath.Join(root, "usr/local/lib/arivu-capture")
			if err := os.MkdirAll(captureDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(captureDir, "version"), []byte("old"), 0o644); err != nil {
				t.Fatal(err)
			}
			restoreDownloads := upgradeReleaseDownloads(t, []byte("new app"), []byte("new installer"))
			defer restoreDownloads()
			archive := validCaptureArchive(t, "new")
			oldCaptureDownload := captureArchiveDownloadFunc
			captureArchiveDownloadFunc = func(context.Context, string, []byte, string) (string, func(), error) {
				return archive, func() {}, nil
			}
			defer func() { captureArchiveDownloadFunc = oldCaptureDownload }()

			activate := func(context.Context, string) error {
				if activationFails {
					return errors.New("capture unhealthy")
				}
				return nil
			}
			err := upgrade(context.Background(), HostFacts{Arch: "amd64"}, ApplyOptions{
				ArtifactURL:          "https://release.example/arivu-linux-amd64",
				InstallerArtifactURL: "https://release.example/arivu-installer-linux-amd64",
				CaptureArtifactURL:   "https://release.example/arivu-capture-linux-amd64.tar.gz",
				ChecksumsURL:         "https://release.example/SHA256SUMS",
			}, "v1.2.3", root, activate)
			if activationFails {
				if err == nil || !strings.Contains(err.Error(), "capture unhealthy") {
					t.Fatalf("upgrade error = %v", err)
				}
				assertFileContentMode(t, filepath.Join(captureDir, "version"), "old", 0o644)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			assertFileContentMode(t, filepath.Join(captureDir, "version"), "new", 0o644)
		})
	}
}

func TestUpgradeRejectsCaptureArtifactWhenCaptureIsDisabled(t *testing.T) {
	root := upgradeFixture(t)
	restoreDownloads := upgradeReleaseDownloads(t, []byte("new app"), []byte("new installer"))
	defer restoreDownloads()

	err := upgrade(context.Background(), HostFacts{Arch: "amd64"}, ApplyOptions{
		ArtifactURL:        "https://release.example/arivu-linux-amd64",
		CaptureArtifactURL: "https://release.example/arivu-capture-linux-amd64.tar.gz",
		ChecksumsURL:       "https://release.example/SHA256SUMS",
	}, "v1.2.3", root, func(context.Context, string) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "complete capture is disabled") {
		t.Fatalf("upgrade error = %v", err)
	}
}

func TestRemoveCaptureRuntimePreservesOtherArivuFiles(t *testing.T) {
	root := t.TempDir()
	service := rootPath(root, captureServicePath)
	runtime := rootPath(root, captureInstallPath)
	data := rootPath(root, "/var/lib/arivu/arivu.db")
	for _, dir := range []string{filepath.Dir(service), runtime, filepath.Dir(data)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{service, filepath.Join(runtime, "node"), data} {
		if err := os.WriteFile(file, []byte("preserve only data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := removeCaptureRuntime(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{service, runtime} {
		if _, err := os.Stat(removed); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("capture path still exists: %s", removed)
		}
	}
	assertFileContentMode(t, data, "preserve only data", 0o644)
}

func TestInstallExecutableBinaryPreservesExecuteBitUnderRestrictiveUmask(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bin", "arivu")
	old := syscall.Umask(0o117)
	t.Cleanup(func() { syscall.Umask(old) })

	if err := installExecutableBinary(path, []byte("#!/bin/sh\n")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("installed binary mode = %o, want execute bits set", info.Mode().Perm())
	}
}

func TestBinaryReplacementCommitPreservesExecuteBitUnderRestrictiveUmask(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usr/local/bin/arivu")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	old := syscall.Umask(0o117)
	t.Cleanup(func() { syscall.Umask(old) })

	replacement := &binaryReplacement{path: path, data: []byte("new binary")}
	if err := replacement.prepare(); err != nil {
		t.Fatal(err)
	}
	if err := replacement.commit(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("upgraded binary mode = %o, want execute bits set", info.Mode().Perm())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new binary" {
		t.Fatalf("content = %q", got)
	}
}

func upgradeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "usr/local/bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "arivu"), []byte("old app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "arivu-installer"), []byte("old installer"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func upgradeReleaseDownloads(t *testing.T, app, installer []byte) func() {
	t.Helper()
	appSum := sha256.Sum256(app)
	installerSum := sha256.Sum256(installer)
	sums := fmt.Sprintf("%x  arivu-linux-amd64\n%x  arivu-installer-linux-amd64\n", appSum, installerSum)
	oldDownload := downloadFunc
	downloadFunc = func(_ context.Context, target string) ([]byte, error) {
		switch pathBase(target) {
		case "arivu-linux-amd64":
			return app, nil
		case "arivu-installer-linux-amd64":
			return installer, nil
		case "SHA256SUMS":
			return []byte(sums), nil
		default:
			return nil, fmt.Errorf("unexpected download %s", target)
		}
	}
	return func() { downloadFunc = oldDownload }
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	assertFileContentMode(t, path, want, 0o755)
}

func assertFileContentMode(t *testing.T, path, want string, mode os.FileMode) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != mode {
		t.Fatalf("%s mode = %o, want %o", path, info.Mode().Perm(), mode)
	}
}

func validCaptureArchive(t *testing.T, version string) string {
	t.Helper()
	headers := []tar.Header{
		{Name: "node", Mode: 0o755, Size: 4, Typeflag: tar.TypeReg},
		{Name: "monolith", Mode: 0o755, Size: 8, Typeflag: tar.TypeReg},
		{Name: "src/index.mjs", Mode: 0o644, Size: 5, Typeflag: tar.TypeReg},
		{Name: "src/preflight.mjs", Mode: 0o644, Size: 9, Typeflag: tar.TypeReg},
		{Name: "node_modules/playwright/cli.js", Mode: 0o644, Size: 3, Typeflag: tar.TypeReg},
		{Name: "browsers", Mode: 0o755, Typeflag: tar.TypeDir},
		{Name: "version", Mode: 0o644, Size: int64(len(version)), Typeflag: tar.TypeReg},
	}
	bodies := [][]byte{[]byte("node"), []byte("monolith"), []byte("index"), []byte("preflight"), []byte("cli"), nil, []byte(version)}
	return writeCaptureTar(t, headers, bodies)
}

func writeCaptureTar(t *testing.T, headers []tar.Header, bodies [][]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "capture.tar.gz")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gz)
	for index := range headers {
		header := headers[index]
		if err := tarWriter.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if header.Size > 0 {
			if _, err := tarWriter.Write(bodies[index]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
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
