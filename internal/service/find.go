package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/mmedum/google-docs-mcp/v2/internal/doc"
)

// FindRequest searches a document's text.
type FindRequest struct {
	Document  string
	Query     string
	Regex     bool
	MatchCase bool
	Tab       string
	Segment   string
	Limit     int
	Context   int
}

// FindMatch is one hit.
type FindMatch struct {
	Handle  string `json:"handle"`
	Match   string `json:"match"`
	Offset  int    `json:"offset"`
	Context string `json:"context"`
}

// FindResult lists hits.
type FindResult struct {
	Query      string      `json:"query"`
	RevisionID string      `json:"revision_id"`
	Matches    []FindMatch `json:"matches"`
	Total      int         `json:"total"`
	Truncated  bool        `json:"truncated"`
	Text       string      `json:"-"`
}

func dropPlaceholders(s string) string { return strings.ReplaceAll(s, string(objectPlaceholder), "") }

// matchLimit bounds how many matches one call lists or replaces.
const matchLimit = 500

// compileFind compiles a regex query, case-insensitive unless matchCase.
func compileFind(query string, matchCase bool) (*regexp.Regexp, error) {
	if !matchCase {
		query = "(?i)" + query
	}
	re, err := regexp.Compile(query)
	if err != nil {
		return nil, Errorf("invalid", "regex: %v", err)
	}
	return re, nil
}

// paragraphText is a paragraph's index-aligned text without its newline,
// the text a regex runs over: string offsets equal index offsets.
func paragraphText(b *doc.Block) string {
	return strings.TrimSuffix(alignedSlice(b.Paragraph, b.Start, b.End), "\n")
}

// findBounds is how many matches a find lists and how much context each
// gets, from what the caller asked.
func findBounds(req FindRequest) (limit, contextChars int) {
	limit = req.Limit
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, matchLimit)
	contextChars = req.Context
	if contextChars <= 0 {
		contextChars = 60
	}
	return limit, contextChars
}

// Find locates text or a regular expression in one tab's segment and
// returns handles with context, in document order.
func (s *Service) Find(ctx context.Context, req FindRequest) (*FindResult, error) {
	if strings.TrimSpace(req.Query) == "" {
		return nil, Errorf("invalid", "query is empty")
	}
	if s.isMarkdown(ctx, req.Document) {
		return s.mdFind(ctx, req)
	}
	limit, contextChars := findBounds(req)
	f, err := s.Fetch(ctx, req.Document)
	if err != nil {
		return nil, err
	}
	tab, seg, err := tabSegment(f.Doc, req.Tab, req.Segment)
	if err != nil {
		return nil, err
	}
	var re *regexp.Regexp
	needle := ""
	if req.Regex {
		if re, err = compileFind(req.Query, req.MatchCase); err != nil {
			return nil, err
		}
	} else {
		needle = doc.Normalize(req.Query)
	}
	res := &FindResult{Query: req.Query, RevisionID: f.Doc.RevisionID}
	// Index-aligned text keeps offsets right past chips, images and
	// footnote references; placeholders are dropped from the output.
	type located struct {
		block *doc.Block
		text  string
		spans [][2]int // rune offsets
	}
	var found []located
	if re != nil {
		for _, b := range seg.AllBlocks() {
			if b.Paragraph == nil {
				continue
			}
			text := paragraphText(b)
			ms := re.FindAllStringIndex(text, -1)
			if len(ms) == 0 {
				continue
			}
			l := located{block: b, text: text}
			for _, m := range ms {
				l.spans = append(l.spans, [2]int{utf8.RuneCountInString(text[:m[0]]), utf8.RuneCountInString(text[:m[1]])})
			}
			found = append(found, l)
		}
	} else {
		for _, h := range f.findText(seg, needle, !req.MatchCase) {
			if n := len(found); n == 0 || found[n-1].block != h.block {
				found = append(found, located{block: h.block, text: paragraphText(h.block)})
			}
			l := &found[len(found)-1]
			l.spans = append(l.spans, [2]int{doc.UTF16ToCodePoint(l.text, h.start-h.block.Start), doc.UTF16ToCodePoint(l.text, h.end-h.block.Start)})
		}
	}
	for _, l := range found {
		for _, sp := range l.spans {
			res.Total++
			if len(res.Matches) >= limit {
				res.Truncated = true
				continue
			}
			start, end := runeByte(l.text, sp[0]), runeByte(l.text, sp[1])
			res.Matches = append(res.Matches, FindMatch{Handle: l.block.Handle, Match: dropPlaceholders(l.text[start:end]), Offset: sp[0],
				Context: dropPlaceholders(snippet(l.text, start, end, contextChars))})
		}
	}
	res.Text = findText(res, segmentName(tab, seg))
	return res, nil
}

// runeByte is where the n-th character of s starts, in bytes.
func runeByte(s string, n int) int {
	for i := range s {
		if n == 0 {
			return i
		}
		n--
	}
	return len(s)
}

// snippet is a match in its context: up to n characters each side, the
// match between « and », and an ellipsis where the context was cut.
// start and end are byte offsets.
func snippet(text string, start, end, n int) string {
	from, to := start, end
	for i := 0; i < n && from > 0; i++ {
		_, size := utf8.DecodeLastRuneInString(text[:from])
		from -= size
	}
	for i := 0; i < n && to < len(text); i++ {
		_, size := utf8.DecodeRuneInString(text[to:])
		to += size
	}
	s := text[from:start] + "«" + text[start:end] + "»" + text[end:to]
	if from > 0 {
		s = "…" + s
	}
	if to < len(text) {
		s += "…"
	}
	return s
}

// findText is what find_in_document shows: the count, then one line per
// match listed.
func findText(res *FindResult, where string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d match(es) for %q in %s (revision %s)", res.Total, res.Query, where, res.RevisionID)
	if res.Truncated {
		fmt.Fprintf(&sb, ", showing %d", len(res.Matches))
	}
	sb.WriteString("\n")
	for _, m := range res.Matches {
		fmt.Fprintf(&sb, "[%s] %s\n", m.Handle, m.Context)
	}
	return strings.TrimRight(sb.String(), "\n")
}
