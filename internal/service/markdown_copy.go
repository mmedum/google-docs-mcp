package service

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/mmedum/google-docs-mcp/v3/internal/doc"
	"github.com/mmedum/google-docs-mcp/v3/internal/gapi"
	"github.com/mmedum/google-docs-mcp/v3/internal/mdfile"
	"github.com/mmedum/google-docs-mcp/v3/internal/plan"
	"github.com/mmedum/google-docs-mcp/v3/internal/render"
)

// No API writes through the editor's live session to a Markdown file,
// so a person and Claude cannot type in one together: every write is an
// upload, and an upload makes an open editor reload. A working copy is
// how they can: a Google Doc made from the file, where this server's
// writes merge with the person's typing as in any Google Doc, written
// back into the file when they are done. The copy names its file and
// the revision it was made from in its appProperties, which only this
// OAuth client sees, so the write-back can tell what changed in the copy
// from what changed in the file since (architecture §7.5).

const (
	copySourceKey = "md_source"
	copyBaseKey   = "md_base"
)

// createWorkingCopy is create_document with from_file.
func (s *Service) createWorkingCopy(ctx context.Context, req CreateRequest) (*CreateResult, error) {
	if strings.TrimSpace(req.Content) != "" {
		return nil, Errorf("invalid", "a working copy's content is the file's; leave content out")
	}
	if !s.isMarkdown(ctx, req.FromFile) {
		return nil, Errorf("invalid", "from_file is not a Markdown file; a working copy is made from a .md file Docs opens in place")
	}
	m, err := s.fetchMarkdown(ctx, req.FromFile, nil)
	if err != nil {
		return nil, err
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = strings.TrimSuffix(m.title(), path.Ext(m.title())) + " (working copy)"
	}
	// Front matter stays in the file: Docs' import would make it a rule
	// and a heading.
	body := m.md.Text()[m.md.FrontMatter().End:]
	f, err := s.api.CreateFile(ctx, gapi.NewFile{Name: title, MimeType: gapi.DocumentMimeType,
		AppProperties: map[string]string{copySourceKey: m.id(), copyBaseKey: m.revision()}}, gapi.MarkdownMimeType, []byte(body))
	if err != nil {
		return nil, wrapAPI(err, "working copy")
	}
	s.rememberKind(f.ID, gapi.DocumentMimeType)
	res := &CreateResult{ID: f.ID, Title: f.Name, Kind: KindDocument, URL: doc.DocumentURL(f.ID)}
	if f.Name == "" {
		res.Title = title
	}
	if m.md.FrontMatter().Present {
		res.Warnings = append(res.Warnings, "the file's front matter stays in the file and is not in the copy; writing the copy back keeps it")
	}
	if n := len(m.md.Comments()); n > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf("the file's %d comment(s) stay in the file; comments made in the copy stay in the copy", n))
	}
	res.Warnings = append(res.Warnings, "the copy is a Google Doc: edit it with any write mode, then write_markdown_file writes its changes back into the Markdown file")
	return res, nil
}

// WriteBackRequest writes a working copy back into its Markdown file.
type WriteBackRequest struct {
	Document string // the working copy, a Google Doc
	// File is the Markdown file, when it is not the one the copy was
	// made from.
	File   string
	Force  bool
	DryRun bool
}

var dataImageRE = regexp.MustCompile(`!\[([^\]\n]*)\]\(data:[^)\n]*\)`)

// WriteBack carries what changed in a working copy back into its
// Markdown file, block by block, keeping the file's own text everywhere
// the copy did not change it, its front matter and the comments Docs
// keeps in it; then the copy remembers the revision it now matches.
func (s *Service) WriteBack(ctx context.Context, req WriteBackRequest) (*EditResult, error) {
	if err := s.requireWritable(); err != nil {
		return nil, err
	}
	docID, target, base, err := s.copyTarget(ctx, req)
	if err != nil {
		return nil, err
	}
	export, warnings, err := s.copyExport(ctx, docID)
	if err != nil {
		return nil, err
	}
	baseBody, base, more, err := s.copyBase(ctx, target, base, req.Force)
	if err != nil {
		return nil, err
	}
	warnings = append(warnings, more...)

	res, err := s.mdWrite(ctx, mdWriteOpts{ref: target, dryRun: req.DryRun, force: req.Force},
		func(m *mdFile) (*mdfile.File, *EditResult, []render.Forced, error) {
			cur := m.md.Text()
			fmEnd := m.md.FrontMatter().End
			body := cur[fmEnd:]
			from := baseBody
			if base == "" {
				from = body
			}
			merged, conflicts := mdfile.Merge(from, body, export)
			r := &EditResult{Mode: string(plan.ModeDirect), Applied: 1, Warnings: slices.Clone(warnings),
				Changes: []plan.OpSummary{{Kind: "write_back", Description: "the working copy's changes", Minimal: true}}}
			if len(conflicts) > 0 {
				if !req.Force {
					return nil, nil, nil, Errorf("conflict", "%d change(s) made in the copy are to blocks also changed in the file since the copy was made: %s; "+
						"pass force: true to write the rest and keep the file's version of those", len(conflicts), describeConflicts(conflicts))
				}
				r.Warnings = append(r.Warnings, fmt.Sprintf("forced: %d change(s) made in the copy conflict with changes made in the file since, and the file's version was kept: %s", len(conflicts), describeConflicts(conflicts)))
			}
			p := &mdPlanned{edits: mdfile.MinimalEdits(cur, cur[:fmEnd]+merged, 0), summary: r.Changes}
			p.opOf = make([]int, len(p.edits))
			if err := p.guard(m.md, req.Force); err != nil {
				return nil, nil, nil, err
			}
			r.Warnings = append(r.Warnings, p.warnings...)
			after, err := m.md.Apply(p.edits)
			if err != nil {
				return nil, nil, nil, Errorf("invalid", "%v; nothing was written", err)
			}
			return after, r, p.forced, nil
		})
	if err != nil || req.DryRun {
		return finish(res), err
	}
	if res.RevisionID != "" {
		if err := s.api.SetAppProperties(ctx, docID, map[string]string{copySourceKey: target, copyBaseKey: res.RevisionID}); err != nil {
			res.Warnings = append(res.Warnings, "written, but the copy could not be told which revision of the file it now matches, so the next write-back may see conflicts: "+messageOf(wrapAPI(err, "working copy")))
		}
	}
	return finish(res), nil
}

// copyTarget finds what a write-back writes into: the working copy, the
// Markdown file, and the revision of it the copy was made from ("" when
// that is not known).
func (s *Service) copyTarget(ctx context.Context, req WriteBackRequest) (string, string, string, error) {
	docID, err := parseRef(req.Document)
	if err != nil {
		return "", "", "", err
	}
	if s.isMarkdown(ctx, docID) {
		return "", "", "", Errorf("invalid", "document is the Markdown file itself; pass the Google Doc working copy create_document from_file made")
	}
	meta, err := s.api.GetFile(ctx, docID)
	if err != nil {
		return "", "", "", wrapAPI(err, "working copy")
	}
	if meta.MimeType != "" && meta.MimeType != gapi.DocumentMimeType {
		return "", "", "", Errorf("invalid", "document is not a Google Doc")
	}
	source, base := meta.AppProperties[copySourceKey], meta.AppProperties[copyBaseKey]
	target := source
	if strings.TrimSpace(req.File) != "" {
		if target, err = parseRef(req.File); err != nil {
			return "", "", "", err
		}
	}
	switch {
	case target == "":
		return "", "", "", Errorf("invalid", "document is not a working copy (create_document from_file makes one); pass file to name the Markdown file to write it into")
	case target != source:
		// Not the file it was made from: nothing says what it was.
		base = ""
	}
	if !s.isMarkdown(ctx, target) {
		return "", "", "", Errorf("invalid", "file is not a Markdown file")
	}
	return docID, target, base, nil
}

// copyExport is the working copy as Docs exports it, with images that
// have no address a Markdown file could point at left empty.
func (s *Service) copyExport(ctx context.Context, docID string) (string, []string, error) {
	data, err := s.api.Export(ctx, docID, gapi.MarkdownMimeType)
	if err != nil {
		return "", nil, wrapAPI(err, "working copy")
	}
	export := string(data)
	n := len(dataImageRE.FindAllString(export, -1))
	if n == 0 {
		return export, nil, nil
	}
	return dataImageRE.ReplaceAllString(export, "![$1]()"),
		[]string{fmt.Sprintf("%d image(s) placed in the copy have no address a Markdown file can point at; they are written as empty images to fill in", n)}, nil
}

// copyBase is the file's text, front matter left out, at the revision the
// copy was made from. When that revision is gone, forced, the write goes
// on without it ("" base), and otherwise it is refused.
func (s *Service) copyBase(ctx context.Context, target, base string, force bool) (string, string, []string, error) {
	if base == "" {
		return "", "", nil, nil
	}
	old, err := s.api.DownloadRevision(ctx, target, base)
	switch {
	case err == nil:
		f := mdfile.Parse(string(old))
		return f.Text()[f.FrontMatter().End:], base, nil, nil
	case force:
		return "", "", []string{"the revision the copy was made from is gone from the file's history, so the copy is written over the file's current text, block by block"}, nil
	}
	return "", "", nil, Errorf("conflict", "the revision of the file the copy was made from (%s) is gone from its history (%s), so what changed in the file since cannot be told from what changed in the copy; "+
		"pass force: true to write the copy over the file's current text, block by block", base, messageOf(wrapAPI(err, "revision "+base)))
}

func finish(res *EditResult) *EditResult {
	if res != nil {
		res.Text = res.text()
	}
	return res
}

func describeConflicts(cs []mdfile.Conflict) string {
	parts := make([]string, 0, min(len(cs), 3))
	for _, c := range cs[:min(len(cs), 3)] {
		parts = append(parts, fmt.Sprintf("%q", doc.Clip(doc.OneLine(c.Base), 50)))
	}
	if len(cs) > 3 {
		parts = append(parts, fmt.Sprintf("and %d more", len(cs)-3))
	}
	return strings.Join(parts, ", ")
}
