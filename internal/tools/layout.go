package tools

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-docs-mcp/internal/plan"
	"github.com/mmedum/google-docs-mcp/internal/service"
)

// LayoutOpInput is one layout_document operation. Every measurement is
// in points: 72 pt to the inch, so a one-inch margin is 72.
type LayoutOpInput struct {
	Op       string         `json:"op" jsonschema:"page (page size, margins, background, page numbering), section (columns and margins of one section), section_break (start a new section), named_style (redefine a heading or body style for the whole tab)"`
	Tab      string         `json:"tab,omitempty" jsonschema:"page and named_style: tab id, title or number; default the first tab"`
	Target   *TargetInput   `json:"target,omitempty" jsonschema:"section: any text or block inside the section to style"`
	Location *LocationInput `json:"location,omitempty" jsonschema:"section_break: where the new section starts"`

	PageWidthPt           float64  `json:"page_width_pt,omitempty" jsonschema:"page: page width in points (US Letter is 612, A4 is 595); set with page_height_pt"`
	PageHeightPt          float64  `json:"page_height_pt,omitempty" jsonschema:"page: page height in points (US Letter is 792, A4 is 842)"`
	MarginTopPt           *float64 `json:"margin_top_pt,omitempty" jsonschema:"page and section: top margin in points"`
	MarginBottomPt        *float64 `json:"margin_bottom_pt,omitempty" jsonschema:"page and section: bottom margin in points"`
	MarginLeftPt          *float64 `json:"margin_left_pt,omitempty" jsonschema:"page and section: left margin in points"`
	MarginRightPt         *float64 `json:"margin_right_pt,omitempty" jsonschema:"page and section: right margin in points"`
	MarginHeaderPt        *float64 `json:"margin_header_pt,omitempty" jsonschema:"page: distance from the top of the page to the header"`
	MarginFooterPt        *float64 `json:"margin_footer_pt,omitempty" jsonschema:"page: distance from the bottom of the page to the footer"`
	Background            string   `json:"background,omitempty" jsonschema:"page: page colour as #rrggbb, or none"`
	Landscape             *bool    `json:"landscape,omitempty" jsonschema:"page and section: turn the page on its side"`
	PageNumberStart       *int     `json:"page_number_start,omitempty" jsonschema:"page and section: the number the first page carries"`
	FirstPageHeaderFooter *bool    `json:"first_page_header_footer,omitempty" jsonschema:"page and section: give the first page its own header and footer"`
	EvenPageHeaderFooter  *bool    `json:"even_page_header_footer,omitempty" jsonschema:"page: give even pages their own header and footer"`

	Columns          int      `json:"columns,omitempty" jsonschema:"section: how many columns the text runs in, 1-3"`
	ColumnGapPt      *float64 `json:"column_gap_pt,omitempty" jsonschema:"section: space after each column in points"`
	ColumnSeparator  string   `json:"column_separator,omitempty" jsonschema:"section: none (default) or between, for a line between columns"`
	ContentDirection string   `json:"content_direction,omitempty" jsonschema:"section: left_to_right (default) or right_to_left"`
	SectionType      string   `json:"section_type,omitempty" jsonschema:"section_break: next_page (default) or continuous. A section's type is fixed by the break that made it and cannot be changed afterwards."`

	Style             string   `json:"style,omitempty" jsonschema:"named_style: which style to redefine — NORMAL_TEXT, TITLE, SUBTITLE, HEADING_1 … HEADING_6"`
	Bold              *bool    `json:"bold,omitempty" jsonschema:"named_style: as in format_document"`
	Italic            *bool    `json:"italic,omitempty"`
	Underline         *bool    `json:"underline,omitempty"`
	Strikethrough     *bool    `json:"strikethrough,omitempty"`
	SmallCaps         *bool    `json:"small_caps,omitempty"`
	Font              string   `json:"font,omitempty" jsonschema:"named_style: font family name, or none to inherit"`
	SizePt            float64  `json:"size_pt,omitempty" jsonschema:"named_style: font size in points"`
	Color             string   `json:"color,omitempty" jsonschema:"named_style: text colour as #rrggbb, or none"`
	TextBackground    string   `json:"text_background,omitempty" jsonschema:"named_style: highlight colour as #rrggbb, or none"`
	Alignment         string   `json:"alignment,omitempty" jsonschema:"named_style: START, CENTER, END, JUSTIFIED"`
	LineSpacing       float64  `json:"line_spacing,omitempty" jsonschema:"named_style: percent, 100 = single"`
	SpaceAbovePt      *float64 `json:"space_above_pt,omitempty"`
	SpaceBelowPt      *float64 `json:"space_below_pt,omitempty"`
	IndentPt          *float64 `json:"indent_pt,omitempty"`
	FirstLineIndentPt *float64 `json:"first_line_indent_pt,omitempty"`
	KeepWithNext      *bool    `json:"keep_with_next,omitempty"`
}

// LayoutInput is the layout_document call.
type LayoutInput struct {
	Document       string          `json:"document" jsonschema:"document id or any docs.google.com URL"`
	Ops            []LayoutOpInput `json:"ops" jsonschema:"operations applied together as one atomic batch"`
	Mode           string          `json:"mode,omitempty" jsonschema:"suggest, direct or comment; default from get_document capabilities"`
	DryRun         bool            `json:"dry_run,omitempty"`
	ExpectRevision string          `json:"expect_revision,omitempty"`
}

// editOp turns one input op into a service op, or reports why it cannot.
func (o LayoutOpInput) editOp(kind plan.OpKind) service.EditOp {
	up := func(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }
	margins := plan.PageMargins{TopPt: o.MarginTopPt, BottomPt: o.MarginBottomPt, LeftPt: o.MarginLeftPt, RightPt: o.MarginRightPt}
	sectionType := ""
	switch up(o.SectionType) {
	case "":
	case "CONTINUOUS":
		sectionType = plan.SectionContinuous
	default:
		sectionType = up(o.SectionType)
	}
	if kind == plan.OpSectionBreak && sectionType == "" {
		sectionType = plan.SectionNextPage
	}
	separator := ""
	switch strings.ToLower(strings.TrimSpace(o.ColumnSeparator)) {
	case "":
	case "between":
		separator = "BETWEEN_EACH_COLUMN"
	case "none":
		separator = "NONE"
	default:
		separator = up(o.ColumnSeparator)
	}
	l := &service.LayoutOp{SectionType: sectionType}
	switch kind {
	case plan.OpPageSetup:
		l.Page = plan.PageSpec{WidthPt: o.PageWidthPt, HeightPt: o.PageHeightPt, PageMargins: margins,
			MarginHeaderPt: o.MarginHeaderPt, MarginFooterPt: o.MarginFooterPt, Background: o.Background,
			PageNumberStart: o.PageNumberStart, Landscape: o.Landscape,
			FirstPageHeaderFooter: o.FirstPageHeaderFooter, EvenPageHeaderFooter: o.EvenPageHeaderFooter}
	case plan.OpSectionStyle:
		l.Section = plan.SectionSpec{PageMargins: margins, Columns: o.Columns,
			ColumnGapPt: o.ColumnGapPt, ColumnSeparator: separator, ContentDirection: up(o.ContentDirection),
			PageNumberStart: o.PageNumberStart, Landscape: o.Landscape, FirstPageHeaderFooter: o.FirstPageHeaderFooter}
	case plan.OpNamedStyle:
		l.NamedStyle = plan.NamedStyleSpec{
			Style: up(o.Style),
			Text: plan.TextStyleSpec{Bold: o.Bold, Italic: o.Italic, Underline: o.Underline, Strikethrough: o.Strikethrough,
				SmallCaps: o.SmallCaps, Font: o.Font, SizePt: o.SizePt, Foreground: o.Color, Background: o.TextBackground},
			Para: plan.ParagraphStyleSpec{Alignment: up(o.Alignment), LineSpacing: o.LineSpacing,
				SpaceAbovePt: o.SpaceAbovePt, SpaceBelowPt: o.SpaceBelowPt, IndentStartPt: o.IndentPt,
				IndentFirstLine: o.FirstLineIndentPt, KeepWithNext: o.KeepWithNext},
		}
	}
	target := o.Target.target()
	if kind == plan.OpPageSetup || kind == plan.OpNamedStyle {
		target = &service.Target{Tab: o.Tab}
	}
	return service.EditOp{Kind: kind, Target: target, Location: o.Location.location(), Layout: l}
}

func registerLayout(s *mcp.Server, d Deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "layout_document",
		Description: "Change how a Google Doc is laid out rather than what it says. Ops: page (page size, margins, " +
			"background colour, landscape, where page numbering starts, whether the first and even pages get their own " +
			"header and footer), section (the same for one section, plus 1-3 columns with an optional separating line " +
			"and the gap between them), section_break (start a new section at a location, continuous or on the next " +
			"page), and named_style (redefine NORMAL_TEXT, TITLE, SUBTITLE or HEADING_1 … HEADING_6 for the whole tab, " +
			"which restyles every paragraph carrying that style and everything written with it later; get_document " +
			"reports the definition of each style the tab's paragraphs currently carry, so read it first to see what " +
			"one looks like today). Lengths are in " +
			"points: 72 to the inch, so US Letter is 612×792 and A4 is 595×842. Use format_document instead to style " +
			"one passage. Same mode, dry_run and expect_revision semantics as edit_document.",
		Annotations: writeSafe,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in LayoutInput) (*mcp.CallToolResult, *service.EditResult, error) {
		ops := make([]service.EditOp, 0, len(in.Ops))
		for i, o := range in.Ops {
			kind := plan.OpKind(strings.ToLower(strings.TrimSpace(o.Op)))
			if !plan.ToolLayout.Has(kind) {
				return nil, nil, fail(service.Errorf("invalid", "op %d: unknown op %q; use %s", i, o.Op, plan.KindList(plan.ToolLayout)))
			}
			if kind == plan.OpSectionStyle && strings.TrimSpace(o.SectionType) != "" {
				return nil, nil, fail(service.Errorf("invalid",
					"op %d: a section's type is read-only; it is fixed by the section_break that made the section", i))
			}
			ops = append(ops, o.editOp(kind))
		}
		res, err := d.Service.Edit(ctx, service.EditRequest{Document: in.Document, Ops: ops, Mode: in.Mode, DryRun: in.DryRun, ExpectRevision: in.ExpectRevision})
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(res.Text), res, nil
	})
}
