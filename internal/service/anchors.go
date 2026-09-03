package service

import (
	"context"
	"sort"

	"github.com/mmedum/google-docs-mcp/internal/doc"
	"github.com/mmedum/google-docs-mcp/internal/gapi"
	"github.com/mmedum/google-docs-mcp/internal/gdocs"
	"github.com/mmedum/google-docs-mcp/internal/plan"
)

// CommentReply is one post in a thread after the head post.
type CommentReply struct {
	ID      string `json:"id,omitempty"`
	Author  string `json:"author,omitempty"`
	Content string `json:"content,omitempty"`
	Created string `json:"created,omitempty"`
	Action  string `json:"action,omitempty"` // resolve, reopen
	Deleted bool   `json:"deleted,omitempty"`
}

// CommentThread is a comment as this server sees it, from either backend.
type CommentThread struct {
	ID       string         `json:"id"`
	Author   string         `json:"author,omitempty"`
	Content  string         `json:"content"`
	Quote    string         `json:"quote,omitempty"`
	Resolved bool           `json:"resolved"`
	Deleted  bool           `json:"deleted,omitempty"`
	Created  string         `json:"created,omitempty"`
	Modified string         `json:"modified,omitempty"`
	Replies  []CommentReply `json:"replies"`
	Handle   string         `json:"handle,omitempty"`
	Anchored bool           `json:"anchored"`
	Start    int64          `json:"-"`
	End      int64          `json:"-"`
	Tab      string         `json:"-"`
	Segment  string         `json:"-"`
}

// comments lists the document's comment threads, located in the
// document, from the preview payload when present, else the Drive API.
// The lookup runs once per fetch.
func (s *Service) comments(ctx context.Context, f *Fetched) ([]CommentThread, error) {
	f.threadsOnce.Do(func() {
		if s.opts.Preview && f.Wire.Comments != nil {
			f.threads = locateComments(f, previewThreads(f.Wire))
			return
		}
		list, err := s.api.ListComments(ctx, f.Doc.ID, false)
		if err != nil {
			f.threadsErr = wrapAPI(err, "comments")
			return
		}
		f.threads = locateComments(f, driveThreads(list))
	})
	return f.threads, f.threadsErr
}

func previewThreads(w *gdocs.Document) []CommentThread {
	out := make([]CommentThread, 0, len(w.Comments))
	for _, c := range w.Comments {
		t := CommentThread{ID: c.CommentID, Author: c.HeadPost.Author.DisplayName, Content: c.HeadPost.Content, Quote: c.PlainTextQuote,
			Resolved: c.Status == "RESOLVED", Created: c.HeadPost.CreateTime, Modified: c.HeadPost.UpdateTime, Replies: []CommentReply{}}
		for _, p := range c.Replies {
			t.Replies = append(t.Replies, CommentReply{ID: p.PostID, Author: p.Author.DisplayName, Content: p.Content, Created: p.CreateTime, Action: commentAction(p.CommentAction)})
		}
		out = append(out, t)
	}
	return out
}

func commentAction(a string) string {
	switch a {
	case "RESOLVE":
		return "resolve"
	case "REOPEN":
		return "reopen"
	}
	return ""
}

func driveThreads(list []*gapi.DriveComment) []CommentThread {
	out := make([]CommentThread, 0, len(list))
	for _, c := range list {
		t := CommentThread{ID: c.ID, Content: c.Content, Resolved: c.Resolved, Deleted: c.Deleted, Created: c.CreatedTime, Modified: c.ModifiedTime, Replies: []CommentReply{}}
		if c.Author != nil {
			t.Author = c.Author.DisplayName
		}
		if c.QuotedFileContent != nil {
			t.Quote = c.QuotedFileContent.Value
		}
		for _, r := range c.Replies {
			rp := CommentReply{ID: r.ID, Content: r.Content, Created: r.CreatedTime, Action: r.Action, Deleted: r.Deleted}
			if r.Author != nil {
				rp.Author = r.Author.DisplayName
			}
			t.Replies = append(t.Replies, rp)
		}
		out = append(out, t)
	}
	return out
}

// previewAnchors maps comment ids to the range the preview payload pins
// them to, when the document carries comment anchors.
func previewAnchors(w *gdocs.Document) map[string]*gdocs.Range {
	if w == nil || len(w.Comments) == 0 {
		return nil
	}
	byAnchor := map[string]*gdocs.Range{}
	gdocs.WalkTabs(w.Tabs, func(t *gdocs.Tab) bool {
		if t.DocumentTab == nil {
			return true
		}
		for id, a := range t.DocumentTab.CommentAnchors {
			// A comment may span several ranges; the guard and the marker
			// cover the union of those in the first range's segment.
			var span *gdocs.Range
			for _, r := range a.Ranges {
				if r == nil {
					continue
				}
				if span == nil {
					c := *r
					if c.TabID == "" && t.TabProperties != nil {
						c.TabID = t.TabProperties.TabID
					}
					span = &c
					continue
				}
				if r.SegmentID == span.SegmentID && (r.TabID == "" || r.TabID == span.TabID) {
					span.StartIndex = min(span.StartIndex, r.StartIndex)
					span.EndIndex = max(span.EndIndex, r.EndIndex)
				}
			}
			if span != nil {
				byAnchor[id] = span
			}
		}
		return true
	})
	out := map[string]*gdocs.Range{}
	for _, c := range w.Comments {
		if r := byAnchor[c.AnchorID]; r != nil && c.AnchorID != "" {
			out[c.CommentID] = r
		}
	}
	return out
}

// locateComments finds where each thread sits in the document: by the
// preview's anchor ranges when present, else by its quoted text, which
// must match exactly once. Paragraph units are built on first use and
// shared across threads.
func locateComments(f *Fetched, threads []CommentThread) []CommentThread {
	d := f.Doc
	anchors := previewAnchors(f.Wire)
	type para struct {
		block *doc.Block
		units []unit
	}
	var paras []para
	built := false
	for i := range threads {
		t := &threads[i]
		if r := anchors[t.ID]; r != nil {
			if b := blockAt(d, r.TabID, r.SegmentID, r.StartIndex); b != nil {
				t.Handle, t.Start, t.End, t.Tab, t.Segment, t.Anchored = b.Handle, r.StartIndex, r.EndIndex, b.Segment.Tab.ID, b.Segment.ID, true
				continue
			}
		}
		q := []rune(doc.Normalize(t.Quote))
		if len(q) == 0 {
			continue
		}
		if !built {
			built = true
			for _, b := range d.AllBlocks() {
				if b.Paragraph != nil {
					paras = append(paras, para{b, units(b.Paragraph)})
				}
			}
		}
		var hit *doc.Block
		var span [2]int64
		hits := 0
		for _, p := range paras {
			for _, m := range matchUnits(p.units, q, false) {
				hits++
				hit, span = p.block, m
			}
		}
		if hits == 1 {
			t.Handle, t.Start, t.End = hit.Handle, span[0], span[1]
			t.Tab, t.Segment = hit.Segment.Tab.ID, hit.Segment.ID
		}
	}
	return threads
}

// blockAt finds the innermost paragraph block covering an index of a
// segment, or the top-level block when no paragraph does.
func blockAt(d *doc.Document, tabID, segID string, index int64) *doc.Block {
	seg := segmentAt(d, tabID, segID)
	if seg == nil {
		return nil
	}
	var best *doc.Block
	for _, b := range seg.AllBlocks() {
		if b.Start <= index && index < b.End && (best == nil || b.Paragraph != nil) {
			best = b
		}
	}
	return best
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
		if t.Handle == "" || t.Tab != tab.ID || t.Segment != seg.ID || t.Resolved || t.Deleted {
			continue
		}
		if t.End > start && t.Start < end {
			add(plan.Anchor{Kind: "comment", ID: t.ID, Start: t.Start, End: t.End, Text: t.Quote})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}

// anchorsWithin filters an anchor list to those overlapping [start, end).
func anchorsWithin(all []plan.Anchor, start, end int64) []plan.Anchor {
	var out []plan.Anchor
	for _, a := range all {
		if a.End > start && a.Start < end {
			out = append(out, a)
		}
	}
	return out
}
