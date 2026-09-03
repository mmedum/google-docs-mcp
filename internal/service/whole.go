package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/mmedum/google-docs-mcp/internal/render"
)

// WholeResult is a whole-document read: every tab's body as markdown.
type WholeResult struct {
	Text       string
	Title      string
	RevisionID string
	Tabs       int
	Truncated  bool
}

// ReadWhole renders every tab's body as markdown under one budget, for
// clients that attach a document as a resource. Tabs are separated by a
// comment naming the tab; when the budget runs out the text ends with a
// comment saying where read_document can continue. No handles, styles,
// suggestions or comments: those are what the read tools are for.
func (s *Service) ReadWhole(ctx context.Context, ref string, maxChars int) (*WholeResult, error) {
	f, err := s.Fetch(ctx, ref)
	if err != nil {
		return nil, err
	}
	d := f.Doc
	res := &WholeResult{Title: d.Title, RevisionID: d.RevisionID, Tabs: len(d.Tabs)}
	var sb strings.Builder
	for i, t := range d.Tabs {
		if len(d.Tabs) > 1 {
			if sb.Len() > 0 {
				sb.WriteString("\n\n")
			}
			fmt.Fprintf(&sb, "<!-- tab %d %q -->\n\n", t.Number, t.Title)
		}
		remaining := 0
		if maxChars > 0 {
			remaining = maxChars - sb.Len()
			if remaining <= 0 {
				res.Truncated = true
				fmt.Fprintf(&sb, "<!-- truncated; %d more tab(s) not shown; use read_document with tab -->", len(d.Tabs)-i)
				break
			}
		}
		r := render.Markdown(t.Body, 0, len(t.Body.Blocks), render.Options{MaxChars: remaining})
		sb.WriteString(r.Text)
		if r.Truncated {
			res.Truncated = true
			fmt.Fprintf(&sb, "\n\n<!-- truncated; continue with read_document tab %d continue_from %s -->", t.Number, r.ContinueFrom)
			break
		}
	}
	res.Text = sb.String()
	return res, nil
}
