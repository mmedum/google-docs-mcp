package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-docs-mcp/internal/service"
)

// RevisionsInput lists version history.
type RevisionsInput struct {
	Document string `json:"document" jsonschema:"document id or any docs.google.com URL"`
	Limit    int    `json:"limit,omitempty" jsonschema:"how many of the newest revisions to return; default 20, max 200"`
}

// DiffInput compares two revisions.
type DiffInput struct {
	Document string `json:"document" jsonschema:"document id or any docs.google.com URL"`
	From     string `json:"from" jsonschema:"the older revision id from list_revisions"`
	To       string `json:"to,omitempty" jsonschema:"the newer revision id from list_revisions; default the current content"`
	Format   string `json:"format,omitempty" jsonschema:"md (default) or txt: which export to compare"`
	Context  int    `json:"context,omitempty" jsonschema:"unchanged lines shown around each change; default 2"`
	MaxChars int    `json:"max_chars,omitempty" jsonschema:"output budget in characters, cut at a hunk boundary; default 20000"`
}

func registerHistory(s *mcp.Server, d Deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "list_revisions",
		Description: "List a Google Doc's version history, newest first: revision id, time and who changed it. These ids " +
			"feed diff_revisions and read_document's revision parameter; they are Drive revision ids, not the revision_id " +
			"that reads and writes return for concurrency control. Drive may omit older revisions of busy documents.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in RevisionsInput) (*mcp.CallToolResult, any, error) {
		res, err := d.Service.ListRevisions(ctx, in.Document, in.Limit)
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(res.Text), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "diff_revisions",
		Description: "Show what changed between two revisions of a Google Doc as a unified diff of Google's markdown " +
			"(or plain text) export, with line counts per hunk. Pass from (older) and optionally to (newer, default " +
			"current). Use list_revisions to pick ids.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in DiffInput) (*mcp.CallToolResult, any, error) {
		if in.MaxChars < 0 || in.Context < 0 {
			return nil, nil, fail(service.Errorf("invalid", "max_chars and context must be positive"))
		}
		res, err := d.Service.DiffRevisions(ctx, service.DiffRequest{Document: in.Document, From: in.From, To: in.To, Format: in.Format, Context: in.Context, MaxChars: min(in.MaxChars, MaxMaxChars)})
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(res.Text), nil, nil
	})
}
