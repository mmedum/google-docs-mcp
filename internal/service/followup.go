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

// Follow-ups are the second batch of an edit: content for what the
// first batch created. A new header, footer or footnote is named by the
// reply; a table inserted with a data grid is found again by the handle
// its position predicts; the content then lands through an ordinary
// edit against the document as it is after the first batch, so it gets
// the same resolution, guard and numbering as anything else.

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
			ops = append(ops, pending{EditOp{Kind: plan.OpSetCells, Table: &TableOp{Table: handle, Data: data}}, fu})
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
		ops = append(ops, pending{EditOp{Kind: plan.OpAppend, Fragment: fu.Fragment, Location: &Location{At: "end", Of: &Target{Tab: fu.Seg.TabID, Segment: id[0]}}}, fu})
	}
	return ops
}

// runFollowups writes the second batch against a fresh read, at most
// fifty ops at a time, and folds its outcome into the result.
func (s *Service) runFollowups(ctx context.Context, pre *Fetched, req EditRequest, ro *resolvedOps, result *EditResult) {
	ops := followupOps(pre.Doc, ro, result)
	for len(ops) > 0 {
		f, err := s.FetchFresh(ctx, req.Document)
		if err != nil {
			result.Warnings = append(result.Warnings, "the edit was applied but re-reading the document to write the follow-up content failed: "+err.Error())
			return
		}
		s.Remember(f)
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
		res, err := s.editFetched(ctx, f, EditRequest{Document: req.Document, Mode: req.Mode, Ops: edits})
		if err != nil {
			result.Warnings = append(result.Warnings, "the edit was applied but writing the follow-up content failed: "+messageOf(err))
			continue
		}
		result.RevisionID = res.RevisionID
		result.SuggestionIDs = append(result.SuggestionIDs, res.SuggestionIDs...)
		result.Warnings = append(result.Warnings, res.Warnings...)
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
