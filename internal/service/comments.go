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
// in the document by the preview's anchors or by quoted text. The
// document fetch and the Drive list are independent and run together.
func (s *Service) ListComments(ctx context.Context, req ListCommentsRequest) (*CommentsResult, error) {
	id, err := parseRef(req.Document)
	if err != nil {
		return nil, err
	}
	type listed struct {
		list []*gapi.DriveComment
		err  error
	}
	ch := make(chan listed, 1)
	go func() {
		list, err := s.api.ListComments(ctx, id, req.IncludeDeleted)
		ch <- listed{list, err}
	}()
	f, err := s.Fetch(ctx, id)
	if err != nil {
		<-ch
		return nil, err
	}
	l := <-ch
	if l.err != nil {
		return nil, wrapAPI(l.err, "comments")
	}
	threads := locateComments(f, driveThreads(l.list))
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
		sb.WriteString(": " + doc.OneLine(t.Content) + "\n")
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
				sb.WriteString(": " + doc.OneLine(r.Content))
			}
			sb.WriteString("\n")
		}
	}
	return strings.TrimRight(sb.String(), "\n")
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
	content, err := commentText(req.Content, "content")
	if err != nil {
		return nil, err
	}
	f, err := s.FetchFresh(ctx, req.Document)
	if err != nil {
		return nil, err
	}
	if err := checkRevision(req.ExpectRevision, f.Doc.RevisionID, "commenting"); err != nil {
		return nil, err
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
		err = s.postDriveComment(ctx, f, content, req.Assignee, res)
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

// commentText validates a comment or reply body against the API limit.
func commentText(text, what string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", Errorf("invalid", "%s is empty", what)
	}
	if len(text) > 2048 {
		return "", Errorf("invalid", "%s is %d bytes; comments are limited to 2048", what, len(text))
	}
	return text, nil
}

// postAnchoredComment pins a comment to a range through the preview API,
// guarded by the revision the range was resolved against.
func (s *Service) postAnchoredComment(ctx context.Context, f *Fetched, rng plan.Rng, content, assignee string, res *AddCommentResult) error {
	env, revision, err := s.batchUpdate(ctx, f, []json.RawMessage{plan.InsertComment(content, rng, assignee)}, "")
	if err != nil {
		return err
	}
	if ids := env.commentIDs(); len(ids) > 0 {
		res.ID = ids[0]
	}
	res.RevisionID, res.Anchored = revision, true
	return nil
}

// postDriveComment posts through the Drive API, quoting the target text
// when there is one; the editor shows such comments unanchored.
func (s *Service) postDriveComment(ctx context.Context, f *Fetched, content, assignee string, res *AddCommentResult) error {
	if assignee != "" {
		res.Warnings = append(res.Warnings, "assignee is only supported with Developer Preview; the comment was posted without one")
	}
	c, err := s.api.CreateComment(ctx, f.Doc.ID, content, res.Quote)
	if err != nil {
		return wrapWriteError(err)
	}
	res.ID = c.ID
	if res.Quote != "" {
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
	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action == "" {
		action = "reply"
	}
	content := strings.TrimSpace(req.Content)
	switch action {
	case "reply":
		var err error
		if content, err = commentText(content, "content"); err != nil {
			return nil, err
		}
	case "resolve", "reopen":
		if len(content) > 2048 {
			return nil, Errorf("invalid", "content is %d bytes; replies are limited to 2048", len(content))
		}
	default:
		return nil, Errorf("invalid", "action %q; use reply, resolve or reopen", req.Action)
	}
	id, commentID, thread, err := s.commentRef(ctx, req.Document, req.CommentID)
	if err != nil {
		return nil, err
	}
	if action == "resolve" && thread.Resolved {
		return nil, Errorf("invalid", "comment %s is already resolved", commentID)
	}
	if action == "reopen" && !thread.Resolved {
		return nil, Errorf("invalid", "comment %s is not resolved", commentID)
	}
	apiAction := action
	if action == "reply" {
		apiAction = ""
	}
	rp, err := s.api.CreateReply(ctx, id, commentID, content, apiAction)
	if err != nil {
		return nil, wrapWriteError(err)
	}
	s.Invalidate(id)
	res := &ReplyResult{CommentID: commentID, ReplyID: rp.ID, Action: action, Resolved: action == "resolve" || (action == "reply" && thread.Resolved)}
	switch action {
	case "reply":
		res.Text = fmt.Sprintf("replied to comment %s (reply %s)", commentID, rp.ID)
	case "resolve":
		res.Text = "resolved comment " + commentID
	default:
		res.Text = "reopened comment " + commentID
	}
	return res, nil
}

// commentRef resolves the document and checks that the thread exists and
// is not deleted.
func (s *Service) commentRef(ctx context.Context, docRef, commentID string) (string, string, *gapi.DriveComment, error) {
	id, err := parseRef(docRef)
	if err != nil {
		return "", "", nil, err
	}
	commentID = strings.TrimSpace(commentID)
	if commentID == "" {
		return "", "", nil, Errorf("invalid", "comment_id is empty; ids come from list_comments")
	}
	thread, err := s.api.GetComment(ctx, id, commentID)
	if err != nil {
		return "", "", nil, wrapAPI(err, "comment")
	}
	if thread.Deleted {
		return "", "", nil, Errorf("not_found", "comment %s was deleted", commentID)
	}
	return id, commentID, thread, nil
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
	id, commentID, _, err := s.commentRef(ctx, req.Document, req.CommentID)
	if err != nil {
		return nil, err
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
