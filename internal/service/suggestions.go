package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mmedum/google-docs-mcp/internal/doc"
	"github.com/mmedum/google-docs-mcp/internal/gapi"
	"github.com/mmedum/google-docs-mcp/internal/gdocs"
	"github.com/mmedum/google-docs-mcp/internal/plan"
)

// Suggestion is one pending suggested edit.
type Suggestion struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"` // insert, delete, replace, structure
	Inserted string `json:"inserted,omitempty"`
	Deleted  string `json:"deleted,omitempty"`
	Handle   string `json:"handle"`
	Author   string `json:"author,omitempty"`
	Status   string `json:"status,omitempty"`
	Summary  string `json:"summary,omitempty"`
	Created  string `json:"created,omitempty"`
}

// SuggestionsResult lists pending suggestions.
type SuggestionsResult struct {
	RevisionID  string       `json:"revision_id"`
	Suggestions []Suggestion `json:"suggestions"`
	Text        string       `json:"-"`
}

// ListSuggestions collects suggestions from the inline view, in document
// order, merged with thread details when the preview provides them.
func (s *Service) ListSuggestions(ctx context.Context, ref string) (*SuggestionsResult, error) {
	f, err := s.Fetch(ctx, ref)
	if err != nil {
		return nil, err
	}
	byID := map[string]*Suggestion{}
	var order []string
	note := func(id, handle, inserted, deleted string, structure bool) {
		sg, ok := byID[id]
		if !ok {
			sg = &Suggestion{ID: id, Handle: handle}
			byID[id] = sg
			order = append(order, id)
		}
		sg.Inserted += inserted
		sg.Deleted += deleted
		if structure && sg.Kind == "" {
			sg.Kind = "structure"
		}
	}
	for _, b := range f.Doc.AllBlocks() {
		for _, id := range b.Inserted {
			note(id, b.Handle, "", "", true)
		}
		for _, id := range b.Deleted {
			note(id, b.Handle, "", "", true)
		}
		if b.Paragraph == nil {
			continue
		}
		for _, r := range b.Paragraph.Runs {
			for _, id := range r.Inserted {
				note(id, b.Handle, strings.TrimSuffix(r.Text, "\n"), "", false)
			}
			for _, id := range r.Deleted {
				note(id, b.Handle, "", strings.TrimSuffix(r.Text, "\n"), false)
			}
		}
	}
	res := &SuggestionsResult{RevisionID: f.Doc.RevisionID, Suggestions: []Suggestion{}}
	threads := map[string]gdocs.SuggestionThread{}
	for _, t := range f.Wire.Suggestions {
		threads[t.SuggestionID] = t
	}
	for _, id := range order {
		sg := byID[id]
		switch {
		case sg.Kind != "":
		case sg.Inserted != "" && sg.Deleted != "":
			sg.Kind = "replace"
		case sg.Inserted != "":
			sg.Kind = "insert"
		default:
			sg.Kind = "delete"
		}
		if t, ok := threads[id]; ok {
			sg.Author, sg.Status, sg.Summary, sg.Created = t.HeadPost.Author.DisplayName, t.Status, t.SummaryText, t.HeadPost.CreateTime
		}
		res.Suggestions = append(res.Suggestions, *sg)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d pending suggestion(s) at revision %s\n", len(res.Suggestions), f.Doc.RevisionID)
	for _, sg := range res.Suggestions {
		fmt.Fprintf(&sb, "- %s [%s] %s", sg.ID, sg.Handle, sg.Kind)
		if sg.Author != "" {
			fmt.Fprintf(&sb, " by %s", sg.Author)
		}
		if sg.Deleted != "" {
			fmt.Fprintf(&sb, " {--%s--}", doc.Clip(sg.Deleted, 80))
		}
		if sg.Inserted != "" {
			fmt.Fprintf(&sb, " {++%s++}", doc.Clip(sg.Inserted, 80))
		}
		sb.WriteString("\n")
	}
	res.Text = strings.TrimRight(sb.String(), "\n")
	return res, nil
}

// ReviewRequest accepts or rejects suggestions.
type ReviewRequest struct {
	Document       string
	Action         string // accept, reject
	IDs            []string
	All            bool
	ExpectRevision string
}

// ReviewResult reports what was reviewed.
type ReviewResult struct {
	RevisionID string   `json:"revision_id"`
	Action     string   `json:"action"`
	IDs        []string `json:"ids"`
	Remaining  int      `json:"remaining"`
	Text       string   `json:"-"`
}

// Review accepts or rejects suggestions (Developer Preview).
func (s *Service) Review(ctx context.Context, req ReviewRequest) (*ReviewResult, error) {
	if err := s.requireWritable(); err != nil {
		return nil, err
	}
	if !s.opts.Preview {
		return nil, Errorf("unavailable", "accepting or rejecting suggestions needs Developer Preview enrolment (GDOCS_PREVIEW=true)")
	}
	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action != "accept" && action != "reject" {
		return nil, Errorf("invalid", "action must be accept or reject")
	}
	if len(req.IDs) == 0 && !req.All {
		return nil, Errorf("invalid", "pass ids or all: true")
	}
	list, err := s.ListSuggestions(ctx, req.Document)
	if err != nil {
		return nil, err
	}
	if req.ExpectRevision != "" && req.ExpectRevision != list.RevisionID {
		return nil, Errorf("conflict", "the document is at revision %s, not %s; re-read before reviewing", list.RevisionID, req.ExpectRevision)
	}
	pending := map[string]bool{}
	var all []string
	for _, sg := range list.Suggestions {
		pending[sg.ID] = true
		all = append(all, sg.ID)
	}
	ids := req.IDs
	if req.All {
		ids = all
	}
	if len(ids) == 0 {
		return nil, Errorf("not_found", "no pending suggestions")
	}
	for _, id := range ids {
		if !pending[id] {
			return nil, Errorf("not_found", "suggestion %q is not pending at this revision; call list_suggestions", id)
		}
	}
	reqs := make([]json.RawMessage, 0, len(ids))
	for _, id := range ids {
		if action == "accept" {
			reqs = append(reqs, plan.AcceptSuggestion(id))
		} else {
			reqs = append(reqs, plan.RejectSuggestion(id))
		}
	}
	f, err := s.Fetch(ctx, req.Document)
	if err != nil {
		return nil, err
	}
	res, err := s.api.BatchUpdate(ctx, f.Doc.ID, &gapi.BatchUpdateRequest{Requests: reqs, WriteControl: &gapi.WriteControl{RequiredRevisionID: list.RevisionID}})
	if err != nil {
		return nil, wrapWriteError(err)
	}
	s.Invalidate(f.Doc.ID)
	out := &ReviewResult{Action: action, IDs: ids, RevisionID: list.RevisionID}
	if res.WriteControl != nil && res.WriteControl.RequiredRevisionID != "" {
		out.RevisionID = res.WriteControl.RequiredRevisionID
	}
	out.Remaining = len(all) - len(ids)
	out.Text = fmt.Sprintf("%sed %d suggestion(s); %d remain pending (revision %s)", action, len(ids), out.Remaining, out.RevisionID)
	return out, nil
}
