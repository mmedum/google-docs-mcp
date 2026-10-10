package service

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/mmedum/google-docs-mcp/v2/internal/doc"
	"github.com/mmedum/google-docs-mcp/v2/internal/markdown"
	"github.com/mmedum/google-docs-mcp/v2/internal/plan"
)

// Tables and images in content are placed after its text (spike E). The
// first batch writes the text with an empty paragraph, a slot, where each
// one goes. Later batches fill the slots one at a time, each against a
// fresh read, highest slot first, so a placement moves only slots already
// filled. Nothing else may share the first batch, or its ops would move
// the slots before they are filled.

// maxEmbeds bounds the tables and images of one call. An image is one
// more batch and a table up to three: insert, fill, tidy.
const maxEmbeds = 10

// checkEmbeds refuses content holding tables or images that the rounds
// cannot place, before anything is written. The planner refuses the
// places a slot cannot go: a header, footer or footnote, and inside a
// paragraph or a table cell. The rules hold in comment mode too, where
// nothing is placed, so a call that posts as comments also runs direct.
func checkEmbeds(ops []EditOp, resolved []plan.Op) error {
	for _, p := range resolved {
		if p.Fragment == nil {
			continue
		}
		switch n := p.Fragment.Embeds(); {
		case n == 0:
			continue
		case len(ops) > 1:
			return Errorf("invalid", "op %d: content with a table or image must be the only op in its call, because later batches "+
				"place them where this one leaves room; send the other ops as a call of their own", p.Seq)
		case ops[0].Kind != plan.OpInsert && ops[0].Kind != plan.OpAppend && ops[0].Kind != plan.OpReplace:
			return Errorf("unsupported", "op %d: a table or image in content goes in an insert, append or replace; "+
				"edit_table and insert_object reach the rest", p.Seq)
		case n > maxEmbeds:
			return Errorf("invalid", "op %d: content holds %d tables and images; at most %d go in one call, so split the content", p.Seq, n, maxEmbeds)
		}
	}
	return nil
}

// describeEmbeds words the rounds for a dry run.
func describeEmbeds(ops []*plan.Op) []string {
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		out = append(out, fmt.Sprintf("op %d: later batches place %s in the empty paragraphs this one leaves", op.Seq, embedCount(op.Slots)))
	}
	return out
}

// embedCount names how many tables and images slots hold: "1 table and
// 2 images".
func embedCount(slots []plan.Slot) string {
	tables, images := 0, 0
	for _, sl := range slots {
		if sl.Block.Kind == markdown.KindTable {
			tables++
		} else {
			images++
		}
	}
	var parts []string
	if tables > 0 {
		parts = append(parts, plural(tables, "table"))
	}
	if images > 0 {
		parts = append(parts, plural(images, "image"))
	}
	return strings.Join(parts, " and ")
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// placeEmbeds fills each op's slots, one round per slot, highest first.
// A slot that cannot be filled is reported; the others still go in,
// since each is checked in the read before its round. It returns the
// last read it holds, so the caller need not take another.
func (s *Service) placeEmbeds(ctx context.Context, req EditRequest, ops []*plan.Op, result *EditResult, f *Fetched) *Fetched {
	for _, op := range ops {
		var placed []plan.Slot
		for i, slot := range slices.Backward(op.Slots) {
			if f == nil {
				var err error
				if f, err = s.reread(ctx, req.Document); err != nil {
					result.Warnings = append(result.Warnings, fmt.Sprintf("op %d: the text was written, but re-reading the document "+
						"to place %s failed: %s; their empty paragraphs are still there", op.Seq, embedCount(op.Slots[:i+1]), messageOf(err)))
					break
				}
			}
			if f.Doc.RevisionID != result.written {
				// The slots were found by index, and a write that is not
				// this call's may have moved them onto other paragraphs.
				result.Warnings = append(result.Warnings, fmt.Sprintf("op %d: the text was written, but somebody else changed the "+
					"document before %s could be placed, so they were not", op.Seq, embedCount(op.Slots[:i+1])))
				break
			}
			after, err := s.placeEmbed(ctx, req, op, slot, result, f)
			switch {
			case err == nil:
				placed = append(placed, slot)
			case classOf(err) == "conflict":
				// Somebody else changed the document: where the slot is
				// now, and whether it is still empty, is not known.
				result.Warnings = append(result.Warnings, fmt.Sprintf("op %d: the text was written, but the %s on line %d of the content "+
					"was not placed: %s", op.Seq, slot.Block.Kind, slot.Block.Line, roundMessage(op.Seq, err)))
			default:
				result.Warnings = append(result.Warnings, fmt.Sprintf("op %d: the text was written, but the %s on line %d of the content "+
					"was not placed: %s; its empty paragraph is still there", op.Seq, slot.Block.Kind, slot.Block.Line, roundMessage(op.Seq, err)))
			}
			f = after
		}
		if len(placed) > 0 {
			annotate(result, op.Seq, ", then placed "+embedCount(placed))
		}
	}
	return f
}

// placeEmbed fills one slot: an image goes into its empty paragraph, a
// table in front of it, after which the empty paragraphs it leaves are
// deleted. It returns the read after the round, or nil when it has none.
func (s *Service) placeEmbed(ctx context.Context, req EditRequest, op *plan.Op, slot plan.Slot, result *EditResult, f *Fetched) (*Fetched, error) {
	seg := segmentAt(f.Doc, op.Seg.TabID, op.Seg.ID)
	b := slotBlock(seg, slot.Index)
	if b == nil {
		return f, Errorf("conflict", "its empty paragraph is no longer where the text left it, so the document changed in between")
	}
	at := &Target{Handle: b.Handle, Tab: op.Seg.TabID}
	next := EditRequest{Document: req.Document, Mode: req.Mode, round: true, own: owned(req, result)}
	if slot.Block.Kind == markdown.KindImage {
		next.Ops = []EditOp{{Seq: op.Seq, Kind: plan.OpInsertObject, Location: &Location{At: "start", Of: at},
			Object: &plan.ObjectParams{Kind: "image", URL: slot.Block.Image.URL}}}
		return s.applyRound(ctx, next, result, f)
	}
	data, rows, cols := slot.Block.Table.Grid()
	// Before a paragraph, a table goes in at the newline of the one ahead
	// of it, and starts where the slot did (resolveInsertTable).
	next.Ops = []EditOp{{Seq: op.Seq, Kind: plan.OpInsertTable, Location: &Location{At: "before", Of: at},
		Table: &TableOp{Rows: rows, Cols: cols, Data: data}, ContentFormat: "text"}}
	after, err := s.applyRound(ctx, next, result, f)
	if err != nil {
		return nil, err
	}
	if after == nil {
		err = Errorf("unavailable", "re-reading the document failed")
	} else {
		after, err = s.dropSlot(ctx, req, op, slot, result, after)
	}
	if err != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("op %d: the table on line %d of the content went in, but the empty "+
			"paragraphs after it were not removed: %s", op.Seq, slot.Block.Line, roundMessage(op.Seq, err)))
	}
	return after, nil
}

// dropSlot deletes the empty paragraphs a table placed at a slot leaves
// after it (spike E). A table put in at the newline of the paragraph
// before starts at the slot and leaves that paragraph's old newline as
// well as the slot; a table with no paragraph before starts one later,
// behind an empty paragraph Docs needs there, and leaves only the slot.
// The slot stays where the body ends or a table follows, since Docs needs
// a paragraph in both places.
func (s *Service) dropSlot(ctx context.Context, req EditRequest, op *plan.Op, slot plan.Slot, result *EditResult, f *Fetched) (*Fetched, error) {
	if f.Doc.RevisionID != result.written {
		return f, Errorf("conflict", "somebody else changed the document in between")
	}
	seg := segmentAt(f.Doc, op.Seg.TabID, op.Seg.ID)
	if seg == nil {
		return f, Errorf("not_found", "its tab is gone")
	}
	i := slices.IndexFunc(seg.Blocks, func(b *doc.Block) bool {
		return b.Table != nil && (b.Start == slot.Index || b.Start == slot.Index+1)
	})
	if i < 0 {
		return f, Errorf("not_found", "the table could not be found again")
	}
	n := 1
	if seg.Blocks[i].Start == slot.Index {
		n = 2
	}
	blanks := seg.Blocks[i+1 : min(i+1+n, len(seg.Blocks))]
	if len(blanks) < n || slices.ContainsFunc(blanks, func(b *doc.Block) bool { return !emptyParagraph(b) }) {
		return f, Errorf("conflict", "what follows the table is not the empty paragraphs it leaves")
	}
	if next := i + 1 + n; next == len(seg.Blocks) || seg.Blocks[next].Paragraph == nil {
		blanks = blanks[:n-1]
	}
	if len(blanks) == 0 {
		return f, nil
	}
	t := &Target{Handle: blanks[0].Handle, Tab: op.Seg.TabID}
	if len(blanks) > 1 {
		t = &Target{From: blanks[0].Handle, To: blanks[len(blanks)-1].Handle, Tab: op.Seg.TabID}
	}
	return s.applyRound(ctx, EditRequest{Document: req.Document, Mode: req.Mode, round: true, own: owned(req, result),
		Ops: []EditOp{{Seq: op.Seq, Kind: plan.OpDelete, Target: t}}}, result, f)
}

// roundMessage is a round's error without the op number the warning
// around it already gives.
func roundMessage(seq int, err error) string {
	return strings.TrimPrefix(messageOf(err), fmt.Sprintf("op %d: ", seq))
}

// slotBlock is the empty top-level paragraph starting at index, or nil.
func slotBlock(seg *doc.Segment, index int64) *doc.Block {
	if seg == nil {
		return nil
	}
	for _, b := range seg.Blocks {
		if b.Start == index && emptyParagraph(b) {
			return b
		}
	}
	return nil
}

// emptyParagraph reports whether a block is a paragraph of nothing but
// its newline, as a slot is.
func emptyParagraph(b *doc.Block) bool {
	return b.Paragraph != nil && b.End-b.Start == 1
}
