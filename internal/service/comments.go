package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mmedum/google-docs-mcp/v2/internal/doc"
	"github.com/mmedum/google-docs-mcp/v2/internal/gapi"
	"github.com/mmedum/google-docs-mcp/v2/internal/gdocs"
	"github.com/mmedum/google-docs-mcp/v2/internal/plan"
	"github.com/mmedum/google-docs-mcp/v2/internal/render"
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
// in the document by the comments view's anchors or by quoted text. The
// document fetch and the Drive list are independent and run together.
func (s *Service) ListComments(ctx context.Context, req ListCommentsRequest) (*CommentsResult, error) {
	id, err := parseRef(req.Document)
	if err != nil {
		return nil, err
	}
	mime, _ := s.kind(ctx, id)
	if gapi.IsMarkdownMimeType(mime) {
		return s.mdListComments(ctx, req)
	}
	word := mime == gapi.WordMimeType
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
	threads := driveThreads(l.list)
	if word && f.Wire != nil {
		// A Word file's own comments are in the file, which Drive cannot
		// see and the Docs comments view lists; the two sets are disjoint
		// (spike G).
		threads = append(threads, viewThreads(f.Wire)...)
	}
	res := tallyThreads(locateComments(f, threads), f.Doc.RevisionID, req.HideResolved)
	if word {
		res.Text += "\nOn a Word file, comments live in the file itself: they are read here, and add_comment writes new ones; " +
			"replying to, resolving or deleting one is done in the Docs editor."
	}
	return res, nil
}

// tallyThreads is a listing's result: the threads counted, the resolved
// ones dropped when hidden, and the text.
func tallyThreads(threads []CommentThread, revision string, hide bool) *CommentsResult {
	res := &CommentsResult{RevisionID: revision, Threads: []CommentThread{}}
	for _, t := range threads {
		if t.Resolved {
			res.Resolved++
		} else if !t.Deleted {
			res.Open++
		}
		if hide && t.Resolved {
			continue
		}
		res.Threads = append(res.Threads, t)
	}
	res.Text = commentsText(res, revision)
	return res
}

func commentsText(res *CommentsResult, revision string) string {
	head := fmt.Sprintf("%d comment thread(s) shown (%d open, %d resolved) at revision %s",
		len(res.Threads), res.Open, res.Resolved, revision)
	threads := make([]render.Thread, 0, len(res.Threads))
	for _, t := range res.Threads {
		threads = append(threads, t.thread())
	}
	if body := render.CommentThreads(threads); body != "" {
		return head + "\n" + body
	}
	return head
}

// thread is the renderer's view of a thread for the full listing, which
// shows every field. The footer of a read fills in fewer (commentMarks).
func (t CommentThread) thread() render.Thread {
	rt := render.Thread{ID: t.ID, Handle: t.Handle, Author: t.Author, Created: t.Created, Quote: t.Quote,
		Content: t.Content, Resolved: t.Resolved, Deleted: t.Deleted, Assignee: t.Assignee, Replies: renderReplies(t.Replies)}
	return rt
}

// renderReplies is the renderer's view of a thread's replies.
func renderReplies(rs []CommentReply) []render.Reply {
	out := make([]render.Reply, 0, len(rs))
	for _, r := range rs {
		out = append(out, render.Reply{Author: r.Author, Content: r.Content, Created: r.Created, Action: r.Action, Deleted: r.Deleted})
	}
	return out
}

// AddCommentRequest posts a new thread.
type AddCommentRequest struct {
	Document       string
	Target         *Target // nil for a comment on the document as a whole
	Content        string
	Assignee       string
	ExpectRevision string
	DryRun         bool
}

// AddCommentResult reports the new thread.
type AddCommentResult struct {
	ID         string   `json:"id"`
	RevisionID string   `json:"revision_id"`
	Handle     string   `json:"handle,omitempty"`
	Quote      string   `json:"quote,omitempty"`
	Anchored   bool     `json:"anchored"`
	DryRun     bool     `json:"dry_run,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
	Text       string   `json:"-"`
}

// commentTarget is the passage a comment is pinned to, which must hold
// text; res gets its quote and its block's handle.
func (s *Service) commentTarget(f *Fetched, t Target, res *AddCommentResult) (*TargetRange, error) {
	r, err := s.ResolveTarget(f, t)
	if err != nil {
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
	return r, nil
}

// pinned says the comment names a passage to be pinned to.
func (r AddCommentRequest) pinned() bool { return r.Target != nil && r.Target.selectorCount() > 0 }

// AddComment posts a comment on a target. When the read had the comments
// view it is pinned to the target's range; otherwise it goes through the
// Drive API quoting the text, which the editor shows unanchored.
func (s *Service) AddComment(ctx context.Context, req AddCommentRequest) (*AddCommentResult, error) {
	if err := s.requireWritable(); err != nil {
		return nil, err
	}
	content, err := commentText(req.Content, "content")
	if err != nil {
		return nil, err
	}
	if s.isMarkdown(ctx, req.Document) {
		return s.mdAddComment(ctx, req, content)
	}
	if req.pinned() && s.isWord(ctx, req.Document) {
		return s.docxAddComment(ctx, req, content)
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
	if req.pinned() {
		if r, err = s.commentTarget(f, *req.Target, res); err != nil {
			return nil, err
		}
	}
	anchored := f.CommentsView && r != nil
	if req.DryRun {
		res.DryRun, res.Anchored = true, anchored
		if strings.TrimSpace(req.Assignee) != "" && !res.Anchored {
			res.Warnings = append(res.Warnings, "assignee needs a comment pinned to a passage; the comment would be posted without one")
		}
		res.Text = "dry run: would post a comment"
		if res.Handle != "" {
			res.Text += fmt.Sprintf(" on %s (%q)", res.Handle, doc.Clip(res.Quote, 60))
		}
		for _, w := range res.Warnings {
			res.Text += "\nwarning: " + w
		}
		return res, nil
	}
	if anchored {
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

// postAnchoredComment pins a comment to a range through the Docs API,
// guarded by the revision the range was resolved against.
func (s *Service) postAnchoredComment(ctx context.Context, f *Fetched, rng plan.Rng, content, assignee string, res *AddCommentResult) error {
	env, revision, err := s.commentBatch(ctx, f, []json.RawMessage{plan.InsertComment(content, rng, assignee)})
	if err != nil {
		return err
	}
	ids := env.commentIDs()
	if len(ids) == 0 {
		return Errorf("ambiguous_outcome", "Google accepted the comment but did not say which one it created; list_comments shows whether it exists before you post it again")
	}
	res.ID, res.RevisionID, res.Anchored = ids[0], revision, true
	return nil
}

// postDriveComment posts through the Drive API, quoting the target text
// when there is one; the editor shows such comments unanchored.
func (s *Service) postDriveComment(ctx context.Context, f *Fetched, content, assignee string, res *AddCommentResult) error {
	if assignee != "" {
		res.Warnings = append(res.Warnings, "assignee needs a comment pinned to a passage; the comment was posted without one")
	}
	c, err := s.api.CreateComment(ctx, f.Doc.ID, content, res.Quote)
	if err != nil {
		return wrapWriteError(err)
	}
	res.ID = c.ID
	if res.Quote != "" {
		res.Warnings = append(res.Warnings, "the comment quotes the text but is not pinned to it in the editor ("+CommentsViewRefused+")")
	}
	return nil
}

// ReplyRequest continues a thread.
type ReplyRequest struct {
	Document     string
	CommentID    string
	SuggestionID string // a suggestion's thread instead of a comment's; plain replies only
	ReplyID      string // edit: the reply to rewrite; empty means the comment itself
	Content      string
	Action       string // reply (default), resolve, reopen, edit
}

// ReplyResult reports the posted reply.
type ReplyResult struct {
	CommentID    string `json:"comment_id"`
	SuggestionID string `json:"suggestion_id,omitempty"`
	ReplyID      string `json:"reply_id"`
	Action       string `json:"action"`
	Resolved     bool   `json:"resolved"`
	Text         string `json:"-"`
}

// Reply posts a reply, resolves or reopens a thread through the Drive
// API, which serves every deployment. A suggestion's thread is the one
// exception, since Drive cannot see it: see replyToSuggestion.
func (s *Service) Reply(ctx context.Context, req ReplyRequest) (*ReplyResult, error) {
	if err := s.requireWritable(); err != nil {
		return nil, err
	}
	action, content, err := replyAction(req)
	if err != nil {
		return nil, err
	}
	if suggestionID := strings.TrimSpace(req.SuggestionID); suggestionID != "" {
		if strings.TrimSpace(req.CommentID) != "" {
			return nil, Errorf("invalid", "comment_id and suggestion_id name two threads; pass one")
		}
		if action != "reply" {
			return nil, Errorf("invalid", "a suggestion's thread takes replies only; accept or reject the suggestion with review_suggestion")
		}
		return s.replyToSuggestion(ctx, req.Document, suggestionID, content)
	}
	if s.isMarkdown(ctx, req.Document) {
		// A comment made through Drive's API is Drive's to answer; one
		// the Docs editor keeps in the file is not in Drive at all.
		id, _ := parseRef(req.Document)
		if _, err := s.api.GetComment(ctx, id, strings.TrimSpace(req.CommentID)); errors.Is(err, gapi.ErrNotFound) {
			return s.mdReply(ctx, req, action, content)
		}
	}
	id, commentID, thread, err := s.commentRef(ctx, req.Document, req.CommentID)
	if err != nil {
		return nil, err
	}
	if action == "edit" {
		return s.editComment(ctx, id, commentID, strings.TrimSpace(req.ReplyID), content, thread)
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

// replyAction is a reply's action, reply by default, and its content
// checked for that action.
func replyAction(req ReplyRequest) (string, string, error) {
	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action == "" {
		action = "reply"
	}
	content := strings.TrimSpace(req.Content)
	switch action {
	case "reply", "edit":
		var err error
		content, err = commentText(content, "content")
		return action, content, err
	case "resolve", "reopen":
		if len(content) > 2048 {
			return "", "", Errorf("invalid", "content is %d bytes; replies are limited to 2048", len(content))
		}
		return action, content, nil
	}
	return "", "", Errorf("invalid", "action %q; use reply, resolve, reopen or edit", req.Action)
}

// replyToSuggestion posts a reply on a suggestion's thread. Drive's
// comments do not include these threads, so the reply goes through the
// Docs API and needs the comments view.
func (s *Service) replyToSuggestion(ctx context.Context, ref, suggestionID, content string) (*ReplyResult, error) {
	if err := s.wordUnsupported(ctx, ref, "a reply on a suggestion's thread"); err != nil {
		return nil, err
	}
	f, err := s.FetchFresh(ctx, ref)
	if err != nil {
		return nil, err
	}
	if !f.CommentsView {
		return nil, Errorf("unavailable", "%s, so a suggestion's thread cannot be replied to", CommentsViewRefused)
	}
	if !slices.ContainsFunc(f.Wire.Suggestions, func(t gdocs.SuggestionThread) bool { return t.SuggestionID == suggestionID }) {
		return nil, Errorf("not_found", "suggestion %s has no thread in this document; ids come from list_suggestions", suggestionID)
	}
	env, _, err := s.commentBatch(ctx, f, []json.RawMessage{plan.ReplyToSuggestion(suggestionID, content)})
	if err != nil {
		return nil, err
	}
	res := &ReplyResult{SuggestionID: suggestionID, ReplyID: env.replyPostID(), Action: "reply"}
	if res.ReplyID == "" {
		return nil, Errorf("ambiguous_outcome", "Google accepted the reply but did not say which one it created; list_suggestions shows whether it exists before you post it again")
	}
	res.Text = fmt.Sprintf("replied to suggestion %s (reply %s)", suggestionID, res.ReplyID)
	return res, nil
}

// editComment rewrites a comment or one of its replies. Google allows
// only the author to, and answers 403 to anyone else.
func (s *Service) editComment(ctx context.Context, id, commentID, replyID, content string, thread *gapi.DriveComment) (*ReplyResult, error) {
	res := &ReplyResult{CommentID: commentID, Action: "edit", Resolved: thread.Resolved}
	if replyID == "" {
		if _, err := s.api.UpdateComment(ctx, id, commentID, content); err != nil {
			return nil, wrapWriteError(err)
		}
		res.Text = "rewrote comment " + commentID
	} else {
		found := false
		for _, r := range thread.Replies {
			found = found || r.ID == replyID
		}
		if !found {
			return nil, Errorf("not_found", "comment %s has no reply %s", commentID, replyID)
		}
		if _, err := s.api.UpdateReply(ctx, id, commentID, replyID, content); err != nil {
			return nil, wrapWriteError(err)
		}
		res.ReplyID = replyID
		res.Text = fmt.Sprintf("rewrote reply %s of comment %s", replyID, commentID)
	}
	s.Invalidate(id)
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
	if errors.Is(err, gapi.ErrNotFound) {
		switch mime, _ := s.kind(ctx, id); {
		case gapi.IsMarkdownMimeType(mime):
			return "", "", nil, mdUnsupported("Drive has no comment "+commentID+"; a comment the Docs editor keeps in the file is not Drive's to delete",
				"the person can delete it in the Docs editor")
		case mime == gapi.WordMimeType:
			return "", "", nil, Errorf("unsupported", "Drive has no comment %s: a comment a Word file holds lives in the file itself, "+
				"and replying to, resolving or deleting one is done in the Docs editor", commentID)
		}
	}
	if err != nil {
		return "", "", nil, wrapAPI(err, "comment")
	}
	if thread.Deleted {
		return "", "", nil, Errorf("not_found", "comment %s was deleted", commentID)
	}
	return id, commentID, thread, nil
}

// askDeleteComment puts the deletion of a thread, or of one reply in it,
// to the person, with what they wrote.
// The document's title comes from Drive rather than a fetch of the
// document, which this tool otherwise never reads.
func (s *Service) askDeleteComment(ctx context.Context, id string, thread *gapi.DriveComment, replyID string) error {
	file, err := s.api.GetFile(ctx, id)
	if err != nil {
		return wrapAPI(err, "document")
	}
	if r := liveReply(thread, replyID); r != nil {
		return ask(ctx, render.AskDeleteComment(id, file.Name, thread.ID, true, userLabel(r.Author), r.Content, nil))
	}
	var replies []string
	for _, r := range thread.Replies {
		if r != nil && !r.Deleted {
			replies = append(replies, r.Content)
		}
	}
	return ask(ctx, render.AskDeleteComment(id, file.Name, thread.ID, false, userLabel(thread.Author), thread.Content, replies))
}

// liveReply is the thread's reply of that id that is not deleted, or nil.
func liveReply(thread *gapi.DriveComment, id string) *gapi.DriveReply {
	for _, r := range thread.Replies {
		if id != "" && r != nil && !r.Deleted && r.ID == id {
			return r
		}
	}
	return nil
}

// DeleteCommentRequest removes a thread or one reply.
type DeleteCommentRequest struct {
	Document  string
	CommentID string
	ReplyID   string
	DryRun    bool
}

// DeleteCommentResult reports the deletion.
type DeleteCommentResult struct {
	CommentID string `json:"comment_id"`
	ReplyID   string `json:"reply_id,omitempty"`
	DryRun    bool   `json:"dry_run,omitempty"`
	Text      string `json:"-"`
}

// DeleteComment deletes a thread (or a single reply). Drive keeps deleted
// threads listable with include_deleted. Only the author may delete.
func (s *Service) DeleteComment(ctx context.Context, req DeleteCommentRequest) (*DeleteCommentResult, error) {
	if err := s.requireDestructive(); err != nil {
		return nil, err
	}
	id, commentID, thread, err := s.commentRef(ctx, req.Document, req.CommentID)
	if err != nil {
		return nil, err
	}
	res := &DeleteCommentResult{CommentID: commentID, ReplyID: strings.TrimSpace(req.ReplyID)}
	if res.ReplyID != "" && liveReply(thread, res.ReplyID) == nil {
		return nil, Errorf("not_found", "comment %s has no reply %s; ids come from list_comments", commentID, res.ReplyID)
	}
	if req.DryRun {
		res.DryRun = true
		res.Text = fmt.Sprintf("dry run: would delete comment %s", commentID)
		if res.ReplyID != "" {
			res.Text = fmt.Sprintf("dry run: would delete reply %s of comment %s", res.ReplyID, commentID)
		}
		return res, nil
	}
	if asks(ctx) {
		if err := s.askDeleteComment(ctx, id, thread, res.ReplyID); err != nil {
			return nil, err
		}
	}
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
