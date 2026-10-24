package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/mmedum/google-docs-mcp/v3/internal/config"
	"github.com/mmedum/google-docs-mcp/v3/internal/gapi"
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

// normalizeExportFormat maps the accepted spellings onto the short names
// ExportMimeTypes is keyed by.
func normalizeExportFormat(format string) string {
	switch format = strings.ToLower(strings.TrimSpace(format)); format {
	case "markdown":
		return "md"
	case "text":
		return "txt"
	}
	return format
}

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
	format := normalizeExportFormat(req.Format)
	mime, ok := gapi.ExportMimeTypes[format]
	if !ok {
		return nil, Errorf("invalid", "format %q; use md, txt, html, pdf, docx, odt, rtf or epub", req.Format)
	}
	id, err := parseRef(req.Document)
	if err != nil {
		return nil, err
	}
	if s.isMarkdown(ctx, id) {
		return s.mdExport(ctx, req, format)
	}
	if !inlineFormats[format] && s.opts.ExportDir == "" {
		return nil, Errorf("unavailable", "binary exports need "+config.EnvPrefix+"EXPORT_DIR to be set to a directory; md, txt and html are returned inline")
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
	dir := filepath.Clean(s.opts.ExportDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, Errorf("unexpected", "create export dir: %v", err)
	}
	path, err := writeExport(dir, fileName(title)+"-"+gapi.ShortID(id), format, data)
	if err != nil {
		return nil, err
	}
	res.Path = path
	res.Text = fmt.Sprintf("exported %q as %s to %s (%d bytes)", title, format, path, len(data))
	return res, nil
}

// maxExportNames bounds the search for a free export file name.
const maxExportNames = 100

// writeExport writes data to a new file in dir, named stem.ext, and
// returns its path. It never overwrites: a name already taken, by an
// earlier export of the same document or by anything else, gains a
// counter (stem-2.ext). O_EXCL makes the choice and the creation one
// step, and it refuses a symbolic link at the name as it refuses a file,
// so nothing is written through a link to a place outside the directory.
// A write that fails removes the file it created.
func writeExport(dir, stem, ext string, data []byte) (string, error) {
	for n := 1; n <= maxExportNames; n++ {
		name := stem + "." + ext
		if n > 1 {
			name = fmt.Sprintf("%s-%d.%s", stem, n, ext)
		}
		path := filepath.Join(dir, name)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err == nil {
			_, err = file.Write(data)
			if cerr := file.Close(); err == nil {
				err = cerr
			}
			if err == nil {
				return path, nil
			}
			_ = os.Remove(path)
		}
		return "", Errorf("unexpected", "write export: %v", err)
	}
	return "", Errorf("blocked", "%d files in the export directory are already named like %s.%s, and an export never "+
		"overwrites one; move or delete earlier exports, then export again", maxExportNames, stem, ext)
}

// fileName turns a document title into a name a person can recognize in
// a directory listing.
//
// Two things an outside user hit, both in the sentence this replaces.
// Substituting a space for each run of unsafe characters turned the
// comma in "Poem, typeset" into a second space, because the space after
// the comma was safe and survived; collapsing runs afterwards is what
// makes a title with punctuation come out looking deliberate. And the
// 80-character limit was a byte slice, so a title with any multi-byte
// character in the wrong place was cut through the middle of a rune —
// unreported, because it needs a long title in a language whose
// characters are not one byte each, which nobody here had exported.
func fileName(title string) string {
	// strings.Fields both collapses the runs and trims the ends.
	name := strings.Join(strings.Fields(unsafeName.ReplaceAllString(title, " ")), " ")
	if name == "" {
		return "document"
	}
	clipped, _ := clipUTF8(name, 80)
	return strings.TrimSpace(clipped)
}
