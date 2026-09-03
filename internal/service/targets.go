package service

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/mmedum/google-docs-mcp/internal/doc"
	"github.com/mmedum/google-docs-mcp/internal/plan"
)

// Target points at content. Exactly one selector is used: text,
// heading_id, heading, handle, handles, or cell.
type Target struct {
	Text       string
	Occurrence int
	Within     string

	HeadingID      string
	Heading        string
	HeadingLevel   int
	IncludeHeading *bool

	Handle  string
	From    string
	To      string
	Cell    string
	Tab     string
	Segment string
}

func (t Target) selectorCount() int {
	n := 0
	for _, set := range []bool{t.Text != "", t.HeadingID != "", t.Heading != "", t.Handle != "", t.From != "" || t.To != "", t.Cell != ""} {
		if set {
			n++
		}
	}
	return n
}

// TargetRange is a target turned into a range.
type TargetRange struct {
	Tab         *doc.Tab
	Segment     *doc.Segment
	Start       int64
	End         int64
	IsBlock     bool // covers whole top-level blocks including trailing newlines
	Text        string
	Aligned     string       // Text with non-text elements as U+FFFC so offsets match the index space; "" when unavailable
	Blocks      []*doc.Block // covered top-level blocks when IsBlock
	Block       *doc.Block   // enclosing block for text matches
	Description string
}

// Rng converts to a planner range.
func (r TargetRange) Rng() plan.Rng {
	return plan.Rng{Start: r.Start, End: r.End, SegmentID: r.Segment.ID, TabID: r.Tab.ID}
}

// SegmentBounds describes the segment for the planner.
func SegmentBounds(tab *doc.Tab, seg *doc.Segment) plan.Segment {
	return plan.Segment{ID: seg.ID, TabID: tab.ID, Start: seg.ContentStart(), End: seg.End()}
}

// ResolveTarget turns a Target into a range on the document, checking
// handles against the memory of the last read.
func (s *Service) ResolveTarget(f *Fetched, t Target) (*TargetRange, error) {
	d := f.Doc
	if t.selectorCount() != 1 {
		return nil, Errorf("invalid", "a target needs exactly one of text, heading_id, heading, handle, from/to, or cell")
	}
	tab, seg, err := tabSegment(d, t.Tab, t.Segment)
	if err != nil {
		return nil, err
	}
	switch {
	case t.Cell != "":
		c, ok := d.FindCell(t.Cell)
		if !ok {
			return nil, Errorf("not_found", "no cell %s; cells are named like tbl1:r2c3", t.Cell)
		}
		return cellTarget(c)
	case t.Text != "":
		return s.resolveText(f, tab, seg, t)
	}
	rs, err := s.resolveBlocks(f, tab, seg, blockSelector{HeadingID: t.HeadingID, Heading: t.Heading, HeadingLevel: t.HeadingLevel, Occurrence: t.Occurrence,
		Handle: t.Handle, From: t.From, To: t.To, IncludeHeading: t.IncludeHeading})
	if err != nil {
		return nil, err
	}
	if rs.Section != nil {
		return sectionRange(rs), nil
	}
	return blockRange(rs.Tab, rs.Segment, rs.From, rs.To-1), nil
}

// cellTarget is the writable content range of a cell: everything before
// its final newline, in the cell's own tab and segment. Cells covered by
// a merge have no content of their own.
func cellTarget(c *doc.Cell) (*TargetRange, error) {
	if c.Covered() {
		return nil, Errorf("invalid", "cell %s is merged into %s; use that cell instead", c.Handle, c.MergedInto.Handle)
	}
	if len(c.Blocks) == 0 {
		return nil, Errorf("invalid", "cell %s has no content blocks", c.Handle)
	}
	seg := c.Blocks[0].Segment
	r := &TargetRange{Tab: seg.Tab, Segment: seg, Start: c.Blocks[0].Start, End: c.ContentEnd(), Text: c.Text(doc.ViewInline), Description: "cell " + c.Handle}
	if len(c.Blocks) == 1 && c.Blocks[0].Paragraph != nil {
		r.Aligned = alignedSlice(c.Blocks[0].Paragraph, r.Start, r.End)
	}
	return r, nil
}

// sectionRange is the range of a resolved section, with or without its
// heading; an empty body is a zero-length range after the heading.
func sectionRange(rs Resolved) *TargetRange {
	seg, sec := rs.Segment, rs.Section
	if rs.From >= rs.To {
		h := seg.Blocks[sec.From]
		return &TargetRange{Tab: rs.Tab, Segment: seg, Start: h.End, End: h.End, Block: h, Description: fmt.Sprintf("the empty body of section %q", h.Paragraph.Text(doc.ViewCurrent))}
	}
	r := blockRange(rs.Tab, seg, rs.From, rs.To-1)
	r.Description = fmt.Sprintf("section %q (%s)", sec.Heading.Paragraph.Text(doc.ViewCurrent), handleRange(seg, rs.From, rs.To))
	return r
}

func blockRange(tab *doc.Tab, seg *doc.Segment, from, to int) *TargetRange {
	blocks := seg.Blocks[from : to+1]
	texts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		texts = append(texts, b.Text(doc.ViewInline))
	}
	r := &TargetRange{Tab: tab, Segment: seg, Start: blocks[0].Start, End: blocks[len(blocks)-1].End, IsBlock: true, Blocks: blocks,
		Text: strings.Join(texts, "\n"), Description: handleRange(seg, from, to+1)}
	if len(blocks) == 1 {
		r.Block = blocks[0]
		if r.Block.Paragraph != nil {
			r.Aligned = alignedSlice(r.Block.Paragraph, r.Start, r.End-1)
		}
	}
	return r
}

// checkedIndex finds a top-level block by handle after validating it
// against the handle memory: a handle from an older revision must still
// name the same text, or be relocatable to exactly one block.
func (s *Service) checkedIndex(f *Fetched, seg *doc.Segment, handle string) (int, error) {
	handle = strings.TrimSpace(handle)
	mem, hasMem := s.Handles(f.Doc.ID)
	if !hasMem {
		return 0, Errorf("unknown", "handle %s: nothing has been read from this document in this session, so handles cannot be checked; read the section with with_handles first, or target exact text", handle)
	}
	remembered, known := mem.Text[handle]
	if !known {
		return 0, Errorf("unknown", "handle %s was not in the last read of this document; re-read the section with with_handles", handle)
	}
	if mem.RevisionID != f.Doc.RevisionID {
		// The document changed since the read that produced this handle.
		for i, b := range seg.Blocks {
			if b.Handle == handle && doc.Normalize(b.Text(doc.ViewInline)) == remembered {
				return i, nil
			}
		}
		var hits []int
		for i, b := range seg.Blocks {
			if remembered != "" && doc.Normalize(b.Text(doc.ViewInline)) == remembered {
				hits = append(hits, i)
			}
		}
		switch len(hits) {
		case 1:
			return hits[0], nil
		case 0:
			return 0, Errorf("stale", "handle %s pointed at %q when read, and that block no longer exists at this revision; re-read the section", handle, doc.Clip(remembered, 60))
		default:
			return 0, Errorf("ambiguous", "handle %s pointed at %q, which now appears %d times; re-read the section and use the current handle", handle, doc.Clip(remembered, 60), len(hits))
		}
	}
	return topLevelIndex(seg, handle)
}

// resolveText finds an exact (normalised) text match inside paragraphs.
func (s *Service) resolveText(f *Fetched, tab *doc.Tab, seg *doc.Segment, t Target) (*TargetRange, error) {
	needle := doc.Normalize(t.Text)
	if needle == "" {
		return nil, Errorf("invalid", "text target is empty")
	}
	blocks := seg.AllBlocks()
	scope := "in " + segmentName(tab, seg)
	if t.Within != "" {
		var err error
		blocks, scope, err = s.withinBlocks(f, tab, seg, t.Within)
		if err != nil {
			return nil, err
		}
	}
	type hit struct {
		block      *doc.Block
		start, end int64
	}
	var hits []hit
	for _, caseFold := range []bool{false, true} {
		for _, b := range blocks {
			if b.Paragraph == nil {
				continue
			}
			for _, m := range matchParagraph(b.Paragraph, needle, caseFold) {
				hits = append(hits, hit{b, m[0], m[1]})
			}
		}
		if len(hits) > 0 {
			break
		}
	}
	switch {
	case len(hits) == 0:
		return nil, Errorf("not_found", "text %q not found %s; use find_in_document to locate it, and quote it exactly", doc.Clip(t.Text, 80), scope)
	case len(hits) > 1 && t.Occurrence <= 0:
		var where []string
		for i, h := range hits {
			if i == 5 {
				where = append(where, "…")
				break
			}
			where = append(where, h.block.Handle)
		}
		return nil, Errorf("ambiguous", "text %q matches %d times %s (%s); add occurrence (1-based) or within", doc.Clip(t.Text, 80), len(hits), scope, strings.Join(where, ", "))
	case t.Occurrence > len(hits):
		return nil, Errorf("not_found", "text %q matches %d time(s) %s; occurrence %d does not exist", doc.Clip(t.Text, 80), len(hits), scope, t.Occurrence)
	}
	h := hits[max(t.Occurrence, 1)-1]
	text := sliceUTF16(h.block.Paragraph, h.start, h.end)
	return &TargetRange{Tab: tab, Segment: seg, Start: h.start, End: h.end, Text: text, Aligned: alignedSlice(h.block.Paragraph, h.start, h.end), Block: h.block,
		Description: fmt.Sprintf("%q in %s", doc.Clip(text, 60), h.block.Handle)}, nil
}

// withinBlocks restricts a text search to a block handle or a section.
func (s *Service) withinBlocks(f *Fetched, tab *doc.Tab, seg *doc.Segment, within string) ([]*doc.Block, string, error) {
	within = strings.TrimSpace(within)
	if strings.HasPrefix(within, "heading:") {
		rs, err := resolveHeadingText(tab, seg, blockSelector{Heading: strings.TrimPrefix(within, "heading:")})
		if err != nil {
			return nil, "", err
		}
		return doc.Flatten(seg.Blocks[rs.From:rs.To]), "within section " + strings.TrimPrefix(within, "heading:"), nil
	}
	if ht, sec, ok := f.Doc.HeadingByID(within); ok {
		if ht != tab || seg != tab.Body {
			return nil, "", Errorf("invalid", "within %s names a section of tab %d's body; set tab and segment to match", within, ht.Number)
		}
		return doc.Flatten(seg.Blocks[sec.From:sec.To]), "within section " + within, nil
	}
	i, err := s.checkedIndex(f, seg, within)
	if err != nil {
		// A handle nested in a table of this segment is a valid scope.
		if b, ok := f.Doc.FindHandle(within); ok && b.Segment == seg {
			return doc.Flatten([]*doc.Block{b}), "within " + within, nil
		}
		return nil, "", err
	}
	return doc.Flatten([]*doc.Block{seg.Blocks[i]}), "within " + within, nil
}

// unit is one comparable character of a paragraph with its UTF-16 span.
type unit struct {
	r          rune
	start, end int64
}

// units flattens a paragraph's text runs into normalised characters with
// whitespace runs collapsed, keeping the original offsets so matches map
// back to API indices.
func units(p *doc.Paragraph) []unit {
	var out []unit
	for _, run := range p.Runs {
		if run.Kind != doc.RunText {
			continue
		}
		pos := run.Start
		for _, r := range run.Text {
			w := int64(utf16.RuneLen(r))
			nr, keep := doc.NormalizeRune(r)
			switch {
			case !keep:
			case unicode.IsSpace(nr):
				if n := len(out); n > 0 && out[n-1].r == ' ' {
					out[n-1].end = pos + w
				} else {
					out = append(out, unit{' ', pos, pos + w})
				}
			default:
				out = append(out, unit{nr, pos, pos + w})
			}
			pos += w
		}
	}
	// Trailing whitespace (the paragraph newline) never matches.
	for len(out) > 0 && out[len(out)-1].r == ' ' {
		out = out[:len(out)-1]
	}
	return out
}

// matchParagraph returns every [start, end) UTF-16 span where the
// normalised needle occurs in the paragraph.
func matchParagraph(p *doc.Paragraph, needle string, caseFold bool) [][2]int64 {
	return matchUnits(units(p), []rune(needle), caseFold)
}

// matchUnits is matchParagraph over precomputed units.
func matchUnits(us []unit, nr []rune, caseFold bool) [][2]int64 {
	if len(nr) == 0 || len(us) < len(nr) {
		return nil
	}
	eq := func(a, b rune) bool {
		if caseFold {
			return unicode.ToLower(a) == unicode.ToLower(b)
		}
		return a == b
	}
	var out [][2]int64
	for i := 0; i+len(nr) <= len(us); i++ {
		ok := true
		for j := range nr {
			if !eq(us[i+j].r, nr[j]) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, [2]int64{us[i].start, us[i+len(nr)-1].end})
			i += len(nr) - 1
		}
	}
	return out
}

// sliceUTF16 returns the paragraph text between two absolute offsets.
func sliceUTF16(p *doc.Paragraph, start, end int64) string {
	var b strings.Builder
	for _, run := range p.Runs {
		if run.Kind != doc.RunText || run.End <= start || run.Start >= end {
			continue
		}
		from := max(start, run.Start) - run.Start
		to := min(end, run.End) - run.Start
		b.WriteString(run.Text[doc.UTF16ToByte(run.Text, from):doc.UTF16ToByte(run.Text, to)])
	}
	return b.String()
}

// objectPlaceholder stands in for one UTF-16 unit of a non-text element
// in index-aligned text.
const objectPlaceholder = '\uFFFC'

// alignedSlice returns the paragraph content between two absolute offsets
// with every non-text element (chip, image, footnote reference, break)
// replaced by one placeholder per UTF-16 unit, so string offsets equal
// index offsets. Chips contribute a placeholder, not their display text.
func alignedSlice(p *doc.Paragraph, start, end int64) string {
	var b strings.Builder
	for _, run := range p.Runs {
		if run.End <= start || run.Start >= end {
			continue
		}
		from := max(start, run.Start) - run.Start
		to := min(end, run.End) - run.Start
		if run.Kind == doc.RunText {
			b.WriteString(run.Text[doc.UTF16ToByte(run.Text, from):doc.UTF16ToByte(run.Text, to)])
			continue
		}
		for range to - from {
			b.WriteRune(objectPlaceholder)
		}
	}
	return b.String()
}
