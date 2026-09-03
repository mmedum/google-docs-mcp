package plan

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/mmedum/google-docs-mcp/internal/doc"
	"github.com/mmedum/google-docs-mcp/internal/markdown"
)

// Mode says how a write lands.
type Mode string

// Modes.
const (
	ModeDirect  Mode = "direct"
	ModeSuggest Mode = "suggest"
	ModeComment Mode = "comment"
)

// OpKind is an operation type.
type OpKind string

// Content operations (edit_document).
const (
	OpInsert       OpKind = "insert"
	OpAppend       OpKind = "append"
	OpReplace      OpKind = "replace"
	OpDelete       OpKind = "delete"
	OpReplaceAll   OpKind = "replace_all"
	OpPageBreak    OpKind = "insert_break"
	OpCreateHeader OpKind = "create_header"
	OpCreateFooter OpKind = "create_footer"
	OpFootnote     OpKind = "insert_footnote"
)

// Formatting operations (format_document).
const (
	OpTextStyle       OpKind = "text_style"
	OpParagraphStyle  OpKind = "paragraph_style"
	OpBullets         OpKind = "bullets"
	OpClearFormatting OpKind = "clear_formatting"
)

// Segment describes the index space an op works in. Start is the first
// index content can occupy (after the leading section break), End is the
// segment's end index (one past its final newline).
type Segment struct {
	ID    string
	TabID string
	Start int64
	End   int64
}

// Anchor is content inside a range that a deletion would destroy.
type Anchor struct {
	Kind  string // comment, suggestion, image, footnote
	ID    string
	Start int64
	End   int64
	Text  string
}

// Params are the caller-supplied arguments an op carries unchanged
// from the tool layer to the planner.
type Params struct {
	Find      string
	Replace   string
	MatchCase bool
	Text      TextStyleSpec
	Para      ParagraphStyleSpec
	Bullets   string // bullet, numbered, checkbox, none
}

// Op is one resolved operation. The service fills in ranges, text and
// anchors; the planner only does arithmetic. Seq is the caller's op
// number; several ops may share it when the service expands one caller
// op (set_cells), and their summaries are then aggregated.
type Op struct {
	Seq         int
	Kind        OpKind
	Seg         Segment
	Description string
	Params

	// Target is the affected range for replace, delete and formatting.
	Target *Rng
	// TargetIsBlock says Target covers whole blocks including the trailing newline.
	TargetIsBlock bool
	// TargetText is the current text of Target (inline view, no trailing newline).
	TargetText string

	// Insert is the insertion point for insert, append, breaks and footnotes.
	Insert *Loc
	// AtEnd says the insertion point is the segment's final newline (append form).
	AtEnd bool
	// Inline says the insertion point is inside a paragraph: the first
	// fragment paragraph merges into it and no boundary newline is added.
	Inline bool
	// NearBullet says the paragraph at the insertion point is a list item.
	NearBullet bool

	Fragment *markdown.Fragment

	// CommentAnchor is where a comment-mode proposal attaches. Defaults to Target.
	CommentAnchor *Rng
	Anchors       []Anchor

	// TableAt is the start of the table a table op works on.
	TableAt *Loc
	Table   TableParams
	Object  ObjectParams
	// SegmentRef names the header or footer a delete_header/delete_footer removes.
	SegmentRef string
}

// Options control planning.
type Options struct {
	Mode  Mode
	Force bool
}

// Proposal is a comment-mode change description.
type Proposal struct {
	Seq     int
	Content string
	Range   Rng
	Quote   string
}

// Followup is content to insert into a segment that a reply will name
// (a new header, footer or footnote).
type Followup struct {
	Seq      int
	Kind     OpKind
	Fragment *markdown.Fragment
	TabID    string
}

// NeedsFollowup reports whether the op creates a segment whose content
// is inserted in a second batch.
func (op *Op) NeedsFollowup() bool {
	return op.Kind == OpFootnote || op.Kind == OpCreateHeader || op.Kind == OpCreateFooter
}

// OpSummary describes what an op compiled to.
type OpSummary struct {
	Seq         int    `json:"op"`
	Kind        OpKind `json:"kind"`
	Description string `json:"target"`
	Requests    int    `json:"requests"`
	Minimal     bool   `json:"minimal_diff,omitempty"`
}

// Result is a plan.
type Result struct {
	Requests  []json.RawMessage
	Proposals []Proposal
	Followups []Followup
	Warnings  []string
	Summary   []OpSummary
}

// ErrBlocked is returned when the guard refuses a direct edit.
var ErrBlocked = errors.New("blocked")

// Plan compiles ops for the mode. Formatting requests come first (they
// shift nothing), then content ops in descending index order, then
// replace_all.
func Plan(ops []Op, o Options) (*Result, error) {
	if o.Mode == "" {
		o.Mode = ModeDirect
	}
	res := &Result{}
	for i := range ops {
		if err := validate(&ops[i]); err != nil {
			return nil, err
		}
	}
	summaries := map[int]*OpSummary{}
	add := func(op *Op, reqs []json.RawMessage, minimal bool) {
		res.Requests = append(res.Requests, reqs...)
		if sm := summaries[op.Seq]; sm != nil {
			sm.Requests += len(reqs)
			sm.Minimal = sm.Minimal && minimal
			return
		}
		summaries[op.Seq] = &OpSummary{Seq: op.Seq, Kind: op.Kind, Description: op.Description, Requests: len(reqs), Minimal: minimal}
	}
	finish := func() *Result {
		for i := range ops {
			if s := summaries[ops[i].Seq]; s != nil {
				res.Summary = append(res.Summary, *s)
				delete(summaries, ops[i].Seq)
			}
		}
		return res
	}
	if o.Mode == ModeComment {
		for i := range ops {
			p, err := proposal(&ops[i])
			if err != nil {
				return nil, err
			}
			res.Proposals = append(res.Proposals, p)
			add(&ops[i], nil, false)
		}
		return finish(), nil
	}
	if err := guard(ops, o, res); err != nil {
		return res, err
	}
	if err := checkOverlaps(ops); err != nil {
		return nil, err
	}
	var formats, content, global []*Op
	for i := range ops {
		op := &ops[i]
		switch op.Kind {
		case OpTextStyle, OpParagraphStyle, OpBullets, OpClearFormatting:
			formats = append(formats, op)
		case OpReplaceAll:
			global = append(global, op)
		default:
			content = append(content, op)
		}
	}
	sort.SliceStable(content, func(i, j int) bool {
		a, b := keyIndex(content[i]), keyIndex(content[j])
		if a != b {
			return a > b
		}
		// At one index a deletion must run before an insertion there,
		// or the inserted text would be swallowed by the delete.
		ra, rb := content[i].Target != nil, content[j].Target != nil
		if ra != rb {
			return ra
		}
		return content[i].Seq > content[j].Seq
	})
	for _, op := range formats {
		add(op, formatRequests(op), false)
	}
	for _, op := range content {
		reqs, minimal, err := contentRequests(op)
		if err != nil {
			return nil, err
		}
		add(op, reqs, minimal)
		if op.NeedsFollowup() {
			res.Followups = append(res.Followups, Followup{Seq: op.Seq, Kind: op.Kind, Fragment: op.Fragment, TabID: op.Seg.TabID})
		}
	}
	for _, op := range global {
		add(op, []json.RawMessage{ReplaceAllText(op.Find, op.Replace, op.MatchCase, op.Seg.TabID)}, false)
	}
	return finish(), nil
}

func validate(op *Op) error {
	needTarget := func() error {
		if op.Target == nil {
			return fmt.Errorf("op %d (%s): no target", op.Seq, op.Kind)
		}
		return nil
	}
	needInsert := func() error {
		if op.Insert == nil {
			return fmt.Errorf("op %d (%s): no insertion point", op.Seq, op.Kind)
		}
		return nil
	}
	needFragment := func() error {
		if op.Fragment == nil || len(op.Fragment.Blocks) == 0 {
			return fmt.Errorf("op %d (%s): content is empty", op.Seq, op.Kind)
		}
		return nil
	}
	switch op.Kind {
	case OpInsert, OpAppend:
		return errors.Join(needInsert(), needFragment())
	case OpReplace:
		return errors.Join(needTarget(), needFragment())
	case OpDelete, OpClearFormatting:
		return needTarget()
	case OpTextStyle:
		if op.Text.IsZero() {
			return fmt.Errorf("op %d: text_style changes nothing; set bold, italic, font, color, link or similar", op.Seq)
		}
		if err := op.Text.Validate(); err != nil {
			return fmt.Errorf("op %d: %w", op.Seq, err)
		}
		return needTarget()
	case OpParagraphStyle:
		if op.Para.IsZero() {
			return fmt.Errorf("op %d: paragraph_style changes nothing; set named_style, alignment, spacing or indent", op.Seq)
		}
		if err := op.Para.Validate(); err != nil {
			return fmt.Errorf("op %d: %w", op.Seq, err)
		}
		return needTarget()
	case OpBullets:
		switch op.Bullets {
		case "bullet", "numbered", "checkbox", "none":
		default:
			return fmt.Errorf("op %d: bullets must be bullet, numbered, checkbox or none", op.Seq)
		}
		return needTarget()
	case OpReplaceAll:
		if op.Find == "" {
			return fmt.Errorf("op %d: replace_all needs find text", op.Seq)
		}
		return nil
	case OpPageBreak:
		return needInsert()
	case OpFootnote:
		return errors.Join(needInsert(), needFragment())
	case OpCreateHeader, OpCreateFooter:
		return needFragment()
	case OpDeleteHeader, OpDeleteFooter:
		if op.SegmentRef == "" {
			return fmt.Errorf("op %d (%s): no segment", op.Seq, op.Kind)
		}
		return nil
	case OpInsertObject:
		return validateObjectOp(op)
	}
	if IsTableOp(op.Kind) {
		return validateTableOp(op)
	}
	return fmt.Errorf("op %d: unknown kind %q", op.Seq, op.Kind)
}

func keyIndex(op *Op) int64 {
	if op.Target != nil {
		return op.Target.Start
	}
	if op.Insert != nil {
		return op.Insert.Index
	}
	if op.TableAt != nil {
		return op.TableAt.Index
	}
	return -1
}

// guard blocks direct edits that would destroy anchored content.
func guard(ops []Op, o Options, res *Result) error {
	for i := range ops {
		op := &ops[i]
		if !Deletes(op.Kind) || len(op.Anchors) == 0 {
			continue
		}
		if o.Mode == ModeSuggest {
			res.Warnings = append(res.Warnings, fmt.Sprintf("op %d: the range holds %s; as a suggestion nothing is removed until accepted", op.Seq, describeAnchors(op.Anchors)))
			continue
		}
		if o.Force {
			res.Warnings = append(res.Warnings, fmt.Sprintf("op %d: forced; %s will be lost", op.Seq, describeAnchors(op.Anchors)))
			continue
		}
		return fmt.Errorf("%w: op %d (%s of %s) would destroy %s; use mode suggest or comment, narrow the target, or pass force: true", ErrBlocked, op.Seq, op.Kind, op.Description, describeAnchors(op.Anchors))
	}
	return nil
}

// Deletes reports whether the kind removes existing content, which is
// what the overwrite guard protects.
func Deletes(k OpKind) bool {
	switch k {
	case OpReplace, OpDelete, OpDeleteRows, OpDeleteColumns, OpDeleteHeader, OpDeleteFooter:
		return true
	}
	return false
}

func describeAnchors(as []Anchor) string {
	counts := map[string][]string{}
	var kinds []string
	for _, a := range as {
		if _, ok := counts[a.Kind]; !ok {
			kinds = append(kinds, a.Kind)
		}
		counts[a.Kind] = append(counts[a.Kind], a.ID)
	}
	parts := make([]string, 0, len(kinds))
	for _, k := range kinds {
		ids := counts[k]
		label := k
		if len(ids) != 1 {
			label += "s"
		}
		parts = append(parts, fmt.Sprintf("%d %s (%s)", len(ids), label, strings.Join(ids, ", ")))
	}
	return strings.Join(parts, " and ")
}

// checkOverlaps rejects content ops whose ranges touch each other.
func checkOverlaps(ops []Op) error {
	type span struct {
		op   *Op
		s, e int64
	}
	var spans []span
	structural := map[Loc]*Op{}
	for i := range ops {
		op := &ops[i]
		switch op.Kind {
		case OpTextStyle, OpParagraphStyle, OpBullets, OpClearFormatting, OpReplaceAll, OpCreateHeader, OpCreateFooter, OpDeleteHeader, OpDeleteFooter:
			continue
		}
		switch {
		case op.Target != nil:
			spans = append(spans, span{op, op.Target.Start, op.Target.End})
		case op.Insert != nil:
			spans = append(spans, span{op, op.Insert.Index, op.Insert.Index})
		case op.TableAt != nil:
			// The table's first index stands for the whole grid: a delete of
			// the table block overlaps it, cell edits inside it do not.
			spans = append(spans, span{op, op.TableAt.Index, op.TableAt.Index + 1})
			if isStructural(op.Kind) {
				if prev := structural[*op.TableAt]; prev != nil {
					return fmt.Errorf("ops %d and %d both change the grid of %s; rows and columns renumber after the first, so split them into separate calls", prev.Seq, op.Seq, op.Description)
				}
				structural[*op.TableAt] = op
			}
		}
	}
	for i := 0; i < len(spans); i++ {
		for j := i + 1; j < len(spans); j++ {
			a, b := spans[i], spans[j]
			if a.op.Seg.ID != b.op.Seg.ID || a.op.Seg.TabID != b.op.Seg.TabID {
				continue
			}
			if a.op.TableAt != nil && b.op.TableAt != nil {
				continue // two ops on one table: the structural rule above decides
			}
			// Half-open overlap; it also covers an insertion point that
			// falls strictly inside another op's range.
			if a.s < b.e && b.s < a.e {
				return fmt.Errorf("ops %d and %d overlap (%s and %s); split the call or widen one target", a.op.Seq, b.op.Seq, a.op.Description, b.op.Description)
			}
		}
	}
	return nil
}

func formatRequests(op *Op) []json.RawMessage {
	r := *op.Target
	switch op.Kind {
	case OpTextStyle:
		return []json.RawMessage{UpdateTextStyle(r, op.Text)}
	case OpParagraphStyle:
		return []json.RawMessage{UpdateParagraphStyle(r, op.Para)}
	case OpClearFormatting:
		return []json.RawMessage{ClearTextStyle(r)}
	case OpBullets:
		switch op.Bullets {
		case "none":
			return []json.RawMessage{DeleteBullets(r)}
		case "numbered":
			return []json.RawMessage{CreateBullets(r, NumberedPreset)}
		case "checkbox":
			return []json.RawMessage{CreateBullets(r, CheckboxPreset)}
		default:
			return []json.RawMessage{CreateBullets(r, BulletPreset)}
		}
	}
	return nil
}

func contentRequests(op *Op) ([]json.RawMessage, bool, error) {
	switch op.Kind {
	case OpInsert, OpAppend:
		c, err := CompileFragment(op.Fragment, *op.Insert, FragmentOptions{Prefix: op.AtEnd, Suffix: !op.AtEnd && !op.Inline, NearBullet: op.NearBullet})
		if err != nil {
			return nil, false, fmt.Errorf("op %d: %w", op.Seq, err)
		}
		return c.Requests, false, nil
	case OpPageBreak:
		return []json.RawMessage{InsertPageBreak(*op.Insert)}, false, nil
	case OpDelete:
		return []json.RawMessage{DeleteRange(deleteRange(op))}, false, nil
	case OpReplace:
		return replaceRequests(op)
	case OpFootnote:
		return []json.RawMessage{CreateFootnote(*op.Insert)}, false, nil
	case OpCreateHeader:
		return []json.RawMessage{CreateHeader(op.Seg.TabID)}, false, nil
	case OpCreateFooter:
		return []json.RawMessage{CreateFooter(op.Seg.TabID)}, false, nil
	case OpDeleteHeader:
		return []json.RawMessage{DeleteHeader(op.SegmentRef, op.Seg.TabID)}, false, nil
	case OpDeleteFooter:
		return []json.RawMessage{DeleteFooter(op.SegmentRef, op.Seg.TabID)}, false, nil
	case OpInsertObject:
		reqs, err := objectRequests(op)
		return reqs, false, err
	}
	if IsTableOp(op.Kind) {
		reqs, err := tableRequests(op)
		return reqs, false, err
	}
	return nil, false, fmt.Errorf("op %d: unknown kind %q", op.Seq, op.Kind)
}

// deleteRange adjusts a block deletion so the segment keeps its final
// newline: a last block is removed together with the newline before it.
func deleteRange(op *Op) Rng {
	r := *op.Target
	if !op.TargetIsBlock {
		return r
	}
	if r.End >= op.Seg.End {
		r.End = op.Seg.End - 1
		if r.Start > op.Seg.Start {
			r.Start--
		}
	}
	return r
}

func replaceRequests(op *Op) ([]json.RawMessage, bool, error) {
	seg := op.Seg
	// Minimal diff when both sides are one paragraph of plain text.
	if newText, ok := op.Fragment.SingleParagraph(); ok && !strings.Contains(op.TargetText, "\n") {
		edits := MinimalEdits(op.TargetText, newText, op.Target.Start)
		if len(edits) == 0 {
			return nil, true, nil
		}
		return EditRequests(edits, seg), true, nil
	}
	// Whole-range replacement: delete, then insert at the same spot.
	var reqs []json.RawMessage
	del := deleteRange(op)
	reqs = append(reqs, DeleteRange(del))
	opts := FragmentOptions{NearBullet: op.NearBullet}
	at := Loc{Index: del.Start, SegmentID: seg.ID, TabID: seg.TabID}
	switch {
	case !op.TargetIsBlock:
		// Inside a paragraph: the first fragment paragraph merges with it.
	case op.Target.End >= seg.End && op.Target.Start > seg.Start:
		// Last block: we deleted the newline before it; re-add it in front.
		opts.Prefix = true
	case op.Target.End >= seg.End:
		// Only block in the segment: text sits before the final newline.
	default:
		opts.Suffix = true
	}
	c, err := CompileFragment(op.Fragment, at, opts)
	if err != nil {
		return nil, false, fmt.Errorf("op %d: %w", op.Seq, err)
	}
	return append(reqs, c.Requests...), false, nil
}

func proposal(op *Op) (Proposal, error) {
	anchor := op.CommentAnchor
	if anchor == nil {
		anchor = op.Target
	}
	if anchor == nil {
		return Proposal{}, fmt.Errorf("op %d (%s): nothing to anchor a comment to", op.Seq, op.Kind)
	}
	p := Proposal{Seq: op.Seq, Range: *anchor, Quote: op.TargetText}
	quote := func(s string) string { return "“" + doc.Clip(s, 80) + "”" }
	switch op.Kind {
	case OpReplace:
		p.Content = "Proposed change to " + quote(op.TargetText) + ":\n\n" + op.Fragment.PlainText()
	case OpDelete:
		p.Content = "Proposed deletion of " + quote(op.TargetText) + "."
	case OpInsert:
		p.Content = "Proposed insertion at " + op.Description + ":\n\n" + op.Fragment.PlainText()
	case OpAppend:
		p.Content = "Proposed addition at the end of " + op.Description + ":\n\n" + op.Fragment.PlainText()
	case OpReplaceAll:
		p.Content = fmt.Sprintf("Proposed: replace every %s with %s in this tab.", quote(op.Find), quote(op.Replace))
	case OpPageBreak:
		p.Content = "Proposed page break at " + op.Description + "."
	case OpFootnote:
		p.Content = "Proposed footnote at " + op.Description + ":\n\n" + op.Fragment.PlainText()
	case OpCreateHeader:
		p.Content = "Proposed header:\n\n" + op.Fragment.PlainText()
	case OpCreateFooter:
		p.Content = "Proposed footer:\n\n" + op.Fragment.PlainText()
	case OpTextStyle:
		p.Content = "Proposed formatting for " + quote(op.TargetText) + ": " + describeText(op.Text) + "."
	case OpParagraphStyle:
		p.Content = "Proposed paragraph style for " + quote(op.TargetText) + ": " + describePara(op.Para) + "."
	case OpBullets:
		p.Content = "Proposed: " + map[string]string{"bullet": "make this a bulleted list", "numbered": "make this a numbered list", "checkbox": "make this a checklist", "none": "remove the list formatting"}[op.Bullets] + "."
	case OpClearFormatting:
		p.Content = "Proposed: clear the formatting of " + quote(op.TargetText) + "."
	case OpDeleteHeader, OpDeleteFooter:
		p.Content = "Proposed: remove the " + strings.TrimPrefix(string(op.Kind), "delete_") + " of this tab."
	case OpInsertObject:
		p.Content = objectProposal(op)
	default:
		if IsTableOp(op.Kind) {
			p.Content = tableProposal(op)
		}
	}
	return p, nil
}

func describeText(s TextStyleSpec) string {
	var parts []string
	flag := func(name string, v *bool) {
		if v == nil {
			return
		}
		if *v {
			parts = append(parts, name)
		} else {
			parts = append(parts, "not "+name)
		}
	}
	flag("bold", s.Bold)
	flag("italic", s.Italic)
	flag("underlined", s.Underline)
	flag("struck through", s.Strikethrough)
	flag("small caps", s.SmallCaps)
	if s.Font != "" {
		parts = append(parts, "font "+s.Font)
	}
	if s.SizePt > 0 {
		parts = append(parts, fmt.Sprintf("%gpt", s.SizePt))
	}
	if s.Foreground != "" {
		parts = append(parts, "colour "+s.Foreground)
	}
	if s.Background != "" {
		parts = append(parts, "background "+s.Background)
	}
	if s.Link != "" {
		parts = append(parts, "link "+s.Link)
	}
	if s.Baseline != "" {
		parts = append(parts, strings.ToLower(s.Baseline))
	}
	return strings.Join(parts, ", ")
}

func describePara(s ParagraphStyleSpec) string {
	var parts []string
	if s.NamedStyle != "" {
		parts = append(parts, strings.ToLower(strings.ReplaceAll(s.NamedStyle, "_", " ")))
	}
	if s.Alignment != "" {
		parts = append(parts, "aligned "+strings.ToLower(s.Alignment))
	}
	if s.LineSpacing > 0 {
		parts = append(parts, fmt.Sprintf("line spacing %g%%", s.LineSpacing))
	}
	if s.SpaceAbovePt != nil {
		parts = append(parts, fmt.Sprintf("%gpt above", *s.SpaceAbovePt))
	}
	if s.SpaceBelowPt != nil {
		parts = append(parts, fmt.Sprintf("%gpt below", *s.SpaceBelowPt))
	}
	if s.IndentStartPt != nil {
		parts = append(parts, fmt.Sprintf("indent %gpt", *s.IndentStartPt))
	}
	if s.IndentFirstLine != nil {
		parts = append(parts, fmt.Sprintf("first line indent %gpt", *s.IndentFirstLine))
	}
	if s.KeepWithNext != nil {
		parts = append(parts, "keep with next")
	}
	return strings.Join(parts, ", ")
}

// SuggestModeUnsupported lists request types the API refuses in SUGGEST
// mode; later phases that build those requests must check it.
var SuggestModeUnsupported = map[string]bool{
	"addDocumentTab": true, "createNamedRange": true, "deleteFooter": true, "deleteHeader": true,
	"deleteNamedRange": true, "deleteTab": true, "updateDocumentTabProperties": true, "updateTableColumnProperties": true,
}
