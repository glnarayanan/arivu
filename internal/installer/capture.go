package installer

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	captureInstallPath       = "/usr/local/lib/arivu-capture"
	captureServicePath       = "/etc/systemd/system/arivu-capture.service"
	captureArchiveMaxBytes   = int64(512 << 20)
	captureExtractMaxBytes   = int64(2 << 30)
	captureExtractMaxEntries = 50000
)

var (
	captureArchiveDownloadFunc = downloadCaptureArchive
	captureHTTPClientFunc      = newCaptureHTTPClient
)

const (
	captureArchiveDownloadTimeout = 8 * time.Minute
	captureResponseHeaderTimeout  = 30 * time.Second
)

type directoryReplacement struct {
	path        string
	tmp         string
	previous    string
	hadOriginal bool
	applied     bool
}

func prepareCaptureRuntime(ctx context.Context, root, artifactURL, sumsURL string) (*directoryReplacement, error) {
	if err := validateDownloadURL(artifactURL); err != nil {
		return nil, err
	}
	if err := validateDownloadURL(sumsURL); err != nil {
		return nil, err
	}
	sums, err := downloadFunc(ctx, sumsURL)
	if err != nil {
		return nil, err
	}
	return prepareCaptureRuntimeFromSums(ctx, root, artifactURL, sums)
}

func prepareCaptureRuntimeFromSums(ctx context.Context, root, artifactURL string, sums []byte) (*directoryReplacement, error) {
	target := rootPath(root, captureInstallPath)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, err
	}
	archivePath, cleanup, err := captureArchiveDownloadFunc(ctx, artifactURL, sums, filepath.Dir(target))
	if err != nil {
		return nil, err
	}
	defer cleanup()
	replacement := &directoryReplacement{path: target}
	if err := replacement.prepare(archivePath); err != nil {
		replacement.cleanup()
		return nil, err
	}
	if root == "/" || root == "" {
		if err := ensureCaptureUser(ctx); err != nil {
			replacement.cleanup()
			return nil, err
		}
		if err := runCommand(ctx, filepath.Join(replacement.tmp, "node"), filepath.Join(replacement.tmp, "node_modules/playwright/cli.js"), "install-deps", "chromium"); err != nil {
			replacement.cleanup()
			return nil, fmt.Errorf("install capture host libraries: %w", err)
		}
		if err := validateCaptureRuntime(ctx, replacement.tmp); err != nil {
			replacement.cleanup()
			return nil, err
		}
	}
	return replacement, nil
}

func ensureCaptureUser(ctx context.Context) error {
	if _, err := user.Lookup("arivu-capture"); err == nil {
		return nil
	}
	return runCommand(ctx, "useradd", "--system", "--no-create-home", "--gid", "arivu", "--shell", "/usr/sbin/nologin", "arivu-capture")
}

func validateCaptureRuntime(ctx context.Context, dir string) error {
	node := filepath.Join(dir, "node")
	preflight := filepath.Join(dir, "src/preflight.mjs")
	env := []string{
		"ARIVU_MONOLITH_PATH=" + filepath.Join(dir, "monolith"),
		"PLAYWRIGHT_BROWSERS_PATH=" + filepath.Join(dir, "browsers"),
	}
	args := append([]string{"-u", "arivu-capture", "--", "env"}, env...)
	args = append(args, node, preflight)
	if err := runCommand(ctx, "runuser", args...); err != nil {
		return fmt.Errorf("validate capture runtime: %w", err)
	}
	return nil
}

func (r *directoryReplacement) prepare(archivePath string) error {
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
	tmp, err := os.MkdirTemp(filepath.Dir(r.path), ".arivu-capture-")
	if err != nil {
		return err
	}
	r.tmp = tmp
	if err := os.Chmod(r.tmp, 0o755); err != nil {
		return err
	}
	if err := extractCaptureArchive(archivePath, r.tmp); err != nil {
		return err
	}
	return validateCaptureLayout(r.tmp)
}

func (r *directoryReplacement) commit() error {
	if _, err := os.Stat(r.path); err == nil {
		_ = os.RemoveAll(r.previous)
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
	r.applied = true
	return nil
}

func (r *directoryReplacement) rollback() error {
	if !r.applied {
		return nil
	}
	r.applied = false
	if err := os.RemoveAll(r.path); err != nil {
		return err
	}
	if r.hadOriginal {
		return os.Rename(r.previous, r.path)
	}
	return nil
}

func (r *directoryReplacement) cleanup() {
	if r.tmp != "" {
		_ = os.RemoveAll(r.tmp)
	}
	if r.applied {
		_ = os.RemoveAll(r.previous)
	}
}

func extractCaptureArchive(archivePath, destination string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open capture archive: %w", err)
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	var total int64
	entries := 0
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read capture archive: %w", err)
		}
		entries++
		if entries > captureExtractMaxEntries || header.Size < 0 || total > captureExtractMaxBytes-header.Size {
			return errors.New("capture archive exceeds extraction limits")
		}
		total += header.Size
		cleanName := path.Clean(header.Name)
		if path.IsAbs(header.Name) || cleanName == ".." || strings.HasPrefix(cleanName, "../") {
			return errors.New("capture archive contains an unsafe path")
		}
		name := strings.TrimPrefix(cleanName, "./")
		if name == "." || name == "" {
			continue
		}
		target := filepath.Join(destination, filepath.FromSlash(name))
		if !strings.HasPrefix(target, destination+string(os.PathSeparator)) {
			return errors.New("capture archive escaped its staging directory")
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(header.Mode) & 0o755
			if mode&0o400 == 0 {
				mode |= 0o400
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.CopyN(out, reader, header.Size)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("capture archive contains unsupported entry %q", header.Name)
		}
	}
	return nil
}

func validateCaptureLayout(root string) error {
	for _, relative := range []string{"node", "monolith", "src/index.mjs", "src/preflight.mjs", "node_modules/playwright/cli.js"} {
		info, err := os.Stat(filepath.Join(root, relative))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("capture runtime is missing %s", relative)
		}
		if (relative == "node" || relative == "monolith") && info.Mode().Perm()&0o111 == 0 {
			return fmt.Errorf("capture runtime %s is not executable", relative)
		}
	}
	if info, err := os.Stat(filepath.Join(root, "browsers")); err != nil || !info.IsDir() {
		return errors.New("capture runtime is missing browsers")
	}
	return nil
}

func downloadCaptureArchive(ctx context.Context, target string, sums []byte, dir string) (string, func(), error) {
	expected, err := checksumFor(sums, pathBase(target))
	if err != nil {
		return "", func() {}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", func() {}, err
	}
	client := captureHTTPClientFunc()
	response, err := client.Do(req)
	if err != nil {
		return "", func() {}, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		return "", func() {}, fmt.Errorf("download %s: status %d", target, response.StatusCode)
	}
	file, err := os.CreateTemp(dir, ".arivu-capture-*.tar.gz")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.Remove(file.Name()) }
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, captureArchiveMaxBytes+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		cleanup()
		return "", func() {}, errors.Join(copyErr, closeErr)
	}
	if written > captureArchiveMaxBytes {
		cleanup()
		return "", func() {}, errors.New("capture archive exceeds download limit")
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected {
		cleanup()
		return "", func() {}, fmt.Errorf("checksum mismatch for %s", pathBase(target))
	}
	return file.Name(), cleanup, nil
}

func newCaptureHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = captureResponseHeaderTimeout
	return &http.Client{Transport: transport, Timeout: captureArchiveDownloadTimeout, CheckRedirect: func(request *http.Request, _ []*http.Request) error {
		if request.URL.Scheme != "https" {
			return errors.New("capture archive redirect must use https")
		}
		return nil
	}}
}

func checksumFor(sums []byte, name string) (string, error) {
	expected, ok := checksumFromManifest(sums, name)
	if !ok || len(expected) != sha256.Size*2 {
		return "", fmt.Errorf("checksum for %s not found", name)
	}
	if _, err := hex.DecodeString(expected); err != nil {
		return "", fmt.Errorf("checksum for %s not found", name)
	}
	return expected, nil
}

func removeCaptureRuntime(ctx context.Context, root string) error {
	if root == "/" || root == "" {
		_ = runCommand(ctx, "systemctl", "disable", "--now", "arivu-capture.service")
	}
	if err := os.Remove(rootPath(root, captureServicePath)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.RemoveAll(rootPath(root, captureInstallPath))
}

func CaptureInstallStatus(root string) (configured bool, installed bool) {
	if opts, err := OptionsFromEnvFile(rootPath(root, "/etc/arivu/arivu.env")); err == nil {
		configured = opts.CaptureEnabled
	}
	if info, err := os.Stat(rootPath(root, captureInstallPath)); err == nil && info.IsDir() {
		installed = true
	}
	return configured, installed
}
