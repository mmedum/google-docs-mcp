package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/mmedum/google-docs-mcp/internal/doc"
	"github.com/mmedum/google-docs-mcp/internal/gapi"
)

// ExportRequest converts a document.
type ExportRequest struct {
	Document string
	Format   string
	MaxChars int
}

// ExportResult is either inline text or a file path.
type ExportResult struct {
	Format    string `json:"format"`
	MimeType  string `json:"mime_type"`
	Inline    bool   `json:"inline"`
	Path      string `json:"path,omitempty"`
	Bytes     int    `json:"bytes"`
	Truncated bool   `json:"truncated,omitempty"`
	Text      string `json:"-"`
}

var inlineFormats = map[string]bool{"md": true, "txt": true, "html": true}

// dataURI matches the base64 image payloads Google's markdown and HTML
// exports embed; they are noise to a reader and swamp any budget.
var dataURI = regexp.MustCompile(`data:[a-z]+/[a-z0-9.+-]+;base64,[A-Za-z0-9+/=]{16,}`)

// stripDataURIs replaces embedded base64 payloads with a short marker.
func stripDataURIs(text string) string {
	return dataURI.ReplaceAllStringFunc(text, func(m string) string {
		return strings.SplitN(m, ";base64,", 2)[0] + ";base64,<omitted>"
	})
}

// clipUTF8 cuts text to at most limit bytes on a character boundary.
func clipUTF8(text string, limit int) (string, bool) {
	if limit <= 0 || len(text) <= limit {
		return text, false
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut], true
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._ -]+`)

// Export downloads the document as md, txt or html (returned inline) or
// pdf, docx, odt, rtf, epub (written under the export directory).
func (s *Service) Export(ctx context.Context, req ExportRequest) (*ExportResult, error) {
	format := strings.ToLower(strings.TrimSpace(req.Format))
	if format == "markdown" {
		format = "md"
	}
	if format == "text" {
		format = "txt"
	}
	mime, ok := gapi.ExportMimeTypes[format]
	if !ok {
		return nil, Errorf("invalid", "format %q; use md, txt, html, pdf, docx, odt, rtf or epub", req.Format)
	}
	id, err := doc.ParseID(req.Document)
	if err != nil {
		return nil, Errorf("invalid", "%q is not a Google Docs document id or URL", req.Document)
	}
	if !inlineFormats[format] && s.opts.ExportDir == "" {
		return nil, Errorf("unavailable", "binary exports need GDOCS_EXPORT_DIR to be set to a directory; md, txt and html are returned inline")
	}
	data, err := s.api.Export(ctx, id, mime)
	if err != nil {
		return nil, wrapAPI(err, "export")
	}
	res := &ExportResult{Format: format, MimeType: mime, Bytes: len(data)}
	if inlineFormats[format] {
		res.Inline = true
		limit := req.MaxChars
		if limit <= 0 {
			limit = DefaultMaxChars
		}
		res.Text, res.Truncated = clipUTF8(stripDataURIs(string(data)), limit)
		return res, nil
	}
	// Drive metadata is a small call; the title names the file.
	title := "document"
	if file, err := s.api.GetFile(ctx, id); err == nil && file.Name != "" {
		title = file.Name
	}
	name := strings.TrimSpace(unsafeName.ReplaceAllString(title, " "))
	if name == "" {
		name = "document"
	}
	if len(name) > 80 {
		name = name[:80]
	}
	dir := filepath.Clean(s.opts.ExportDir)
	path := filepath.Join(dir, fmt.Sprintf("%s-%s.%s", name, gapi.ShortID(id), format))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, Errorf("unexpected", "create export dir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return nil, Errorf("unexpected", "write export: %v", err)
	}
	res.Path = path
	res.Text = fmt.Sprintf("exported %q as %s to %s (%d bytes)", title, format, path, len(data))
	return res, nil
}
