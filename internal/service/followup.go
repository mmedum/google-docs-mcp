package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/mmedum/google-docs-mcp/internal/doc"
	"github.com/mmedum/google-docs-mcp/internal/plan"
)

// Follow-ups are the later batches of an edit, each planned against the
// document as it is by then, so each gets the same resolution, guard and
// numbering as anything else. Two kinds of op need one: content for what
// the first batch created (a new header, footer or footnote is named by
// the reply; a table inserted with a data grid is found again by the
// handle its position predicts), and the second and later changes to one
// table's grid, whose rows and columns are numbered only once the change
// before them has landed.

// chainStructural splits a call into the batch to plan now and the ops
// that wait. Everything goes in the first batch until an op changes a
// table's grid; after that, every op addressed in that table's rows and
// columns waits for a batch of its own, one round per grid change, so
// each is numbered against the grid the one before it left. The key is
// the handle as written, which is what lets the split happen before the
// ops are resolved: handles are canonical, so two ops on one table name
// it identically and land in the planner on one table location.
func chainStructural(ops []EditOp) (batch []EditOp, later [][]EditOp) {
	if !anyStructural(ops) {
		return ops, nil
	}
	round := map[string]int{}
	batch = make([]EditOp, 0, len(ops))
	for _, op := range ops {
		n := 0
		if info, ok := plan.Info(op.Kind); ok && info.Shape == plan.ShapeTable && op.Table != nil {
			table := strings.TrimSpace(op.Table.Table)
			n = round[table]
			if info.Structural {
				round[table] = n + 1
			}
		}
		if n == 0 {
			batch = append(batch, op)
			continue
		}
		// A table reaches round n only after an op of round n-1 was put
		// away, so later grows by at most one here.
		if len(later) < n {
			later = append(later, nil)
		}
		later[n-1] = append(later[n-1], op)
	}
	return batch, later
}

func anyStructural(ops []EditOp) bool {
	for _, op := range ops {
		if op.Table != nil && plan.Structural(op.Kind) {
			return true
		}
	}
	return false
}

// precheckLater refuses the whole call, before any of it is written, for
// what is wrong in a held-back op whatever the ops before it do: a row
// or column numbered below one, or a cell that is not named like r2c3.
// What depends on the new grid — a row number past its end — can only be
// checked when the round runs, which is why a dry run leaves these ops
// unresolved too. The table itself needs no check here: an op is held
// back only behind another op on the same table, which the first batch
// resolves and reports.
func precheckLater(later [][]EditOp) error {
	for _, ops := range later {
		for _, op := range ops {
			for _, n := range append(append([]int{}, op.Table.RowList...), op.Table.ColList...) {
				if n < 1 {
					return Errorf("invalid", "op %d: rows and columns are numbered from 1", op.Seq)
				}
			}
			for _, ref := range cellRefs(op.Table) {
				if _, _, err := parseCell(ref, op.Table.Table); err != nil {
					return Errorf("invalid", "op %d: %s", op.Seq, messageOf(err))
				}
			}
		}
	}
	return nil
}

// cellRefs lists every cell an op names.
func cellRefs(t *TableOp) []string {
	refs := make([]string, 0, len(t.Cells)+3)
	for _, c := range t.Cells {
		refs = append(refs, c.Cell)
	}
	for _, ref := range []string{t.StartCell, t.FromCell, t.ToCell} {
		if ref != "" {
			refs = append(refs, ref)
		}
	}
	return refs
}

// describeRounds words the held-back ops for a dry run, which cannot
// resolve them: the grid their numbers mean does not exist yet.
func describeRounds(later [][]EditOp) []string {
	var out []string
	for _, ops := range later {
		for _, op := range ops {
			out = append(out, fmt.Sprintf("op %d: a later batch runs %s on %s, against the grid the change before it leaves",
				op.Seq, op.Kind, op.Table.Table))
		}
	}
	return out
}

// runRounds applies the held-back ops, one batch per round, each against
// the document as the round before it left it. A round that fails stops
// the rest: they were numbered for a grid that never came about. It
// returns the last read it holds, so the caller need not take another.
func (s *Service) runRounds(ctx context.Context, req EditRequest, later [][]EditOp, result *EditResult, f *Fetched) *Fetched {
	for i, ops := range later {
		if f == nil {
			var err error
			if f, err = s.FetchFresh(ctx, req.Document); err != nil {
				result.Warnings = append(result.Warnings, fmt.Sprintf("the edit was applied but re-reading the document for %s failed: %s", opNumbers(later[i:]), err.Error()))
				return nil
			}
			s.Remember(f)
		}
		res, after, err := s.editFetched(ctx, f, EditRequest{Document: req.Document, Mode: req.Mode, Ops: ops, Force: req.Force})
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("the earlier ops were applied but %s failed: %s", opNumbers(later[i:]), messageOf(err)))
			return nil
		}
		// These ops are the caller's own, so they report as themselves;
		// a follow-up instead annotates the op it completes.
		result.Applied += res.Applied
		result.Changes = append(result.Changes, res.Changes...)
		fold(result, res)
		f = after
	}
	return f
}

// fold merges what a later batch produced into the call's result, apart
// from the ops it counts and summarises, which each caller folds its own
// way.
func fold(dst, src *EditResult) {
	dst.RevisionID = src.RevisionID
	dst.SuggestionIDs = append(dst.SuggestionIDs, src.SuggestionIDs...)
	dst.CommentIDs = append(dst.CommentIDs, src.CommentIDs...)
	dst.Warnings = append(dst.Warnings, src.Warnings...)
}

// opNumbers names the ops still waiting, for a warning.
func opNumbers(later [][]EditOp) string {
	var ns []string
	for _, ops := range later {
		for _, op := range ops {
			ns = append(ns, strconv.Itoa(op.Seq))
		}
	}
	if len(ns) == 1 {
		return "op " + ns[0]
	}
	return "ops " + strings.Join(ns, ", ")
}

// describeFollowups words the plan's follow-ups for a dry run.
func describeFollowups(fus []*plan.Op) []string {
	var out []string
	for _, fu := range fus {
		if fu.Kind == plan.OpInsertTable {
			cells := 0
			for _, row := range fu.Table.Data {
				cells += len(row)
			}
			out = append(out, fmt.Sprintf("op %d: a second batch fills the new %d×%d table with %d cell(s)", fu.Seq, fu.Table.Rows, fu.Table.Cols, cells))
			continue
		}
		out = append(out, fmt.Sprintf("op %d: a second batch writes the %s content", fu.Seq, plan.Noun(fu.Kind)))
	}
	return out
}

// pending is one follow-up edit: the op that will write the content and
// the first-batch op it completes.
type pending struct {
	op EditOp
	fu *plan.Op
}

// followupOps turns the plan's follow-ups into the ops of the second
// edit. Table handles are predicted from the pre-edit document and the
// batch; segment ids come from the replies.
func followupOps(pre *doc.Document, ro *resolvedOps, result *EditResult) []pending {
	ids := newSegmentIDs(ro.env)
	var ops []pending
	for _, fu := range ro.planned.Followups {
		if fu.Kind == plan.OpInsertTable {
			handle := newTableHandle(pre, ro.ops, fu)
			data := fitGrid(fu.Table.Data, fu.Table.Rows, fu.Table.Cols, fu.Seq, result)
			ops = append(ops, pending{EditOp{Seq: fu.Seq, Kind: plan.OpSetCells, Table: &TableOp{Table: handle, Data: data}}, fu})
			continue
		}
		id := ids[fu.Kind]
		if len(id) == 0 {
			result.Warnings = append(result.Warnings, fmt.Sprintf("op %d: %s was created but Google returned no segment id; its content was not inserted", fu.Seq, fu.Kind))
			continue
		}
		ids[fu.Kind] = id[1:]
		// The content appends into the new segment, whose sole paragraph
		// is blank; the insertion point fills it (see insertPoint.atBlank).
		ops = append(ops, pending{EditOp{Seq: fu.Seq, Kind: plan.OpAppend, Fragment: fu.Fragment, Location: &Location{At: "end", Of: &Target{Tab: fu.Seg.TabID, Segment: id[0]}}}, fu})
	}
	return ops
}

// runFollowups writes the follow-up content against a fresh read, at
// most fifty ops at a time, and folds its outcome into the result. It
// returns the last read it holds, so the caller need not take another.
func (s *Service) runFollowups(ctx context.Context, pre *Fetched, req EditRequest, ro *resolvedOps, result *EditResult) *Fetched {
	ops := followupOps(pre.Doc, ro, result)
	var f *Fetched
	for len(ops) > 0 {
		if f == nil {
			var err error
			if f, err = s.FetchFresh(ctx, req.Document); err != nil {
				result.Warnings = append(result.Warnings, "the edit was applied but re-reading the document to write the follow-up content failed: "+err.Error())
				return nil
			}
			s.Remember(f)
		}
		batch := ops[:min(len(ops), 50)]
		ops = ops[len(batch):]
		var ready []pending
		var edits []EditOp
		for _, p := range batch {
			if p.fu.Kind == plan.OpInsertTable {
				handle := p.op.Table.Table
				if b, ok := f.Doc.FindHandle(handle); !ok || b.Table == nil || b.Table.Rows != p.fu.Table.Rows || b.Table.Cols != p.fu.Table.Cols || !emptyTable(b) {
					result.Warnings = append(result.Warnings, fmt.Sprintf("op %d: the table was created but could not be found again to fill it; use set_cells", p.fu.Seq))
					continue
				}
			}
			ready = append(ready, p)
			edits = append(edits, p.op)
		}
		if len(ready) == 0 {
			continue
		}

		res, after, err := s.editFetched(ctx, f, EditRequest{Document: req.Document, Mode: req.Mode, Ops: edits})
		if err != nil {
			result.Warnings = append(result.Warnings, "the edit was applied but writing the follow-up content failed: "+messageOf(err))
			f = nil
			continue
		}
		// A follow-up completes an op the first batch already reported,
		// so it annotates that summary instead of adding one of its own.
		fold(result, res)
		f = after
		for _, p := range ready {
			note := ", content written"
			if p.fu.Kind == plan.OpInsertTable {
				note = ", filled as " + p.op.Table.Table
			}
			for j := range result.Changes {
				if result.Changes[j].Seq == p.fu.Seq {
					result.Changes[j].Description += note
				}
			}
		}
	}
	return f
}

// newSegmentIDs collects created header, footer and footnote ids from
// replies, in request order.
func newSegmentIDs(env replyEnvelope) map[plan.OpKind][]string {
	out := map[plan.OpKind][]string{}
	for _, r := range plan.SegmentReplies() {
		kind := r.Kind
		env.each(r.Reply, func(raw json.RawMessage) {
			var v map[string]string
			if json.Unmarshal(raw, &v) == nil {
				for _, id := range v { // headerId, footerId or footnoteId
					if id != "" {
						out[kind] = append(out[kind], id)
					}
				}
			}
		})
	}
	return out
}

// newTableHandle predicts the handle of the table an insert_table op
// creates: the tables before its index in the pre-edit segment, minus
// those a delete op in the batch removes, plus the batch's other table
// insertions at lower indices, then one more.
func newTableHandle(pre *doc.Document, ops []plan.Op, fu *plan.Op) string {
	seg := segmentAt(pre, fu.Seg.TabID, fu.Seg.ID)
	if seg == nil {
		return ""
	}
	n := 0
	for _, b := range seg.Blocks {
		if b.Table == nil || b.Start >= fu.Insert.Index {
			continue
		}
		deleted := false
		for _, op := range ops {
			if op.Kind == plan.OpDelete && op.Target != nil && op.Seg == fu.Seg && op.Target.Start <= b.Start && op.Target.End >= b.End {
				deleted = true
			}
		}
		if !deleted {
			n++
		}
	}
	for _, op := range ops {
		if op.Kind == plan.OpInsertTable && op.Seg == fu.Seg && op.Insert.Index < fu.Insert.Index {
			n++
		}
	}
	return seg.Prefix + "tbl" + strconv.Itoa(n+1)
}

// emptyTable reports whether every cell of a table holds only its newline.
func emptyTable(b *doc.Block) bool {
	for _, row := range b.Table.Cells {
		for _, c := range row {
			if strings.TrimSpace(c.Text(doc.ViewInline)) != "" {
				return false
			}
		}
	}
	return true
}

// fitGrid trims data to the table's size, warning about what was dropped,
// without touching the caller's rows.
func fitGrid(data [][]string, rows, cols, seq int, result *EditResult) [][]string {
	if len(data) > rows {
		data = data[:rows]
		result.Warnings = append(result.Warnings, fmt.Sprintf("op %d: data has more rows than the table; extra rows were dropped", seq))
	}
	data = append([][]string(nil), data...)
	for ri := range data {
		if len(data[ri]) > cols {
			data[ri] = data[ri][:cols]
			result.Warnings = append(result.Warnings, fmt.Sprintf("op %d: row %d has more cells than the table; extras were dropped", seq, ri+1))
		}
	}
	return data
}
