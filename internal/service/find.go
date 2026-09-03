package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"

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
	tab, ok := f.Doc.Tab(req.Tab)
	if !ok {
		return nil, Errorf("not_found", "no tab %q; tabs: %s", req.Tab, tabList(f.Doc))
	}
	seg, err := selectSegment(tab, req.Segment)
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
	for _, b := range seg.AllBlocks() {
		if b.Paragraph == nil {
			continue
		}
		text := b.Paragraph.Text(doc.ViewInline)
		var spans [][2]int // rune offsets
		if re != nil {
			for _, m := range re.FindAllStringIndex(text, -1) {
				spans = append(spans, [2]int{len([]rune(text[:m[0]])), len([]rune(text[:m[1]]))})
			}
		} else {
			for _, m := range matchParagraph(b.Paragraph, needle, !req.MatchCase) {
				spans = append(spans, [2]int{doc.UTF16ToCodePoint(text, m[0]-b.Start), doc.UTF16ToCodePoint(text, m[1]-b.Start)})
			}
		}
		runes := []rune(text)
		for _, sp := range spans {
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
			res.Matches = append(res.Matches, FindMatch{Handle: b.Handle, Match: string(runes[sp[0]:sp[1]]), Offset: sp[0], Context: ctxText})
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
