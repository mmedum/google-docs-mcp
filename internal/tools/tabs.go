package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-docs-mcp/internal/service"
)

// TabInput is the manage_tabs call.
type TabInput struct {
	Document       string `json:"document" jsonschema:"document id or any docs.google.com URL"`
	Action         string `json:"action" jsonschema:"add, rename, or move"`
	Tab            string `json:"tab,omitempty" jsonschema:"rename, move: the tab to change, by id, title or number"`
	Title          string `json:"title,omitempty" jsonschema:"add, rename: the tab title"`
	Position       int    `json:"position,omitempty" jsonschema:"add, move: 1-based position among its sibling tabs"`
	Parent         string `json:"parent,omitempty" jsonschema:"add, move: nest under this tab (id, title or number)"`
	Emoji          string `json:"emoji,omitempty" jsonschema:"add, rename: a single emoji as the tab icon"`
	Content        string `json:"content,omitempty" jsonschema:"add: initial content as markdown"`
	ExpectRevision string `json:"expect_revision,omitempty"`
}

// DeleteTabInput is the delete_tab call.
type DeleteTabInput struct {
	Document       string `json:"document" jsonschema:"document id or any docs.google.com URL"`
	Tab            string `json:"tab" jsonschema:"the tab to delete, by id, title or number; its child tabs go with it"`
	ExpectRevision string `json:"expect_revision,omitempty"`
}

func registerTabs(s *mcp.Server, d Deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "manage_tabs",
		Description: "Add, rename or move a tab of a Google Doc. add takes a title, optional position, parent tab, emoji " +
			"and initial markdown content; rename changes the title or emoji; move changes the position among siblings " +
			"or nests the tab under a parent. Tab changes are always direct edits (the API cannot suggest them). Other " +
			"tools address tabs by id, title or number; get_document lists them.",
		Annotations: writeSafe,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in TabInput) (*mcp.CallToolResult, *service.TabResult, error) {
		res, err := d.Service.ManageTabs(ctx, service.TabRequest{Document: in.Document, Action: in.Action, Tab: in.Tab, Title: in.Title, Position: in.Position,
			Parent: in.Parent, Emoji: in.Emoji, Content: in.Content, ExpectRevision: in.ExpectRevision})
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(res.Text), res, nil
	})

	if !d.Config.EnableDestructive {
		return
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "delete_tab",
		Description: "Delete a tab of a Google Doc together with everything in it and any child tabs. Irreversible " +
			"through this server (version history keeps the content). Ask the person first; a document keeps at " +
			"least one tab.",
		Annotations: destructive,
		Meta:        destructiveMeta,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in DeleteTabInput) (*mcp.CallToolResult, *service.TabResult, error) {
		res, err := d.Service.DeleteTab(ctx, service.TabRequest{Document: in.Document, Tab: in.Tab, ExpectRevision: in.ExpectRevision})
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(res.Text), res, nil
	})
}
