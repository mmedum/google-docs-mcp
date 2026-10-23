package plan

import (
	"encoding/json"
	"strings"

	"github.com/mmedum/google-docs-mcp/v3/internal/doc"
)

// How a fragment's list items get their levels. Google's rules, measured
// live in spike F (internal/gapi/rawbullets_integration_test.go):
//
//   - Every inserted paragraph takes the bullet of the paragraph at the
//     insertion point, as InsertTextRequest documents.
//   - createParagraphBullets skips a paragraph that already has a
//     bullet, leaving its leading tabs as text; the lists guide says a
//     bullet's level cannot be changed, only deleted and made again.
//   - A list it makes joins the list of the paragraph just before when
//     their kinds match, as documented, and counts levels from that
//     paragraph's: a level is that paragraph's level plus the tabs beyond
//     the fewest in the range. So a run can start neither deeper nor
//     shallower than the item before it.
//   - deleteParagraphBullets leaves the indent of the level it removed,
//     and a new bullet adds its own to it.
//
// So a list item that took the right bullet keeps it: making a list over
// it would split it from the items after it. Every other run of items is
// rebuilt in a window: bullets deleted, indent cleared, a tab for each
// level over the window's base, a temporary empty paragraph at the start
// when the window starts deeper than its base (it pins the fewest tabs to
// none), the list made, the temporary paragraph removed. When the item
// before is deeper than the window, the window takes in the items before
// it until one is not, rebuilding them at their own levels. The window is
// gdoc's (listnest.py), measured there and again here. A rebuilt item gets
// its level's standard indent, so one set by hand is lost, and in suggest
// mode it shows a bullet change that changes nothing. That happens only
// when the item before the content is deeper than where the content goes,
// in a list of one of the server's presets: a checklist or a custom list
// is never rebuilt, since a list made after it would not join it.

// ListItem is a list paragraph next to an insertion: its level, whether
// its list is numbered, the server's preset its list matches ("" for
// none), and where it starts.
type ListItem struct {
	Level   int
	Ordered bool
	Preset  string
	Start   int64
	// IndentStart and IndentFirstLine are the paragraph's own indents in
	// points, 0 when it sets none.
	IndentStart, IndentFirstLine float64
}

// handIndented says a list item's indents are its own, not its level's:
// a list from one of the server's presets indents each level 36pt more,
// its first line 18pt less (spike F). A rebuilt item gets its level's.
func (it ListItem) handIndented() bool {
	level := 36 * float64(it.Level+1)
	return (it.IndentStart != 0 || it.IndentFirstLine != 0) && (it.IndentStart != level || it.IndentFirstLine != level-18)
}

// ownIndents puts back the indents of an item set by hand, at its start
// in the segment r is in; nil when its indents are its level's.
func (it ListItem) ownIndents(r Rng) json.RawMessage {
	if !it.handIndented() {
		return nil
	}
	in, first := it.IndentStart, it.IndentFirstLine
	r.Start, r.End = it.Start, it.Start+1
	return UpdateParagraphStyle(r, ParagraphStyleSpec{IndentStartPt: &in, IndentFirstLine: &first})
}

// ListContext is the list around an insertion, from the document as the
// service read it.
type ListContext struct {
	// At is the paragraph at the insertion point, or the first block a
	// replacement covers; After is the block after a replacement. Nil
	// when that paragraph is no list item.
	At, After *ListItem
	// Preceding are the list items just before the inserted paragraphs,
	// nearest first, back to a top-level one or to the first paragraph
	// that is no list item.
	Preceding []ListItem
	// Anchor is the list item the content is placed against: the one it
	// goes before or after, replaces, or is written into. A list the
	// content starts with continues its list at its level when their
	// kinds match, and starts its own otherwise.
	Anchor *ListItem
}

// presetGlyphs are the glyph types and symbols the first three levels of
// a list made from each of the server's presets report (spike F).
var presetGlyphs = map[string][3][2]string{
	BulletPreset:   {{"", "●"}, {"", "○"}, {"", "■"}},
	NumberedPreset: {{"DECIMAL", ""}, {"ALPHA", ""}, {"ROMAN", ""}},
}

// PresetOf is the server's preset a list's glyphs match, or "" for any
// other list: a checklist, a custom one. A list made from a preset joins
// the list just before it only when the presets match.
func PresetOf(l *doc.ListInfo) string {
	if l == nil || len(l.Levels) < 3 {
		return ""
	}
	for preset, glyphs := range presetGlyphs {
		match := true
		for i, g := range glyphs {
			match = match && l.Levels[i].GlyphType == g[0] && l.Levels[i].Glyph == g[1]
		}
		if match {
			return preset
		}
	}
	return ""
}

// presetFor is the preset the server makes a list of this kind from.
func presetFor(ordered bool) string {
	if ordered {
		return NumberedPreset
	}
	return BulletPreset
}

// listWindow is one createParagraphBullets over new list paragraphs and
// the existing ones before them it rebuilds.
type listWindow struct {
	pieces   []*piece
	existing []ListItem // nearest first
	base     int
}

// joins says a list of the given kind, made from the server's preset for
// it right after this item, joins this item's list: Google joins only a
// list with a matching preset, so a checklist or a custom list never does.
func (it *ListItem) joins(ordered bool) bool {
	return it != nil && it.Preset != "" && it.Preset == presetFor(ordered)
}

// made is the bullet a piece ends with when a window makes its list.
func made(p *piece) *ListItem {
	return &ListItem{Level: p.level, Ordered: p.block.Ordered, Preset: presetFor(p.block.Ordered)}
}

// planLists gives every list piece its level and its leading tabs, keeps
// those whose inherited bullet is already right, and returns the windows
// to build, lowest first. A list that starts the fragment lands in the
// list around it; a later one follows a paragraph of the fragment, so it
// starts a list of its own unless the piece before it is a list item it
// joins.
func planLists(pieces []*piece, o FragmentOptions) []*listWindow {
	var windows []*listWindow
	for s := 0; s < len(pieces); {
		if !pieces[s].listItem {
			s++
			continue
		}
		e := s + 1
		// Lists of one kind in a row are one list to Google, which joins
		// them, so they are planned as one.
		for e < len(pieces) && pieces[e].listItem && pieces[e].block.Ordered == pieces[s].block.Ordered {
			e++
		}
		if s == 0 {
			windows = append(windows, leadingWindows(pieces[:e], o)...)
		} else {
			windows = append(windows, followingWindow(pieces[s:e], pieceItem(pieces, s-1, o)))
		}
		s = e
	}
	for _, w := range windows {
		for _, p := range w.pieces {
			p.tabs = p.level - w.base
			p.text = strings.Repeat("\t", p.tabs) + p.text
		}
	}
	return windows
}

// pieceItem is the bullet pieces[i] ends with, as the paragraph before a
// list: a list item's, or the existing paragraph's a first piece merges
// into, or none, the bullet any other paragraph inherited being deleted.
func pieceItem(pieces []*piece, i int, o FragmentOptions) *ListItem {
	switch p := pieces[i]; {
	case p.listItem:
		return p.item
	case i == 0 && o.mergesFirst():
		return o.Inherited
	}
	return nil
}

func followingWindow(group []*piece, before *ListItem) *listWindow {
	w := &listWindow{pieces: group}
	if before.joins(group[0].block.Ordered) {
		w.base = before.Level
	}
	for _, p := range group {
		p.level = w.base + p.block.Nesting
		p.item = made(p)
	}
	return w
}

// leadingWindows plans the list that starts the fragment, whose items
// take the level of the list item the content is placed against.
func leadingWindows(group []*piece, o FragmentOptions) []*listWindow {
	ordered := group[0].block.Ordered
	kept := markKept(group, o, ordered)
	var spans []*span
	for k := 0; k < len(group); {
		if kept[k] {
			k++
			continue
		}
		hi := k
		for hi < len(group) && !kept[hi] {
			hi++
		}
		spans = widen(&span{lo: k, hi: hi}, spans, group, o, ordered)
		k = hi
	}
	windows := make([]*listWindow, 0, len(spans))
	for _, s := range spans {
		s.pieces = group[s.lo:s.hi]
		for _, p := range s.pieces {
			p.item = made(p)
		}
		windows = append(windows, &s.listWindow)
	}
	return windows
}

// markKept gives each item of the leading list its level, and says which
// keep the bullet they inherited because it is already right: of the same
// kind and level, whatever list it is (a checklist item stays one). A
// first item merged into the paragraph at the insertion point is that
// paragraph, which keeps its style: a list item stays as it is, and so
// does any other paragraph, the list starting after it.
func markKept(group []*piece, o FragmentOptions, ordered bool) []bool {
	inh := o.Inherited
	lead := 0
	switch a := o.List.Anchor; {
	case o.ListLevel != nil:
		lead = *o.ListLevel
	case a != nil && a.Ordered == ordered:
		lead = a.Level
	}
	kept := make([]bool, len(group))
	for i, p := range group {
		p.level = lead + p.block.Nesting
		merged := i == 0 && o.mergesFirst()
		switch {
		case merged && inh == nil:
			kept[i] = true
		case merged || inh != nil && inh.Ordered == ordered && inh.Level == p.level:
			kept[i] = true
			p.level = inh.Level
			p.item = &ListItem{Level: inh.Level, Ordered: inh.Ordered, Preset: inh.Preset}
		}
	}
	return kept
}

// span is a window of the leading list as group indices [lo, hi), with
// the existing items before the content it rebuilds, nearest first.
type span struct {
	lo, hi int
	listWindow
}

// widen sets w's base, first taking in what comes before it while that is
// deeper than the window and in a list the window joins: kept items of
// the content, with the window before them if one ends there, or the list
// items before the content. It returns spans with w last.
func widen(w *span, spans []*span, group []*piece, o FragmentOptions, ordered bool) []*span {
	for {
		least := group[w.lo].level
		for _, p := range group[w.lo:w.hi] {
			least = min(least, p.level)
		}
		var before *ListItem
		switch {
		case w.lo > 0:
			before = pieceItem(group, w.lo-1, o)
		case len(w.existing) < len(o.List.Preceding):
			before = &o.List.Preceding[len(w.existing)]
		}
		if !before.joins(ordered) {
			w.base = 0
			return append(spans, w)
		}
		if before.Level <= least {
			w.base = before.Level
			return append(spans, w)
		}
		if w.lo == 0 {
			w.existing = append(w.existing, *before)
			continue
		}
		w.lo--
		if n := len(spans); n > 0 && spans[n-1].hi == w.lo {
			w.lo, w.existing = spans[n-1].lo, spans[n-1].existing
			spans = spans[:n-1]
		}
	}
}

// listRequests builds the windows last, lowest first, so the paragraph
// before each is final when its list is made. Making a list consumes its
// tabs, which moves every later window back by as many. A window that
// rebuilds existing items puts their tabs in first, which moves its own
// pieces on until the list consumes them again.
func listRequests(windows []*listWindow, o FragmentOptions, at Loc, rng func(int64, int64) Rng) []json.RawMessage {
	loc := func(i int64) Loc { at.Index = i; return at }
	var reqs []json.RawMessage
	var consumed int64
	for _, w := range windows {
		shift := -consumed
		least := w.pieces[0].tabs
		for _, e := range w.existing {
			n := e.Level - w.base
			least = min(least, n)
			if n > 0 {
				// Highest first, so the starts below stay where they were.
				reqs = append(reqs, InsertText(strings.Repeat("\t", n), loc(e.Start)))
				shift += int64(n)
			}
		}
		for _, p := range w.pieces {
			least = min(least, p.tabs)
			consumed += int64(p.tabs)
		}
		first, last := w.pieces[0], w.pieces[len(w.pieces)-1]
		start := first.start + shift
		if len(w.existing) > 0 {
			start = w.existing[len(w.existing)-1].Start
		}
		end := last.end + shift
		if last.end == last.start {
			end++ // an empty item: its newline makes the range cover it
		}
		preset := presetFor(first.block.Ordered)
		if o.Inherited != nil || len(w.existing) > 0 {
			reqs = append(reqs, DeleteBullets(rng(start, end)))
		}
		// Cleared even with no bullet deleted: the paragraph the content
		// landed in may carry an indent of its own, and a bullet adds to it.
		reqs = append(reqs, ClearIndent(rng(start, end)))
		if least > 0 {
			reqs = append(reqs, InsertText("\n", loc(start)), CreateBullets(rng(start, end+1), preset), DeleteRange(rng(start, start+1)))
		} else {
			reqs = append(reqs, CreateBullets(rng(start, end), preset))
		}
		// A rebuilt item set apart by hand gets its own indents back.
		for _, e := range w.existing {
			if r := e.ownIndents(rng(start, end)); r != nil {
				reqs = append(reqs, r)
			}
		}
	}
	return reqs
}
