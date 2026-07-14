package browsercapture

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glnarayanan/arivu/internal/capture"
	"github.com/glnarayanan/arivu/internal/config"
	"github.com/glnarayanan/arivu/internal/safefetch"
)

const maxV2HeaderBytes = 256 << 10

type V2Metadata struct {
	FinalURL     string `json:"final_url"`
	CanonicalURL string `json:"canonical_url"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	Byline       string `json:"byline"`
	SiteName     string `json:"site_name"`
	Language     string `json:"language"`
	PublishedAt  string `json:"published_at"`
}

// V2File describes a payload in the byte stream. Path is assigned by Go after
// the payload has been copied into a private directory.
type V2File struct {
	MIME string `json:"mime"`
	Size int64  `json:"size"`
	Path string `json:"-"`
}

type V2Content struct {
	HTML           V2File          `json:"html"`
	Text           V2File          `json:"text"`
	QualityStatus  capture.Quality `json:"quality_status"`
	QualityScore   int             `json:"quality_score"`
	QualityReasons []string        `json:"quality_reasons"`
	Challenge      bool            `json:"challenge"`
}

type V2Artifact struct {
	Type string `json:"type"`
	V2File
}

type V2Media struct {
	SourceURL string `json:"source_url"`
	Role      string `json:"role"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	V2File
}

type V2Component struct {
	Status    string `json:"status"`
	ErrorCode string `json:"error_code"`
}

type V2Result struct {
	root          string
	EngineVersion string                 `json:"engine_version"`
	Metadata      V2Metadata             `json:"metadata"`
	Content       V2Content              `json:"content"`
	Artifacts     []V2Artifact           `json:"artifacts"`
	Media         []V2Media              `json:"media"`
	Components    map[string]V2Component `json:"components"`
	ErrorCode     string                 `json:"error_code"`
}

type v2Request struct {
	Version             int      `json:"version"`
	URL                 string   `json:"url"`
	Token               string   `json:"token"`
	ProxySocket         string   `json:"proxy_socket"`
	ProxyToken          string   `json:"proxy_token"`
	Formats             []string `json:"formats"`
	AttemptTimeoutMS    int64    `json:"attempt_timeout_ms"`
	NavigationTimeoutMS int64    `json:"navigation_timeout_ms"`
	MaxFileBytes        int64    `json:"max_file_bytes"`
	MaxTotalBytes       int64    `json:"max_total_bytes"`
	MaxMediaFiles       int      `json:"max_media_files"`
	MaxMediaFileBytes   int64    `json:"max_media_file_bytes"`
	MaxMediaTotalBytes  int64    `json:"max_media_total_bytes"`
}

type v2Response struct {
	Version int    `json:"version"`
	Token   string `json:"token"`
	V2Result
}

// RunV2 obtains one fully validated rendered capture before invoking ingest.
// The helper only controls a manifest and byte stream; it never controls or can
// mutate the files passed to ingest.
func RunV2(ctx context.Context, cfg config.BrowserCaptureConfig, rawURL string, ingest func(V2Result) error) error {
	if !cfg.Enabled {
		return nil
	}
	if cfg.Protocol != 2 || cfg.Socket == "" {
		return code("browser_v2_not_configured")
	}
	if err := safefetch.ValidateURL(rawURL); err != nil {
		return code("browser_invalid_url")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	captureContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	runtimeDir := cfg.RuntimeDir
	attemptDir, err := os.MkdirTemp(runtimeDir, "arivu-capture-")
	if err != nil {
		return code("browser_runtime_failed")
	}
	defer os.RemoveAll(attemptDir)
	if err := os.Chmod(attemptDir, 0o770); err != nil {
		return code("browser_runtime_failed")
	}

	request := v2Request{
		Version: 2, URL: rawURL, Token: rand.Text(), ProxySocket: filepath.Join(attemptDir, "egress.sock"), ProxyToken: rand.Text(),
		Formats: requestedFormats(cfg), AttemptTimeoutMS: timeout.Milliseconds(), NavigationTimeoutMS: cfg.NavigationTimeout.Milliseconds(),
		MaxFileBytes: cfg.MaxFileBytes, MaxTotalBytes: cfg.MaxTotalBytes, MaxMediaFiles: cfg.MaxMediaFiles,
		MaxMediaFileBytes: cfg.MaxMediaFileBytes, MaxMediaTotalBytes: cfg.MaxMediaTotalBytes,
	}
	proxy, err := safefetch.StartCaptureProxy(captureContext, request.ProxySocket, request.ProxyToken, safefetch.ProxyLimits{
		MaxResponseBytes: cfg.MaxFileBytes,
		MaxTotalBytes:    cfg.MaxTotalBytes,
	})
	if err != nil {
		return code("browser_proxy_failed")
	}
	defer proxy.Close()

	result, err := callV2(captureContext, cfg.Socket, request, cfg)
	if err != nil {
		return err
	}
	if result.Content.HTML.Size == 0 {
		return code(result.ErrorCode)
	}
	defer os.RemoveAll(result.root)
	if err := ingest(result); err != nil {
		return fmt.Errorf("browser_ingest_failed: %w", err)
	}
	if result.ErrorCode != "" {
		return code(result.ErrorCode)
	}
	return nil
}

func callV2(ctx context.Context, socket string, request v2Request, cfg config.BrowserCaptureConfig) (V2Result, error) {
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", socket)
	if err != nil {
		if contextTimedOut(ctx) {
			return V2Result{}, code("browser_timeout")
		}
		return V2Result{}, code("browser_helper_unavailable")
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		if contextTimedOut(ctx) {
			return V2Result{}, code("browser_timeout")
		}
		return V2Result{}, code("browser_helper_failed")
	}
	reader := bufio.NewReaderSize(conn, maxV2HeaderBytes+1)
	response, err := decodeV2Header(reader)
	if err != nil {
		if contextTimedOut(ctx) {
			return V2Result{}, code("browser_timeout")
		}
		return V2Result{}, err
	}
	if err := validateV2Header(request.Token, request.Formats, cfg, response); err != nil {
		return V2Result{}, err
	}
	if response.Content.HTML.Size == 0 {
		if _, err := reader.ReadByte(); !errors.Is(err, io.EOF) {
			return V2Result{}, code("browser_invalid_response")
		}
		return response.V2Result, nil
	}
	root, err := os.MkdirTemp("", "arivu-capture-result-")
	if err != nil {
		return V2Result{}, code("browser_staging_failed")
	}
	if err := os.Chmod(root, 0o700); err != nil {
		os.RemoveAll(root)
		return V2Result{}, code("browser_staging_failed")
	}
	response.root = root
	if err := receiveV2Files(reader, root, &response.V2Result); err != nil {
		os.RemoveAll(root)
		if contextTimedOut(ctx) {
			return V2Result{}, code("browser_timeout")
		}
		return V2Result{}, err
	}
	return response.V2Result, nil
}

func contextTimedOut(ctx context.Context) bool {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return true
	}
	deadline, ok := ctx.Deadline()
	return ok && !time.Now().Before(deadline)
}

func decodeV2Header(reader *bufio.Reader) (v2Response, error) {
	raw, err := reader.ReadSlice('\n')
	if err != nil || len(raw) > maxV2HeaderBytes+1 {
		return v2Response{}, code("browser_invalid_response")
	}
	raw = raw[:len(raw)-1]
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var response v2Response
	if err := decoder.Decode(&response); err != nil {
		return v2Response{}, code("browser_invalid_response")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return v2Response{}, code("browser_invalid_response")
	}
	return response, nil
}

func validateV2Header(token string, formats []string, cfg config.BrowserCaptureConfig, response v2Response) error {
	if response.Version != 2 || response.Token != token || strings.TrimSpace(response.EngineVersion) == "" || len(response.EngineVersion) > 128 || !validErrorCode(response.ErrorCode) {
		return code("browser_invalid_response")
	}
	if response.Metadata.FinalURL != "" && safefetch.ValidateURL(response.Metadata.FinalURL) != nil {
		return code("browser_invalid_metadata")
	}
	if response.Metadata.CanonicalURL != "" && safefetch.ValidateURL(response.Metadata.CanonicalURL) != nil {
		return code("browser_invalid_metadata")
	}
	hasContent := response.Content.HTML.Size != 0 || response.Content.Text.Size != 0
	if !hasContent {
		if response.ErrorCode == "" || response.Content.QualityStatus != capture.QualityFailed || response.Content.QualityScore != 0 || len(response.Artifacts) != 0 || len(response.Media) != 0 {
			return code("browser_invalid_content")
		}
		return validateComponents(response.Components)
	}
	if response.Metadata.FinalURL == "" || response.Content.HTML.MIME != "text/html" || response.Content.Text.MIME != "text/plain" ||
		response.Content.HTML.Size <= 0 || response.Content.Text.Size <= 0 || response.Content.HTML.Size > cfg.MaxFileBytes || response.Content.Text.Size > cfg.MaxFileBytes ||
		!validReaderQuality(response.Content) || len(response.Media) > cfg.MaxMediaFiles {
		return code("browser_invalid_content")
	}
	if _, ok := response.Components["browser"]; !ok {
		return code("browser_invalid_response")
	}
	if _, ok := response.Components["readability"]; !ok {
		return code("browser_invalid_response")
	}
	wanted := make(map[string]bool)
	for _, format := range formats {
		wanted[format] = true
	}
	seenArtifacts := make(map[string]bool)
	var total int64
	if !addBounded(&total, response.Content.HTML.Size, cfg.MaxTotalBytes) || !addBounded(&total, response.Content.Text.Size, cfg.MaxTotalBytes) {
		return code("browser_output_too_large")
	}
	allowedArtifacts := map[string]string{"screenshot": "image/jpeg", "pdf": "application/pdf", "self_contained_html": "text/html"}
	for _, artifact := range response.Artifacts {
		mime, ok := allowedArtifacts[artifact.Type]
		if !ok || mime != artifact.MIME || !wanted[artifact.Type] || seenArtifacts[artifact.Type] || artifact.Size <= 0 || artifact.Size > cfg.MaxFileBytes {
			return code("browser_invalid_artifact")
		}
		seenArtifacts[artifact.Type] = true
		if !addBounded(&total, artifact.Size, cfg.MaxTotalBytes) {
			return code("browser_output_too_large")
		}
	}
	allowedMedia := map[string]bool{"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true}
	seenMedia := make(map[string]bool)
	var mediaTotal int64
	for _, media := range response.Media {
		if !allowedMedia[media.MIME] || media.Role != "reader_image" || seenMedia[media.SourceURL] || safefetch.ValidateURL(media.SourceURL) != nil || media.Size <= 0 || media.Size > cfg.MaxMediaFileBytes || media.Width < 0 || media.Height < 0 {
			return code("browser_invalid_media")
		}
		seenMedia[media.SourceURL] = true
		if !addBounded(&mediaTotal, media.Size, cfg.MaxMediaTotalBytes) || !addBounded(&total, media.Size, cfg.MaxTotalBytes) {
			return code("browser_output_too_large")
		}
	}
	for format := range wanted {
		if !seenArtifacts[format] && response.ErrorCode == "" {
			return code("browser_missing_artifact")
		}
	}
	return validateComponents(response.Components)
}

func receiveV2Files(reader *bufio.Reader, root string, result *V2Result) error {
	type fileTarget struct {
		name string
		file *V2File
	}
	files := []fileTarget{{"content.html", &result.Content.HTML}, {"content.txt", &result.Content.Text}}
	for i := range result.Artifacts {
		files = append(files, fileTarget{fmt.Sprintf("artifact-%02d", i), &result.Artifacts[i].V2File})
	}
	for i := range result.Media {
		files = append(files, fileTarget{fmt.Sprintf("media-%03d", i), &result.Media[i].V2File})
	}
	for _, item := range files {
		if item.file.Size == 0 {
			continue
		}
		path := filepath.Join(root, item.name)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return code("browser_staging_failed")
		}
		_, copyErr := io.CopyN(file, reader, item.file.Size)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			return code("browser_invalid_response")
		}
		item.file.Path = path
	}
	if _, err := reader.ReadByte(); !errors.Is(err, io.EOF) {
		return code("browser_invalid_response")
	}
	return nil
}

func validReaderQuality(content V2Content) bool {
	if content.QualityScore < 0 || content.QualityScore > 100 {
		return false
	}
	for _, reason := range content.QualityReasons {
		if reason == "" || !validErrorCode(reason) {
			return false
		}
	}
	return content.QualityStatus == capture.QualityComplete || content.QualityStatus == capture.QualityPartial
}

func addBounded(total *int64, size, limit int64) bool {
	if size <= 0 || limit <= 0 || *total > limit-size {
		return false
	}
	*total += size
	return true
}

func validateComponents(components map[string]V2Component) error {
	for name, component := range components {
		if name == "" || !validErrorCode(name) || !validErrorCode(component.ErrorCode) {
			return code("browser_invalid_response")
		}
		switch component.Status {
		case "complete":
			if component.ErrorCode != "" {
				return code("browser_invalid_response")
			}
		case "partial", "failed":
			if component.ErrorCode == "" {
				return code("browser_invalid_response")
			}
		default:
			return code("browser_invalid_response")
		}
	}
	return nil
}

func validErrorCode(value string) bool {
	if len(value) > 64 {
		return false
	}
	for _, r := range value {
		if r != '_' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}
