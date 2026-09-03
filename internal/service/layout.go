package service

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mmedum/google-docs-mcp/internal/doc"
	"github.com/mmedum/google-docs-mcp/internal/plan"
)

// Ops shaped ShapeTab name no range: they work on a whole tab (its page
// setup, its named styles), or on something the tab holds by id (an
// object, a named range). This resolves the tab and, for an op that
// names an object, finds it so the planner knows how to remove it.
func (s *Service) resolveTabOp(f *Fetched, op EditOp, p *plan.Op, out *resolvedOps) error {
	tab, err := tabOf(f.Doc, targetTab(op.Target))
	if err != nil {
		return err
	}
	p.Seg = SegmentBounds(tab, tab.Body)
	switch op.Kind {
	case plan.OpPageSetup:
		p.Description = fmt.Sprintf("page setup of tab %d", tab.Number)
	case plan.OpNamedStyle:
		p.Description = fmt.Sprintf("%s in tab %d", strings.ToLower(p.NamedStyle.Style), tab.Number)
	case plan.OpReplaceImage, plan.OpDeleteObject:
		return s.resolveObjectRef(f, tab, op, p, out)
	case plan.OpDeleteNamedRange, plan.OpReplaceNamedRange:
		return resolveNamedRangeOp(f, tab, op, p, out)
	}
	return nil
}

// resolveObjectRef finds the object an op names. An inline object sits
// in a run, so it is removed by deleting that run's range and the guard
// can see what goes with it; a positioned one floats free of the text
// and is removed by id.
func (s *Service) resolveObjectRef(f *Fetched, tab *doc.Tab, op EditOp, p *plan.Op, out *resolvedOps) error {
	id := strings.TrimSpace(p.Object.ID)
	if id == "" {
		return Errorf("invalid", "name the object to change (its id from a read, like kix.img1)")
	}
	if info := tab.PositionedObjects[id]; info != nil {
		if op.Kind == plan.OpReplaceImage && info.Kind != "image" {
			return Errorf("invalid", "%s is a %s, and only an image can be replaced", id, info.Kind)
		}
		p.Object.Positioned = true
		p.Description = fmt.Sprintf("floating %s %s", info.Kind, id)
		return nil
	}
	info := tab.InlineObjects[id]
	if info == nil {
		return Errorf("not_found", "no object %s in tab %d; object ids come from a read, which shows them as ![image: title](%s, …)", id, tab.Number, "kix.…")
	}
	if op.Kind == plan.OpReplaceImage && info.Kind != "image" {
		return Errorf("invalid", "%s is a %s, and only an image can be replaced", id, info.Kind)
	}
	p.Description = fmt.Sprintf("%s %s", info.Kind, id)
	if op.Kind != plan.OpDeleteObject {
		return nil
	}
	seg, r, ok := objectRun(tab, id)
	if !ok {
		return Errorf("not_found", "object %s is listed in tab %d but does not appear in its text", id, tab.Number)
	}
	p.Seg = SegmentBounds(tab, seg)
	p.Target = &r
	// The guard refuses a deletion that would destroy an image, but this
	// op names that image: only what else sits on it — a comment anchor,
	// a suggestion — still stands in the way.
	for _, a := range f.anchorsIn(seg, r.Start, r.End, out.threads) {
		if a.Kind != "image" || a.ID != id {
			p.Anchors = append(p.Anchors, a)
		}
	}
	out.note(tab.ID, seg.ID, r.Start)
	return nil
}

// objectRun finds the one-unit range an inline object occupies.
func objectRun(tab *doc.Tab, id string) (*doc.Segment, plan.Rng, bool) {
	for _, seg := range tab.Segments() {
		for _, b := range doc.Flatten(seg.Blocks) {
			if b.Paragraph == nil {
				continue
			}
			for _, run := range b.Paragraph.Runs {
				if run.Kind == doc.RunInlineObject && run.ObjectID == id {
					return seg, plan.Rng{Start: run.Start, End: run.End, SegmentID: seg.ID, TabID: tab.ID}, true
				}
			}
		}
	}
	return nil, plan.Rng{}, false
}

// resolveNamedRangeOp checks that the range an op names exists, so the
// caller hears about a typo instead of a silent no-op: the API accepts a
// delete or a replace of a name nothing carries. A replace overwrites
// every range of that name at once, so the guard is shown what all of
// them hold; forgetting a name destroys nothing.
func resolveNamedRangeOp(f *Fetched, tab *doc.Tab, op EditOp, p *plan.Op, out *resolvedOps) error {
	var covered int
	for _, nr := range tab.NamedRanges {
		if nr.Name != p.NamedRange.Name && nr.ID != p.NamedRange.ID {
			continue
		}
		covered++
		seg := segmentByID(tab, nr.Segment)
		if seg == nil {
			continue
		}
		if op.Kind != plan.OpReplaceNamedRange {
			// A delete forgets the name and destroys nothing, so it needs
			// no anchors for the guard — only somewhere to hang a
			// comment-mode proposal.
			if p.CommentAnchor == nil {
				p.CommentAnchor = &plan.Rng{Start: nr.Start, End: nr.End, SegmentID: seg.ID, TabID: tab.ID}
			}
			continue
		}
		p.Anchors = append(p.Anchors, f.anchorsIn(seg, nr.Start, nr.End, out.threads)...)
		out.note(tab.ID, seg.ID, nr.Start)
		if p.CommentAnchor == nil {
			p.CommentAnchor = &plan.Rng{Start: nr.Start, End: nr.End, SegmentID: seg.ID, TabID: tab.ID}
		}
	}
	if covered == 0 {
		return Errorf("not_found", "no named range %s in tab %d", namedRangeLabel(p.NamedRange), tab.Number)
	}
	p.Description = fmt.Sprintf("named range %s (%d range(s)) in tab %d", namedRangeLabel(p.NamedRange), covered, tab.Number)
	return nil
}

func namedRangeLabel(p plan.NamedRangeParams) string {
	if p.ID != "" {
		return p.ID
	}
	return strconv.Quote(p.Name)
}
