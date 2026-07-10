package bookmarks

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	stdhtml "html"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/glnarayanan/arivu/internal/auth"
	"github.com/glnarayanan/arivu/internal/ids"
	nethtml "golang.org/x/net/html"
)

const (
	maxMediaImportBytes = 8 << 20
	maxMediaTextBytes   = maxNoteBody - 2_000
)

var supportedEPUBExtensions = []string{".html", ".htm", ".xhtml", ".xml", ".txt"}

type mediaImportInput struct {
	Title       string `json:"title"`
	SourceURL   string `json:"source_url"`
	SourceType  string `json:"source_type"`
	Text        string `json:"text"`
	OCRText     string `json:"ocr_text"`
	Transcript  string `json:"transcript"`
	Filename    string
	ContentType string
	Data        []byte
}

// ImportMedia turns uploaded documents, images, and transcript payloads into
// normal notes so the existing inbox, export, and search flows can index them.
func (s *Service) ImportMedia(w http.ResponseWriter, r *http.Request, user auth.User) {
	input, err := parseMediaImport(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	kind := mediaSourceKind(input)
	text, err := s.extractMediaText(r.Context(), kind, input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	text = truncateString(cleanWhitespace(text), maxMediaTextBytes)
	if text == "" {
		writeError(w, http.StatusBadRequest, "Text, transcript, OCR text, or a supported readable file is required")
		return
	}
	title := mediaTitle(input)
	body := buildMediaNoteBody(kind, input, text)
	if len(body) > maxNoteBody {
		body = truncateString(body, maxNoteBody)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	id := ids.New()
	source := "media:" + kind
	if _, err := s.db.ExecContext(r.Context(), `INSERT INTO notes(id,user_id,title,body,source,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, id, user.ID, title, body, source, now, now); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not create media note")
		return
	}
	_ = s.upsertItemState(r.Context(), user.ID, "note", id, "inbox", 0, "", now)
	note, _ := s.note(r.Context(), user.ID, id)
	s.decorateNote(r.Context(), user.ID, note)
	s.refreshSearchIndex(r.Context(), user.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"note": note,
		"media": map[string]any{
			"source_type": kind,
			"source_url":  strings.TrimSpace(input.SourceURL),
			"filename":    strings.TrimSpace(input.Filename),
		},
	})
}

func parseMediaImport(r *http.Request) (mediaImportInput, error) {
	contentType := r.Header.Get("Content-Type")
	mediaType, _, _ := mime.ParseMediaType(contentType)
	if strings.HasPrefix(mediaType, "multipart/") {
		return parseMultipartMediaImport(r)
	}
	var input mediaImportInput
	if err := decodeJSON(r, &input); err != nil {
		return input, fmt.Errorf("Invalid request")
	}
	return input, nil
}

func parseMultipartMediaImport(r *http.Request) (mediaImportInput, error) {
	if err := r.ParseMultipartForm(maxMediaImportBytes); err != nil {
		return mediaImportInput{}, fmt.Errorf("Could not read uploaded media")
	}
	input := mediaImportInput{
		Title:      r.FormValue("title"),
		SourceURL:  r.FormValue("source_url"),
		SourceType: r.FormValue("source_type"),
		Text:       r.FormValue("text"),
		OCRText:    r.FormValue("ocr_text"),
		Transcript: r.FormValue("transcript"),
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		if err == http.ErrMissingFile {
			return input, nil
		}
		return input, fmt.Errorf("Could not read uploaded file")
	}
	defer file.Close()
	input.Filename = header.Filename
	input.ContentType = header.Header.Get("Content-Type")
	raw, err := io.ReadAll(io.LimitReader(file, maxMediaImportBytes+1))
	if err != nil {
		return input, fmt.Errorf("Could not read uploaded file")
	}
	if len(raw) > maxMediaImportBytes {
		return input, fmt.Errorf("Uploaded file is too large")
	}
	input.Data = raw
	return input, nil
}

func mediaSourceKind(input mediaImportInput) string {
	explicit := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(input.SourceType), "media:"))
	if explicit != "" {
		return safeMediaKind(explicit)
	}
	if isYouTubeURL(input.SourceURL) {
		return "youtube"
	}
	ext := strings.ToLower(filepath.Ext(input.Filename))
	switch ext {
	case ".epub":
		return "epub"
	case ".pdf":
		return "pdf"
	case ".png", ".jpg", ".jpeg", ".webp", ".gif", ".heic", ".tif", ".tiff":
		return "image"
	case ".html", ".htm":
		return "html"
	case ".md":
		return "markdown"
	case ".txt", ".text":
		return "text"
	}
	contentType := strings.ToLower(strings.TrimSpace(input.ContentType))
	switch {
	case strings.Contains(contentType, "epub"):
		return "epub"
	case strings.Contains(contentType, "pdf"):
		return "pdf"
	case strings.HasPrefix(contentType, "image/"):
		return "image"
	case strings.Contains(contentType, "html"):
		return "html"
	case strings.HasPrefix(contentType, "text/"):
		return "text"
	case strings.TrimSpace(input.Transcript) != "":
		return "transcript"
	}
	return "document"
}

func safeMediaKind(kind string) string {
	kind = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, strings.ToLower(kind))
	kind = strings.Trim(kind, "-_")
	if kind == "" {
		return "document"
	}
	return kind
}

func isYouTubeURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "youtube.com" || host == "www.youtube.com" || host == "m.youtube.com" || host == "youtu.be"
}

func (s *Service) extractMediaText(ctx context.Context, kind string, input mediaImportInput) (string, error) {
	if text := firstNonEmpty(input.Transcript, input.OCRText, input.Text); text != "" {
		return text, nil
	}
	if len(input.Data) == 0 {
		return "", nil
	}
	switch kind {
	case "epub":
		return extractEPUBText(input.Data)
	case "html":
		text, err := decodeTextBytes(input.Data, input.ContentType)
		if err != nil {
			return "", err
		}
		return htmlToText(text), nil
	case "pdf":
		return extractPDFText(input.Data), nil
	case "image":
		return s.extractImageText(ctx, input), nil
	default:
		text, err := decodeTextBytes(input.Data, input.ContentType)
		if err != nil {
			return "", err
		}
		return text, nil
	}
}

func extractEPUBText(data []byte) (string, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("Could not read EPUB")
	}
	var parts []string
	for _, file := range reader.File {
		ext := strings.ToLower(filepath.Ext(file.Name))
		if !slices.Contains(supportedEPUBExtensions, ext) {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(rc, maxMediaTextBytes))
		_ = rc.Close()
		if err != nil {
			continue
		}
		text, err := decodeTextBytes(raw, "")
		if err != nil {
			continue
		}
		if ext == ".txt" {
			parts = append(parts, text)
		} else {
			parts = append(parts, htmlToText(text))
		}
		if len(strings.Join(parts, "\n\n")) >= maxMediaTextBytes {
			break
		}
	}
	return strings.Join(parts, "\n\n"), nil
}

func decodeTextBytes(data []byte, contentType string) (string, error) {
	_ = contentType
	if len(data) > maxMediaTextBytes {
		data = data[:maxMediaTextBytes]
	}
	return string(data), nil
}

func htmlToText(raw string) string {
	doc, err := nethtml.Parse(strings.NewReader(raw))
	if err != nil {
		return stripMarkup(raw)
	}
	var out strings.Builder
	var walk func(*nethtml.Node)
	walk = func(n *nethtml.Node) {
		if n.Type == nethtml.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "noscript") {
			return
		}
		if n.Type == nethtml.TextNode {
			out.WriteString(n.Data)
			out.WriteByte(' ')
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
		if n.Type == nethtml.ElementNode && (n.Data == "p" || n.Data == "div" || strings.HasPrefix(n.Data, "h") || n.Data == "li" || n.Data == "br") {
			out.WriteByte('\n')
		}
	}
	walk(doc)
	return stdhtml.UnescapeString(out.String())
}

func stripMarkup(raw string) string {
	tagPattern := regexp.MustCompile(`(?s)<script.*?</script>|<style.*?</style>|<[^>]+>`)
	return stdhtml.UnescapeString(tagPattern.ReplaceAllString(raw, " "))
}

func extractPDFText(data []byte) string {
	text := string(data)
	text = regexp.MustCompile(`\\([nrt])`).ReplaceAllString(text, " ")
	text = regexp.MustCompile(`(?s)<</.*?>>|/[\w#]+|\b\d+\s+\d+\s+obj\b|endobj|stream|endstream`).ReplaceAllString(text, " ")
	var out strings.Builder
	for _, r := range text {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			out.WriteByte(' ')
		case unicode.IsPrint(r) && (unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsPunct(r) || unicode.IsSpace(r) || unicode.IsSymbol(r)):
			out.WriteRune(r)
		default:
			out.WriteByte(' ')
		}
		if out.Len() >= maxMediaTextBytes {
			break
		}
	}
	return printableChunks(out.String())
}

func printableChunks(raw string) string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '(' || r == ')' || r == '[' || r == ']'
	})
	var chunks []string
	for _, field := range fields {
		field = cleanWhitespace(field)
		if len(field) < 6 {
			continue
		}
		letters := 0
		for _, r := range field {
			if unicode.IsLetter(r) {
				letters++
			}
		}
		if letters >= 3 {
			chunks = append(chunks, field)
		}
	}
	return strings.Join(chunks, "\n")
}

func (s *Service) extractImageText(ctx context.Context, input mediaImportInput) string {
	contentType := strings.TrimSpace(input.ContentType)
	if contentType == "" {
		contentType = http.DetectContentType(input.Data)
	}
	text, err := s.aiClient(ctx).ExtractImageText(ctx, contentType, input.Data)
	if err != nil {
		return ""
	}
	return text
}

func mediaTitle(input mediaImportInput) string {
	if title := strings.TrimSpace(input.Title); title != "" {
		return title
	}
	if input.Filename != "" {
		name := strings.TrimSuffix(filepath.Base(input.Filename), filepath.Ext(input.Filename))
		return fallback(name, "Imported media")
	}
	if input.SourceURL != "" {
		return input.SourceURL
	}
	return "Imported media"
}

func buildMediaNoteBody(kind string, input mediaImportInput, text string) string {
	var parts []string
	parts = append(parts, "# "+mediaTitle(input))
	parts = append(parts, "Source type: "+kind)
	if strings.TrimSpace(input.SourceURL) != "" {
		parts = append(parts, "Source URL: "+strings.TrimSpace(input.SourceURL))
	}
	if strings.TrimSpace(input.Filename) != "" {
		parts = append(parts, "File: "+strings.TrimSpace(input.Filename))
	}
	parts = append(parts, "", strings.TrimSpace(text))
	return strings.Join(parts, "\n")
}

func cleanWhitespace(raw string) string {
	return strings.TrimSpace(regexp.MustCompile(`[ \t\r\f\v]+`).ReplaceAllString(strings.ReplaceAll(raw, "\u00a0", " "), " "))
}

func truncateString(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return strings.TrimSpace(value[:limit])
}
