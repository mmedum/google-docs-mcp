package doc

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mmedum/google-docs-mcp/internal/gdocs"
)

// Parse converts a documents.get response into the model.
func Parse(d *gdocs.Document) (*Document, error) {
	if d == nil {
		return nil, errors.New("doc: nil document")
	}
	out := &Document{
		ID:                  d.DocumentID,
		Title:               d.Title,
		RevisionID:          d.RevisionID,
		SuggestionsViewMode: d.SuggestionsViewMode,
	}
	if len(d.Tabs) == 0 {
		// Response without tabs content: treat the top-level fields as
		// the single tab.
		legacy := &gdocs.Tab{
			TabProperties: &gdocs.TabProperties{Title: d.Title},
			DocumentTab: &gdocs.DocumentTab{
				Body: d.Body, Headers: d.Headers, Footers: d.Footers, Footnotes: d.Footnotes,
				Lists: d.Lists, InlineObjects: d.InlineObjects,
			},
		}
		out.Tabs = append(out.Tabs, parseTab(legacy, 1))
		return out, nil
	}
	var walk func([]*gdocs.Tab)
	walk = func(tabs []*gdocs.Tab) {
		for _, t := range tabs {
			out.Tabs = append(out.Tabs, parseTab(t, len(out.Tabs)+1))
			walk(t.ChildTabs)
		}
	}
	walk(d.Tabs)
	return out, nil
}

func parseTab(t *gdocs.Tab, number int) *Tab {
	tab := &Tab{Number: number, Lists: map[string]*ListInfo{}, InlineObjects: map[string]*InlineObjectInfo{}}
	if p := t.TabProperties; p != nil {
		tab.ID, tab.Title, tab.Index, tab.ParentID, tab.Nesting = p.TabID, p.Title, p.Index, p.ParentTabID, int(p.NestingLevel)
	}
	dt := t.DocumentTab
	if dt == nil {
		dt = &gdocs.DocumentTab{}
	}
	for id, l := range dt.Lists {
		info := &ListInfo{ID: id}
		if l.ListProperties != nil {
			for _, nl := range l.ListProperties.NestingLevels {
				if nl == nil {
					continue
				}
				info.Levels = append(info.Levels, ListLevel{Ordered: isOrderedGlyph(nl.GlyphType), Glyph: nl.GlyphSymbol, GlyphType: nl.GlyphType})
			}
		}
		tab.Lists[id] = info
	}
	for id, obj := range dt.InlineObjects {
		tab.InlineObjects[id] = parseInlineObject(id, &obj)
	}

	prefix := tab.Prefix()
	var body []*gdocs.StructuralElement
	if dt.Body != nil {
		body = dt.Body.Content
	}
	tab.Body = parseSegment(tab, SegmentBody, "", 0, prefix, body)

	for i, id := range sortedKeys(dt.Headers) {
		h := dt.Headers[id]
		tab.Headers = append(tab.Headers, parseSegment(tab, SegmentHeader, id, i+1, prefix+"header"+itoa(i+1)+"/", h.Content))
	}
	for i, id := range sortedKeys(dt.Footers) {
		f := dt.Footers[id]
		tab.Footers = append(tab.Footers, parseSegment(tab, SegmentFooter, id, i+1, prefix+"footer"+itoa(i+1)+"/", f.Content))
	}

	tab.Footnotes = parseFootnotes(tab, dt, prefix)
	return tab
}

// parseFootnotes orders footnote segments by the number their references
// carry in the body, then by id for unnumbered ones.
func parseFootnotes(tab *Tab, dt *gdocs.DocumentTab, prefix string) []*Segment {
	numbers := map[string]string{}
	for _, b := range tab.Body.AllBlocks() {
		if b.Paragraph == nil {
			continue
		}
		for _, r := range b.Paragraph.Runs {
			if r.Kind == RunFootnoteRef && r.FootnoteID != "" {
				numbers[r.FootnoteID] = r.FootnoteNumber
			}
		}
	}
	ids := sortedKeys(dt.Footnotes)
	sort.SliceStable(ids, func(i, j int) bool {
		ni, oki := strconv.Atoi(numbers[ids[i]])
		nj, okj := strconv.Atoi(numbers[ids[j]])
		switch {
		case oki == nil && okj == nil:
			return ni < nj
		case oki == nil:
			return true
		case okj == nil:
			return false
		}
		return ids[i] < ids[j]
	})
	out := make([]*Segment, 0, len(ids))
	for i, id := range ids {
		fn := dt.Footnotes[id]
		label := numbers[id]
		if label == "" {
			label = itoa(i + 1)
		}
		seg := parseSegment(tab, SegmentFootnote, id, i+1, prefix+"footnote"+label+"/", fn.Content)
		seg.FootnoteNumber = numbers[id]
		out = append(out, seg)
	}
	return out
}

func parseSegment(tab *Tab, kind SegmentKind, id string, number int, prefix string, content []*gdocs.StructuralElement) *Segment {
	seg := &Segment{Kind: kind, ID: id, Number: number, Prefix: prefix, Tab: tab}
	seg.Blocks = parseBlocks(seg, nil, prefix, content)
	return seg
}

func parseBlocks(seg *Segment, cell *Cell, prefix string, content []*gdocs.StructuralElement) []*Block {
	counters := map[BlockKind]int{}
	var out []*Block
	for _, se := range content {
		if se == nil {
			continue
		}
		b := &Block{Start: se.StartIndex, End: se.EndIndex, Segment: seg, Cell: cell}
		var short string
		switch {
		case se.Paragraph != nil:
			b.Kind, short = KindParagraph, "p"
			b.Paragraph = parseParagraph(seg.Tab, se.Paragraph)
		case se.Table != nil:
			b.Kind, short = KindTable, "tbl"
		case se.SectionBreak != nil:
			b.Kind, short = KindSectionBreak, "sb"
			b.Inserted, b.Deleted = se.SectionBreak.SuggestedInsertionIDs, se.SectionBreak.SuggestedDeletionIDs
			b.SectionBreak = &SectionBreakInfo{}
			if ss := se.SectionBreak.SectionStyle; ss != nil {
				b.SectionBreak.Type = ss.SectionType
				b.SectionBreak.DefaultHeaderID = ss.DefaultHeaderID
				b.SectionBreak.DefaultFooterID = ss.DefaultFooterID
			}
		case se.TableOfContents != nil:
			b.Kind, short = KindTOC, "toc"
			b.Inserted, b.Deleted = se.TableOfContents.SuggestedInsertionIDs, se.TableOfContents.SuggestedDeletionIDs
		default:
			continue
		}
		counters[b.Kind]++
		b.Ordinal = counters[b.Kind]
		b.Handle = prefix + short + itoa(b.Ordinal)
		switch b.Kind {
		case KindTable:
			b.Table = parseTable(seg, se.Table, b.Handle)
			b.Inserted, b.Deleted = se.Table.SuggestedInsertionIDs, se.Table.SuggestedDeletionIDs
		case KindTOC:
			b.TOC = &TOC{Blocks: parseBlocks(seg, cell, b.Handle+"/", se.TableOfContents.Content)}
		}
		out = append(out, b)
	}
	return out
}

func parseTable(seg *Segment, t *gdocs.Table, handle string) *Table {
	tbl := &Table{Handle: handle, Rows: int(t.Rows), Cols: int(t.Columns)}
	for ri, row := range t.TableRows {
		if row == nil {
			continue
		}
		var cells []*Cell
		for ci, tc := range row.TableCells {
			if tc == nil {
				continue
			}
			c := &Cell{Table: tbl, Row: ri + 1, Col: ci + 1, Start: tc.StartIndex, End: tc.EndIndex, RowSpan: 1, ColSpan: 1}
			c.Handle = fmt.Sprintf("%s:r%dc%d", handle, ri+1, ci+1)
			if st := tc.TableCellStyle; st != nil {
				if st.RowSpan > 0 {
					c.RowSpan = int(st.RowSpan)
				}
				if st.ColumnSpan > 0 {
					c.ColSpan = int(st.ColumnSpan)
				}
			}
			c.Blocks = parseBlocks(seg, c, c.Handle+"/", tc.Content)
			cells = append(cells, c)
		}
		tbl.Cells = append(tbl.Cells, cells)
	}
	if tbl.Rows == 0 {
		tbl.Rows = len(tbl.Cells)
	}
	if tbl.Cols == 0 && len(tbl.Cells) > 0 {
		tbl.Cols = len(tbl.Cells[0])
	}
	return tbl
}

func parseParagraph(tab *Tab, p *gdocs.Paragraph) *Paragraph {
	para := &Paragraph{PositionedObjectIDs: p.PositionedObjectIDs}
	if ps := p.ParagraphStyle; ps != nil {
		para.NamedStyle = ps.NamedStyleType
		para.HeadingID = ps.HeadingID
		para.Alignment = ps.Alignment
		if ps.IndentStart != nil {
			para.IndentStartPt = ps.IndentStart.Magnitude
		}
		para.Level, para.IsTitle, para.IsSubtitle = headingLevel(ps.NamedStyleType)
	}
	if b := p.Bullet; b != nil {
		bi := &BulletInfo{ListID: b.ListID, Nesting: int(b.NestingLevel)}
		if li := tab.Lists[b.ListID]; li != nil && bi.Nesting < len(li.Levels) {
			bi.Ordered = li.Levels[bi.Nesting].Ordered
			bi.Glyph = li.Levels[bi.Nesting].Glyph
		}
		para.Bullet = bi
	}
	for _, el := range p.Elements {
		if r := parseElement(el); r != nil {
			para.Runs = append(para.Runs, r)
		}
	}
	return para
}

func parseElement(el *gdocs.ParagraphElement) *Run {
	if el == nil {
		return nil
	}
	r := &Run{Start: el.StartIndex, End: el.EndIndex}
	switch {
	case el.TextRun != nil:
		r.Kind = RunText
		r.Text = el.TextRun.Content
		r.Style = parseTextStyle(el.TextRun.TextStyle)
		r.Inserted, r.Deleted = el.TextRun.SuggestedInsertionIDs, el.TextRun.SuggestedDeletionIDs
	case el.InlineObjectElement != nil:
		e := el.InlineObjectElement
		r.Kind, r.ObjectID = RunInlineObject, e.InlineObjectID
		r.Style = parseTextStyle(e.TextStyle)
		r.Inserted, r.Deleted = e.SuggestedInsertionIDs, e.SuggestedDeletionIDs
	case el.FootnoteReference != nil:
		e := el.FootnoteReference
		r.Kind, r.FootnoteID, r.FootnoteNumber = RunFootnoteRef, e.FootnoteID, e.FootnoteNumber
		r.Style = parseTextStyle(e.TextStyle)
		r.Inserted, r.Deleted = e.SuggestedInsertionIDs, e.SuggestedDeletionIDs
	case el.PageBreak != nil:
		r.Kind = RunPageBreak
		r.Inserted, r.Deleted = el.PageBreak.SuggestedInsertionIDs, el.PageBreak.SuggestedDeletionIDs
	case el.ColumnBreak != nil:
		r.Kind = RunColumnBreak
		r.Inserted, r.Deleted = el.ColumnBreak.SuggestedInsertionIDs, el.ColumnBreak.SuggestedDeletionIDs
	case el.HorizontalRule != nil:
		r.Kind = RunHorizontalRule
		r.Inserted, r.Deleted = el.HorizontalRule.SuggestedInsertionIDs, el.HorizontalRule.SuggestedDeletionIDs
	case el.Person != nil:
		e := el.Person
		r.Kind = RunPerson
		if e.PersonProperties != nil {
			r.PersonName, r.PersonEmail = e.PersonProperties.Name, e.PersonProperties.Email
		}
		r.Text = r.PersonName
		if r.Text == "" {
			r.Text = r.PersonEmail
		}
		r.Style = parseTextStyle(e.TextStyle)
		r.Inserted, r.Deleted = e.SuggestedInsertionIDs, e.SuggestedDeletionIDs
	case el.RichLink != nil:
		e := el.RichLink
		r.Kind = RunRichLink
		if e.RichLinkProperties != nil {
			r.LinkTitle, r.LinkURI = e.RichLinkProperties.Title, e.RichLinkProperties.URI
		}
		r.Text = r.LinkTitle
		if r.Text == "" {
			r.Text = r.LinkURI
		}
		r.Style = parseTextStyle(e.TextStyle)
		r.Inserted, r.Deleted = e.SuggestedInsertionIDs, e.SuggestedDeletionIDs
	case el.DateElement != nil:
		e := el.DateElement
		r.Kind = RunDate
		if e.DateElementProperties != nil {
			r.Text = e.DateElementProperties.DisplayText
			if r.Text == "" {
				r.Text = e.DateElementProperties.Timestamp
			}
		}
		r.Style = parseTextStyle(e.TextStyle)
		r.Inserted, r.Deleted = e.SuggestedInsertionIDs, e.SuggestedDeletionIDs
	case el.Equation != nil:
		r.Kind = RunEquation
		r.Inserted, r.Deleted = el.Equation.SuggestedInsertionIDs, el.Equation.SuggestedDeletionIDs
	case el.AutoText != nil:
		r.Kind, r.AutoTextType = RunAutoText, el.AutoText.Type
		r.Style = parseTextStyle(el.AutoText.TextStyle)
		r.Inserted, r.Deleted = el.AutoText.SuggestedInsertionIDs, el.AutoText.SuggestedDeletionIDs
	default:
		return nil
	}
	return r
}

func parseTextStyle(ts *gdocs.TextStyle) TextStyle {
	var s TextStyle
	if ts == nil {
		return s
	}
	s.Bold, s.Italic, s.Underline, s.Strikethrough, s.SmallCaps = ts.Bold, ts.Italic, ts.Underline, ts.Strikethrough, ts.SmallCaps
	if ts.BaselineOffset != "" && ts.BaselineOffset != "NONE" && ts.BaselineOffset != "BASELINE_OFFSET_UNSPECIFIED" {
		s.Baseline = ts.BaselineOffset
	}
	if ts.WeightedFontFamily != nil {
		s.FontFamily = ts.WeightedFontFamily.FontFamily
	}
	if ts.FontSize != nil {
		s.FontSizePt = ts.FontSize.Magnitude
	}
	s.Foreground = hexColor(ts.ForegroundColor)
	s.Background = hexColor(ts.BackgroundColor)
	if l := ts.Link; l != nil {
		s.LinkURL, s.LinkHeadingID, s.LinkBookmark, s.LinkTabID = l.URL, l.HeadingID, l.BookmarkID, l.TabID
		if l.Heading != nil && s.LinkHeadingID == "" {
			s.LinkHeadingID = l.Heading.ID
		}
	}
	return s
}

func parseInlineObject(id string, obj *gdocs.InlineObject) *InlineObjectInfo {
	info := &InlineObjectInfo{ID: id, Kind: "object"}
	if obj.InlineObjectProperties == nil || obj.InlineObjectProperties.EmbeddedObject == nil {
		return info
	}
	eo := obj.InlineObjectProperties.EmbeddedObject
	info.Title, info.Description = eo.Title, eo.Description
	switch {
	case eo.ImageProperties != nil:
		info.Kind = "image"
		info.SourceURI, info.ContentURI = eo.ImageProperties.SourceURI, eo.ImageProperties.ContentURI
	case eo.EmbeddedDrawingProperties != nil:
		info.Kind = "drawing"
	case eo.LinkedContentReference != nil:
		info.Kind = "chart"
	}
	if eo.Size != nil {
		if eo.Size.Width != nil {
			info.WidthPt = eo.Size.Width.Magnitude
		}
		if eo.Size.Height != nil {
			info.HeightPt = eo.Size.Height.Magnitude
		}
	}
	return info
}

func headingLevel(named string) (level int, title, subtitle bool) {
	switch named {
	case "TITLE":
		return 0, true, false
	case "SUBTITLE":
		return 0, false, true
	}
	if strings.HasPrefix(named, "HEADING_") {
		if n, err := strconv.Atoi(strings.TrimPrefix(named, "HEADING_")); err == nil && n >= 1 && n <= 6 {
			return n, false, false
		}
	}
	return 0, false, false
}

func isOrderedGlyph(glyphType string) bool {
	switch glyphType {
	case "DECIMAL", "ZERO_DECIMAL", "UPPER_ALPHA", "ALPHA", "UPPER_ROMAN", "ROMAN":
		return true
	}
	return false
}

func hexColor(c *gdocs.OptionalColor) string {
	if c == nil || c.Color == nil || c.Color.RgbColor == nil {
		return ""
	}
	rgb := c.Color.RgbColor
	to := func(f float64) int {
		v := int(f*255 + 0.5)
		if v < 0 {
			return 0
		}
		if v > 255 {
			return 255
		}
		return v
	}
	return fmt.Sprintf("#%02x%02x%02x", to(rgb.Red), to(rgb.Green), to(rgb.Blue))
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func itoa(n int) string { return strconv.Itoa(n) }

func equalFold(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

func parseTabNumber(ref string) (int, bool) {
	ref = strings.ToLower(strings.TrimSpace(ref))
	ref = strings.TrimPrefix(ref, "tab")
	n, err := strconv.Atoi(ref)
	return n, err == nil
}
