package ooxml

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/mmedum/google-docs-mcp/v3/internal/doc"
)

// Track makes edits tracked changes, which Docs reads as suggestions: what
// an edit removes is marked deleted and what it adds inserted, under an
// author and a time.
type Track struct {
	Author string
	Date   string // RFC 3339 in UTC, as Word writes it
}

// Run is new text and how it looks. A tab in Text becomes a tab, a line
// break a break. Plain text takes the look around it; Styled text is bold,
// italic, struck through or code exactly as it says.
type Run struct {
	Text                       string
	Styled                     bool
	Bold, Italic, Strike, Code bool
	Link                       string
}

// NewPara is a new paragraph: a heading of a level, a list item, or a
// plain paragraph, and its text.
type NewPara struct {
	Heading int    // 1 to 6
	NumID   string // the list, from ListID or NewList; "" for none
	Ilvl    int
	Runs    []Run
}

// ErrUnsupported is an edit this package will not make in a file, saying
// why.
var ErrUnsupported = errors.New("unsupported")

func unsupported(why string) error {
	return fmt.Errorf("%w: %s", ErrUnsupported, why)
}

// pending is one change queued in a paragraph: units [a,b) removed, then
// insert put in their place, both made when the file is written so that
// several changes to one run come out as one rewrite of it.
type pending struct {
	a, b   int
	insert string
	track  *Track
}

// paraAt is paragraph i, with [a,b) checked to be inside it.
func (f *File) paraAt(i, a, b int) (*Para, error) {
	if i < 0 || i >= len(f.Paras) {
		return nil, fmt.Errorf("no paragraph %d", i)
	}
	p := f.Paras[i]
	if a < 0 || b < a || b > p.Len() {
		return nil, fmt.Errorf("[%d,%d) is outside paragraph %d, which has %d units", a, b, i, p.Len())
	}
	return p, nil
}

// queue holds a paragraph's pending changes until the file is written.
func (f *File) queue(i int, pe pending) error {
	if _, err := f.paraAt(i, pe.a, pe.b); err != nil {
		return err
	}
	if f.queued == nil {
		f.queued = map[int][]pending{}
	}
	for _, q := range f.queued[i] {
		if pe.a < q.b && q.a < pe.b {
			return errors.New("two edits reach the same text")
		}
	}
	f.queued[i] = append(f.queued[i], pe)
	return nil
}

// Replace puts runs in place of units [a,b) of paragraph i; a == b is an
// insertion, no runs a deletion. New text takes the look of the text it
// replaces, or of the text before it, with the runs' own on top. A field's
// code counts no unit, so a change across a field keeps it. Text a pending
// suggestion inserted or deleted is refused in either mode: written into
// it, a direct change would go with the suggestion when it is accepted or
// rejected.
func (f *File) Replace(i, a, b int, runs []Run, tr *Track) error {
	p, err := f.paraAt(i, a, b)
	if err != nil {
		return err
	}
	host := p.hostRun(a)
	if a < b {
		// Text typed over a selection takes the look of its first character.
		host = p.runs[p.units[a]]
	}
	for u := a; u < b; u++ {
		r := p.runs[p.units[u]]
		switch {
		case r.field:
			return unsupported("the text runs into a field, whose code cannot be cut")
		case inside(r, "del"):
			return unsupported("the text is already marked deleted")
		case inside(r, "ins"):
			return unsupported("the text is part of a pending suggestion; accept or reject it first")
		case host != nil && r.in != host.in:
			return unsupported("the text runs across a link, a field or a tracked change; narrow the target to one side of it")
		}
	}
	if host != nil && a == b && (inside(host, "ins") || inside(host, "del")) {
		return unsupported("the place is inside a pending suggestion; accept or reject it first")
	}
	base := ""
	if host != nil {
		base = host.rPr
	}
	ins, err := f.runsXML(base, runs, host != nil && inside(host, "hyperlink"), tr)
	if err != nil {
		return err
	}
	return f.queue(i, pending{a: a, b: b, insert: ins, track: tr})
}

// hostRun is the run whose look new text at unit a takes: the one holding
// the text before it, or after it at the paragraph's start; nil in an
// empty paragraph.
func (p *Para) hostRun(a int) *run {
	switch {
	case a > 0:
		return p.runs[p.units[a-1]]
	case len(p.units) > 0:
		return p.runs[p.units[0]]
	}
	return nil
}

func inside(r *run, name string) bool {
	for w := r.in; w != nil; w = w.parent {
		if w.name == name {
			return true
		}
	}
	return false
}

// InsertParas puts new paragraphs before paragraph i, or after it.
func (f *File) InsertParas(i int, after bool, paras []NewPara, tr *Track) error {
	p, err := f.paraAt(i, 0, 0)
	if err != nil {
		return err
	}
	var b strings.Builder
	for _, np := range paras {
		x, err := f.paraXML(np, tr)
		if err != nil {
			return err
		}
		b.WriteString(x)
	}
	at := p.el.start
	if after {
		at = p.el.end
	}
	f.splices = append(f.splices, splice{start: at, end: at, text: b.String()})
	return nil
}

// DeleteParas removes paragraphs i to j. A table cell, and the body, must
// keep a last paragraph, so one of those goes only when replaced says new
// paragraphs come after it.
func (f *File) DeleteParas(i, j int, replaced bool, tr *Track) error {
	if i < 0 || j < i || j >= len(f.Paras) {
		return fmt.Errorf("no paragraphs %d to %d", i, j)
	}
	for k := i; k <= j; k++ {
		p := f.Paras[k]
		switch {
		case p.section:
			return unsupported("a paragraph there ends a section; deleting it would remove the section break")
		case p.cell != f.Paras[i].cell:
			return unsupported("the paragraphs are in different table cells")
		case (k+1 == len(f.Paras) || p.cell >= 0 && f.Paras[k+1].cell != p.cell) && !replaced:
			return unsupported("the paragraph is the last of its table cell or of the document, which keeps one")
		case k > i && strings.Contains(string(f.doc[f.Paras[k-1].el.end:p.el.start]), "<"+f.w.tag("tbl")):
			return unsupported("a table lies between the paragraphs")
		}
	}
	if tr == nil {
		f.splices = append(f.splices, splice{start: f.Paras[i].el.start, end: f.Paras[j].el.end})
		return nil
	}
	for k := i; k <= j; k++ {
		p := f.Paras[k]
		if p.markIns || slices.ContainsFunc(p.runs, func(r *run) bool { return inside(r, "ins") }) {
			return unsupported("a paragraph there holds a pending suggestion; accept or reject it first")
		}
		// Text already marked deleted stays as it is.
		for _, s := range p.stretches(func(r *run) bool { return !inside(r, "del") }) {
			if err := f.queue(k, pending{a: s[0], b: s[1], track: tr}); err != nil {
				return err
			}
		}
		if !p.markDel {
			f.markDeleted(p, tr)
		}
	}
	return nil
}

// stretches are the paragraph's longest runs of units [a,b) whose runs
// keep says to.
func (p *Para) stretches(keep func(*run) bool) [][2]int {
	var out [][2]int
	for u := 0; u < len(p.units); {
		if !keep(p.runs[p.units[u]]) {
			u++
			continue
		}
		a := u
		for u < len(p.units) && keep(p.runs[p.units[u]]) {
			u++
		}
		out = append(out, [2]int{a, u})
	}
	return out
}

// markDeleted marks a paragraph's mark deleted, in the run properties of
// its paragraph properties, each made when it has none.
func (f *File) markDeleted(p *Para, tr *Track) {
	mark := f.trackTag("del", tr, true)
	var s splice
	switch {
	case p.markRPr.present():
		s = f.openChild(p.markRPr, mark)
	case p.pPr.present() && !p.pPr.empty():
		// Run properties go late in paragraph properties.
		s = splice{start: p.pPrTail, end: p.pPrTail, text: f.w.el("rPr", mark)}
	case p.pPr.present():
		s = f.openChild(p.pPr, f.w.el("rPr", mark))
	default:
		s = f.openChild(p.el, f.w.el("pPr", f.w.el("rPr", mark)))
	}
	f.splices = append(f.splices, s)
}

// openChild puts inner first in element e: an empty <x .../> is written
// out in full to take it, its attributes kept.
func (f *File) openChild(e span, inner string) splice {
	if !e.empty() {
		return splice{start: e.open, end: e.open, text: inner}
	}
	tag := strings.TrimSuffix(string(f.doc[e.start:e.end]), "/>")
	name := tag[1:]
	if i := strings.IndexAny(name, " \t\r\n"); i >= 0 {
		name = name[:i]
	}
	return splice{start: e.start, end: e.end, text: tag + ">" + inner + "</" + name + ">"}
}

// trackTag is the w:ins or w:del element of a tracked change, empty when
// it marks a paragraph, open when it wraps runs.
func (f *File) trackTag(kind string, tr *Track, empty bool) string {
	w := f.w
	s := fmt.Sprintf(`<%s %s="%d" %s="%s" %s="%s"`, w.tag(kind), w.tag("id"), f.id(), w.tag("author"), escape(tr.Author), w.tag("date"), escape(tr.Date))
	if empty {
		return s + "/>"
	}
	return s + ">"
}

func (f *File) wrapTracked(kind string, tr *Track, inner string) string {
	return f.trackTag(kind, tr, false) + inner + "</" + f.w.tag(kind) + ">"
}

// paraXML is a new paragraph's markup.
func (f *File) paraXML(np NewPara, tr *Track) (string, error) {
	w := f.w
	var pPr strings.Builder
	if np.Heading > 0 {
		id, err := f.HeadingStyle(np.Heading)
		if err != nil {
			return "", err
		}
		pPr.WriteString(w.val("pStyle", id))
	}
	if np.NumID != "" {
		pPr.WriteString(w.el("numPr", w.val("ilvl", strconv.Itoa(np.Ilvl))+w.val("numId", np.NumID)))
	}
	if tr != nil {
		pPr.WriteString(w.el("rPr", f.trackTag("ins", tr, true)))
	}
	runs, err := f.runsXML("", np.Runs, false, tr)
	if err != nil {
		return "", err
	}
	if pPr.Len() > 0 {
		return w.el("p", w.el("pPr", pPr.String())+runs), nil
	}
	return w.el("p", runs), nil
}

// runsXML is new runs' markup, each with base's look and its own on top,
// tracked as inserted when tr says so. A tracked insertion goes inside a
// link, which may not go inside one.
func (f *File) runsXML(base string, runs []Run, inLink bool, tr *Track) (string, error) {
	var b strings.Builder
	for _, r := range runs {
		if r.Text == "" {
			continue
		}
		x := f.w.el("r", f.mergeRPr(base, r)+f.textXML(r.Text))
		if tr != nil {
			x = f.wrapTracked("ins", tr, x)
		}
		if r.Link != "" {
			if inLink {
				return "", unsupported("a link inside a link")
			}
			if f.r == "" {
				return "", unsupported("the file declares no relationships namespace for a link")
			}
			id, err := f.addRelationship(relHyperlink, r.Link, true)
			if err != nil {
				return "", err
			}
			x = fmt.Sprintf(`<%s %s:id="%s">%s</%s>`, f.w.tag("hyperlink"), f.r, id, x, f.w.tag("hyperlink"))
		}
		b.WriteString(x)
	}
	return b.String(), nil
}

// textXML is text as run content: w:t, with a tab as w:tab and a line
// break as w:br.
func (f *File) textXML(s string) string {
	var b strings.Builder
	start := 0
	flush := func(end int) {
		if end > start {
			b.WriteString(f.w.text("t", s[start:end]))
		}
	}
	for i, c := range s {
		switch c {
		case '\t':
			flush(i)
			b.WriteString(f.w.empty("tab"))
			start = i + 1
		case '\v', '\n':
			flush(i)
			b.WriteString(f.w.empty("br"))
			start = i + 1
		}
	}
	flush(len(s))
	return b.String()
}

// rPrOrder is the order a run's properties are written in (ECMA-376,
// CT_RPr); Word holds a file to it.
var rPrOrder = []string{"rStyle", "rFonts", "b", "bCs", "i", "iCs", "caps", "smallCaps", "strike", "dstrike", "outline",
	"shadow", "emboss", "imprint", "noProof", "snapToGrid", "vanish", "webHidden", "color", "spacing", "w", "kern",
	"position", "sz", "szCs", "highlight", "u", "effect", "bdr", "shd", "fitText", "vertAlign", "rtl", "cs", "em", "lang",
	"eastAsianLayout", "specVanish", "oMath"}

// mergeRPr is base's run properties with r's look on top: for styled
// text, bold, italic, strike and code font set as r says; a link's color
// and underline added; any tracked formatting change left out.
func (f *File) mergeRPr(base string, r Run) string {
	w := f.w
	kids := rPrKids(base)
	set := func(name, xml string, on bool) {
		delete(kids, name)
		if on {
			kids[name] = xml
		}
	}
	if r.Styled {
		set("b", w.empty("b"), r.Bold)
		set("bCs", w.empty("bCs"), r.Bold)
		set("i", w.empty("i"), r.Italic)
		set("iCs", w.empty("iCs"), r.Italic)
		set("strike", w.empty("strike"), r.Strike)
	}
	if r.Code {
		set("rFonts", fmt.Sprintf(`<%s %s="Courier New" %s="Courier New" %s="Courier New"/>`, w.tag("rFonts"), w.tag("ascii"), w.tag("hAnsi"), w.tag("cs")), true)
	}
	if r.Link != "" {
		set("color", w.val("color", "1155CC"), true)
		set("u", w.val("u", "single"), true)
	}
	delete(kids, "rPrChange")
	if len(kids) == 0 {
		return ""
	}
	var b strings.Builder
	for _, name := range rPrOrder {
		b.WriteString(kids[name])
	}
	return w.el("rPr", b.String())
}

// rPrKids are the children of a run's properties, by local name, as
// written.
func rPrKids(rPr string) map[string]string {
	kids := map[string]string{}
	if rPr == "" {
		return kids
	}
	inner := rPr[strings.Index(rPr, ">")+1:]
	if i := strings.LastIndex(inner, "</"); i >= 0 {
		inner = inner[:i]
	}
	for _, el := range topElements(inner) {
		kids[localName(el)] = el
	}
	return kids
}

// topElements splits markup into its top-level elements.
func topElements(s string) []string {
	var out []string
	depth, start := 0, -1
	for i := 0; i < len(s); i++ {
		if s[i] != '<' {
			continue
		}
		end := strings.IndexByte(s[i:], '>')
		if end < 0 {
			break
		}
		tag := s[i : i+end+1]
		switch {
		case strings.HasPrefix(tag, "</"):
			depth--
			if depth == 0 && start >= 0 {
				out = append(out, s[start:i+end+1])
				start = -1
			}
		case strings.HasSuffix(tag, "/>"):
			if depth == 0 {
				out = append(out, tag)
			}
		case strings.HasPrefix(tag, "<?"), strings.HasPrefix(tag, "<!"):
		default:
			if depth == 0 {
				start = i
			}
			depth++
		}
		i += end
	}
	return out
}

// localName is an element's name without its prefix.
func localName(el string) string {
	name := el[1:]
	if i := strings.IndexAny(name, " />"); i >= 0 {
		name = name[:i]
	}
	if i := strings.IndexByte(name, ':'); i >= 0 {
		name = name[i+1:]
	}
	return name
}

// flush turns the queued changes into splices, one per stretch of runs
// they reach in a paragraph.
func (f *File) flush() {
	for _, i := range slices.Sorted(maps.Keys(f.queued)) {
		f.splices = append(f.splices, f.paraSplices(f.Paras[i], f.queued[i])...)
	}
	f.queued = nil
}

// paraSplices are one paragraph's queued changes as splices.
func (f *File) paraSplices(p *Para, pes []pending) []splice {
	// By start, a point before a removal that starts there.
	sort.SliceStable(pes, func(i, j int) bool { return pes[i].a < pes[j].a || pes[i].a == pes[j].a && pes[i].b < pes[j].b })
	if p.el.empty() {
		// An empty <w:p/> holds no run, so every change is a point in it,
		// and it is written out in full to take them.
		var b strings.Builder
		for _, pe := range pes {
			b.WriteString(pe.insert)
		}
		return []splice{f.openChild(p.el, b.String())}
	}
	// Each change reaches the runs from first to last; a change at a run
	// boundary reaches none and is a splice of its own.
	type group struct {
		first, last int
		pes         []pending
	}
	var groups []*group
	var ss []splice
	for _, pe := range pes {
		first, last, ok := p.reach(pe)
		if !ok {
			at := p.boundary(pe.a, f)
			ss = append(ss, splice{start: at, end: at, text: pe.insert})
			continue
		}
		if n := len(groups); n > 0 && first <= groups[n-1].last {
			g := groups[n-1]
			g.last = max(g.last, last)
			g.pes = append(g.pes, pe)
			continue
		}
		groups = append(groups, &group{first: first, last: last, pes: []pending{pe}})
	}
	for _, g := range groups {
		ss = append(ss, splice{start: p.runs[g.first].el.start, end: p.runs[g.last].el.end, text: f.rewrite(p, g.first, g.last, g.pes)})
	}
	return ss
}

// reach is the runs a change rewrites: those its removed units are in, or
// the one a point inside a run splits.
func (p *Para) reach(pe pending) (int, int, bool) {
	if pe.a < pe.b {
		return p.units[pe.a], p.units[pe.b-1], true
	}
	if pe.a > 0 && pe.a < len(p.units) && p.units[pe.a-1] == p.units[pe.a] {
		r := p.units[pe.a]
		return r, r, true
	}
	return 0, 0, false
}

// boundary is where a point between runs sits in document.xml: after the
// run holding the unit before it, or before the first run, or, in an empty
// paragraph, before its end tag.
func (p *Para) boundary(a int, f *File) int {
	switch r := p.hostRun(a); {
	case r == nil:
		return p.el.end - len("</"+f.w.tag("p")+">")
	case a > 0:
		return r.el.end
	default:
		return r.el.start
	}
}

// rewrite is runs first to last with the changes made: text kept as it
// was, text removed dropped or, tracked, marked deleted, and each change's
// new text right after what it removes, or at its point.
func (f *File) rewrite(p *Para, first, last int, pes []pending) string {
	var b strings.Builder
	next := 0
	inserts := func(to int) {
		for next < len(pes) && pes[next].b == to {
			b.WriteString(pes[next].insert)
			next++
		}
	}
	for ri := first; ri <= last; ri++ {
		r := p.runs[ri]
		if ri > first {
			b.Write(f.doc[p.runs[ri-1].el.end:r.el.start])
		}
		if r.u0 == r.u1 {
			// A run with no text, a comment's reference: kept whole.
			b.Write(f.doc[r.el.start:r.el.end])
			continue
		}
		cuts := []int{r.u0, r.u1}
		for _, pe := range pes {
			for _, x := range []int{pe.a, pe.b} {
				if x > r.u0 && x < r.u1 {
					cuts = append(cuts, x)
				}
			}
		}
		sort.Ints(cuts)
		for k := 0; k+1 < len(cuts); k++ {
			from, to := cuts[k], cuts[k+1]
			if from == to {
				continue
			}
			var removal *pending
			for i := range pes {
				if pes[i].a <= from && to <= pes[i].b {
					removal = &pes[i]
				}
			}
			part := f.partial(r, from-r.u0, to-r.u0, to == r.u1, removal != nil)
			switch {
			case removal == nil:
				b.WriteString(part)
			case removal.track != nil && part != "":
				b.WriteString(f.wrapTracked("del", removal.track, part))
			}
			inserts(to)
		}
	}
	for ; next < len(pes); next++ {
		b.WriteString(pes[next].insert)
	}
	return b.String()
}

// partial is run r cut to its units [from,to): its own start tag and
// properties, its text cut, its other elements whole. An element that
// counts no unit goes with the part it starts in, or the last one. A
// deleted part's text is written as w:delText. Nothing is written for a
// part with nothing in it.
func (f *File) partial(r *run, from, to int, last, deleted bool) string {
	var b strings.Builder
	pos := 0
	for _, k := range r.kids {
		switch {
		case k.n == 0:
			if pos >= from && (pos < to || last) {
				b.Write(f.doc[k.start:k.end])
			}
		case !k.isText:
			if pos >= from && pos < to {
				b.Write(f.doc[k.start:k.end])
			}
		default:
			lo, hi := max(from, pos)-pos, min(to, pos+k.n)-pos
			if lo < hi {
				name := "t"
				if deleted || k.deleted {
					name = "delText"
				}
				b.WriteString(f.w.text(name, k.text[doc.UTF16ToByte(k.text, int64(lo)):doc.UTF16ToByte(k.text, int64(hi))]))
			}
		}
		pos += k.n
	}
	if b.Len() == 0 {
		return ""
	}
	return string(f.doc[r.el.start:r.el.open]) + r.rPr + b.String() + "</" + f.w.tag("r") + ">"
}

// NewComment is a comment to pin to a passage.
type NewComment struct {
	Author, Initials, Date string
	Text                   string // a paragraph per line
}

// Comment pins a comment to the units from a in paragraph i to b in
// paragraph j, and returns its id.
func (f *File) Comment(i, a, j, b int, c NewComment) (string, error) {
	if j < i || (i == j && b < a) {
		return "", errors.New("the passage ends before it starts")
	}
	w := f.w
	sid := strconv.Itoa(f.id())
	idAttr := fmt.Sprintf(`%s="%s"`, w.tag("id"), sid)
	start := fmt.Sprintf(`<%s %s/>`, w.tag("commentRangeStart"), idAttr)
	end := fmt.Sprintf(`<%s %s/>`, w.tag("commentRangeEnd"), idAttr) + w.el("r", fmt.Sprintf(`<%s %s/>`, w.tag("commentReference"), idAttr))
	if err := f.queue(i, pending{a: a, b: a, insert: start}); err != nil {
		return "", err
	}
	if err := f.queue(j, pending{a: b, b: b, insert: end}); err != nil {
		return "", err
	}
	if err := f.addComment(sid, c); err != nil {
		return "", err
	}
	return sid, nil
}
