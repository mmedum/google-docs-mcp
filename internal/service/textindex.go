package service

import (
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/mmedum/google-docs-mcp/internal/doc"
	"github.com/mmedum/google-docs-mcp/internal/plan"
)

// segText is the searchable form of one segment: every paragraph's
// normalised characters in document order with their UTF-16 spans, as
// one string so the standard library's substring search does the work.
// Paragraphs are separated by a newline, which no normalised needle
// contains, so a match never crosses paragraphs. Built once per fetch
// and segment, on first use.
type segText struct {
	units []unit     // normalised characters; a '\n' unit separates paragraphs
	text  string     // the units' runes, one rune per unit
	paras []textPara // paragraph blocks in order with their unit ranges

	foldOnce sync.Once
	fold     string // text lower-cased rune by rune, still one rune per unit
}

type textPara struct {
	block    *doc.Block
	from, to int // unit range
	// byteAt is where the paragraph's first unit starts in text, foldAt
	// the same in fold; lower-casing keeps one rune per unit but may
	// change how many bytes it takes.
	byteAt int
	foldAt int
}

// textHit is one match: the paragraph and the UTF-16 span.
type textHit struct {
	block      *doc.Block
	start, end int64
}

func buildSegText(seg *doc.Segment) *segText {
	// Size everything from one counting pass so the build allocates a
	// handful of times, not once per paragraph.
	blocks := seg.AllBlocks()
	runes, bytes, paras := 0, 0, 0
	for _, b := range blocks {
		if b.Paragraph == nil {
			continue
		}
		paras++
		for _, r := range b.Paragraph.Runs {
			if r.Kind == doc.RunText {
				runes += utf8.RuneCountInString(r.Text)
				bytes += len(r.Text)
			}
		}
	}
	x := &segText{units: make([]unit, 0, runes+paras), paras: make([]textPara, 0, paras)}
	var sb strings.Builder
	sb.Grow(bytes + paras)
	for _, b := range blocks {
		if b.Paragraph == nil {
			continue
		}
		if len(x.units) > 0 {
			x.units = append(x.units, unit{r: '\n'})
			sb.WriteByte('\n')
		}
		from, byteAt := len(x.units), sb.Len()
		x.units = appendUnits(x.units, b.Paragraph)
		for _, u := range x.units[from:] {
			sb.WriteRune(u.r)
		}
		x.paras = append(x.paras, textPara{block: b, from: from, to: len(x.units), byteAt: byteAt})
	}
	x.text = sb.String()
	return x
}

// buildFold lower-cases rune by rune, so the fold keeps one rune per
// unit even where the byte length changes, and records where each
// paragraph starts in it.
func (x *segText) buildFold() {
	var sb strings.Builder
	sb.Grow(len(x.text))
	next := 0
	for i, u := range x.units {
		if next < len(x.paras) && x.paras[next].from == i {
			x.paras[next].foldAt = sb.Len()
			next++
		}
		sb.WriteRune(unicode.ToLower(u.r))
	}
	x.fold = sb.String()
}

func foldString(s string) string {
	return strings.Map(unicode.ToLower, s)
}

// find lists every non-overlapping occurrence of a normalised needle in
// document order.
func (x *segText) find(needle string, caseFold bool) []textHit {
	if needle == "" {
		return nil
	}
	hay := x.text
	if caseFold {
		x.foldOnce.Do(x.buildFold)
		hay = x.fold
		needle = foldString(needle)
	}
	// A needle never holds the newline that separates paragraphs, so a
	// match lies inside one paragraph: find it by its byte offset, then
	// count runes from its start to reach the unit index.
	byteAt := func(k int) int {
		if caseFold {
			return x.paras[k].foldAt
		}
		return x.paras[k].byteAt
	}
	var hits []textHit
	needleRunes := utf8.RuneCountInString(needle)
	for off := 0; ; {
		i := strings.Index(hay[off:], needle)
		if i < 0 {
			return hits
		}
		start := off + i
		off = start + len(needle)
		p := sort.Search(len(x.paras), func(k int) bool { return byteAt(k) > start }) - 1
		if p < 0 {
			continue
		}
		ui := x.paras[p].from + utf8.RuneCountInString(hay[byteAt(p):start])
		uj := ui + needleRunes
		if ui >= x.paras[p].from && uj <= x.paras[p].to {
			hits = append(hits, textHit{x.paras[p].block, x.units[ui].start, x.units[uj-1].end})
		}
	}
}

// text returns the segment's searchable text, built on first use.
func (f *Fetched) text(seg *doc.Segment) *segText {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.texts == nil {
		f.texts = map[*doc.Segment]*segText{}
	}
	x := f.texts[seg]
	if x == nil {
		x = buildSegText(seg)
		f.texts[seg] = x
	}
	return x
}

// findText lists the occurrences of a normalised needle in one segment.
func (f *Fetched) findText(seg *doc.Segment, needle string, caseFold bool) []textHit {
	return f.text(seg).find(needle, caseFold)
}

// segAnchors lists everything in a segment a deletion could destroy
// short of comments: pending suggestions, inline objects and footnote
// references, every occurrence, sorted by start. Built once per fetch
// and segment.
func (f *Fetched) segAnchors(seg *doc.Segment) []plan.Anchor {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.anchors == nil {
		f.anchors = map[*doc.Segment][]plan.Anchor{}
	}
	if out, ok := f.anchors[seg]; ok {
		return out
	}
	var out []plan.Anchor
	for _, b := range seg.AllBlocks() {
		for _, id := range b.Inserted {
			out = append(out, plan.Anchor{Kind: "suggestion", ID: id, Start: b.Start, End: b.End})
		}
		for _, id := range b.Deleted {
			out = append(out, plan.Anchor{Kind: "suggestion", ID: id, Start: b.Start, End: b.End})
		}
		if b.Paragraph == nil {
			continue
		}
		for _, r := range b.Paragraph.Runs {
			for _, id := range r.Inserted {
				out = append(out, plan.Anchor{Kind: "suggestion", ID: id, Start: r.Start, End: r.End, Text: r.Text})
			}
			for _, id := range r.Deleted {
				out = append(out, plan.Anchor{Kind: "suggestion", ID: id, Start: r.Start, End: r.End, Text: r.Text})
			}
			switch r.Kind {
			case doc.RunInlineObject:
				out = append(out, plan.Anchor{Kind: "image", ID: r.ObjectID, Start: r.Start, End: r.End})
			case doc.RunFootnoteRef:
				out = append(out, plan.Anchor{Kind: "footnote", ID: r.FootnoteID, Start: r.Start, End: r.End})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	f.anchors[seg] = out
	return out
}

// anchorsIn lists everything inside [start, end) of a segment that a
// deletion would destroy: pending suggestions, inline objects, footnote
// references, and located comment threads. An id that occurs several
// times in the range is reported once, at its first occurrence.
func (f *Fetched) anchorsIn(seg *doc.Segment, start, end int64, threads []CommentThread) []plan.Anchor {
	var out []plan.Anchor
	seen := map[string]bool{}
	for _, a := range f.segAnchors(seg) {
		if a.Start >= end {
			break
		}
		if a.End > start {
			out = appendUnique(seen, out, a)
		}
	}
	for _, t := range threads {
		if t.Handle == "" || t.Tab != seg.Tab.ID || t.Segment != seg.ID || t.Resolved || t.Deleted {
			continue
		}
		if t.End > start && t.Start < end {
			out = appendUnique(seen, out, plan.Anchor{Kind: "comment", ID: t.ID, Start: t.Start, End: t.End, Text: t.Quote})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}

// appendUnique adds the anchors dst does not already hold.
func appendUnique(seen map[string]bool, dst []plan.Anchor, as ...plan.Anchor) []plan.Anchor {
	for _, a := range as {
		if !seen[a.Key()] {
			seen[a.Key()] = true
			dst = append(dst, a)
		}
	}
	return dst
}
