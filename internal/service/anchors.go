package service

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/mmedum/google-docs-mcp/internal/doc"
	"github.com/mmedum/google-docs-mcp/internal/plan"
)

// CommentThread is a comment as this server sees it, from either backend.
type CommentThread struct {
	ID       string `json:"id"`
	Author   string `json:"author,omitempty"`
	Content  string `json:"content"`
	Quote    string `json:"quote,omitempty"`
	Resolved bool   `json:"resolved"`
	Created  string `json:"created,omitempty"`
	Replies  int    `json:"replies"`
	Handle   string `json:"handle,omitempty"`
	Start    int64  `json:"-"`
	End      int64  `json:"-"`
	Tab      string `json:"-"`
	Segment  string `json:"-"`
}

// previewComment mirrors the Developer Preview comment thread shape
// observed live: no range, but the quoted text.
type previewComment struct {
	CommentID string `json:"commentId"`
	AnchorID  string `json:"anchorId"`
	Status    string `json:"status"`
	Quote     string `json:"plainTextQuote"`
	HeadPost  struct {
		Content    string `json:"content"`
		CreateTime string `json:"createTime"`
		Author     struct {
			DisplayName string `json:"displayName"`
		} `json:"author"`
	} `json:"headPost"`
	Replies []json.RawMessage `json:"replies"`
}

// comments lists the document's comment threads through the preview
// payload when present, else the Drive API.
func (s *Service) comments(ctx context.Context, f *Fetched) ([]CommentThread, error) {
	var out []CommentThread
	if s.opts.Preview && f.Preview.Comments != nil {
		var raw []previewComment
		if err := json.Unmarshal(f.Preview.Comments, &raw); err == nil {
			for _, c := range raw {
				out = append(out, CommentThread{ID: c.CommentID, Author: c.HeadPost.Author.DisplayName, Content: c.HeadPost.Content,
					Quote: c.Quote, Resolved: c.Status == "RESOLVED", Created: c.HeadPost.CreateTime, Replies: len(c.Replies)})
			}
			return s.locateComments(f.Doc, out), nil
		}
	}
	list, err := s.api.ListComments(ctx, f.Doc.ID, false)
	if err != nil {
		return nil, wrapAPI(err, "comments")
	}
	for _, c := range list {
		t := CommentThread{ID: c.ID, Content: c.Content, Resolved: c.Resolved, Created: c.CreatedTime, Replies: len(c.Replies)}
		if c.Author != nil {
			t.Author = c.Author.DisplayName
		}
		if c.QuotedFileContent != nil {
			t.Quote = c.QuotedFileContent.Value
		}
		out = append(out, t)
	}
	return s.locateComments(f.Doc, out), nil
}

// locateComments finds each thread's quoted text in the document so the
// guard and the listing can name a block. Quotes that appear more than
// once or not at all stay unlocated.
func (s *Service) locateComments(d *doc.Document, threads []CommentThread) []CommentThread {
	for i := range threads {
		q := doc.Normalize(threads[i].Quote)
		if q == "" {
			continue
		}
		var hits []CommentThread
		for _, t := range d.Tabs {
			for _, seg := range t.Segments() {
				for _, b := range seg.AllBlocks() {
					if b.Paragraph == nil {
						continue
					}
					for _, m := range matchParagraph(b.Paragraph, q, false) {
						hits = append(hits, CommentThread{Handle: b.Handle, Start: m[0], End: m[1], Tab: t.ID, Segment: seg.ID})
					}
				}
			}
		}
		if len(hits) == 1 {
			threads[i].Handle, threads[i].Start, threads[i].End, threads[i].Tab, threads[i].Segment = hits[0].Handle, hits[0].Start, hits[0].End, hits[0].Tab, hits[0].Segment
		}
	}
	return threads
}

// anchorsIn lists everything inside [start, end) of a segment that a
// deletion would destroy: pending suggestions, inline objects, footnote
// references, and located comment threads.
func anchorsIn(tab *doc.Tab, seg *doc.Segment, start, end int64, threads []CommentThread) []plan.Anchor {
	var out []plan.Anchor
	seen := map[string]bool{}
	add := func(a plan.Anchor) {
		key := a.Kind + ":" + a.ID
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, a)
	}
	for _, b := range seg.AllBlocks() {
		if b.End <= start || b.Start >= end {
			continue
		}
		for _, id := range b.Inserted {
			add(plan.Anchor{Kind: "suggestion", ID: id, Start: b.Start, End: b.End})
		}
		for _, id := range b.Deleted {
			add(plan.Anchor{Kind: "suggestion", ID: id, Start: b.Start, End: b.End})
		}
		if b.Paragraph == nil {
			continue
		}
		for _, r := range b.Paragraph.Runs {
			if r.End <= start || r.Start >= end {
				continue
			}
			for _, id := range r.Inserted {
				add(plan.Anchor{Kind: "suggestion", ID: id, Start: r.Start, End: r.End, Text: r.Text})
			}
			for _, id := range r.Deleted {
				add(plan.Anchor{Kind: "suggestion", ID: id, Start: r.Start, End: r.End, Text: r.Text})
			}
			switch r.Kind {
			case doc.RunInlineObject:
				add(plan.Anchor{Kind: "image", ID: r.ObjectID, Start: r.Start, End: r.End})
			case doc.RunFootnoteRef:
				add(plan.Anchor{Kind: "footnote", ID: r.FootnoteID, Start: r.Start, End: r.End})
			}
		}
	}
	for _, t := range threads {
		if t.Handle == "" || t.Tab != tab.ID || t.Segment != seg.ID || t.Resolved {
			continue
		}
		if t.End > start && t.Start < end {
			add(plan.Anchor{Kind: "comment", ID: t.ID, Start: t.Start, End: t.End, Text: t.Quote})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}
