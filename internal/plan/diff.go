package plan

import (
	"encoding/json"
	"sort"

	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/mmedum/google-docs-mcp/internal/doc"
)

// Edit is one hunk of a minimal diff, in UTF-16 units of the segment.
type Edit struct {
	Pos    int64
	Delete int64 // units to delete at Pos, 0 for pure insert
	Insert string
}

// MinimalEdits computes the smallest set of edits that turn oldText into
// newText, positioned relative to start. Unchanged spans keep their
// formatting and anything anchored to them.
func MinimalEdits(oldText, newText string, start int64) []Edit {
	if oldText == newText {
		return nil
	}
	dmp := diffmatchpatch.New()
	diffs := dmp.DiffMain(oldText, newText, false)
	diffs = dmp.DiffCleanupSemantic(diffs)
	byPos := map[int64]*Edit{}
	var order []int64
	pos := start
	get := func(p int64) *Edit {
		e, ok := byPos[p]
		if !ok {
			e = &Edit{Pos: p}
			byPos[p] = e
			order = append(order, p)
		}
		return e
	}
	lastDelete := int64(-1)
	for _, d := range diffs {
		n := doc.UTF16Len(d.Text)
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			pos += n
			lastDelete = -1
		case diffmatchpatch.DiffDelete:
			lastDelete = pos
			get(pos).Delete += n
			pos += n
		case diffmatchpatch.DiffInsert:
			// An insertion right after a deletion replaces it: same slot.
			at := pos
			if lastDelete >= 0 {
				at = lastDelete
			}
			get(at).Insert += d.Text
		}
	}
	sort.Slice(order, func(i, j int) bool { return order[i] > order[j] })
	out := make([]Edit, 0, len(order))
	for _, p := range order {
		out = append(out, *byPos[p])
	}
	return out
}

// EditRequests turns edits into requests in descending position order,
// deleting before inserting at each position.
func EditRequests(edits []Edit, seg Segment) []json.RawMessage {
	var reqs []json.RawMessage
	for _, e := range edits {
		if e.Delete > 0 {
			reqs = append(reqs, DeleteRange(Rng{Start: e.Pos, End: e.Pos + e.Delete, SegmentID: seg.ID, TabID: seg.TabID}))
		}
		if e.Insert != "" {
			reqs = append(reqs, InsertText(e.Insert, Loc{Index: e.Pos, SegmentID: seg.ID, TabID: seg.TabID}))
		}
	}
	return reqs
}
