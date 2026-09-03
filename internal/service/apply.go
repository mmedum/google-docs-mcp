package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mmedum/google-docs-mcp/internal/doc"
	"github.com/mmedum/google-docs-mcp/internal/gapi"
	"github.com/mmedum/google-docs-mcp/internal/plan"
	"github.com/mmedum/google-docs-mcp/internal/render"
)

type replyEnvelope struct {
	Replies             []map[string]json.RawMessage `json:"replies"`
	SuggestionResponses []struct {
		CreatedSuggestionIDs []string `json:"createdSuggestionIds"`
	} `json:"suggestionResponses"`
	CommentUpdateState string `json:"commentUpdateState"`
}

// apply sends a plan to Google in the chosen mode and records ids.
func (s *Service) apply(ctx context.Context, f *Fetched, planned *plan.Result, mode plan.Mode, result *EditResult) error {
	if mode == plan.ModeComment {
		return s.applyProposals(ctx, f, planned.Proposals, result)
	}
	if len(planned.Requests) == 0 {
		result.Warnings = append(result.Warnings, "no changes were needed; the document already matches")
		return nil
	}
	wc := &gapi.WriteControl{RequiredRevisionID: f.Doc.RevisionID}
	if mode == plan.ModeSuggest {
		wc.WriteMode = "SUGGEST"
	}
	res, err := s.api.BatchUpdate(ctx, f.Doc.ID, &gapi.BatchUpdateRequest{Requests: planned.Requests, WriteControl: wc})
	if err != nil {
		return wrapWriteError(err)
	}
	s.Invalidate(f.Doc.ID)
	env := decodeReplies(res.Raw)
	for _, sr := range env.SuggestionResponses {
		result.SuggestionIDs = append(result.SuggestionIDs, sr.CreatedSuggestionIDs...)
	}
	revision := f.Doc.RevisionID
	if res.WriteControl != nil && res.WriteControl.RequiredRevisionID != "" {
		revision = res.WriteControl.RequiredRevisionID
	}
	if len(planned.Followups) == 0 {
		return nil
	}
	// New headers, footers and footnotes get their content in a second
	// batch, once the replies have named the new segments.
	ids := newSegmentIDs(env)
	var second []json.RawMessage
	for _, fu := range planned.Followups {
		id := ids[fu.Kind]
		if len(id) == 0 {
			result.Warnings = append(result.Warnings, fmt.Sprintf("op %d: %s was created but Google returned no segment id; its content was not inserted", fu.Seq, fu.Kind))
			continue
		}
		ids[fu.Kind] = id[1:]
		c, err := plan.CompileFragment(fu.Fragment, plan.Loc{Index: 0, SegmentID: id[0], TabID: fu.TabID}, plan.FragmentOptions{})
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("op %d: %v", fu.Seq, err))
			continue
		}
		second = append(second, c.Requests...)
	}
	if len(second) == 0 {
		return nil
	}
	wc2 := &gapi.WriteControl{RequiredRevisionID: revision, WriteMode: wc.WriteMode}
	res2, err := s.api.BatchUpdate(ctx, f.Doc.ID, &gapi.BatchUpdateRequest{Requests: second, WriteControl: wc2})
	if err != nil {
		result.Warnings = append(result.Warnings, "the header/footer/footnote was created but filling it failed: "+wrapWriteError(err).Error())
		return nil
	}
	for _, sr := range decodeReplies(res2.Raw).SuggestionResponses {
		result.SuggestionIDs = append(result.SuggestionIDs, sr.CreatedSuggestionIDs...)
	}
	return nil
}

func decodeReplies(raw json.RawMessage) replyEnvelope {
	var env replyEnvelope
	_ = json.Unmarshal(raw, &env)
	return env
}

// newSegmentIDs collects created header, footer and footnote ids from
// replies, in request order.
func newSegmentIDs(env replyEnvelope) map[plan.OpKind][]string {
	out := map[plan.OpKind][]string{}
	for _, r := range env.Replies {
		var v struct {
			HeaderID   string `json:"headerId"`
			FooterID   string `json:"footerId"`
			FootnoteID string `json:"footnoteId"`
		}
		if raw, ok := r["createHeader"]; ok && json.Unmarshal(raw, &v) == nil && v.HeaderID != "" {
			out[plan.OpCreateHeader] = append(out[plan.OpCreateHeader], v.HeaderID)
		}
		if raw, ok := r["createFooter"]; ok && json.Unmarshal(raw, &v) == nil && v.FooterID != "" {
			out[plan.OpCreateFooter] = append(out[plan.OpCreateFooter], v.FooterID)
		}
		if raw, ok := r["createFootnote"]; ok && json.Unmarshal(raw, &v) == nil && v.FootnoteID != "" {
			out[plan.OpFootnote] = append(out[plan.OpFootnote], v.FootnoteID)
		}
	}
	return out
}

// applyProposals posts comment-mode proposals through the preview API
// when available (anchored) or the Drive API (quoted, unanchored).
func (s *Service) applyProposals(ctx context.Context, f *Fetched, proposals []plan.Proposal, result *EditResult) error {
	if len(proposals) == 0 {
		return nil
	}
	if s.opts.Preview {
		reqs := make([]json.RawMessage, 0, len(proposals))
		for _, p := range proposals {
			reqs = append(reqs, plan.InsertComment(p.Content, p.Range, ""))
		}
		res, err := s.api.BatchUpdate(ctx, f.Doc.ID, &gapi.BatchUpdateRequest{Requests: reqs, WriteControl: &gapi.WriteControl{RequiredRevisionID: f.Doc.RevisionID}})
		if err != nil {
			return wrapWriteError(err)
		}
		s.Invalidate(f.Doc.ID)
		for _, r := range decodeReplies(res.Raw).Replies {
			var v struct {
				CommentThread struct {
					CommentID string `json:"commentId"`
				} `json:"commentThread"`
			}
			if raw, ok := r["insertComment"]; ok && json.Unmarshal(raw, &v) == nil && v.CommentThread.CommentID != "" {
				result.CommentIDs = append(result.CommentIDs, v.CommentThread.CommentID)
			}
		}
		return nil
	}
	for _, p := range proposals {
		c, err := s.api.CreateComment(ctx, f.Doc.ID, p.Content, p.Quote)
		if err != nil {
			if len(result.CommentIDs) > 0 {
				result.Warnings = append(result.Warnings, fmt.Sprintf("op %d: comment failed after %d were posted: %v", p.Seq, len(result.CommentIDs), wrapWriteError(err)))
				continue
			}
			return wrapWriteError(err)
		}
		result.CommentIDs = append(result.CommentIDs, c.ID)
	}
	result.Warnings = append(result.Warnings, "comments were posted through the Drive API: they quote the text but are not pinned to it in the editor (Developer Preview anchors them)")
	return nil
}

func wrapWriteError(err error) error {
	e := wrapAPI(err, "document")
	var se *Error
	if errors.As(e, &se) && se.Class == "invalid" {
		se.Message = "Google rejected the batch: " + se.Message + "; nothing was applied"
	}
	return e
}

// regionPreview renders the blocks around the edited region with
// handles so the caller can confirm without another read.
func regionPreview(d *doc.Document, ro *resolvedOps) string {
	if ro == nil || !ro.hasPreview {
		return ""
	}
	tab, ok := d.Tab(ro.previewTab)
	if !ok {
		return ""
	}
	var seg *doc.Segment
	for _, sg := range tab.Segments() {
		if sg.ID == ro.previewSeg {
			seg = sg
			break
		}
	}
	if seg == nil {
		return ""
	}
	idx := -1
	for i, b := range seg.Blocks {
		if b.Kind == doc.KindSectionBreak {
			continue
		}
		if ro.previewIndex < b.End {
			idx = i
			break
		}
	}
	if idx < 0 {
		idx = len(seg.Blocks) - 1
	}
	from, to := max(idx-1, 0), min(idx+2, len(seg.Blocks))
	r := render.Markdown(seg, from, to, render.Options{WithHandles: true, Suggestions: true, MaxChars: 4000})
	return strings.TrimSpace(r.Text)
}
