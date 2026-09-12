package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mmedum/google-docs-mcp/internal/doc"
	"github.com/mmedum/google-docs-mcp/internal/gdocs"
	"github.com/mmedum/google-docs-mcp/internal/plan"
)

// Suggestion is one pending suggested edit.
type Suggestion struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"` // insert, delete, replace, structure, format
	Inserted string `json:"inserted,omitempty"`
	Deleted  string `json:"deleted,omitempty"`
	// Formats is what the suggestion restyles, one entry per thing it
	// touches: "text: bold", "paragraph: alignment". A suggestion can
	// carry both text and formatting, which is what one batch in suggest
	// mode produces when it inserts styled content.
	Formats []string `json:"formats,omitempty"`
	// Restyled is the text a formatting suggestion covers, quoted the way
	// Inserted and Deleted are; empty when it covers no text.
	Restyled string `json:"restyled,omitempty"`
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
	set := &suggestionSet{byID: map[string]*Suggestion{}}
	set.collectEdits(f.Doc)
	set.collectFormats(f.Doc)
	threads := map[string]gdocs.SuggestionThread{}
	for _, t := range f.Wire.Suggestions {
		threads[t.SuggestionID] = t
	}
	res := &SuggestionsResult{RevisionID: f.Doc.RevisionID, Suggestions: set.list(threads)}
	res.Text = renderSuggestions(res)
	return res, nil
}

// suggestionSet collects suggestions by id, keeping the order they were
// first seen in, which is document order.
type suggestionSet struct {
	byID  map[string]*Suggestion
	order []string
}

func (s *suggestionSet) note(id, handle, inserted, deleted string, structure bool) *Suggestion {
	sg, ok := s.byID[id]
	if !ok {
		sg = &Suggestion{ID: id, Handle: handle}
		s.byID[id] = sg
		s.order = append(s.order, id)
	}
	sg.Inserted += inserted
	sg.Deleted += deleted
	if structure && sg.Kind == "" {
		sg.Kind = "structure"
	}
	return sg
}

// collectEdits walks the suggestion ids on blocks and runs: the ids of
// every suggestion that adds or removes something.
func (s *suggestionSet) collectEdits(d *doc.Document) {
	for _, b := range d.AllBlocks() {
		for _, id := range b.Inserted {
			s.note(id, b.Handle, "", "", true)
		}
		for _, id := range b.Deleted {
			s.note(id, b.Handle, "", "", true)
		}
		if b.Paragraph == nil {
			continue
		}
		for _, r := range b.Paragraph.Runs {
			for _, id := range r.Inserted {
				s.note(id, b.Handle, strings.TrimSuffix(r.Text, "\n"), "", false)
			}
			for _, id := range r.Deleted {
				s.note(id, b.Handle, "", strings.TrimSuffix(r.Text, "\n"), false)
			}
		}
	}
}

// collectFormats adds the suggestions that change formatting, which
// collectEdits cannot see: a restyling on its own adds and removes
// nothing, so no run or block carries its id.
//
// The index is already in document order, with the entries belonging to
// no block last, so this reads it straight through. An earlier version
// bucketed it by handle and re-walked the blocks to rebuild that order,
// which produced the same list and put the rule in two packages: a
// carrier added to doc and not here would have been dropped in silence,
// which is the shape of the bug this all comes from.
func (s *suggestionSet) collectFormats(d *doc.Document) {
	for _, fs := range d.FormatSuggestions {
		sg := s.note(fs.ID, fs.Handle, "", "", false)
		sg.Formats = append(sg.Formats, fs.Target+": "+doc.PropList(fs.Props))
		if sg.Restyled == "" {
			sg.Restyled = fs.Text
		}
	}
}

// list classifies each suggestion and merges in the thread details the
// preview provides.
func (s *suggestionSet) list(threads map[string]gdocs.SuggestionThread) []Suggestion {
	out := make([]Suggestion, 0, len(s.order))
	for _, id := range s.order {
		sg := s.byID[id]
		switch {
		case sg.Kind != "":
		case sg.Inserted != "" && sg.Deleted != "":
			sg.Kind = "replace"
		case sg.Inserted != "":
			sg.Kind = "insert"
		case sg.Deleted != "":
			sg.Kind = "delete"
		default:
			// Nothing added and nothing removed: a suggestion that only
			// restyles. Before #46 this branch was the delete case, on the
			// documents where such a suggestion was reported at all.
			sg.Kind = "format"
		}
		if t, ok := threads[id]; ok {
			sg.Author, sg.Status, sg.Summary, sg.Created = t.HeadPost.Author.DisplayName, t.Status, t.SummaryText, t.HeadPost.CreateTime
		}
		out = append(out, *sg)
	}
	return out
}

func renderSuggestions(res *SuggestionsResult) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d pending suggestion(s) at revision %s\n", len(res.Suggestions), res.RevisionID)
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
		for _, fm := range sg.Formats {
			fmt.Fprintf(&sb, " (%s)", fm)
		}
		if sg.Restyled != "" {
			fmt.Fprintf(&sb, " {==%s==}", doc.Clip(sg.Restyled, 80))
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// ReviewRequest accepts or rejects suggestions.
type ReviewRequest struct {
	Document       string
	Action         string // accept, reject, discard
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
	switch action {
	case "accept", "reject", "discard":
	default:
		return nil, Errorf("invalid", "action must be accept, reject or discard")
	}
	if len(req.IDs) == 0 && !req.All {
		return nil, Errorf("invalid", "pass ids or all: true")
	}
	id, err := parseRef(req.Document)
	if err != nil {
		return nil, err
	}
	s.Invalidate(id) // review against the document as it is now, not the cache
	list, err := s.ListSuggestions(ctx, req.Document)
	if err != nil {
		return nil, err
	}
	if err := checkRevision(req.ExpectRevision, list.RevisionID, "reviewing"); err != nil {
		return nil, err
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
		switch action {
		case "accept":
			reqs = append(reqs, plan.AcceptSuggestion(id))
		case "reject":
			reqs = append(reqs, plan.RejectSuggestion(id))
		default:
			reqs = append(reqs, plan.DeleteSuggestion(id))
		}
	}
	f, err := s.Fetch(ctx, req.Document)
	if err != nil {
		return nil, err
	}
	f.Doc.RevisionID = list.RevisionID // guard against the revision the list came from
	_, revision, err := s.batchUpdate(ctx, f, reqs, "")
	if err != nil {
		s.Invalidate(f.Doc.ID)
		return nil, err
	}
	out := &ReviewResult{Action: action, IDs: ids, RevisionID: revision}
	out.Remaining = len(all) - len(ids)
	verb := action + "ed"
	if action == "discard" {
		verb = "discarded"
	}
	out.Text = fmt.Sprintf("%s %d suggestion(s); %d remain pending (revision %s)", verb, len(ids), out.Remaining, out.RevisionID)
	return out, nil
}
