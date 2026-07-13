// Package browsercapture implements the optional, dependency-free helper boundary.
package browsercapture

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/glnarayanan/arivu/internal/config"
	"github.com/glnarayanan/arivu/internal/safefetch"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Artifact struct {
	Type string `json:"type"`
	MIME string `json:"mime"`
	Path string `json:"path"`
}
type request struct {
	Version       int      `json:"version"`
	URL           string   `json:"url"`
	OutputDir     string   `json:"output_dir"`
	Token         string   `json:"token"`
	Formats       []string `json:"formats"`
	MaxFileBytes  int64    `json:"max_file_bytes"`
	MaxTotalBytes int64    `json:"max_total_bytes"`
}
type response struct {
	Version   int        `json:"version"`
	Token     string     `json:"token"`
	Artifacts []Artifact `json:"artifacts"`
	ErrorCode string     `json:"error_code"`
}
type Error struct{ Code string }

func (e *Error) Error() string { return e.Code }
func code(s string) error      { return &Error{Code: s} }

func Run(ctx context.Context, cfg config.BrowserCaptureConfig, rawURL string, ingest func(Artifact, io.Reader) error) error {
	if !cfg.Enabled {
		return nil
	}
	if cfg.Command == "" {
		return code("browser_helper_not_configured")
	}
	if err := safefetch.ValidateURL(rawURL); err != nil {
		return code("browser_invalid_url")
	}
	u, _ := url.Parse(rawURL)
	if u.Scheme != "http" && u.Scheme != "https" {
		return code("browser_invalid_url")
	}
	dir, err := os.MkdirTemp("", "arivu-browser-")
	if err != nil {
		return code("browser_staging_failed")
	}
	defer os.RemoveAll(dir)
	_ = os.Chmod(dir, 0700)
	token := rand.Text()
	formats := requestedFormats(cfg)
	req := request{1, rawURL, dir, token, formats, cfg.MaxFileBytes, cfg.MaxTotalBytes}
	in, _ := json.Marshal(req)
	cctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, cfg.Command)
	cmd.Stdin = strings.NewReader(string(in))
	var out limitedBuffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		if errors.Is(cctx.Err(), context.DeadlineExceeded) {
			return code("browser_timeout")
		}
		return code("browser_helper_failed")
	}
	var res response
	if out.overflow {
		return code("browser_invalid_response")
	}
	dec := json.NewDecoder(strings.NewReader(out.String()))
	dec.DisallowUnknownFields()
	if dec.Decode(&res) != nil || res.Version != 1 || res.Token != token {
		return code("browser_invalid_response")
	}
	helperError := res.ErrorCode
	base, _ := filepath.Abs(dir)
	var total int64
	allowed := map[string]string{"screenshot": "image/png", "pdf": "application/pdf", "self_contained_html": "text/html"}
	wanted, seen := map[string]bool{}, map[string]bool{}
	for _, format := range formats {
		wanted[format] = true
	}
	for _, a := range res.Artifacts {
		expected, ok := allowed[a.Type]
		if !ok || !wanted[a.Type] || seen[a.Type] || a.MIME != expected {
			return code("browser_invalid_artifact")
		}
		seen[a.Type] = true
		p, err := filepath.Abs(filepath.Join(dir, a.Path))
		if err != nil || p == base || !strings.HasPrefix(p, base+string(os.PathSeparator)) {
			return code("browser_path_escape")
		}
		info, err := os.Lstat(p)
		if err != nil || !info.Mode().IsRegular() || info.Size() > cfg.MaxFileBytes {
			return code("browser_output_too_large")
		}
		total += info.Size()
		if total > cfg.MaxTotalBytes {
			return code("browser_output_too_large")
		}
		f, err := os.Open(p)
		if err != nil {
			return code("browser_artifact_unreadable")
		}
		err = ingest(a, io.LimitReader(f, cfg.MaxFileBytes+1))
		f.Close()
		if err != nil {
			return fmt.Errorf("browser_ingest_failed: %w", err)
		}
	}
	for format := range wanted {
		if !seen[format] && helperError == "" {
			return code("browser_missing_artifact")
		}
	}
	if helperError != "" {
		return code(helperError)
	}
	return nil
}

type limitedBuffer struct {
	b        strings.Builder
	overflow bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := (64 << 10) - b.b.Len()
	if remaining > 0 {
		if remaining < len(p) {
			_, _ = b.b.Write(p[:remaining])
		} else {
			_, _ = b.b.Write(p)
		}
	}
	if n > remaining {
		b.overflow = true
	}
	return n, nil
}

func (b *limitedBuffer) String() string { return b.b.String() }

func requestedFormats(cfg config.BrowserCaptureConfig) []string {
	formats := make([]string, 0, 3)
	if cfg.SelfContainedHTML {
		formats = append(formats, "self_contained_html")
	}
	if cfg.Screenshot {
		formats = append(formats, "screenshot")
	}
	if cfg.PDF {
		formats = append(formats, "pdf")
	}
	return formats
}
