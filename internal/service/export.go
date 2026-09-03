package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

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
	f, err := s.Fetch(ctx, req.Document)
	if err != nil {
		return nil, err
	}
	if !inlineFormats[format] && s.opts.ExportDir == "" {
		return nil, Errorf("unavailable", "binary exports need GDOCS_EXPORT_DIR to be set to a directory; md, txt and html are returned inline")
	}
	data, err := s.api.Export(ctx, f.Doc.ID, mime)
	if err != nil {
		return nil, wrapAPI(err, "export")
	}
	res := &ExportResult{Format: format, MimeType: mime, Bytes: len(data)}
	if inlineFormats[format] {
		res.Inline = true
		text := string(data)
		limit := req.MaxChars
		if limit <= 0 {
			limit = 20000
		}
		if len(text) > limit {
			cut := limit
			for cut > 0 && !utf8.RuneStart(text[cut]) {
				cut--
			}
			text = text[:cut]
			res.Truncated = true
		}
		res.Text = text
		return res, nil
	}
	name := strings.TrimSpace(unsafeName.ReplaceAllString(f.Doc.Title, " "))
	if name == "" {
		name = "document"
	}
	if len(name) > 80 {
		name = name[:80]
	}
	dir := filepath.Clean(s.opts.ExportDir)
	path := filepath.Join(dir, fmt.Sprintf("%s-%s.%s", name, shortID(f.Doc.ID), format))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, Errorf("unexpected", "create export dir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return nil, Errorf("unexpected", "write export: %v", err)
	}
	res.Path = path
	res.Text = fmt.Sprintf("exported %q as %s to %s (%d bytes)", f.Doc.Title, format, path, len(data))
	return res, nil
}
