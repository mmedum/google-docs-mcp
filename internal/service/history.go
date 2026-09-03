package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/mmedum/google-docs-mcp/internal/gapi"
	"github.com/mmedum/google-docs-mcp/internal/render"
)

// RevisionInfo is one entry of a document's version history. Its id is
// a Drive revision id, distinct from the revision_id the read and write
// tools return (that one is the Docs API's concurrency token).
type RevisionInfo struct {
	ID          string `json:"id"`
	Modified    string `json:"modified,omitempty"`
	ModifiedBy  string `json:"modified_by,omitempty"`
	KeepForever bool   `json:"kept,omitempty"`
}

// RevisionsResult lists revisions, newest first.
type RevisionsResult struct {
	Revisions []RevisionInfo `json:"revisions"`
	Total     int            `json:"total"`
	Text      string         `json:"-"`
}

// ListRevisions returns the newest revisions of a document. Drive may
// omit older revisions of frequently edited documents.
func (s *Service) ListRevisions(ctx context.Context, ref string, limit int) (*RevisionsResult, error) {
	id, err := parseRef(ref)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	revs, err := s.api.ListRevisions(ctx, id)
	if err != nil {
		return nil, wrapAPI(err, "revision history")
	}
	res := &RevisionsResult{Revisions: []RevisionInfo{}, Total: len(revs)}
	for i := len(revs) - 1; i >= 0 && len(res.Revisions) < limit; i-- {
		r := revs[i]
		res.Revisions = append(res.Revisions, RevisionInfo{ID: r.ID, Modified: r.ModifiedTime, ModifiedBy: userLabel(r.LastModifyingUser), KeepForever: r.KeepForever})
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d revision(s), newest first", res.Total)
	if len(res.Revisions) < res.Total {
		fmt.Fprintf(&sb, ", showing %d", len(res.Revisions))
	}
	sb.WriteString(" (Drive may omit older revisions of busy documents)\n")
	for _, r := range res.Revisions {
		fmt.Fprintf(&sb, "- %s", r.ID)
		if r.Modified != "" {
			fmt.Fprintf(&sb, " at %s", r.Modified)
		}
		if r.ModifiedBy != "" {
			fmt.Fprintf(&sb, " by %s", r.ModifiedBy)
		}
		if r.KeepForever {
			sb.WriteString(" (kept)")
		}
		sb.WriteString("\n")
	}
	res.Text = strings.TrimRight(sb.String(), "\n")
	return res, nil
}

// DiffRequest compares two revisions.
type DiffRequest struct {
	Document string
	From     string // Drive revision id
	To       string // Drive revision id; empty means the current content
	Format   string // md (default) or txt
	Context  int
	MaxChars int
}

// DiffResult is a unified diff between two exports.
type DiffResult struct {
	From      string           `json:"from"`
	To        string           `json:"to"`
	Format    string           `json:"format"`
	Stats     render.DiffStats `json:"stats"`
	Truncated bool             `json:"truncated,omitempty"`
	Text      string           `json:"-"`
}

// DiffRevisions exports two revisions through Google's converter, side by
// side, and returns their line-level unified diff.
func (s *Service) DiffRevisions(ctx context.Context, req DiffRequest) (*DiffResult, error) {
	id, err := parseRef(req.Document)
	if err != nil {
		return nil, err
	}
	from := strings.TrimSpace(req.From)
	if from == "" {
		return nil, Errorf("invalid", "from is empty; pass a revision id from list_revisions")
	}
	format, mime, err := exportTextFormat(req.Format)
	if err != nil {
		return nil, err
	}
	type export struct {
		data []byte
		err  error
	}
	oldCh := make(chan export, 1)
	go func() {
		data, err := s.api.ExportRevision(ctx, id, from, mime)
		oldCh <- export{data, err}
	}()
	var newText []byte
	to := strings.TrimSpace(req.To)
	if to == "" {
		to = "current"
		newText, err = s.api.Export(ctx, id, mime)
	} else {
		newText, err = s.api.ExportRevision(ctx, id, to, mime)
	}
	old := <-oldCh
	if old.err != nil {
		return nil, wrapAPI(old.err, "revision "+from)
	}
	if err != nil {
		return nil, wrapAPI(err, "revision "+to)
	}
	contextLines := req.Context
	if contextLines <= 0 {
		contextLines = 2
	}
	maxChars := req.MaxChars
	if maxChars <= 0 {
		maxChars = DefaultMaxChars
	}
	d := render.UnifiedDiff(stripDataURIs(string(old.data)), stripDataURIs(string(newText)), contextLines, maxChars)
	res := &DiffResult{From: from, To: to, Format: format, Stats: d.Stats, Truncated: d.Truncated}
	var sb strings.Builder
	fmt.Fprintf(&sb, "revision %s → %s (%s): +%d −%d lines in %d hunk(s)", from, to, format, d.Stats.Added, d.Stats.Removed, d.Stats.Hunks)
	if d.Truncated {
		sb.WriteString(", truncated; raise max_chars or diff a narrower pair")
	}
	if d.Stats.Hunks == 0 {
		sb.WriteString("\nno differences")
	} else {
		sb.WriteString("\n" + d.Text)
	}
	res.Text = sb.String()
	return res, nil
}

// exportTextFormat maps a short text format name to its MIME type,
// accepting only the formats a diff or an old revision can be read in.
func exportTextFormat(format string) (string, string, error) {
	switch format = normalizeExportFormat(format); format {
	case "", "md":
		return "md", gapi.ExportMimeTypes["md"], nil
	case "txt":
		return "txt", gapi.ExportMimeTypes["txt"], nil
	}
	return "", "", Errorf("invalid", "format %q; use md or txt", format)
}

// ReadRevision returns a markdown or text export of an old revision,
// budgeted. Old revisions have no structure to address, so there are no
// handles or scopes.
func (s *Service) ReadRevision(ctx context.Context, ref, revision, format string, maxChars int) (*ReadResult, error) {
	id, err := parseRef(ref)
	if err != nil {
		return nil, err
	}
	f, mime, err := exportTextFormat(format)
	if err != nil {
		return nil, err
	}
	data, err := s.api.ExportRevision(ctx, id, revision, mime)
	if err != nil {
		return nil, wrapAPI(err, "revision "+revision)
	}
	res := &ReadResult{Revision: revision, Scope: "revision " + revision + " (" + f + " export; no handles, no revision_id)", Segment: "body"}
	res.Text, res.Truncated = clipUTF8(stripDataURIs(string(data)), maxChars)
	res.Chars = len(res.Text)
	res.Text = res.header() + res.Text
	return res, nil
}
