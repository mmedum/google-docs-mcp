package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-docs-mcp/internal/service"
)

// FindInput is the find_in_document call.
type FindInput struct {
	Document  string `json:"document" jsonschema:"document id or any docs.google.com URL"`
	Query     string `json:"query" jsonschema:"text to find (normalised exact match) or a regular expression when regex is true"`
	Regex     bool   `json:"regex,omitempty" jsonschema:"treat query as an RE2 regular expression"`
	MatchCase bool   `json:"match_case,omitempty" jsonschema:"match case exactly; default case-insensitive"`
	Tab       string `json:"tab,omitempty" jsonschema:"tab id, title or number; default the first tab"`
	Segment   string `json:"segment,omitempty" jsonschema:"body (default), header, footer or footnote"`
	Limit     int    `json:"limit,omitempty" jsonschema:"maximum matches to return; default 50"`
	Context   int    `json:"context,omitempty" jsonschema:"characters of context on each side; default 60"`
}

// SearchInput is the search_documents call.
type SearchInput struct {
	Query         string `json:"query,omitempty" jsonschema:"words that must appear in the document text"`
	Title         string `json:"title,omitempty" jsonschema:"words that must appear in the title"`
	ModifiedAfter string `json:"modified_after,omitempty" jsonschema:"only documents modified after this date (YYYY-MM-DD or RFC 3339)"`
	Owner         string `json:"owner,omitempty" jsonschema:"owner email address"`
	Limit         int    `json:"limit,omitempty" jsonschema:"page size, default 20, max 100"`
	PageToken     string `json:"page_token,omitempty" jsonschema:"next_page_token from a previous result"`
}

// ExportInput is the export_document call.
type ExportInput struct {
	Document string `json:"document" jsonschema:"document id or any docs.google.com URL"`
	Format   string `json:"format" jsonschema:"md, txt or html (returned inline); pdf, docx, odt, rtf or epub (written under GDOCS_EXPORT_DIR)"`
	MaxChars int    `json:"max_chars,omitempty" jsonschema:"inline formats: character budget, default 20000"`
}

func registerMoreRead(s *mcp.Server, d Deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "find_in_document",
		Description: "Find text or a regular expression in a Google Doc and return every match with its block handle, " +
			"offset and surrounding context. Use it to locate passages before targeting them with edit_document " +
			"(quote the matched text as the target) and to check how many times a phrase occurs.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in FindInput) (*mcp.CallToolResult, *service.FindResult, error) {
		res, err := d.Service.Find(ctx, service.FindRequest{Document: in.Document, Query: in.Query, Regex: in.Regex, MatchCase: in.MatchCase,
			Tab: in.Tab, Segment: in.Segment, Limit: in.Limit, Context: in.Context})
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(res.Text), res, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "search_documents",
		Description: "Locate Google Docs the signed-in account can open, by words in the title or the text, optionally " +
			"limited by owner or modification date, newest first. Returns ids and URLs to pass to the other tools. " +
			"With no filters it lists recently modified documents.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SearchInput) (*mcp.CallToolResult, *service.SearchResult, error) {
		res, err := d.Service.Search(ctx, service.SearchRequest{Query: in.Query, Title: in.Title, ModifiedAfter: in.ModifiedAfter, Owner: in.Owner, Limit: in.Limit, PageToken: in.PageToken})
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(res.Text), res, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "export_document",
		Description: "Export a Google Doc through Google's converters. md, txt and html come back inline (budgeted by " +
			"max_chars); pdf, docx, odt, rtf and epub are written as files under the server's export directory and " +
			"the path is returned. The markdown export is Google's own rendering, useful for a faithful whole-document " +
			"dump; read_document is better for working with sections.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ExportInput) (*mcp.CallToolResult, *service.ExportResult, error) {
		res, err := d.Service.Export(ctx, service.ExportRequest{Document: in.Document, Format: in.Format, MaxChars: in.MaxChars})
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(res.Text), res, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "list_suggestions",
		Description: "List pending suggested edits (tracked changes) in a Google Doc: id, kind (insert, delete, replace, " +
			"structure), the inserted and deleted text, the block handle, and, with Developer Preview, the author and " +
			"status. Ids feed review_suggestion.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in DocumentInput) (*mcp.CallToolResult, *service.SuggestionsResult, error) {
		res, err := d.Service.ListSuggestions(ctx, in.Document)
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(res.Text), res, nil
	})
}
