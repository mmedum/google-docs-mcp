package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mmedum/google-docs-mcp/internal/config"
	"github.com/mmedum/google-docs-mcp/internal/doc"
	"github.com/mmedum/google-docs-mcp/internal/gapi"
	"github.com/mmedum/google-docs-mcp/internal/markdown"
	"github.com/mmedum/google-docs-mcp/internal/plan"
)

// Location says where an insertion goes.
type Location struct {
	At string // start, end, before, after
	Of *Target
}

// EditOp is one operation of edit_document or format_document after
// tool-level validation, before resolution.
type EditOp struct {
	// Seq is the caller's op number, stamped by numbered() on the way in.
	// It travels with the op so that one held back for a later batch is
	// still named as the caller sent it. A path that builds EditOps
	// itself must set it, or every op reports as op 0.
	Seq           int
	Kind          plan.OpKind
	Target        *Target
	Location      *Location
	Content       string
	ContentFormat string // markdown (default) or text
	// Fragment is content already parsed; set by follow-ups instead of Content.
	Fragment *markdown.Fragment
	plan.Params
	Table  *TableOp
	Object *plan.ObjectParams
	// Layout carries the page, section and named-style specs, and the
	// section type a section_break starts.
	Layout *LayoutOp
	// NamedRange names the range a named-range op works on.
	NamedRange *plan.NamedRangeParams
}

// LayoutOp is one layout_document operation before resolution.
type LayoutOp struct {
	Page        plan.PageSpec
	Section     plan.SectionSpec
	NamedStyle  plan.NamedStyleSpec
	SectionType string
}

// EditRequest is a write call.
type EditRequest struct {
	Document       string
	Ops            []EditOp
	Mode           string
	DryRun         bool
	Force          bool
	ExpectRevision string
}

// EditResult reports what happened.
type EditResult struct {
	RevisionID    string           `json:"revision_id"`
	Mode          string           `json:"mode"`
	DryRun        bool             `json:"dry_run"`
	Applied       int              `json:"ops_applied"`
	Changes       []plan.OpSummary `json:"changes"`
	SuggestionIDs []string         `json:"suggestion_ids,omitempty"`
	CommentIDs    []string         `json:"comment_ids,omitempty"`
	Warnings      []string         `json:"warnings,omitempty"`
	// Preview is the edited region rendered with handles, in the JSON as
	// well as the text because a client may show the model only one.
	Preview string `json:"preview,omitempty"`
	// Requests is the dry run's request list, for tests and debugging;
	// the person and the model see RequestKinds, never the indices.
	Requests     json.RawMessage `json:"-"`
	RequestKinds []string        `json:"request_kinds,omitempty"`
	Proposals    []plan.Proposal `json:"-"`
	// Followups says what a second batch does after the first has landed
	// (a dry run lists them; an applied edit has run them).
	Followups []string `json:"followups,omitempty"`
	// Text is the result as the model reads it.
	Text string `json:"-"`
}

func (r *EditResult) text() string {
	var b strings.Builder
	if r.DryRun {
		fmt.Fprintf(&b, "dry run in %s mode at revision %s: %d op(s) planned, nothing sent\n", r.Mode, r.RevisionID, len(r.Changes))
	} else {
		fmt.Fprintf(&b, "applied %d op(s) in %s mode; revision %s\n", r.Applied, r.Mode, r.RevisionID)
	}
	for _, c := range r.Changes {
		fmt.Fprintf(&b, "- op %d %s: %s", c.Seq, c.Kind, c.Description)
		if c.Minimal {
			b.WriteString(" (minimal diff)")
		}
		b.WriteString("\n")
	}
	if len(r.SuggestionIDs) > 0 {
		fmt.Fprintf(&b, "suggestion ids: %s\n", strings.Join(r.SuggestionIDs, ", "))
	}
	if len(r.CommentIDs) > 0 {
		fmt.Fprintf(&b, "comment ids: %s\n", strings.Join(r.CommentIDs, ", "))
	}
	writeWarnings(&b, r.Warnings)
	if r.DryRun && len(r.Proposals) > 0 {
		b.WriteString("proposed comments:\n")
		for _, p := range r.Proposals {
			fmt.Fprintf(&b, "- op %d: %s\n", p.Seq, doc.OneLine(p.Content))
		}
	}
	if r.DryRun && len(r.RequestKinds) > 0 {
		fmt.Fprintf(&b, "requests: %s\n", strings.Join(r.RequestKinds, ", "))
	}
	for _, fu := range r.Followups {
		fmt.Fprintf(&b, "then %s\n", fu)
	}
	if r.Preview != "" {
		label := "region after the edit"
		if r.DryRun {
			label = "region as it is now"
		}
		fmt.Fprintf(&b, "%s:\n%s\n", label, r.Preview)
	}
	return strings.TrimRight(b.String(), "\n")
}

type resolvedOps struct {
	ops     []plan.Op
	threads []CommentThread
	// planned and env are set once the ops are planned and applied, for
	// the follow-ups.
	planned *plan.Result
	env     replyEnvelope
	// preview locates the edited region for the before/after rendering.
	previewTab   string
	previewSeg   string
	previewIndex int64
	hasPreview   bool
}

func (r *resolvedOps) note(tabID, segID string, index int64) {
	if !r.hasPreview || index < r.previewIndex {
		r.previewTab, r.previewSeg, r.previewIndex, r.hasPreview = tabID, segID, index, true
	}
}

// Edit applies (or previews) a batch of operations.
func (s *Service) Edit(ctx context.Context, req EditRequest) (*EditResult, error) {
	if err := validateEditRequest(req); err != nil {
		return nil, err
	}
	req.Ops = numbered(req.Ops)
	f, err := s.FetchFresh(ctx, req.Document)
	if err != nil {
		return nil, err
	}
	res, _, err := s.editFetched(ctx, f, req)
	if res != nil {
		res.Text = res.text()
	}
	return res, err
}

// numbered stamps each op with its place in the call.
func numbered(ops []EditOp) []EditOp {
	out := make([]EditOp, len(ops))
	for i, op := range ops {
		op.Seq = i
		out[i] = op
	}
	return out
}

func validateEditRequest(req EditRequest) error {
	if len(req.Ops) == 0 {
		return Errorf("invalid", "ops is empty")
	}
	if len(req.Ops) > 50 {
		return Errorf("invalid", "at most 50 ops per call")
	}
	return nil
}

// editFetched plans against a document the caller already holds. A
// revision conflict is re-planned once against a fresh read. It returns
// the read taken after the last batch, which a caller chaining batches
// passes to the next one instead of taking its own.
func (s *Service) editFetched(ctx context.Context, f *Fetched, req EditRequest) (*EditResult, *Fetched, error) {
	mode, err := s.mode(req.Mode)
	if err != nil {
		return nil, nil, err
	}
	if err := validateEditRequest(req); err != nil {
		return nil, nil, err
	}
	if err := checkRevision(req.ExpectRevision, f.Doc.RevisionID, "editing"); err != nil {
		return nil, nil, err
	}
	// A grid change renumbers the table's rows and columns, so the second
	// and later changes of one table wait for a batch of their own. In
	// comment mode nothing is applied and every op must reach the
	// proposals, so the ops stay together.
	batch, later := req, [][]EditOp(nil)
	if mode != plan.ModeComment {
		batch.Ops, later = chainStructural(req.Ops)
		if err := precheckLater(later); err != nil {
			return nil, nil, err
		}
	}
	result, ro, err := s.planAndApply(ctx, f, batch, mode)
	if errors.Is(err, gapi.ErrConflict) {
		s.log.InfoContext(ctx, "revision conflict; re-planning once")
		if f, err = s.FetchFresh(ctx, req.Document); err != nil {
			return nil, nil, err
		}
		if err := checkRevision(req.ExpectRevision, f.Doc.RevisionID, "editing"); err != nil {
			return nil, nil, err
		}
		result, ro, err = s.planAndApply(ctx, f, batch, mode)
		if errors.Is(err, gapi.ErrConflict) {
			return nil, nil, Errorf("conflict", "the document changed twice while planning this edit; re-read and try again")
		}
	}
	if err != nil {
		return result, nil, err
	}
	if req.DryRun {
		result.Followups = append(result.Followups, describeRounds(later)...)
		return result, nil, nil
	}
	result.Applied = len(batch.Ops)
	var after *Fetched
	if mode != plan.ModeComment && len(ro.planned.Followups) > 0 {
		after = s.runFollowups(ctx, f, req, ro, result)
	}
	if len(later) > 0 {
		after = s.runRounds(ctx, req, later, result, after)
	}
	if after == nil {
		if after, err = s.FetchFresh(ctx, req.Document); err != nil {
			result.Warnings = append(result.Warnings, "applied, but re-reading the document failed: "+err.Error())
			return result, nil, nil //nolint:nilerr // the edit was applied; report it with a warning
		}
	}
	// The caller sees the post-edit handles in the preview, so they are
	// what later writes must be checked against.
	s.Remember(after)
	if blocksShifted(f.Doc, after.Doc) {
		result.Warnings = append(result.Warnings, "the number of blocks changed, so handles after the edited region now name different blocks; use the handles in the preview or re-read before targeting by handle")
	}
	result.RevisionID = after.Doc.RevisionID
	result.Preview = regionPreview(after.Doc, ro)
	return result, after, nil
}

// blocksShifted reports whether a segment gained or lost top-level blocks
// somewhere before its last block, which renumbers the handles after
// that point. A pure append at the end shifts nothing.
func blocksShifted(before, after *doc.Document) bool {
	segs := map[string]*doc.Segment{}
	for _, t := range after.Tabs {
		for _, seg := range t.Segments() {
			segs[t.ID+"/"+seg.ID] = seg
		}
	}
	for _, t := range before.Tabs {
		for _, old := range t.Segments() {
			cur := segs[t.ID+"/"+old.ID]
			if cur == nil || len(cur.Blocks) == len(old.Blocks) {
				continue
			}
			n := min(len(old.Blocks), len(cur.Blocks))
			first := n
			for i := range n {
				if doc.Normalize(old.Blocks[i].Text(doc.ViewInline)) != doc.Normalize(cur.Blocks[i].Text(doc.ViewInline)) {
					first = i
					break
				}
			}
			if first < len(old.Blocks)-1 {
				return true
			}
		}
	}
	return false
}

// planAndApply resolves, plans, and (unless dry-running) applies once.
func (s *Service) planAndApply(ctx context.Context, f *Fetched, req EditRequest, mode plan.Mode) (*EditResult, *resolvedOps, error) {
	ro, err := s.resolveOps(ctx, f, req.Ops, mode)
	if err != nil {
		return nil, nil, err
	}
	planned, err := plan.Plan(ro.ops, plan.Options{Mode: mode, Force: req.Force})
	if err != nil {
		var unsupported *markdown.UnsupportedError
		switch {
		case errors.Is(err, plan.ErrBlocked):
			return nil, nil, &Error{Class: "blocked", Message: strings.TrimPrefix(err.Error(), "blocked: "), Err: err}
		case errors.As(err, &unsupported):
			return nil, nil, &Error{Class: "unsupported", Message: err.Error(), Err: err}
		}
		return nil, nil, Errorf("invalid", "%v", err)
	}
	ro.planned = planned
	result := &EditResult{RevisionID: f.Doc.RevisionID, Mode: string(mode), DryRun: req.DryRun, Changes: planned.Summary, Warnings: planned.Warnings, Proposals: planned.Proposals}
	if mode != plan.ModeComment {
		result.Followups = describeFollowups(planned.Followups)
	}
	if req.DryRun {
		if b, err := json.MarshalIndent(planned.Requests, "", "  "); err == nil && len(planned.Requests) > 0 {
			result.Requests = b
		}
		for _, r := range planned.Requests {
			result.RequestKinds = append(result.RequestKinds, plan.Kind(r))
		}
		result.Preview = regionPreview(f.Doc, ro)
		return result, ro, nil
	}
	env, err := s.apply(ctx, f, planned, mode, result)
	if err != nil {
		return nil, nil, err
	}
	ro.env = env
	return result, ro, nil
}

func (s *Service) mode(m string) (plan.Mode, error) {
	m = strings.ToLower(strings.TrimSpace(m))
	if m == "" {
		m = string(s.opts.DefaultWriteMode)
	}
	if m == "" {
		m = string(config.WriteDirect)
	}
	if err := s.requireWritable(); err != nil {
		return "", err
	}
	switch plan.Mode(m) {
	case plan.ModeDirect, plan.ModeComment:
		return plan.Mode(m), nil
	case plan.ModeSuggest:
		if !s.opts.Preview {
			return "", Errorf("unavailable", "suggestion mode needs Developer Preview enrolment (GDOCS_PREVIEW=true); use mode comment or direct")
		}
		return plan.ModeSuggest, nil
	}
	return "", Errorf("invalid", "mode %q; use suggest, direct or comment", m)
}

// resolveOps turns EditOps into planner ops against a fetched document.
func (s *Service) resolveOps(ctx context.Context, f *Fetched, ops []EditOp, mode plan.Mode) (*resolvedOps, error) {
	out := &resolvedOps{}
	for _, op := range ops {
		if plan.Deletes(op.Kind) {
			threads, err := s.comments(ctx, f)
			if err != nil {
				if mode == plan.ModeDirect {
					// The guard cannot see comment anchors; a direct edit
					// would then delete them silently.
					return nil, Errorf("unavailable", "could not check comment anchors before a direct edit: %s; retry, or use mode suggest or comment", messageOf(err))
				}
				s.log.WarnContext(ctx, "comment lookup failed; the guard cannot see comment anchors", "err", err)
			}
			out.threads = threads
			break
		}
	}
	for _, op := range ops {
		i := op.Seq
		if op.Kind == plan.OpSetCells {
			expanded, err := s.expandSetCells(f, i, op, out)
			if err != nil {
				return nil, Errorf(classOf(err), "op %d: %s", i, messageOf(err))
			}
			out.ops = append(out.ops, expanded...)
			continue
		}
		info, ok := plan.Info(op.Kind)
		if !ok {
			return nil, Errorf("invalid", "op %d: unknown kind %q", i, op.Kind)
		}
		p := plan.Op{Seq: i, Kind: op.Kind, Params: op.Params}
		if l := op.Layout; l != nil {
			p.Page, p.Section, p.NamedStyle, p.SectionType = l.Page, l.Section, l.NamedStyle, l.SectionType
		}
		if op.NamedRange != nil {
			p.NamedRange = *op.NamedRange
		}
		if op.Object != nil {
			p.Object = *op.Object
		}
		switch {
		case op.Fragment != nil:
			p.Fragment = op.Fragment
		case op.Content != "" || info.Content:
			frag, err := parseContent(op.Content, op.ContentFormat)
			if err != nil {
				return nil, Errorf("unsupported", "op %d: %v", i, err)
			}
			p.Fragment = frag
		}
		var err error
		switch {
		case op.Kind == plan.OpInsertTable:
			err = s.resolveInsertTable(f, op, &p, out)
		case op.Kind == plan.OpReplaceAll:
			err = s.resolveReplaceAll(f, op, &p, mode, out.threads)
		case info.Shape == plan.ShapeInsert:
			err = s.resolveInsertOp(f, op, &p, out)
		case info.Shape == plan.ShapeTarget:
			err = s.resolveTargetOp(f, op, &p, out)
		case info.Shape == plan.ShapeSegment:
			err = s.resolveDeleteSegment(f, op, &p, out)
		case info.Shape == plan.ShapeTable:
			err = s.resolveTableOp(f, op, &p, out)
		case info.Shape == plan.ShapeTab:
			err = s.resolveTabOp(f, op, &p, out)
		case info.Shape == plan.ShapeNone:
			err = resolveCreateSegment(f, op, &p)
		default:
			err = Errorf("invalid", "unknown kind %q", op.Kind)
		}
		if err != nil {
			return nil, Errorf(classOf(err), "op %d: %s", i, messageOf(err))
		}
		out.ops = append(out.ops, p)
	}
	return out, nil
}

// resolveDeleteSegment names the header or footer to remove and lists
// what it holds so the guard can refuse a direct deletion.
func (s *Service) resolveDeleteSegment(f *Fetched, op EditOp, p *plan.Op, out *resolvedOps) error {
	var t Target
	if op.Target != nil {
		t = *op.Target
	}
	kind := plan.Noun(op.Kind)
	if t.Segment == "" {
		t.Segment = kind
	}
	tab, seg, err := tabSegment(f.Doc, t.Tab, t.Segment)
	if err != nil {
		return err
	}
	if string(seg.Kind) != kind {
		return Errorf("invalid", "%s is a %s, not a %s", seg.Label(), seg.Kind, kind)
	}
	p.Seg = SegmentBounds(tab, seg)
	p.SegmentRef = seg.ID
	p.Description = fmt.Sprintf("%s of tab %d", seg.Label(), tab.Number)
	p.Anchors = f.anchorsIn(seg, 0, seg.End(), out.threads)
	p.CommentAnchor = firstParagraphRng(tab, tab.Body, tab.Body.Blocks)
	out.note(tab.ID, seg.ID, 0)
	return nil
}

// firstParagraphRng is the range of the first paragraph among blocks, the
// place a comment about a segment or a table attaches; nil when none.
func firstParagraphRng(tab *doc.Tab, seg *doc.Segment, blocks []*doc.Block) *plan.Rng {
	for _, b := range blocks {
		if b.Paragraph != nil {
			return blockRng(tab, seg, b)
		}
	}
	return nil
}

func (s *Service) resolveInsertOp(f *Fetched, op EditOp, p *plan.Op, out *resolvedOps) error {
	loc := Location{At: "end"}
	if op.Location != nil {
		loc = *op.Location
	} else if op.Kind != plan.OpAppend {
		return Errorf("invalid", "%s needs a location", op.Kind)
	}
	if loc.At == "" && loc.Of == nil {
		loc.At = "end"
	}
	ip, err := s.resolveLocation(f, loc)
	if err != nil {
		return err
	}
	ip.fill(p, out)
	if op.Kind == plan.OpInsertObject {
		if op.Object == nil {
			return Errorf("invalid", "insert_object needs an object")
		}
		p.Object = *op.Object
		if ip.segment.Kind == doc.SegmentFootnote && p.Object.Kind == "image" {
			return Errorf("unsupported", "images cannot be inserted in footnotes")
		}
	}
	return nil
}

func (s *Service) resolveTargetOp(f *Fetched, op EditOp, p *plan.Op, out *resolvedOps) error {
	if op.Target == nil {
		return Errorf("invalid", "%s needs a target", op.Kind)
	}
	r, err := s.ResolveTarget(f, *op.Target)
	if err != nil {
		return err
	}
	if r.Start == r.End {
		if op.Kind != plan.OpReplace || r.Block == nil {
			return Errorf("invalid", "%s is empty", r.Description)
		}
		// Replacing an empty section body is an insertion after its
		// heading: a new paragraph, not text glued onto the next heading.
		ip, err := blockRelative(&TargetRange{Tab: r.Tab, Segment: r.Segment, Blocks: []*doc.Block{r.Block}, Description: r.Description}, "after")
		if err != nil {
			return err
		}
		p.Kind = plan.OpInsert
		ip.fill(p, out)
		p.Description = r.Description
		return nil
	}
	p.Seg = SegmentBounds(r.Tab, r.Segment)
	rng := r.Rng()
	p.Target = &rng
	p.TargetIsBlock = r.IsBlock
	p.TargetText = r.Text
	p.TargetAligned = r.Aligned
	p.Description = r.Description
	p.NearBullet = hasBullet(r.Block)
	if op.Kind == plan.OpDelete || op.Kind == plan.OpReplace {
		p.Anchors = f.anchorsIn(r.Segment, r.Start, r.End, out.threads)
	}
	out.note(r.Tab.ID, r.Segment.ID, r.Start)
	return nil
}

func (s *Service) resolveReplaceAll(f *Fetched, op EditOp, p *plan.Op, mode plan.Mode, threads []CommentThread) error {
	tab, err := tabOf(f.Doc, targetTab(op.Target))
	if err != nil {
		return err
	}
	p.Seg = SegmentBounds(tab, tab.Body)
	p.Description = fmt.Sprintf("every %q in tab %d", op.Find, tab.Number)
	// replaceAllText touches every segment of the tab; the guard sees
	// what each match would destroy.
	needle := doc.Normalize(op.Find)
	seen := map[string]bool{}
	for _, seg := range tab.Segments() {
		for _, m := range f.findText(seg, needle, !op.MatchCase) {
			p.Anchors = appendUnique(seen, p.Anchors, f.anchorsIn(seg, m.start, m.end, threads)...)
		}
	}
	if mode != plan.ModeComment {
		return nil
	}
	r, err := s.ResolveTarget(f, Target{Text: op.Find, Occurrence: 1, Tab: tab.ID})
	if err != nil {
		return err
	}
	rng := r.Rng()
	p.CommentAnchor = &rng
	p.TargetText = r.Text
	return nil
}

func resolveCreateSegment(f *Fetched, op EditOp, p *plan.Op) error {
	tab, err := tabOf(f.Doc, targetTab(op.Target))
	if err != nil {
		return err
	}
	existing := tab.Headers
	if op.Kind == plan.OpCreateFooter {
		existing = tab.Footers
	}
	kind := plan.Noun(op.Kind)
	if len(existing) > 0 {
		return Errorf("invalid", "tab %d already has a %s; edit it with segment: %s", tab.Number, kind, existing[0].Label())
	}
	p.Seg = SegmentBounds(tab, tab.Body)
	p.Description = fmt.Sprintf("new %s of tab %d", kind, tab.Number)
	p.CommentAnchor = firstParagraphRng(tab, tab.Body, tab.Body.Blocks)
	return nil
}

func targetTab(t *Target) string {
	if t == nil {
		return ""
	}
	return t.Tab
}

func parseContent(content, format string) (*markdown.Fragment, error) {
	if strings.EqualFold(strings.TrimSpace(format), "text") {
		return markdown.Plain(content), nil
	}
	return markdown.Parse(content)
}

type insertPoint struct {
	tab         *doc.Tab
	segment     *doc.Segment
	index       int64
	atEnd       bool
	inline      bool
	fills       bool  // the paragraph is blank, so the content styles it
	clearTo     int64 // whitespace in that paragraph the content replaces
	nearBullet  bool
	description string
	anchor      *plan.Rng
}

func (ip *insertPoint) bounds() plan.Segment { return SegmentBounds(ip.tab, ip.segment) }

// atBlank points the insertion at a blank paragraph: the content merges
// into it, takes its own paragraph style, and replaces the whitespace
// that paragraph holds.
func (ip *insertPoint) atBlank(b *doc.Block) {
	ip.index, ip.inline, ip.fills, ip.clearTo = b.Start, true, true, b.End-1
}

// fill records the insertion point on a planner op.
func (ip *insertPoint) fill(p *plan.Op, out *resolvedOps) {
	p.Seg = ip.bounds()
	p.Insert = &plan.Loc{Index: ip.index, SegmentID: ip.segment.ID, TabID: ip.tab.ID}
	p.AtEnd, p.Inline, p.Fill, p.ClearTo, p.NearBullet = ip.atEnd, ip.inline, ip.fills, ip.clearTo, ip.nearBullet
	p.Description = ip.description
	p.CommentAnchor = ip.anchor
	out.note(ip.tab.ID, ip.segment.ID, ip.index)
}

// resolveLocation turns a Location into an insertion index.
func (s *Service) resolveLocation(f *Fetched, loc Location) (*insertPoint, error) {
	at := strings.ToLower(strings.TrimSpace(loc.At))
	if at == "" {
		at = "end"
		if loc.Of != nil {
			at = "after"
		}
	}
	var t Target
	if loc.Of != nil {
		t = *loc.Of
	}
	if loc.Of == nil || (at == "start" || at == "end") && t.selectorCount() == 0 {
		return s.segmentEdge(f, t, at)
	}
	r, err := s.ResolveTarget(f, t)
	if err != nil {
		return nil, err
	}
	if r.IsBlock {
		return blockRelative(r, at)
	}
	return textRelative(r, at)
}

// segmentEdge is the start or end of a whole segment.
func (s *Service) segmentEdge(f *Fetched, t Target, at string) (*insertPoint, error) {
	tab, seg, err := tabSegment(f.Doc, t.Tab, t.Segment)
	if err != nil {
		return nil, err
	}
	content := seg.ContentBlocks()
	if len(content) == 0 {
		return nil, Errorf("invalid", "%s has no content blocks", segmentName(tab, seg))
	}
	ip := &insertPoint{tab: tab, segment: seg}
	switch at {
	case "start":
		first := content[0]
		if first.Kind == doc.KindTable {
			return nil, Errorf("invalid", "%s starts with a table; insert after it or before the first paragraph", segmentName(tab, seg))
		}
		ip.index, ip.nearBullet, ip.description, ip.anchor = first.Start, hasBullet(first), "start of "+segmentName(tab, seg), blockRng(tab, seg, first)
		if first.IsBlankParagraph() {
			ip.atBlank(first)
		}
	case "end":
		last := content[len(content)-1]
		ip.description, ip.anchor = "end of "+segmentName(tab, seg), blockRng(tab, seg, last)
		ip.nearBullet = hasBullet(last)
		if last.IsBlankParagraph() {
			// Fill the blank paragraph instead of leaving it above the content.
			ip.atBlank(last)
		} else {
			ip.index, ip.atEnd = last.End-1, true
		}
	default:
		return nil, Errorf("invalid", "location %q needs a target (of) to be before or after", at)
	}
	return ip, nil
}

// blockRelative positions relative to whole blocks.
func blockRelative(r *TargetRange, at string) (*insertPoint, error) {
	tab, seg := r.Tab, r.Segment
	bounds := SegmentBounds(tab, seg)
	first, last := r.Blocks[0], r.Blocks[len(r.Blocks)-1]
	ip := &insertPoint{tab: tab, segment: seg}
	switch at {
	case "before":
		if first.Kind == doc.KindTable {
			prev := previousParagraph(seg, first)
			if prev == nil {
				return nil, Errorf("invalid", "cannot insert before %s: the table is the first block; insert after it instead", first.Handle)
			}
			ip.index, ip.atEnd, ip.nearBullet, ip.anchor = prev.End-1, true, hasBullet(prev), blockRng(tab, seg, prev)
		} else {
			ip.index, ip.nearBullet, ip.anchor = first.Start, hasBullet(first), blockRng(tab, seg, first)
		}
		ip.description = "before " + r.Description
	case "after", "end":
		next := nextBlock(seg, last)
		switch {
		case last.End >= bounds.End || (next != nil && next.Kind == doc.KindTable):
			// No paragraph boundary follows (end of segment, or a table
			// starts there), so borrow this block's own newline.
			if last.Kind == doc.KindTable {
				return nil, Errorf("invalid", "cannot insert after %s: a table with no paragraph after it; insert before the next paragraph instead", last.Handle)
			}
			ip.index, ip.atEnd, ip.nearBullet = last.End-1, true, hasBullet(last)
		default:
			ip.index, ip.nearBullet = last.End, hasBullet(next)
		}
		ip.anchor, ip.description = blockRng(tab, seg, last), "after "+r.Description
	case "start":
		if first.Kind != doc.KindParagraph {
			return nil, Errorf("invalid", "start of %s is not a paragraph", first.Handle)
		}
		ip.index, ip.inline, ip.nearBullet, ip.anchor, ip.description = first.Start, true, hasBullet(first), blockRng(tab, seg, first), "start of "+r.Description
		if first.IsBlankParagraph() {
			ip.atBlank(first)
		}
	default:
		return nil, Errorf("invalid", "location %q; use start, end, before or after", at)
	}
	return ip, nil
}

// textRelative positions inline next to a text match or cell content.
func textRelative(r *TargetRange, at string) (*insertPoint, error) {
	ip := &insertPoint{tab: r.Tab, segment: r.Segment, inline: true, nearBullet: hasBullet(r.Block)}
	switch at {
	case "before", "start":
		ip.index = r.Start
	case "after", "end":
		ip.index = r.End
	default:
		return nil, Errorf("invalid", "location %q; use start, end, before or after", at)
	}
	ip.description = at + " " + r.Description
	if r.Block != nil {
		ip.anchor = blockRng(r.Tab, r.Segment, r.Block)
	} else {
		rng := r.Rng()
		ip.anchor = &rng
	}
	return ip, nil
}

func hasBullet(b *doc.Block) bool {
	return b != nil && b.Paragraph != nil && b.Paragraph.Bullet != nil
}

func blockRng(tab *doc.Tab, seg *doc.Segment, b *doc.Block) *plan.Rng {
	return &plan.Rng{Start: b.Start, End: b.End, SegmentID: seg.ID, TabID: tab.ID}
}

func previousParagraph(seg *doc.Segment, b *doc.Block) *doc.Block {
	var prev *doc.Block
	for _, x := range seg.Blocks {
		if x == b {
			return prev
		}
		if x.Kind == doc.KindParagraph {
			prev = x
		}
	}
	return nil
}

func nextBlock(seg *doc.Segment, b *doc.Block) *doc.Block {
	for i, x := range seg.Blocks {
		if x == b && i+1 < len(seg.Blocks) {
			return seg.Blocks[i+1]
		}
	}
	return nil
}

func classOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Class
	}
	return "unexpected"
}

func messageOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Message
	}
	return err.Error()
}
