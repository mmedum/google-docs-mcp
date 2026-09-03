package service

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mmedum/google-docs-mcp/internal/doc"
)

// ReadScope selects what part of a document to read. All fields are
// optional; the zero scope is the whole body of the first tab.
type ReadScope struct {
	Tab          string
	Segment      string
	HeadingID    string
	Heading      string
	HeadingLevel int
	Occurrence   int
	FromHandle   string
	ToHandle     string
	ContinueFrom string
}

// Resolved is a block range of one segment. Section is set when the
// range came from a heading.
type Resolved struct {
	Tab         *doc.Tab
	Segment     *doc.Segment
	From        int
	To          int
	Section     *doc.Section
	Description string
}

// blockSelector is the part of a read scope or a write target that
// names whole blocks. At most one of heading_id, heading, handle and
// from/to is set.
type blockSelector struct {
	HeadingID      string
	Heading        string
	HeadingLevel   int
	Occurrence     int
	Handle         string
	From           string
	To             string
	IncludeHeading *bool
}

func (b blockSelector) isSet() bool {
	return b.HeadingID != "" || b.Heading != "" || b.Handle != "" || b.From != "" || b.To != ""
}

var segmentRef = regexp.MustCompile(`^(body|header|footer|footnote)\s*(\d*)$`)

// ResolveScope turns a ReadScope into a block range.
func (s *Service) ResolveScope(f *Fetched, sc ReadScope) (Resolved, error) {
	tab, seg, err := tabSegment(f.Doc, sc.Tab, sc.Segment)
	if err != nil {
		return Resolved{}, err
	}
	sel := blockSelector{HeadingID: sc.HeadingID, Heading: sc.Heading, HeadingLevel: sc.HeadingLevel, Occurrence: sc.Occurrence, From: sc.FromHandle, To: sc.ToHandle}
	if sel.isSet() {
		return s.resolveBlocks(f, tab, seg, sel)
	}
	res := Resolved{Tab: tab, Segment: seg, From: 0, To: len(seg.Blocks), Description: segmentName(tab, seg)}
	if sc.ContinueFrom != "" {
		i, err := s.checkedIndex(f, seg, sc.ContinueFrom)
		if err != nil {
			return Resolved{}, err
		}
		res.From = i
		res.Description = fmt.Sprintf("%s, continuing from %s", segmentName(tab, seg), sc.ContinueFrom)
	}
	return res, nil
}

// resolveBlocks turns a block selector into a block range. Handles are
// checked against the memory of the last read; a heading id may point
// into another tab, which then replaces the given one.
func (s *Service) resolveBlocks(f *Fetched, tab *doc.Tab, seg *doc.Segment, sel blockSelector) (Resolved, error) {
	var res Resolved
	switch {
	case sel.HeadingID != "":
		ht, sec, ok := f.Doc.HeadingByID(sel.HeadingID)
		if !ok {
			return Resolved{}, Errorf("not_found", "no heading with id %q in this document; call get_outline for current ids", sel.HeadingID)
		}
		res = sectionResolved(ht, ht.Body, sec)
	case sel.Heading != "":
		var err error
		if res, err = resolveHeadingText(tab, seg, sel); err != nil {
			return Resolved{}, err
		}
	case sel.Handle != "":
		i, err := s.checkedIndex(f, seg, sel.Handle)
		if err != nil {
			return Resolved{}, err
		}
		res = Resolved{Tab: tab, Segment: seg, From: i, To: i + 1}
	default:
		from, to := 0, len(seg.Blocks)
		var err error
		if sel.From != "" {
			if from, err = s.checkedIndex(f, seg, sel.From); err != nil {
				return Resolved{}, err
			}
		}
		if sel.To != "" {
			j, err := s.checkedIndex(f, seg, sel.To)
			if err != nil {
				return Resolved{}, err
			}
			if j < from {
				return Resolved{}, Errorf("invalid", "to handle %s comes before from handle %s", sel.To, sel.From)
			}
			to = j + 1
		}
		res = Resolved{Tab: tab, Segment: seg, From: from, To: to}
	}
	if res.Section != nil && sel.IncludeHeading != nil && !*sel.IncludeHeading {
		res.From++
	}
	if res.Description == "" {
		res.Description = fmt.Sprintf("%s, blocks %s", segmentName(res.Tab, res.Segment), handleRange(res.Segment, res.From, res.To))
	}
	return res, nil
}

func sectionResolved(tab *doc.Tab, seg *doc.Segment, sec doc.Section) Resolved {
	return Resolved{Tab: tab, Segment: seg, From: sec.From, To: sec.To, Section: &sec,
		Description: fmt.Sprintf("%s, section %q (%s)", segmentName(tab, seg), sec.Heading.Paragraph.Text(doc.ViewCurrent), handleRange(seg, sec.From, sec.To))}
}

func resolveHeadingText(tab *doc.Tab, seg *doc.Segment, sel blockSelector) (Resolved, error) {
	if seg.Kind != doc.SegmentBody {
		return Resolved{}, Errorf("invalid", "heading scopes apply to the body; segment %q has no sections", seg.Label())
	}
	secs := seg.SectionsByHeading(sel.Heading, sel.HeadingLevel)
	switch {
	case len(secs) == 0:
		return Resolved{}, Errorf("not_found", "no heading %q in %s; %s", sel.Heading, segmentName(tab, seg), closestHeadings(seg, sel.Heading))
	case len(secs) > 1 && sel.Occurrence <= 0:
		hs := make([]string, 0, len(secs))
		for _, s := range secs {
			hs = append(hs, s.Heading.Handle)
		}
		return Resolved{}, Errorf("ambiguous", "heading %q matches %d sections (%s); pass occurrence (1-based) or use heading_id from get_outline", sel.Heading, len(secs), strings.Join(hs, ", "))
	case sel.Occurrence > len(secs):
		return Resolved{}, Errorf("not_found", "heading %q has %d match(es), occurrence %d does not exist", sel.Heading, len(secs), sel.Occurrence)
	}
	return sectionResolved(tab, seg, secs[max(sel.Occurrence, 1)-1]), nil
}

func selectSegment(tab *doc.Tab, ref string) (*doc.Segment, error) {
	ref = strings.ToLower(strings.TrimSpace(ref))
	if ref == "" {
		return tab.Body, nil
	}
	m := segmentRef.FindStringSubmatch(ref)
	if m == nil {
		return nil, Errorf("invalid", "segment %q; use body, header, footer, footnote, optionally numbered (header2, footnote3)", ref)
	}
	kind, num := m[1], m[2]
	if kind == "body" {
		return tab.Body, nil
	}
	var list []*doc.Segment
	switch kind {
	case "header":
		list = tab.Headers
	case "footer":
		list = tab.Footers
	case "footnote":
		list = tab.Footnotes
	}
	if len(list) == 0 {
		return nil, Errorf("not_found", "tab %d has no %ss", tab.Number, kind)
	}
	if num == "" {
		return list[0], nil
	}
	if kind == "footnote" {
		for _, s := range list {
			if s.FootnoteNumber == num {
				return s, nil
			}
		}
	}
	n, _ := strconv.Atoi(num)
	if n < 1 || n > len(list) {
		return nil, Errorf("not_found", "tab %d has %d %s(s); %s%s does not exist", tab.Number, len(list), kind, kind, num)
	}
	return list[n-1], nil
}

func topLevelIndex(seg *doc.Segment, handle string) (int, error) {
	handle = strings.TrimSpace(handle)
	for i, b := range seg.Blocks {
		if b.Handle == handle {
			return i, nil
		}
	}
	for _, b := range seg.AllBlocks() {
		if b.Handle == handle {
			return 0, Errorf("invalid", "handle %s is inside a table cell; scope to the enclosing table block instead", handle)
		}
	}
	if strings.Contains(handle, "/") && !strings.HasPrefix(handle, seg.Prefix) {
		return 0, Errorf("not_found", "handle %s belongs to a different tab or segment than %s", handle, seg.Label())
	}
	return 0, Errorf("not_found", "no block %s in %s at this revision; re-read the outline or section", handle, seg.Label())
}

func segmentName(tab *doc.Tab, seg *doc.Segment) string {
	return fmt.Sprintf("tab %d %s", tab.Number, seg.Label())
}

func handleRange(seg *doc.Segment, from, to int) string {
	if to <= from || from >= len(seg.Blocks) {
		return "empty"
	}
	last := min(to, len(seg.Blocks)) - 1
	if last == from {
		return seg.Blocks[from].Handle
	}
	return seg.Blocks[from].Handle + "…" + seg.Blocks[last].Handle
}

func tabList(d *doc.Document) string {
	parts := make([]string, 0, len(d.Tabs))
	for _, t := range d.Tabs {
		parts = append(parts, fmt.Sprintf("%d %q", t.Number, t.Title))
	}
	return strings.Join(parts, ", ")
}

func closestHeadings(seg *doc.Segment, want string) string {
	needle := strings.ToLower(doc.Normalize(want))
	var hits []string
	for _, sec := range seg.Sections() {
		text := sec.Heading.Paragraph.Text(doc.ViewCurrent)
		if strings.Contains(strings.ToLower(doc.Normalize(text)), needle) || strings.Contains(needle, strings.ToLower(doc.Normalize(text))) {
			hits = append(hits, fmt.Sprintf("%q (%s)", text, sec.Heading.Handle))
		}
		if len(hits) == 5 {
			break
		}
	}
	if len(hits) == 0 {
		return "call get_outline for the heading list"
	}
	return "similar headings: " + strings.Join(hits, ", ")
}
