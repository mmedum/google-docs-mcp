package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-docs-mcp/internal/render"
	"github.com/mmedum/google-docs-mcp/internal/service"
)

// MaxMaxChars caps a caller's budget.
const MaxMaxChars = 400000

// DocumentInput identifies a document.
type DocumentInput struct {
	Document string `json:"document" jsonschema:"document id or any docs.google.com URL"`
}

// OutlineInput selects a document and optionally one tab.
type OutlineInput struct {
	Document string `json:"document" jsonschema:"document id or any docs.google.com URL"`
	Tab      string `json:"tab,omitempty" jsonschema:"tab id, title or number to limit the outline to; default all tabs"`
}

// OutlineOutput is the structured outline.
type OutlineOutput struct {
	RevisionID string              `json:"revision_id"`
	Tabs       []render.OutlineTab `json:"tabs"`
}

// ReadInput scopes a read.
type ReadInput struct {
	Document           string `json:"document" jsonschema:"document id or any docs.google.com URL"`
	Tab                string `json:"tab,omitempty" jsonschema:"tab id, title or number; default the first tab"`
	Segment            string `json:"segment,omitempty" jsonschema:"body (default), header, footer or footnote, optionally numbered (header2, footnote3)"`
	HeadingID          string `json:"heading_id,omitempty" jsonschema:"read one section by its stable heading id from get_outline; overrides tab and segment"`
	Heading            string `json:"heading,omitempty" jsonschema:"read one section by its heading text (whole heading, case-insensitive)"`
	HeadingLevel       int    `json:"heading_level,omitempty" jsonschema:"only match headings of this level (1-6) when using heading"`
	Occurrence         int    `json:"occurrence,omitempty" jsonschema:"which match to use when the heading text repeats, 1-based"`
	FromHandle         string `json:"from_handle,omitempty" jsonschema:"first block handle of a range, e.g. p12 (from get_outline or a read with with_handles)"`
	ToHandle           string `json:"to_handle,omitempty" jsonschema:"last block handle of the range, inclusive"`
	ContinueFrom       string `json:"continue_from,omitempty" jsonschema:"the continue_from handle returned by a truncated read; resumes there"`
	Format             string `json:"format,omitempty" jsonschema:"markdown (default), text, or raw (Docs API JSON for the scoped blocks)"`
	WithHandles        bool   `json:"with_handles,omitempty" jsonschema:"prefix every block with its handle like [p12] and headings with {heading_id}; needed before targeting specific blocks; costs about 15% more tokens"`
	WithStyles         bool   `json:"with_styles,omitempty" jsonschema:"annotate fonts, sizes, colours, underline and alignment that markdown cannot express, e.g. {font: Arial 11pt, color: #c00}"`
	IncludeSuggestions bool   `json:"include_suggestions,omitempty" jsonschema:"show pending suggested edits as CriticMarkup: {++inserted++} and {--deleted--} followed by {>>s:<suggestion id><<}; default shows the committed text without them"`
	IncludeComments    bool   `json:"include_comments,omitempty" jsonschema:"mark commented passages with {>>c:<comment id><<} right after the text they cover and list those threads below the content"`
	MaxChars           int    `json:"max_chars,omitempty" jsonschema:"output budget in characters, cut at a block boundary; default 20000, maximum 400000"`
	Revision           string `json:"revision,omitempty" jsonschema:"read an old revision by its id from list_revisions: Google's markdown or text export of the whole document at that time, with no handles or scoping"`
}

// ReadOutput describes what was read. Revision is a Drive revision id
// when an old revision was read; revision_id is then empty because that
// content has no concurrency token.
type ReadOutput struct {
	RevisionID   string `json:"revision_id,omitempty"`
	Revision     string `json:"revision,omitempty"`
	Tab          int    `json:"tab"`
	TabID        string `json:"tab_id,omitempty"`
	TabTitle     string `json:"tab_title,omitempty"`
	Segment      string `json:"segment"`
	Scope        string `json:"scope"`
	Blocks       int    `json:"blocks"`
	Chars        int    `json:"chars"`
	Truncated    bool   `json:"truncated"`
	ContinueFrom string `json:"continue_from,omitempty"`
}

func registerRead(s *mcp.Server, d Deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "get_document",
		Description: "Get metadata and structure counts for a Google Doc: title, tabs, the current revision id, owner, " +
			"last modification, paragraph and word counts, pending suggestion count, and this server's capabilities " +
			"(whether suggestion mode is available, the default write mode, read-only). Cheap; call it first when handed a " +
			"document id or URL. Then use get_outline for the heading tree and read_document for content.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in DocumentInput) (*mcp.CallToolResult, *service.Info, error) {
		info, err := d.Service.Info(ctx, in.Document)
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(infoText(info)), info, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "get_outline",
		Description: "Return the heading tree of a Google Doc per tab: each heading's stable heading_id, block handle, " +
			"level, text, and the size of its section in blocks and words, plus per-tab counts of paragraphs, tables, " +
			"headers, footers and footnotes. Use it to pick a section to read with read_document (pass heading_id) " +
			"instead of reading a long document whole, and to learn block handles. Cheap.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in OutlineInput) (*mcp.CallToolResult, *OutlineOutput, error) {
		res, err := d.Service.Outline(ctx, in.Document, in.Tab)
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(res.Text), &OutlineOutput{RevisionID: res.RevisionID, Tabs: res.Tabs}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "read_document",
		Description: "Read part or all of a Google Doc as markdown (default), plain text, or raw Docs API JSON. " +
			"Scope it to one section with heading_id (from get_outline) or heading text, to a block range with " +
			"from_handle/to_handle, to another tab with tab, or to a header, footer or footnote with segment; " +
			"with no scope it reads the whole body of the first tab. Output is budgeted by max_chars (default 20000 " +
			"characters); when truncated the result carries continue_from to pass back. Set with_handles to see block " +
			"handles ([p12]) and heading ids before targeting content, with_styles to see formatting markdown cannot " +
			"show, include_suggestions to see pending suggested edits inline as {++inserted++} / {--deleted--}, and " +
			"include_comments to see {>>c:id<<} markers after commented passages with the threads listed below. " +
			"Empty paragraphs are kept so structure is faithful. Handles are valid for the revision_id returned.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ReadInput) (*mcp.CallToolResult, *ReadOutput, error) {
		if in.MaxChars < 0 {
			return nil, nil, fail(service.Errorf("invalid", "max_chars must be positive"))
		}
		maxChars := in.MaxChars
		if maxChars == 0 {
			maxChars = service.DefaultMaxChars
		}
		if maxChars > MaxMaxChars {
			maxChars = MaxMaxChars
		}
		if in.HeadingLevel < 0 || in.HeadingLevel > 6 {
			return nil, nil, fail(service.Errorf("invalid", "heading_level must be 1-6"))
		}
		if in.Occurrence < 0 {
			return nil, nil, fail(service.Errorf("invalid", "occurrence must be 1 or more"))
		}
		if in.Revision != "" {
			res, err := d.Service.ReadRevision(ctx, in.Document, in.Revision, in.Format, maxChars)
			if err != nil {
				return nil, nil, fail(err)
			}
			out := &ReadOutput{Revision: res.Revision, Segment: res.Segment, Scope: res.Scope, Chars: res.Chars, Truncated: res.Truncated}
			return text(readHeader(out, in.Format) + res.Text), out, nil
		}
		res, err := d.Service.Read(ctx, service.ReadRequest{
			Document: in.Document,
			Scope: service.ReadScope{
				Tab: in.Tab, Segment: in.Segment, HeadingID: in.HeadingID, Heading: in.Heading,
				HeadingLevel: in.HeadingLevel, Occurrence: in.Occurrence,
				FromHandle: in.FromHandle, ToHandle: in.ToHandle, ContinueFrom: in.ContinueFrom,
			},
			Format:          in.Format,
			Options:         render.Options{WithHandles: in.WithHandles, WithStyles: in.WithStyles, Suggestions: in.IncludeSuggestions, MaxChars: maxChars},
			IncludeComments: in.IncludeComments,
		})
		if err != nil {
			return nil, nil, fail(err)
		}
		out := &ReadOutput{RevisionID: res.RevisionID, Tab: res.TabNumber, TabID: res.TabID, TabTitle: res.TabTitle,
			Segment: res.Segment, Scope: res.Scope, Blocks: res.Blocks, Chars: res.Chars, Truncated: res.Truncated, ContinueFrom: res.ContinueFrom}
		return text(readHeader(out, in.Format) + res.Text), out, nil
	})
}

func readHeader(o *ReadOutput, format string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<!-- %s", o.Scope)
	if o.RevisionID != "" {
		fmt.Fprintf(&b, " · revision %s · %d block(s)", o.RevisionID, o.Blocks)
	}
	if o.Truncated {
		b.WriteString(" · truncated")
		if o.ContinueFrom != "" {
			fmt.Fprintf(&b, ", continue_from %s", o.ContinueFrom)
		}
	}
	b.WriteString(" -->\n")
	if strings.EqualFold(format, service.FormatRaw) {
		return ""
	}
	return b.String()
}

func infoText(i *service.Info) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n%s\nrevision %s\n", i.Title, i.URL, i.RevisionID)
	if i.Owner != "" {
		fmt.Fprintf(&b, "owner %s\n", i.Owner)
	}
	if i.LastModified != "" {
		fmt.Fprintf(&b, "last modified %s", i.LastModified)
		if i.LastModifiedBy != "" {
			fmt.Fprintf(&b, " by %s", i.LastModifiedBy)
		}
		b.WriteString("\n")
	}
	st := i.Stats
	fmt.Fprintf(&b, "%d tab(s), %d paragraphs, %d headings, %d tables, %d inline objects, %d footnotes, %d words",
		st.Tabs, st.Paragraphs, st.Headings, st.Tables, st.InlineObjects, st.Footnotes, st.Words)
	if st.Suggestions > 0 {
		fmt.Fprintf(&b, ", %d pending suggestion(s)", st.Suggestions)
	}
	b.WriteString("\n")
	for _, t := range i.Tabs {
		fmt.Fprintf(&b, "- tab %d %q", t.Number, t.Title)
		if t.ID != "" {
			fmt.Fprintf(&b, " (id %s)", t.ID)
		}
		fmt.Fprintf(&b, ": %d headings, %d blocks\n", t.Headings, t.Blocks)
	}
	c := i.Capabilities
	fmt.Fprintf(&b, "server: write modes %s (default %s), preview %t, read-only %t\n", strings.Join(c.WriteModes, "/"), c.DefaultWriteMode, c.Preview, c.ReadOnly)
	for _, w := range i.Warnings {
		fmt.Fprintf(&b, "warning: %s\n", w)
	}
	return strings.TrimRight(b.String(), "\n")
}
