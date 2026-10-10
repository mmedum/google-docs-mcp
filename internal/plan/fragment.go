package plan

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mmedum/google-docs-mcp/v2/internal/doc"
	"github.com/mmedum/google-docs-mcp/v2/internal/markdown"
)

// CodeFont is the font used for code blocks and inline code.
const CodeFont = "Courier New"

// FragmentOptions say how a fragment lands relative to existing text.
type FragmentOptions struct {
	// Prefix puts a newline before the fragment: used when inserting
	// before a segment's final newline (append at end).
	Prefix bool
	// Suffix puts a newline after the fragment: used when inserting at a
	// block boundary so the following block stays separate.
	Suffix bool
	// NearBullet says the paragraph at the insertion point is a list
	// item; inserted non-list paragraphs then explicitly drop the bullet
	// they would inherit.
	NearBullet bool
	// Inline says the insertion point is inside an existing paragraph:
	// the first fragment paragraph merges into it and keeps its style.
	Inline bool
	// Fill says the paragraph at an inline insertion point is empty, so
	// the first fragment paragraph takes its own style instead.
	Fill bool
	// Slots lets the fragment hold tables and images: each becomes an
	// empty paragraph, its slot, which a later round fills (spike E).
	// Without it they are refused.
	Slots bool
	// Isolate gives a slot that ends the fragment a newline of its own.
	// Set where the newline it would end with follows text the batch
	// deletes: as a suggestion that text stays, inside the slot.
	Isolate bool
}

// Slot is the empty paragraph a table or image of the content waits in.
// Index is where it starts once the batch has been applied.
type Slot struct {
	Index int64
	Block *markdown.Block
}

// Compiled is a fragment laid out at an insertion point.
type Compiled struct {
	Requests []json.RawMessage
	Text     string
	Length   int64 // UTF-16 units inserted
	// Start and End bound the inserted content (excluding the prefix
	// newline, including the suffix newline).
	Start int64
	End   int64
	// Slots are where the tables and images go, in order.
	Slots []Slot
}

type piece struct {
	block    *markdown.Block
	text     string
	start    int64
	end      int64
	listItem bool
	slot     bool
}

// CompileFragment lays a fragment out at `at` and returns the requests:
// one insertText (none for a lone slot filling a blank paragraph), a
// style reset over the whole insertion, paragraph
// styles, inline styles, and finally list creation in descending order
// (createParagraphBullets consumes the nesting tabs, so it goes last).
func CompileFragment(f *markdown.Fragment, at Loc, o FragmentOptions) (*Compiled, error) {
	if f == nil || len(f.Blocks) == 0 {
		return nil, fmt.Errorf("nothing to insert: the content is empty")
	}
	pieces, err := fragmentPieces(f, o)
	if err != nil {
		return nil, err
	}
	var sb strings.Builder
	cursor := at.Index
	if o.Prefix {
		sb.WriteString("\n")
		cursor++
	}
	contentStart := cursor
	for i, p := range pieces {
		if i > 0 {
			sb.WriteString("\n")
			cursor++
		}
		p.start = cursor
		sb.WriteString(p.text)
		cursor += doc.UTF16Len(p.text)
		p.end = cursor
	}
	if o.Isolate && pieces[len(pieces)-1].slot {
		sb.WriteString("\n")
		cursor++
	}
	if o.Suffix {
		sb.WriteString("\n")
		cursor++
	}
	text := sb.String()
	seg, tab := at.SegmentID, at.TabID
	rng := func(s, e int64) Rng { return Rng{Start: s, End: e, SegmentID: seg, TabID: tab} }
	paraRange := func(p *piece) Rng {
		if p.end == p.start {
			return rng(p.start, p.start+1)
		}
		return rng(p.start, p.end)
	}

	c := &Compiled{Text: text, Length: doc.UTF16Len(text), Start: contentStart, End: cursor}
	for _, p := range pieces {
		if p.slot {
			c.Slots = append(c.Slots, Slot{Index: p.start, Block: p.block})
		}
	}
	// A lone slot filling a blank paragraph is that paragraph, and an
	// insertText needs text.
	if text != "" {
		c.Requests = append(c.Requests, InsertText(text, at))
	}
	// Inserted text inherits the style of its neighbors; reset it.
	if cursor > contentStart {
		c.Requests = append(c.Requests, ClearTextStyle(rng(contentStart, cursor)))
	}
	for i, p := range pieces {
		c.Requests = append(c.Requests, pieceRequests(p, o, i == 0 && o.Inline && !o.Fill, rng, paraRange)...)
	}
	c.Requests = append(c.Requests, listRequests(pieces, rng)...)
	return c, nil
}

// fragmentPieces lays a fragment's blocks out as paragraphs: one per
// block, one per code line, and an empty slot per table or image.
func fragmentPieces(f *markdown.Fragment, o FragmentOptions) ([]*piece, error) {
	var pieces []*piece
	for _, b := range f.Blocks {
		switch b.Kind {
		case markdown.KindTable, markdown.KindImage:
			// An insertion inside a paragraph merges the first and last
			// pieces into its text, so neither could be an empty slot.
			if !o.Slots || o.Inline && !o.Fill {
				return nil, &markdown.UnsupportedError{Construct: string(b.Kind), Line: b.Line, Hint: "a table or image in content " +
					"goes in an insert, append or replace in the body, between blocks; edit_table and insert_object reach the rest"}
			}
			if err := checkSlot(b); err != nil {
				return nil, err
			}
			pieces = append(pieces, &piece{block: b, slot: true})
		case markdown.KindCode:
			for _, line := range b.Lines {
				pieces = append(pieces, &piece{block: b, text: line})
			}
		case markdown.KindListItem:
			pieces = append(pieces, &piece{block: b, text: strings.Repeat("\t", b.Nesting) + b.Text(), listItem: true})
		default:
			pieces = append(pieces, &piece{block: b, text: b.Text()})
		}
	}
	return pieces, nil
}

// checkSlot refuses a table or image the later round could not place,
// before the text is written: the limits insert_table and insert_object
// hold an op to.
func checkSlot(b *markdown.Block) error {
	switch b.Kind {
	case markdown.KindTable:
		if rows, cols := len(b.Table.Rows), len(b.Table.Rows[0]); rows > maxTableRows || cols > maxTableCols {
			return &markdown.UnsupportedError{Construct: fmt.Sprintf("%d×%d table", rows, cols), Line: b.Line,
				Hint: fmt.Sprintf("a table holds at most %d rows and %d columns", maxTableRows, maxTableCols)}
		}
	case markdown.KindImage:
		if len(b.Image.URL) > maxImageURL {
			return &markdown.UnsupportedError{Construct: "image address over 2 kB", Line: b.Line}
		}
	}
	return nil
}

// pieceRequests styles one laid-out paragraph: named style, bullet
// removal when inherited, code font, and inline formatting. A piece
// merged into an existing paragraph keeps that paragraph's style.
func pieceRequests(p *piece, o FragmentOptions, merged bool, rng func(int64, int64) Rng, paraRange func(*piece) Rng) []json.RawMessage {
	var reqs []json.RawMessage
	if !merged {
		ps := ParagraphStyleSpec{NamedStyle: "NORMAL_TEXT"}
		if p.block.Kind == markdown.KindHeading {
			ps.NamedStyle = fmt.Sprintf("HEADING_%d", min(max(p.block.Level, 1), 6))
		}
		reqs = append(reqs, UpdateParagraphStyle(paraRange(p), ps))
		if o.NearBullet && !p.listItem {
			reqs = append(reqs, DeleteBullets(paraRange(p)))
		}
	}
	if p.block.Kind == markdown.KindCode {
		if p.end > p.start {
			reqs = append(reqs, UpdateTextStyle(rng(p.start, p.end), TextStyleSpec{Font: CodeFont}))
		}
		return reqs
	}
	pos := p.start
	if p.listItem {
		pos += int64(p.block.Nesting) // leading tabs
	}
	for _, in := range p.block.Inlines {
		n := doc.UTF16Len(in.Text)
		if spec := inlineSpec(in); !spec.IsZero() && n > 0 {
			reqs = append(reqs, UpdateTextStyle(rng(pos, pos+n), spec))
		}
		pos += n
	}
	return reqs
}

// listRequests creates lists last, in descending order, because
// createParagraphBullets consumes the nesting tabs and shifts what follows.
func listRequests(pieces []*piece, rng func(int64, int64) Rng) []json.RawMessage {
	type group struct {
		start, end int64
		ordered    bool
	}
	var groups []group
	for i := 0; i < len(pieces); i++ {
		p := pieces[i]
		if !p.listItem {
			continue
		}
		g := group{start: p.start, end: p.end, ordered: p.block.Ordered}
		for i+1 < len(pieces) && pieces[i+1].listItem && pieces[i+1].block.ListID == p.block.ListID {
			i++
			g.end = pieces[i].end
		}
		groups = append(groups, g)
	}
	reqs := make([]json.RawMessage, 0, len(groups))
	for i := len(groups) - 1; i >= 0; i-- {
		g := groups[i]
		preset := BulletPreset
		if g.ordered {
			preset = NumberedPreset
		}
		end := g.end
		if end == g.start {
			end++
		}
		reqs = append(reqs, CreateBullets(rng(g.start, end), preset))
	}
	return reqs
}

func inlineSpec(in markdown.Inline) TextStyleSpec {
	var s TextStyleSpec
	if in.Bold {
		s.Bold = new(true)
	}
	if in.Italic {
		s.Italic = new(true)
	}
	if in.Strike {
		s.Strikethrough = new(true)
	}
	if in.Code {
		s.Font = CodeFont
	}
	if in.Link != "" {
		s.Link = in.Link
	}
	return s
}
