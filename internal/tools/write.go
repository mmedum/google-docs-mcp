package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-docs-mcp/internal/plan"
	"github.com/mmedum/google-docs-mcp/internal/service"
)

// TargetInput points at content. Set exactly one selector.
type TargetInput struct {
	Text           string `json:"text,omitempty" jsonschema:"exact text to match, quoted verbatim from a read (curly quotes, dashes and spacing are normalised); must occur once unless occurrence or within narrows it"`
	Occurrence     int    `json:"occurrence,omitempty" jsonschema:"which occurrence of text to use when it repeats, 1-based"`
	Within         string `json:"within,omitempty" jsonschema:"restrict a text match to a block handle (p12), a heading_id, or heading:<heading text>"`
	HeadingID      string `json:"heading_id,omitempty" jsonschema:"a whole section by its stable heading id from get_outline: the heading plus everything until the next heading of the same or higher level"`
	Heading        string `json:"heading,omitempty" jsonschema:"a whole section by its heading text (whole heading, case-insensitive)"`
	HeadingLevel   int    `json:"heading_level,omitempty" jsonschema:"only match headings of this level (1-6) with heading"`
	IncludeHeading *bool  `json:"include_heading,omitempty" jsonschema:"for section targets: false selects only the content below the heading; default true"`
	Handle         string `json:"handle,omitempty" jsonschema:"one whole block by handle (p12, tbl1) from get_outline, find_in_document, or a read with with_handles"`
	FromHandle     string `json:"from_handle,omitempty" jsonschema:"first block of a whole-block range"`
	ToHandle       string `json:"to_handle,omitempty" jsonschema:"last block of the range, inclusive"`
	Cell           string `json:"cell,omitempty" jsonschema:"the content of one table cell, e.g. tbl1:r2c3"`
	Tab            string `json:"tab,omitempty" jsonschema:"tab id, title or number; default the first tab"`
	Segment        string `json:"segment,omitempty" jsonschema:"body (default), header, footer or footnote, optionally numbered"`
}

func (t *TargetInput) target() *service.Target {
	if t == nil {
		return nil
	}
	return &service.Target{Text: t.Text, Occurrence: t.Occurrence, Within: t.Within, HeadingID: t.HeadingID, Heading: t.Heading,
		HeadingLevel: t.HeadingLevel, IncludeHeading: t.IncludeHeading, Handle: t.Handle, From: t.FromHandle, To: t.ToHandle,
		Cell: t.Cell, Tab: t.Tab, Segment: t.Segment}
}

// LocationInput says where an insertion goes.
type LocationInput struct {
	At string       `json:"at,omitempty" jsonschema:"start, end, before or after; with no target: start or end of the body (default end); with a target: before or after it (default after), or start/end for inside a paragraph"`
	Of *TargetInput `json:"of,omitempty" jsonschema:"the block, section or text the position is relative to"`
}

func (l *LocationInput) location() *service.Location {
	if l == nil {
		return nil
	}
	return &service.Location{At: l.At, Of: l.Of.target()}
}

// EditOpInput is one edit_document operation.
type EditOpInput struct {
	Op            string         `json:"op" jsonschema:"insert, append, replace, delete, replace_all, insert_break (page break), insert_footnote, create_header, create_footer, delete_header, delete_footer"`
	Target        *TargetInput   `json:"target,omitempty" jsonschema:"what replace, delete or replace_all (tab only) act on"`
	Location      *LocationInput `json:"location,omitempty" jsonschema:"where insert, insert_break and insert_footnote go; append defaults to the end of the body"`
	Content       string         `json:"content,omitempty" jsonschema:"new content as markdown: paragraphs, # headings, **bold**, *italic*, ~~strike~~, code, [links](url), bullet and numbered lists (nested by indentation). Tables and images are not accepted here."`
	ContentFormat string         `json:"content_format,omitempty" jsonschema:"markdown (default) or text for verbatim text, one paragraph per line"`
	Find          string         `json:"find,omitempty" jsonschema:"replace_all: the text to find"`
	Replace       string         `json:"replace,omitempty" jsonschema:"replace_all: the replacement text (may be empty)"`
	MatchCase     bool           `json:"match_case,omitempty" jsonschema:"replace_all: match case exactly"`
}

// EditInput is the edit_document call.
type EditInput struct {
	Document       string        `json:"document" jsonschema:"document id or any docs.google.com URL"`
	Ops            []EditOpInput `json:"ops" jsonschema:"operations applied together as one atomic batch; targets are resolved against the document as it is now"`
	Mode           string        `json:"mode,omitempty" jsonschema:"suggest (tracked changes a person accepts; needs preview), direct (edit the text), or comment (post each change as a comment on the passage, changing nothing); default from get_document capabilities"`
	DryRun         bool          `json:"dry_run,omitempty" jsonschema:"resolve and plan everything, show the exact requests and the current text of the region, but send nothing"`
	ExpectRevision string        `json:"expect_revision,omitempty" jsonschema:"fail if the document is no longer at this revision id"`
	Force          bool          `json:"force,omitempty" jsonschema:"direct mode only: allow deleting ranges that hold comment anchors, pending suggestions, images or footnotes; ask the person first"`
}

// FormatOpInput is one format_document operation.
type FormatOpInput struct {
	Op                string      `json:"op" jsonschema:"text_style, paragraph_style, bullets, clear_formatting"`
	Target            TargetInput `json:"target"`
	Bold              *bool       `json:"bold,omitempty"`
	Italic            *bool       `json:"italic,omitempty"`
	Underline         *bool       `json:"underline,omitempty"`
	Strikethrough     *bool       `json:"strikethrough,omitempty"`
	SmallCaps         *bool       `json:"small_caps,omitempty"`
	Font              string      `json:"font,omitempty" jsonschema:"font family name, or none to inherit"`
	SizePt            float64     `json:"size_pt,omitempty" jsonschema:"font size in points"`
	Color             string      `json:"color,omitempty" jsonschema:"text colour as #rrggbb, or none"`
	Background        string      `json:"background,omitempty" jsonschema:"highlight colour as #rrggbb, or none"`
	Link              string      `json:"link,omitempty" jsonschema:"URL to link the text to, or none to remove the link"`
	Baseline          string      `json:"baseline,omitempty" jsonschema:"SUPERSCRIPT, SUBSCRIPT or NONE"`
	NamedStyle        string      `json:"named_style,omitempty" jsonschema:"paragraph_style: NORMAL_TEXT, TITLE, SUBTITLE, HEADING_1 … HEADING_6"`
	Alignment         string      `json:"alignment,omitempty" jsonschema:"paragraph_style: START, CENTER, END, JUSTIFIED"`
	LineSpacing       float64     `json:"line_spacing,omitempty" jsonschema:"paragraph_style: percent, 100 = single"`
	SpaceAbovePt      *float64    `json:"space_above_pt,omitempty"`
	SpaceBelowPt      *float64    `json:"space_below_pt,omitempty"`
	IndentPt          *float64    `json:"indent_pt,omitempty" jsonschema:"paragraph_style: left indent in points"`
	FirstLineIndentPt *float64    `json:"first_line_indent_pt,omitempty"`
	KeepWithNext      *bool       `json:"keep_with_next,omitempty"`
	Bullets           string      `json:"bullets,omitempty" jsonschema:"bullets op: bullet, numbered, checkbox, or none to remove list formatting"`
}

// FormatInput is the format_document call.
type FormatInput struct {
	Document       string          `json:"document" jsonschema:"document id or any docs.google.com URL"`
	Ops            []FormatOpInput `json:"ops"`
	Mode           string          `json:"mode,omitempty" jsonschema:"suggest, direct or comment; default from get_document capabilities"`
	DryRun         bool            `json:"dry_run,omitempty"`
	ExpectRevision string          `json:"expect_revision,omitempty"`
}

func registerWrite(s *mcp.Server, d Deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "edit_document",
		Description: "Change the text of a Google Doc with one atomic batch of operations. Address content by exact text " +
			"(quoted from a read), by heading_id or heading (a whole section), by block handle, or by cell; never by " +
			"position numbers. Ops: insert (at a location), append (end of body), replace (minimal diff, so untouched " +
			"words keep their formatting and comments), delete, replace_all (find/replace in one tab), insert_break " +
			"(page break), insert_footnote, create_header, create_footer, delete_header, delete_footer (target: tab and " +
			"segment). Content is markdown. mode chooses how the " +
			"change lands: suggest = tracked change for a person to accept, direct = edit the text, comment = post " +
			"each proposed change as a comment and change nothing. Direct edits refuse to delete ranges holding " +
			"comments, suggestions, images or footnotes unless force is set. Use dry_run to preview the plan. " +
			"Returns the new revision id, suggestion or comment ids, and a rendered preview of the edited region.",
		Annotations: writeSafe,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in EditInput) (*mcp.CallToolResult, *service.EditResult, error) {
		ops := make([]service.EditOp, 0, len(in.Ops))
		for i, o := range in.Ops {
			kind := plan.OpKind(strings.ToLower(strings.TrimSpace(o.Op)))
			switch kind {
			case plan.OpInsert, plan.OpAppend, plan.OpReplace, plan.OpDelete, plan.OpReplaceAll, plan.OpPageBreak, plan.OpFootnote, plan.OpCreateHeader, plan.OpCreateFooter, plan.OpDeleteHeader, plan.OpDeleteFooter:
			default:
				return nil, nil, fail(service.Errorf("invalid", "op %d: unknown op %q; use insert, append, replace, delete, replace_all, insert_break, insert_footnote, create_header, create_footer, delete_header or delete_footer", i, o.Op))
			}
			eo := service.EditOp{Kind: kind, Target: o.Target.target(), Content: o.Content, ContentFormat: o.ContentFormat,
				Params: plan.Params{Find: o.Find, Replace: o.Replace, MatchCase: o.MatchCase}}
			eo.Location = o.Location.location()
			ops = append(ops, eo)
		}
		res, err := d.Service.Edit(ctx, service.EditRequest{Document: in.Document, Ops: ops, Mode: in.Mode, DryRun: in.DryRun, ExpectRevision: in.ExpectRevision, Force: in.Force})
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(editText(res)), res, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "format_document",
		Description: "Change formatting in a Google Doc without changing its text: text_style (bold, italic, underline, " +
			"strikethrough, small caps, font, size, colour, highlight, link, superscript), paragraph_style (named style " +
			"such as HEADING_2 or NORMAL_TEXT, alignment, line spacing, spacing, indents), bullets (bullet, numbered, " +
			"checkbox, or none), and clear_formatting. Targets work as in edit_document. Same mode, dry_run and " +
			"expect_revision semantics.",
		Annotations: writeSafe,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in FormatInput) (*mcp.CallToolResult, *service.EditResult, error) {
		ops := make([]service.EditOp, 0, len(in.Ops))
		for i, o := range in.Ops {
			kind := plan.OpKind(strings.ToLower(strings.TrimSpace(o.Op)))
			t := o.Target
			eo := service.EditOp{Kind: kind, Target: t.target()}
			// Enum and colour validation lives in the planner; the tool only
			// normalises case so people can write heading_2 or center.
			switch kind {
			case plan.OpTextStyle:
				eo.Text = plan.TextStyleSpec{Bold: o.Bold, Italic: o.Italic, Underline: o.Underline, Strikethrough: o.Strikethrough, SmallCaps: o.SmallCaps,
					Font: o.Font, SizePt: o.SizePt, Foreground: o.Color, Background: o.Background, Link: o.Link, Baseline: strings.ToUpper(strings.TrimSpace(o.Baseline))}
			case plan.OpParagraphStyle:
				eo.Para = plan.ParagraphStyleSpec{NamedStyle: strings.ToUpper(strings.TrimSpace(o.NamedStyle)), Alignment: strings.ToUpper(strings.TrimSpace(o.Alignment)),
					LineSpacing: o.LineSpacing, SpaceAbovePt: o.SpaceAbovePt, SpaceBelowPt: o.SpaceBelowPt, IndentStartPt: o.IndentPt, IndentFirstLine: o.FirstLineIndentPt, KeepWithNext: o.KeepWithNext}
			case plan.OpBullets:
				eo.Bullets = strings.ToLower(strings.TrimSpace(o.Bullets))
			case plan.OpClearFormatting:
			default:
				return nil, nil, fail(service.Errorf("invalid", "op %d: unknown op %q; use text_style, paragraph_style, bullets or clear_formatting", i, o.Op))
			}
			ops = append(ops, eo)
		}
		res, err := d.Service.Edit(ctx, service.EditRequest{Document: in.Document, Ops: ops, Mode: in.Mode, DryRun: in.DryRun, ExpectRevision: in.ExpectRevision})
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(editText(res)), res, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "create_document",
		Description: "Create a new Google Doc in the signed-in account's Drive, optionally with initial content written " +
			"as markdown (headings, formatting, lists). Returns the id and URL. Initial content is written directly " +
			"because a new document has nothing to suggest against.",
		Annotations: writeSafe,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in CreateInput) (*mcp.CallToolResult, *service.CreateResult, error) {
		res, err := d.Service.Create(ctx, service.CreateRequest{Title: in.Title, Content: in.Content, ContentFormat: in.ContentFormat})
		if err != nil {
			return nil, nil, fail(err)
		}
		msg := fmt.Sprintf("created %q\n%s\nrevision %s", res.Title, res.URL, res.RevisionID)
		for _, w := range res.Warnings {
			msg += "\nwarning: " + w
		}
		return text(msg), res, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "review_suggestion",
		Description: "Accept or reject pending suggested edits by id (from list_suggestions) or all of them. Needs " +
			"Developer Preview. Accepting applies the suggested text; rejecting discards it. Pass expect_revision to " +
			"refuse if the document changed since the list was read.",
		Annotations: writeSafe,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ReviewInput) (*mcp.CallToolResult, *service.ReviewResult, error) {
		res, err := d.Service.Review(ctx, service.ReviewRequest{Document: in.Document, Action: in.Action, IDs: in.IDs, All: in.All, ExpectRevision: in.ExpectRevision})
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(res.Text), res, nil
	})
}

// CreateInput is the create_document call.
type CreateInput struct {
	Title         string `json:"title" jsonschema:"document title"`
	Content       string `json:"content,omitempty" jsonschema:"initial content as markdown"`
	ContentFormat string `json:"content_format,omitempty" jsonschema:"markdown (default) or text"`
}

// ReviewInput is the review_suggestion call.
type ReviewInput struct {
	Document       string   `json:"document" jsonschema:"document id or any docs.google.com URL"`
	Action         string   `json:"action" jsonschema:"accept or reject"`
	IDs            []string `json:"ids,omitempty" jsonschema:"suggestion ids from list_suggestions"`
	All            bool     `json:"all,omitempty" jsonschema:"act on every pending suggestion"`
	ExpectRevision string   `json:"expect_revision,omitempty"`
}

func editText(r *service.EditResult) string {
	var b strings.Builder
	if r.DryRun {
		fmt.Fprintf(&b, "dry run in %s mode at revision %s: %d op(s) planned, nothing sent\n", r.Mode, r.RevisionID, len(r.Changes))
	} else {
		fmt.Fprintf(&b, "applied %d op(s) in %s mode; revision %s\n", r.Applied, r.Mode, r.RevisionID)
	}
	for _, c := range r.Changes {
		fmt.Fprintf(&b, "- op %d %s: %s", c.Seq, c.Kind, c.Description)
		if c.Minimal {
			b.WriteString(" (minimal diff)")
		}
		b.WriteString("\n")
	}
	if len(r.SuggestionIDs) > 0 {
		fmt.Fprintf(&b, "suggestion ids: %s\n", strings.Join(r.SuggestionIDs, ", "))
	}
	if len(r.CommentIDs) > 0 {
		fmt.Fprintf(&b, "comment ids: %s\n", strings.Join(r.CommentIDs, ", "))
	}
	for _, w := range r.Warnings {
		fmt.Fprintf(&b, "warning: %s\n", w)
	}
	if r.DryRun && len(r.Proposals) > 0 {
		b.WriteString("proposed comments:\n")
		for _, p := range r.Proposals {
			fmt.Fprintf(&b, "- op %d: %s\n", p.Seq, strings.ReplaceAll(p.Content, "\n", " "))
		}
	}
	if r.DryRun && len(r.RequestKinds) > 0 {
		fmt.Fprintf(&b, "requests: %s\n", strings.Join(r.RequestKinds, ", "))
	}
	if r.Preview != "" {
		label := "region after the edit"
		if r.DryRun {
			label = "region as it is now"
		}
		fmt.Fprintf(&b, "%s:\n%s\n", label, r.Preview)
	}
	return strings.TrimRight(b.String(), "\n")
}
