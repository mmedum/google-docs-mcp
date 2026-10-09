package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mmedum/google-docs-mcp/v2/internal/doc"
	"github.com/mmedum/google-docs-mcp/v2/internal/gapi"
	"github.com/mmedum/google-docs-mcp/v2/internal/plan"
	"github.com/mmedum/google-docs-mcp/v2/internal/render"
)

type replyEnvelope struct {
	Replies             []map[string]json.RawMessage `json:"replies"`
	SuggestionResponses []struct {
		CreatedSuggestionIDs []string `json:"createdSuggestionIds"`
	} `json:"suggestionResponses"`
	CommentUpdateState string `json:"commentUpdateState"`
}

// each calls fn with the payload of every reply of one request kind.
func (e replyEnvelope) each(kind string, fn func(raw json.RawMessage)) {
	for _, r := range e.Replies {
		if raw, ok := r[kind]; ok {
			fn(raw)
		}
	}
}

// suggestionIDs lists the suggestions the batch created.
func (e replyEnvelope) suggestionIDs() []string {
	var out []string
	for _, sr := range e.SuggestionResponses {
		out = append(out, sr.CreatedSuggestionIDs...)
	}
	return out
}

// commentIDs lists the threads insertComment requests created.
func (e replyEnvelope) commentIDs() []string {
	var out []string
	e.each("insertComment", func(raw json.RawMessage) {
		var v struct {
			CommentThread struct {
				CommentID string `json:"commentId"`
			} `json:"commentThread"`
		}
		if json.Unmarshal(raw, &v) == nil && v.CommentThread.CommentID != "" {
			out = append(out, v.CommentThread.CommentID)
		}
	})
	return out
}

// replyPostID is the id of the post an addCommentReply request created.
func (e replyEnvelope) replyPostID() string {
	id := ""
	e.each("addCommentReply", func(raw json.RawMessage) {
		var v struct {
			Post struct {
				PostID string `json:"postId"`
			} `json:"post"`
		}
		if json.Unmarshal(raw, &v) == nil && id == "" {
			id = v.Post.PostID
		}
	})
	return id
}

// addedTabID is the id of the tab an addDocumentTab request created.
func (e replyEnvelope) addedTabID() string {
	id := ""
	e.each("addDocumentTab", func(raw json.RawMessage) {
		var v struct {
			TabProperties struct {
				TabID string `json:"tabId"`
			} `json:"tabProperties"`
		}
		if json.Unmarshal(raw, &v) == nil && id == "" {
			id = v.TabProperties.TabID
		}
	})
	return id
}

// batchUpdate sends requests guarded by the fetched revision, drops the
// cached copy, and returns the decoded replies and the new revision.
// writeMode is "" or "SUGGEST".
func (s *Service) batchUpdate(ctx context.Context, f *Fetched, reqs []json.RawMessage, writeMode string) (replyEnvelope, string, error) {
	wc := &gapi.WriteControl{RequiredRevisionID: f.Doc.RevisionID, WriteMode: writeMode}
	res, err := s.api.BatchUpdate(ctx, f.Doc.ID, &gapi.BatchUpdateRequest{Requests: reqs, WriteControl: wc})
	if err != nil {
		return replyEnvelope{}, "", wrapWriteError(err)
	}
	s.Invalidate(f.Doc.ID)
	revision := f.Doc.RevisionID
	if res.WriteControl != nil && res.WriteControl.RequiredRevisionID != "" {
		revision = res.WriteControl.RequiredRevisionID
	}
	return decodeReplies(res.Raw), revision, nil
}

// commentBatch sends a batch of comments or replies and nothing else.
// Google can answer 200 and still save none of them, naming no reason;
// since the batch held only comments, nothing was written and asking
// again is safe. Only here: a review's accept or reject also counts as a
// thread post, and saying "nothing was posted" after one applied would
// invite applying it twice.
func (s *Service) commentBatch(ctx context.Context, f *Fetched, reqs []json.RawMessage) (replyEnvelope, string, error) {
	env, revision, err := s.batchUpdate(ctx, f, reqs, "")
	if err == nil && env.CommentUpdateState == "ALL_FAILED_UNKNOWN_REASON" {
		return replyEnvelope{}, "", Errorf("server", "Google saved none of the comments and gave no reason; nothing was posted, so try again shortly")
	}
	return env, revision, err
}

// apply sends a plan to Google in the chosen mode, records ids and
// returns the replies so the follow-ups can find what was created.
func (s *Service) apply(ctx context.Context, f *Fetched, planned *plan.Result, mode plan.Mode, result *EditResult) (replyEnvelope, error) {
	if mode == plan.ModeComment {
		return replyEnvelope{}, s.applyProposals(ctx, f, planned.Proposals, result)
	}
	if len(planned.Requests) == 0 {
		result.Warnings = append(result.Warnings, "no changes were needed; the document already matches")
		return replyEnvelope{}, nil
	}
	writeMode := ""
	if mode == plan.ModeSuggest {
		writeMode = "SUGGEST"
	}
	env, revision, err := s.batchUpdate(ctx, f, planned.Requests, writeMode)
	if err != nil {
		return replyEnvelope{}, err
	}
	result.written = revision
	result.SuggestionIDs = append(result.SuggestionIDs, env.suggestionIDs()...)
	return env, nil
}

func decodeReplies(raw json.RawMessage) replyEnvelope {
	var env replyEnvelope
	_ = json.Unmarshal(raw, &env)
	return env
}

// applyProposals posts comment-mode proposals through the Docs API when
// the read had the comments view (anchored), else the Drive API (quoted,
// unanchored).
func (s *Service) applyProposals(ctx context.Context, f *Fetched, proposals []plan.Proposal, result *EditResult) error {
	if len(proposals) == 0 {
		return nil
	}
	if f.CommentsView {
		reqs := make([]json.RawMessage, 0, len(proposals))
		for _, p := range proposals {
			reqs = append(reqs, plan.InsertComment(p.Content, p.Range, ""))
		}
		env, _, err := s.commentBatch(ctx, f, reqs)
		if err != nil {
			return err
		}
		result.CommentIDs = append(result.CommentIDs, env.commentIDs()...)
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
	result.Warnings = append(result.Warnings, "comments were posted through the Drive API: they quote the text but are not pinned to it in the editor ("+CommentsViewRefused+")")
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
	seg := segmentAt(d, ro.previewTab, ro.previewSeg)
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
