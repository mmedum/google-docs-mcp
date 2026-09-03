package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/mmedum/google-docs-mcp/internal/doc"
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

// Find locates text or a regular expression in one tab's segment and
// returns handles with context, in document order.
func (s *Service) Find(ctx context.Context, req FindRequest) (*FindResult, error) {
	if strings.TrimSpace(req.Query) == "" {
		return nil, Errorf("invalid", "query is empty")
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	contextChars := req.Context
	if contextChars <= 0 {
		contextChars = 60
	}
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
		pat := req.Query
		if !req.MatchCase {
			pat = "(?i)" + pat
		}
		re, err = regexp.Compile(pat)
		if err != nil {
			return nil, Errorf("invalid", "regex: %v", err)
		}
	} else {
		needle = doc.Normalize(req.Query)
	}
	res := &FindResult{Query: req.Query, RevisionID: f.Doc.RevisionID}
	// Index-aligned text keeps offsets right past chips, images and
	// footnote references; placeholders are dropped from the output.
	aligned := func(b *doc.Block) string { return strings.TrimSuffix(alignedSlice(b.Paragraph, b.Start, b.End), "\n") }
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
			text := aligned(b)
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
				found = append(found, located{block: h.block, text: aligned(h.block)})
			}
			l := &found[len(found)-1]
			l.spans = append(l.spans, [2]int{doc.UTF16ToCodePoint(l.text, h.start-h.block.Start), doc.UTF16ToCodePoint(l.text, h.end-h.block.Start)})
		}
	}
	for _, l := range found {
		runes := []rune(l.text)
		for _, sp := range l.spans {
			res.Total++
			if len(res.Matches) >= limit {
				res.Truncated = true
				continue
			}
			from, to := max(sp[0]-contextChars, 0), min(sp[1]+contextChars, len(runes))
			ctxText := string(runes[from:sp[0]]) + "«" + string(runes[sp[0]:sp[1]]) + "»" + string(runes[sp[1]:to])
			if from > 0 {
				ctxText = "…" + ctxText
			}
			if to < len(runes) {
				ctxText += "…"
			}
			res.Matches = append(res.Matches, FindMatch{Handle: l.block.Handle, Match: dropPlaceholders(string(runes[sp[0]:sp[1]])), Offset: sp[0], Context: dropPlaceholders(ctxText)})
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d match(es) for %q in %s (revision %s)", res.Total, req.Query, segmentName(tab, seg), f.Doc.RevisionID)
	if res.Truncated {
		fmt.Fprintf(&sb, ", showing %d", len(res.Matches))
	}
	sb.WriteString("\n")
	for _, m := range res.Matches {
		fmt.Fprintf(&sb, "[%s] %s\n", m.Handle, m.Context)
	}
	res.Text = strings.TrimRight(sb.String(), "\n")
	return res, nil
}
