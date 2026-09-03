package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mmedum/google-docs-mcp/internal/doc"
	"github.com/mmedum/google-docs-mcp/internal/gapi"
	"github.com/mmedum/google-docs-mcp/internal/plan"
)

// ListCommentsRequest selects comment threads.
type ListCommentsRequest struct {
	Document       string
	HideResolved   bool
	IncludeDeleted bool
}

// CommentsResult lists threads with their full history.
type CommentsResult struct {
	RevisionID string          `json:"revision_id"`
	Threads    []CommentThread `json:"threads"`
	Open       int             `json:"open"`
	Resolved   int             `json:"resolved"`
	Text       string          `json:"-"`
}

// ListComments returns every comment thread through the Drive API, which
// carries replies, resolution and deletion for every deployment, located
// in the document by the preview's anchors or by quoted text.
func (s *Service) ListComments(ctx context.Context, req ListCommentsRequest) (*CommentsResult, error) {
	f, err := s.Fetch(ctx, req.Document)
	if err != nil {
		return nil, err
	}
	list, err := s.api.ListComments(ctx, f.Doc.ID, req.IncludeDeleted)
	if err != nil {
		return nil, wrapAPI(err, "comments")
	}
	threads := locateComments(f, driveThreads(list))
	res := &CommentsResult{RevisionID: f.Doc.RevisionID, Threads: []CommentThread{}}
	for _, t := range threads {
		if t.Resolved {
			res.Resolved++
		} else if !t.Deleted {
			res.Open++
		}
		if req.HideResolved && t.Resolved {
			continue
		}
		res.Threads = append(res.Threads, t)
	}
	res.Text = commentsText(res, f.Doc.RevisionID)
	return res, nil
}

func commentsText(res *CommentsResult, revision string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d comment thread(s) shown (%d open, %d resolved) at revision %s\n", len(res.Threads), res.Open, res.Resolved, revision)
	for _, t := range res.Threads {
		fmt.Fprintf(&sb, "- %s", t.ID)
		if t.Handle != "" {
			fmt.Fprintf(&sb, " [%s]", t.Handle)
		}
		if t.Author != "" {
			fmt.Fprintf(&sb, " by %s", t.Author)
		}
		if t.Created != "" {
			fmt.Fprintf(&sb, " (%s)", t.Created)
		}
		switch {
		case t.Deleted:
			sb.WriteString(" [deleted]")
		case t.Resolved:
			sb.WriteString(" [resolved]")
		}
		if t.Quote != "" {
			fmt.Fprintf(&sb, " on “%s”", doc.Clip(t.Quote, 60))
		}
		sb.WriteString(": " + oneLine(t.Content) + "\n")
		for _, r := range t.Replies {
			sb.WriteString("    ↳ ")
			if r.Author != "" {
				sb.WriteString(r.Author)
			} else {
				sb.WriteString("someone")
			}
			if r.Action != "" {
				sb.WriteString(" " + r.Action + "d")
			}
			if r.Created != "" {
				fmt.Fprintf(&sb, " (%s)", r.Created)
			}
			if r.Deleted {
				sb.WriteString(" [deleted]")
			}
			if r.Content != "" {
				sb.WriteString(": " + oneLine(r.Content))
			}
			sb.WriteString("\n")
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// AddCommentRequest posts a new thread.
type AddCommentRequest struct {
	Document       string
	Target         *Target // nil for a comment on the document as a whole
	Content        string
	Assignee       string
	ExpectRevision string
}

// AddCommentResult reports the new thread.
type AddCommentResult struct {
	ID         string   `json:"id"`
	RevisionID string   `json:"revision_id"`
	Handle     string   `json:"handle,omitempty"`
	Quote      string   `json:"quote,omitempty"`
	Anchored   bool     `json:"anchored"`
	Warnings   []string `json:"warnings,omitempty"`
	Text       string   `json:"-"`
}

// AddComment posts a comment on a target. With Developer Preview it is
// pinned to the target's range; otherwise it goes through the Drive API
// quoting the text, which the editor shows unanchored.
func (s *Service) AddComment(ctx context.Context, req AddCommentRequest) (*AddCommentResult, error) {
	if err := s.requireWritable(); err != nil {
		return nil, err
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return nil, Errorf("invalid", "content is empty")
	}
	if len(content) > 2048 {
		return nil, Errorf("invalid", "content is %d bytes; comments are limited to 2048", len(content))
	}
	f, err := s.FetchFresh(ctx, req.Document)
	if err != nil {
		return nil, err
	}
	if req.ExpectRevision != "" && req.ExpectRevision != f.Doc.RevisionID {
		return nil, Errorf("conflict", "the document is at revision %s, not %s; re-read before commenting", f.Doc.RevisionID, req.ExpectRevision)
	}
	res := &AddCommentResult{RevisionID: f.Doc.RevisionID}
	var r *TargetRange
	if req.Target != nil && req.Target.selectorCount() > 0 {
		if r, err = s.ResolveTarget(f, *req.Target); err != nil {
			return nil, err
		}
		if r.Start == r.End {
			return nil, Errorf("invalid", "%s is empty; a comment needs text to attach to", r.Description)
		}
		res.Quote = r.Text
		if r.Block != nil {
			res.Handle = r.Block.Handle
		} else if len(r.Blocks) > 0 {
			res.Handle = r.Blocks[0].Handle
		}
	}
	if s.opts.Preview && r != nil {
		err = s.postAnchoredComment(ctx, f, r.Rng(), content, strings.TrimSpace(req.Assignee), res)
	} else {
		err = s.postDriveComment(ctx, f, content, req.Assignee, r != nil, res)
	}
	if err != nil {
		return nil, err
	}
	res.Text = fmt.Sprintf("comment %s posted", res.ID)
	if res.Handle != "" {
		res.Text += fmt.Sprintf(" on %s (%q)", res.Handle, doc.Clip(res.Quote, 60))
	}
	if !res.Anchored {
		res.Text += " (unanchored)"
	}
	for _, w := range res.Warnings {
		res.Text += "\nwarning: " + w
	}
	return res, nil
}

// postAnchoredComment pins a comment to a range through the preview API,
// guarded by the revision the range was resolved against.
func (s *Service) postAnchoredComment(ctx context.Context, f *Fetched, rng plan.Rng, content, assignee string, res *AddCommentResult) error {
	reqs := []json.RawMessage{plan.InsertComment(content, rng, assignee)}
	out, err := s.api.BatchUpdate(ctx, f.Doc.ID, &gapi.BatchUpdateRequest{Requests: reqs, WriteControl: &gapi.WriteControl{RequiredRevisionID: f.Doc.RevisionID}})
	if err != nil {
		return wrapWriteError(err)
	}
	s.Invalidate(f.Doc.ID)
	for _, rep := range decodeReplies(out.Raw).Replies {
		var v struct {
			CommentThread struct {
				CommentID string `json:"commentId"`
			} `json:"commentThread"`
		}
		if raw, ok := rep["insertComment"]; ok && json.Unmarshal(raw, &v) == nil {
			res.ID = v.CommentThread.CommentID
		}
	}
	if out.WriteControl != nil && out.WriteControl.RequiredRevisionID != "" {
		res.RevisionID = out.WriteControl.RequiredRevisionID
	}
	res.Anchored = true
	return nil
}

// postDriveComment posts through the Drive API, quoting the target text
// when there is one; the editor shows such comments unanchored.
func (s *Service) postDriveComment(ctx context.Context, f *Fetched, content, assignee string, targeted bool, res *AddCommentResult) error {
	if assignee != "" {
		res.Warnings = append(res.Warnings, "assignee is only supported with Developer Preview; the comment was posted without one")
	}
	c, err := s.api.CreateComment(ctx, f.Doc.ID, content, res.Quote)
	if err != nil {
		return wrapWriteError(err)
	}
	res.ID = c.ID
	if targeted {
		res.Warnings = append(res.Warnings, "the comment quotes the text but is not pinned to it in the editor (Developer Preview anchors comments)")
	}
	return nil
}

// ReplyRequest continues a thread.
type ReplyRequest struct {
	Document  string
	CommentID string
	Content   string
	Action    string // reply (default), resolve, reopen
}

// ReplyResult reports the posted reply.
type ReplyResult struct {
	CommentID string `json:"comment_id"`
	ReplyID   string `json:"reply_id"`
	Action    string `json:"action"`
	Resolved  bool   `json:"resolved"`
	Text      string `json:"-"`
}

// Reply posts a reply, resolves or reopens a thread through the Drive
// API, which serves every deployment.
func (s *Service) Reply(ctx context.Context, req ReplyRequest) (*ReplyResult, error) {
	if err := s.requireWritable(); err != nil {
		return nil, err
	}
	id, err := doc.ParseID(req.Document)
	if err != nil {
		return nil, Errorf("invalid", "%q is not a Google Docs document id or URL", req.Document)
	}
	commentID := strings.TrimSpace(req.CommentID)
	if commentID == "" {
		return nil, Errorf("invalid", "comment_id is empty; ids come from list_comments")
	}
	action := strings.ToLower(strings.TrimSpace(req.Action))
	content := strings.TrimSpace(req.Content)
	switch action {
	case "", "reply":
		action = ""
		if content == "" {
			return nil, Errorf("invalid", "content is empty")
		}
	case "resolve", "reopen":
	default:
		return nil, Errorf("invalid", "action %q; use reply, resolve or reopen", req.Action)
	}
	if len(content) > 2048 {
		return nil, Errorf("invalid", "content is %d bytes; replies are limited to 2048", len(content))
	}
	thread, err := s.api.GetComment(ctx, id, commentID)
	if err != nil {
		return nil, wrapAPI(err, "comment")
	}
	if thread.Deleted {
		return nil, Errorf("not_found", "comment %s was deleted", commentID)
	}
	if action == "resolve" && thread.Resolved {
		return nil, Errorf("invalid", "comment %s is already resolved", commentID)
	}
	if action == "reopen" && !thread.Resolved {
		return nil, Errorf("invalid", "comment %s is not resolved", commentID)
	}
	rp, err := s.api.CreateReply(ctx, id, commentID, content, action)
	if err != nil {
		return nil, wrapWriteError(err)
	}
	s.Invalidate(id)
	res := &ReplyResult{CommentID: commentID, ReplyID: rp.ID, Action: action, Resolved: thread.Resolved}
	switch action {
	case "":
		res.Action = "reply"
		res.Text = fmt.Sprintf("replied to comment %s (reply %s)", commentID, rp.ID)
	case "resolve":
		res.Resolved = true
		res.Text = fmt.Sprintf("resolved comment %s", commentID)
	case "reopen":
		res.Resolved = false
		res.Text = fmt.Sprintf("reopened comment %s", commentID)
	}
	return res, nil
}

// DeleteCommentRequest removes a thread or one reply.
type DeleteCommentRequest struct {
	Document  string
	CommentID string
	ReplyID   string
}

// DeleteCommentResult reports the deletion.
type DeleteCommentResult struct {
	CommentID string `json:"comment_id"`
	ReplyID   string `json:"reply_id,omitempty"`
	Text      string `json:"-"`
}

// DeleteComment deletes a thread (or a single reply). Drive keeps deleted
// threads listable with include_deleted. Only the author may delete.
func (s *Service) DeleteComment(ctx context.Context, req DeleteCommentRequest) (*DeleteCommentResult, error) {
	if err := s.requireDestructive(); err != nil {
		return nil, err
	}
	id, err := doc.ParseID(req.Document)
	if err != nil {
		return nil, Errorf("invalid", "%q is not a Google Docs document id or URL", req.Document)
	}
	commentID := strings.TrimSpace(req.CommentID)
	if commentID == "" {
		return nil, Errorf("invalid", "comment_id is empty; ids come from list_comments")
	}
	if _, err := s.api.GetComment(ctx, id, commentID); err != nil {
		return nil, wrapAPI(err, "comment")
	}
	res := &DeleteCommentResult{CommentID: commentID, ReplyID: strings.TrimSpace(req.ReplyID)}
	if res.ReplyID != "" {
		if err := s.api.DeleteReply(ctx, id, commentID, res.ReplyID); err != nil {
			return nil, wrapWriteError(err)
		}
		res.Text = fmt.Sprintf("deleted reply %s of comment %s", res.ReplyID, commentID)
	} else {
		if err := s.api.DeleteComment(ctx, id, commentID); err != nil {
			return nil, wrapWriteError(err)
		}
		res.Text = fmt.Sprintf("deleted comment %s", commentID)
	}
	s.Invalidate(id)
	return res, nil
}
