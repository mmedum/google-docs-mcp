package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-docs-mcp/internal/service"
)

// ListCommentsInput selects comment threads.
type ListCommentsInput struct {
	Document       string `json:"document" jsonschema:"document id or any docs.google.com URL"`
	HideResolved   bool   `json:"hide_resolved,omitempty" jsonschema:"leave out resolved threads; default shows them"`
	IncludeDeleted bool   `json:"include_deleted,omitempty" jsonschema:"also list deleted threads (their text is gone)"`
}

// AddCommentInput posts a thread.
type AddCommentInput struct {
	Document       string       `json:"document" jsonschema:"document id or any docs.google.com URL"`
	Target         *TargetInput `json:"target,omitempty" jsonschema:"the text, section, block or cell to comment on; omit for a comment on the document as a whole"`
	Content        string       `json:"content" jsonschema:"the comment text (plain text, up to 2048 bytes)"`
	Assignee       string       `json:"assignee,omitempty" jsonschema:"email address to assign the comment to (Developer Preview only)"`
	ExpectRevision string       `json:"expect_revision,omitempty" jsonschema:"fail if the document is no longer at this revision id"`
}

// ReplyInput continues a thread.
type ReplyInput struct {
	Document  string `json:"document" jsonschema:"document id or any docs.google.com URL"`
	CommentID string `json:"comment_id" jsonschema:"thread id from list_comments or add_comment"`
	Content   string `json:"content,omitempty" jsonschema:"reply text; required for a plain reply, optional with resolve or reopen"`
	Action    string `json:"action,omitempty" jsonschema:"reply (default), resolve, or reopen"`
}

// DeleteCommentInput removes a thread or a reply.
type DeleteCommentInput struct {
	Document  string `json:"document" jsonschema:"document id or any docs.google.com URL"`
	CommentID string `json:"comment_id" jsonschema:"thread id from list_comments"`
	ReplyID   string `json:"reply_id,omitempty" jsonschema:"delete only this reply of the thread"`
}

func registerCommentsRead(s *mcp.Server, d Deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "list_comments",
		Description: "List the comment threads of a Google Doc with their full history: author, time, quoted text, the " +
			"block handle the comment sits on, resolved state, and every reply (including resolve and reopen actions). " +
			"Resolved threads are included unless hide_resolved is set; deleted ones only with include_deleted. Thread " +
			"ids feed reply_comment and delete_comment.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ListCommentsInput) (*mcp.CallToolResult, *service.CommentsResult, error) {
		res, err := d.Service.ListComments(ctx, service.ListCommentsRequest{Document: in.Document, HideResolved: in.HideResolved, IncludeDeleted: in.IncludeDeleted})
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(res.Text), res, nil
	})
}

func registerCommentsWrite(s *mcp.Server, d Deps) {
	writeAnn := &mcp.ToolAnnotations{DestructiveHint: new(false), OpenWorldHint: new(false)}

	mcp.AddTool(s, &mcp.Tool{
		Name: "add_comment",
		Description: "Post a comment on a Google Doc. With a target (exact text, heading_id, handle or cell) the comment " +
			"is pinned to that passage when Developer Preview is on; without preview it quotes the text and the editor " +
			"shows it unanchored, which the result says. Without a target it is a comment on the document as a whole. " +
			"Nothing in the document text changes. To propose an edit as a comment, use edit_document with mode comment.",
		Annotations: writeAnn,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in AddCommentInput) (*mcp.CallToolResult, *service.AddCommentResult, error) {
		res, err := d.Service.AddComment(ctx, service.AddCommentRequest{Document: in.Document, Target: in.Target.target(), Content: in.Content, Assignee: in.Assignee, ExpectRevision: in.ExpectRevision})
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(res.Text), res, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "reply_comment",
		Description: "Reply to a comment thread, resolve it, or reopen it. Resolving and reopening are reversible; both " +
			"may carry a message. Thread ids come from list_comments.",
		Annotations: writeAnn,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ReplyInput) (*mcp.CallToolResult, *service.ReplyResult, error) {
		res, err := d.Service.Reply(ctx, service.ReplyRequest{Document: in.Document, CommentID: in.CommentID, Content: in.Content, Action: in.Action})
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(res.Text), res, nil
	})

	if !d.Config.EnableDestructive {
		return
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "delete_comment",
		Description: "Delete a comment thread, or one reply of it, from a Google Doc. Only the author can delete; a " +
			"resolved thread is usually the better outcome (reply_comment with action resolve). Ask the person first.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(true), OpenWorldHint: new(false)},
		Meta:        mcp.Meta{"anthropic/requiresUserInteraction": true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in DeleteCommentInput) (*mcp.CallToolResult, *service.DeleteCommentResult, error) {
		res, err := d.Service.DeleteComment(ctx, service.DeleteCommentRequest{Document: in.Document, CommentID: in.CommentID, ReplyID: in.ReplyID})
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(res.Text), res, nil
	})
}
