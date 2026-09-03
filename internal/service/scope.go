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

// Resolved is a block range of one segment.
type Resolved struct {
	Tab         *doc.Tab
	Segment     *doc.Segment
	From        int
	To          int
	Description string
}

var segmentRef = regexp.MustCompile(`^(body|header|footer|footnote)\s*(\d*)$`)

// ResolveScope turns a ReadScope into a block range.
func ResolveScope(d *doc.Document, sc ReadScope) (Resolved, error) {
	tab, ok := d.Tab(sc.Tab)
	if !ok {
		return Resolved{}, Errorf("not_found", "no tab %q; tabs: %s", sc.Tab, tabList(d))
	}
	if sc.HeadingID != "" {
		ht, sec, ok := d.HeadingByID(sc.HeadingID)
		if !ok {
			return Resolved{}, Errorf("not_found", "no heading with id %q in this document; call get_outline for current ids", sc.HeadingID)
		}
		return Resolved{Tab: ht, Segment: ht.Body, From: sec.From, To: sec.To,
			Description: fmt.Sprintf("%s, section %q (%s)", segmentName(ht, ht.Body), sec.Heading.Paragraph.Text(doc.ViewCurrent), handleRange(ht.Body, sec.From, sec.To))}, nil
	}
	seg, err := selectSegment(tab, sc.Segment)
	if err != nil {
		return Resolved{}, err
	}
	res := Resolved{Tab: tab, Segment: seg, From: 0, To: len(seg.Blocks)}

	switch {
	case sc.Heading != "":
		return resolveHeadingText(tab, seg, sc)
	case sc.FromHandle != "" || sc.ToHandle != "":
		if sc.FromHandle != "" {
			i, err := topLevelIndex(seg, sc.FromHandle)
			if err != nil {
				return Resolved{}, err
			}
			res.From = i
		}
		if sc.ToHandle != "" {
			j, err := topLevelIndex(seg, sc.ToHandle)
			if err != nil {
				return Resolved{}, err
			}
			if j < res.From {
				return Resolved{}, Errorf("invalid", "to_handle %s comes before from_handle %s", sc.ToHandle, sc.FromHandle)
			}
			res.To = j + 1
		}
		res.Description = fmt.Sprintf("%s, blocks %s", segmentName(tab, seg), handleRange(seg, res.From, res.To))
		return res, nil
	}
	if sc.ContinueFrom != "" {
		i, err := topLevelIndex(seg, sc.ContinueFrom)
		if err != nil {
			return Resolved{}, err
		}
		res.From = i
		res.Description = fmt.Sprintf("%s, continuing from %s", segmentName(tab, seg), sc.ContinueFrom)
		return res, nil
	}
	res.Description = segmentName(tab, seg)
	return res, nil
}

func resolveHeadingText(tab *doc.Tab, seg *doc.Segment, sc ReadScope) (Resolved, error) {
	if seg.Kind != doc.SegmentBody {
		return Resolved{}, Errorf("invalid", "heading scopes apply to the body; segment %q has no sections", seg.Label())
	}
	secs := seg.SectionsByHeading(sc.Heading, sc.HeadingLevel)
	switch {
	case len(secs) == 0:
		return Resolved{}, Errorf("not_found", "no heading %q in %s; %s", sc.Heading, segmentName(tab, seg), closestHeadings(seg, sc.Heading))
	case len(secs) > 1 && sc.Occurrence <= 0:
		hs := make([]string, 0, len(secs))
		for _, s := range secs {
			hs = append(hs, s.Heading.Handle)
		}
		return Resolved{}, Errorf("ambiguous", "heading %q matches %d sections (%s); pass occurrence (1-based) or use heading_id from get_outline", sc.Heading, len(secs), strings.Join(hs, ", "))
	case sc.Occurrence > len(secs):
		return Resolved{}, Errorf("not_found", "heading %q has %d match(es), occurrence %d does not exist", sc.Heading, len(secs), sc.Occurrence)
	}
	sec := secs[max(sc.Occurrence, 1)-1]
	return Resolved{Tab: tab, Segment: seg, From: sec.From, To: sec.To,
		Description: fmt.Sprintf("%s, section %q (%s)", segmentName(tab, seg), sec.Heading.Paragraph.Text(doc.ViewCurrent), handleRange(seg, sec.From, sec.To))}, nil
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
