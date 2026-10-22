package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mmedum/google-docs-mcp/v2/internal/doc"
	"github.com/mmedum/google-docs-mcp/v2/internal/gapi"
	"github.com/mmedum/google-docs-mcp/v2/internal/markdown"
	"github.com/mmedum/google-docs-mcp/v2/internal/ooxml"
	"github.com/mmedum/google-docs-mcp/v2/internal/plan"
)

// Word files (.docx). The Docs API reads one, its tracked changes as
// suggestions and its comments as comments, and refuses every write to it
// (spike G). So a write is made the way the file is stored: the ops
// resolve against the Docs model, as on a Google Doc, handles, headings,
// exact text and the overwrite guard alike, and each paragraph they reach
// is found in the file by its place and its text. The change is made in
// the file's XML (internal/ooxml), a suggestion as a tracked change and a
// comment as a Word comment, and uploaded through Drive as one revision,
// which Docs reads again within seconds (spike H).

// wordElsewhere is where the rest of the tools' work goes.
const wordElsewhere = "formatting, tables, images, layout and tabs need the file saved as a Google Doc (File > Save as Google Docs)"

// docxReads is how many times a write looks for Docs to have read the
// revision it uploaded, which took four seconds in spike H: once after
// the wait a Markdown write takes, then a second apart.
const docxReads = 8

func (s *Service) isWord(ctx context.Context, ref string) bool {
	mime, _ := s.kind(ctx, ref)
	return mime == gapi.WordMimeType
}

// wordTime is now as Word dates a tracked change or a comment.
func (s *Service) wordTime() string { return s.now().UTC().Format(time.RFC3339) }

// wordMap is a Word file beside its Docs model: the paragraphs of the
// Docs body, tables' included, in the order the file holds them.
type wordMap struct {
	f      *Fetched
	file   *ooxml.File
	blocks []*doc.Block
	tables []*doc.Block
}

func newWordMap(f *Fetched, file *ooxml.File) (*wordMap, error) {
	w := &wordMap{f: f, file: file}
	if len(f.Doc.Tabs) > 0 {
		for _, b := range f.Doc.Tabs[0].Body.AllBlocks() {
			switch b.Kind {
			case doc.KindParagraph:
				if spanned(b) {
					continue
				}
				w.blocks = append(w.blocks, b)
			case doc.KindTable:
				w.tables = append(w.tables, b)
			}
		}
	}
	if len(w.blocks) != len(file.Paras) {
		return nil, errStale(fmt.Sprintf("Docs reads %d paragraphs and the file holds %d", len(w.blocks), len(file.Paras)))
	}
	return w, nil
}

// spanned says paragraph b is in a cell another cell's column span covers.
// Docs keeps such a cell, empty, and a Word file has none: Word writes a
// cell spanning columns once (gridSpan), and only a row span's later rows
// as cells of their own (vMerge), which Docs reads as the covered cells
// under the span's first column.
func spanned(b *doc.Block) bool {
	c := b.Cell
	return c != nil && c.Covered() && c.Col != c.MergedInto.Col
}

// staleError says the Docs model and the file disagree: Docs has not read
// a revision yet, or a person's edit in the Docs editor has not reached
// the file.
type staleError struct{ why string }

func (e *staleError) Error() string { return e.why }

func errStale(why string) error { return &staleError{why: why} }

// check is paragraph i's text checked to be the same in Docs and the
// file.
func (w *wordMap) check(i int) error {
	if got, want := w.file.Paras[i].Text, paragraphText(w.blocks[i]); got != want {
		return errStale(fmt.Sprintf("Docs reads %s as %q and the file holds %q", w.blocks[i].Handle, doc.Clip(want, 60), doc.Clip(got, 60)))
	}
	return nil
}

// para is the paragraph holding Docs index x, checked, and x's offset in
// it.
func (w *wordMap) para(x int64) (int, int, error) {
	for i, b := range w.blocks {
		if b.Start <= x && x < b.End {
			return i, int(x - b.Start), w.check(i)
		}
	}
	return 0, 0, Errorf("unsupported", "the place is not in a paragraph of the body; on a Word file only the body's text is written")
}

// span is the paragraphs a whole-block range covers, which must be
// paragraphs and nothing else.
func (w *wordMap) span(r plan.Rng) (int, int, error) {
	for _, t := range w.tables {
		if t.Start >= r.Start && t.End <= r.End {
			return 0, 0, Errorf("unsupported", "the target holds a table, which a Word file's text ops do not write; target the paragraphs around it")
		}
	}
	first, last := -1, -1
	for i, b := range w.blocks {
		if b.Start >= r.Start && b.End <= r.End {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return 0, 0, Errorf("unsupported", "the target holds no paragraph")
	}
	for i := first; i <= last; i++ {
		if err := w.check(i); err != nil {
			return 0, 0, err
		}
	}
	return first, last, nil
}

// point is Docs index x as a paragraph, checked, and an offset no
// further than its end, for the end of a passage.
func (w *wordMap) point(x int64) (int, int, error) {
	for i, b := range w.blocks {
		if b.Start <= x && x <= b.End {
			if x == b.End && i+1 < len(w.blocks) && w.blocks[i+1].Start == x {
				continue
			}
			return i, min(int(x-b.Start), w.file.Paras[i].Len()), w.check(i)
		}
	}
	return 0, 0, Errorf("unsupported", "the passage is not in the body")
}

// inBody says op is in the body, the one part of a Word file its text ops
// write.
func (w *wordMap) inBody(op *plan.Op) bool {
	return op.Seg.ID == "" && (op.Seg.TabID == "" || len(w.f.Doc.Tabs) == 0 || op.Seg.TabID == w.f.Doc.Tabs[0].ID)
}

// passage is where a Docs range starts and ends in the file, each
// paragraph checked: paragraph i at offset a to paragraph j at offset b.
func (w *wordMap) passage(r plan.Rng) (i, a, j, b int, err error) {
	if i, a, err = w.para(r.Start); err == nil {
		j, b, err = w.point(r.End)
	}
	return i, a, j, b, err
}

// apply makes one op in the file.
func (w *wordMap) apply(op *plan.Op, tr *ooxml.Track) error {
	if !w.inBody(op) {
		return Errorf("unsupported", "op %d: on a Word file only the body's text is written", op.Seq)
	}
	switch op.Kind {
	case plan.OpReplace, plan.OpDelete:
		if op.TargetIsBlock {
			i, j, err := w.span(*op.Target)
			if err != nil {
				return err
			}
			if op.Kind == plan.OpDelete {
				return docxError(op, w.file.DeleteParas(i, j, false, tr))
			}
			return w.replaceParas(op, i, j, tr)
		}
		i, a, j, b, err := w.passage(*op.Target)
		if err != nil {
			return err
		}
		if j != i {
			return Errorf("unsupported", "op %d: the target runs across paragraphs; on a Word file, target whole paragraphs by handle or heading", op.Seq)
		}
		if op.Kind == plan.OpDelete {
			return docxError(op, w.file.Replace(i, a, b, nil, tr))
		}
		return w.replaceText(op, i, a, b, tr)
	case plan.OpInsert, plan.OpAppend:
		i, x, err := w.para(op.Insert.Index)
		if err != nil {
			return err
		}
		switch {
		case op.Fill:
			return w.replaceParas(op, i, i, tr)
		case op.Inline:
			runs, ok := singleParagraph(op.Fragment)
			if !ok {
				return Errorf("unsupported", "op %d: content of more than one paragraph goes between paragraphs in a Word file; place it before or after one", op.Seq)
			}
			return docxError(op, w.file.Replace(i, x, x, runs, tr))
		}
		paras, err := w.newParas(op)
		if err != nil {
			return err
		}
		return docxError(op, w.file.InsertParas(i, op.AtEnd || x > 0, paras, tr))
	}
	return Errorf("unsupported", "op %d (%s): %s", op.Seq, op.Kind, wordElsewhere)
}

// replaceParas puts the op's content in place of paragraphs i to j: the
// new paragraphs go in after them, so a table cell or the body keeps a
// last paragraph.
func (w *wordMap) replaceParas(op *plan.Op, i, j int, tr *ooxml.Track) error {
	paras, err := w.newParas(op)
	if err != nil {
		return err
	}
	if err := w.file.InsertParas(j, true, paras, tr); err != nil {
		return docxError(op, err)
	}
	return docxError(op, w.file.DeleteParas(i, j, true, tr))
}

// replaceText replaces text inside one paragraph: plain text by its
// minimal difference, as on a Google Doc, so what does not change keeps
// its look and what is pinned to it; formatted text, or a target whose
// text does not line up with its range, whole.
func (w *wordMap) replaceText(op *plan.Op, i, a, b int, tr *ooxml.Track) error {
	runs, ok := singleParagraph(op.Fragment)
	if !ok {
		return Errorf("unsupported", "op %d: text inside a paragraph is replaced by one paragraph in a Word file; to write more, replace the whole paragraph by its handle", op.Seq)
	}
	old := op.TargetAligned
	if old == "" {
		old = op.TargetText
	}
	text, plain := op.Fragment.SingleParagraph()
	if !plain || doc.UTF16Len(old) != int64(b-a) {
		return docxError(op, w.file.Replace(i, a, b, runs, tr))
	}
	start := w.blocks[i].Start
	for _, e := range plan.MinimalEdits(old, text, start+int64(a)) {
		at := int(e.Pos - start)
		if err := w.file.Replace(i, at, at+int(e.Delete), []ooxml.Run{{Text: e.Insert}}, tr); err != nil {
			return docxError(op, err)
		}
	}
	return nil
}

// singleParagraph is content of one paragraph as runs.
func singleParagraph(f *markdown.Fragment) ([]ooxml.Run, bool) {
	if f == nil || len(f.Blocks) != 1 || f.Blocks[0].Kind != markdown.KindParagraph {
		return nil, false
	}
	return inlineRuns(f.Blocks[0].Inlines), true
}

// inlineRuns are a block's inlines as runs. Text with any formatting in
// it is styled as it says, all of it; plain text takes the look around it.
func inlineRuns(ins []markdown.Inline) []ooxml.Run {
	styled := false
	for _, in := range ins {
		styled = styled || in.Style() != (markdown.Inline{})
	}
	runs := make([]ooxml.Run, 0, len(ins))
	for _, in := range ins {
		runs = append(runs, ooxml.Run{Text: in.Text, Styled: styled, Bold: in.Bold, Italic: in.Italic, Strike: in.Strike, Code: in.Code, Link: in.Link})
	}
	return runs
}

// newParas are an op's content as new paragraphs. A list that starts the
// content continues the list item it is placed against, at its level,
// when their kinds match, unless list_level names one; another list is a
// list of its own. Lists of one kind in a row are one list.
func (w *wordMap) newParas(op *plan.Op) ([]ooxml.NewPara, error) {
	if op.Fragment == nil {
		return nil, Errorf("invalid", "op %d has no content", op.Seq)
	}
	blocks := op.Fragment.Blocks
	var out []ooxml.NewPara
	numID, lead := "", 0
	for k, b := range blocks {
		switch b.Kind {
		case markdown.KindParagraph:
			out = append(out, ooxml.NewPara{Runs: inlineRuns(b.Inlines)})
		case markdown.KindHeading:
			out = append(out, ooxml.NewPara{Heading: b.HeadingLevel(), Runs: inlineRuns(b.Inlines)})
		case markdown.KindCode:
			for _, line := range b.Lines {
				out = append(out, ooxml.NewPara{Runs: []ooxml.Run{{Text: line, Styled: true, Code: true}}})
			}
		case markdown.KindListItem:
			if k == 0 || blocks[k-1].Kind != markdown.KindListItem || blocks[k-1].Ordered != b.Ordered {
				var err error
				if numID, lead, err = w.listFor(op, k == 0, b.Ordered); err != nil {
					return nil, err
				}
			}
			out = append(out, ooxml.NewPara{NumID: numID, Ilvl: min(lead+b.Nesting, plan.MaxListLevel), Runs: inlineRuns(b.Inlines)})
		default:
			return nil, Errorf("unsupported", "op %d: content with a %s is not written into a Word file; %s", op.Seq, b.Kind, wordElsewhere)
		}
	}
	return out, nil
}

// listFor is the list and the level a list of content starts at: those of
// the item it is placed against when it leads the content and their kinds
// match, else a new list's and the top. list_level, for a leading list,
// names the level.
func (w *wordMap) listFor(op *plan.Op, leading, ordered bool) (string, int, error) {
	numID, level := "", 0
	if leading && op.List.Anchor != nil {
		if i, _, err := w.para(op.List.Anchor.Start); err == nil {
			id, lvl := w.file.ListOf(i)
			if kind, ok := w.file.ListOrdered(id, lvl); id != "" && ok && kind == ordered {
				numID, level = id, lvl
			}
		}
	}
	if numID == "" {
		id, err := w.file.NewList(ordered)
		if err != nil {
			return "", 0, docxError(op, err)
		}
		numID = id
	}
	if leading && op.ListLevel != nil {
		level = *op.ListLevel
	}
	return numID, level, nil
}

// fileComments gives the guard, from the file itself, the comments a
// change to whole paragraphs would cut out with them, for a read without
// the comments view that lists them. Text changed inside a paragraph
// keeps its comments' markers.
func (w *wordMap) fileComments(ops []plan.Op) {
	if w.f.CommentsView {
		return
	}
	for i := range ops {
		op := &ops[i]
		if !op.TargetIsBlock || op.Target == nil {
			continue
		}
		seen := map[string]bool{}
		for _, a := range op.Anchors {
			seen[a.Key()] = true
		}
		for k, b := range w.blocks {
			if b.Start < op.Target.Start || b.End > op.Target.End {
				continue
			}
			for _, id := range w.file.Paras[k].Comments {
				op.Anchors = appendUnique(seen, op.Anchors, plan.Anchor{Kind: "comment", ID: id, Start: b.Start, End: b.End})
			}
		}
	}
}

// comment pins a proposal or a comment to the passage a Docs range covers.
func (w *wordMap) comment(r plan.Rng, c ooxml.NewComment) error {
	i, a, j, b, err := w.passage(r)
	if err != nil {
		return err
	}
	_, err = w.file.Comment(i, a, j, b, c)
	return wordError(err)
}

// wordError classes an error from internal/ooxml: an edit it will not
// make is unsupported, any other failure invalid.
func wordError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ooxml.ErrUnsupported):
		return Errorf("unsupported", "%s", strings.TrimPrefix(err.Error(), ooxml.ErrUnsupported.Error()+": "))
	}
	return Errorf("invalid", "%v", err)
}

// docxError is an error from making op in the file, classed and naming
// the op.
func docxError(op *plan.Op, err error) error { return opError(op.Seq, wordError(err)) }

// docxChange makes a write's change in a Word file beside its Docs model,
// and says what it did and what it forces.
type docxChange func(w *wordMap) (*EditResult, []plan.Forced, error)

type docxOpts struct {
	ref            string
	expectRevision string
	dryRun         bool
}

// docxWrite runs one write to a Word file: it reads the file, its Drive
// metadata and its Docs model; makes the change; checks the file and
// Docs are where the change was made just before uploading; uploads; and
// finds in Drive's history the revision it replaced, a save that landed
// in between put back rather than overwritten. Then it waits for Docs to
// read the new revision, so the next read shows it.
func (s *Service) docxWrite(ctx context.Context, o docxOpts, change docxChange) (*EditResult, error) {
	id, err := parseRef(o.ref)
	if err != nil {
		return nil, err
	}
	for attempt := 1; ; attempt++ {
		res, again, err := s.docxWriteOnce(ctx, id, o, change, attempt)
		if !again {
			return res, err
		}
	}
}

// wordRead is a Word file as a write starts from: its Drive metadata and
// bytes, and its Docs model, read side by side.
type wordRead struct {
	meta *gapi.File
	data []byte
	f    *Fetched
}

func (s *Service) readWord(ctx context.Context, id string) (*wordRead, error) {
	type file struct {
		meta *gapi.File
		data []byte
		err  error
	}
	ch := make(chan file, 1)
	go func() {
		meta, err := s.api.GetFile(ctx, id)
		if err != nil {
			ch <- file{err: wrapAPI(err, "file")}
			return
		}
		if meta.MimeType != gapi.WordMimeType {
			s.rememberKind(id, meta.MimeType)
			ch <- file{err: Errorf("unsupported", "the file is %s now, not a Word file; read it again", meta.MimeType)}
			return
		}
		if size, _ := strconv.Atoi(meta.Size); size > maxUpload {
			ch <- file{err: tooLarge(size)}
			return
		}
		data, err := s.api.DownloadFile(ctx, id)
		if err != nil {
			err = wrapAPI(err, "file")
		}
		ch <- file{meta, data, err}
	}()
	f, err := s.FetchFresh(ctx, id)
	rd := <-ch
	switch {
	case err != nil:
		return nil, err
	case rd.err != nil:
		return nil, rd.err
	case len(rd.data) > maxUpload:
		return nil, tooLarge(len(rd.data))
	}
	return &wordRead{meta: rd.meta, data: rd.data, f: f}, nil
}

func (s *Service) docxWriteOnce(ctx context.Context, id string, o docxOpts, change docxChange, attempt int) (*EditResult, bool, error) {
	rd, err := s.readWord(ctx, id)
	if err != nil {
		return nil, false, err
	}
	f := rd.f
	if err := checkRevision(o.expectRevision, f.Doc.RevisionID, "editing"); err != nil {
		return nil, false, err
	}
	file, err := ooxml.Open(rd.data)
	if err != nil {
		return nil, false, Errorf("unsupported", "the file does not read as a Word file: %v", err)
	}
	w, err := newWordMap(f, file)
	var res *EditResult
	var forced []plan.Forced
	if err == nil {
		res, forced, err = change(w)
	}
	var stale *staleError
	if errors.As(err, &stale) {
		if attempt < writeAttempts {
			s.log.InfoContext(ctx, "the Word file and Docs disagree; waiting", "why", stale.why)
			return nil, true, s.sleep(ctx, saveWait)
		}
		return nil, false, Errorf("conflict", "Docs and the file disagree (%s): the file may be open in the Docs editor with changes not saved to it yet, "+
			"or hold something Docs reads differently there; try again in a moment", stale.why)
	}
	if err != nil {
		return nil, false, err
	}
	res.RevisionID, res.DryRun = f.Doc.RevisionID, o.dryRun
	if !file.Changed() {
		res.Warnings = append(res.Warnings, unchangedWarning)
		return res, false, nil
	}
	out, after, err := file.Bytes()
	if err != nil {
		return nil, false, Errorf("invalid", "the edit could not be written into the file: %v; nothing was uploaded", err)
	}
	if len(out) > maxUpload {
		return nil, false, tooLarge(len(out))
	}
	res.Preview = filePreview(file.Text(), after.Text())
	if o.dryRun {
		return res, false, nil
	}
	if len(forced) > 0 && asks(ctx) {
		if err := ask(ctx, forcedQuestion(f, forced)); err != nil {
			return nil, false, err
		}
	}
	if again, err := s.stillWhereItWas(ctx, id, rd.meta, liveView{revision: f.Doc.RevisionID}, attempt); again || err != nil {
		return nil, again, err
	}
	up, err := s.api.UploadFile(ctx, id, out, gapi.WordMimeType)
	s.Invalidate(id)
	if err != nil {
		return nil, false, wrapAPI(err, "file")
	}
	res.Warnings = append(res.Warnings, fmt.Sprintf("the file before this write is Drive revision %s (Drive keeps it at least 30 days unless the file passes 100 revisions)", rd.meta.HeadRevisionID))
	checkUpload(res, up, out, gapi.WordMimeType)
	if err := s.docxRepair(ctx, id, rd.meta.HeadRevisionID, up, res); err != nil {
		return nil, false, err
	}
	res.RevisionID = s.awaitDocs(ctx, id, f.Doc.RevisionID, res)
	return res, false, nil
}

// docxRepair finds the revision the upload replaced. When it is not the
// one the change was made on, somebody's save landed in between: it is
// put back, and the write is not applied.
func (s *Service) docxRepair(ctx context.Context, id, planned string, up *gapi.File, res *EditResult) error {
	prev := s.savedBetween(ctx, id, planned, up, res)
	if prev == "" {
		return nil
	}
	data, err := s.api.DownloadRevision(ctx, id, prev)
	if err == nil {
		_, err = s.api.UploadFile(ctx, id, data, gapi.WordMimeType)
	}
	if err != nil {
		return Errorf("ambiguous_outcome", "a save landed in the file while this write was uploading (revision %s), and putting it back failed: %s; "+
			"restore revision %s from the file's history", prev, messageOf(wrapAPI(err, "file")), prev)
	}
	return Errorf("conflict", "a save landed in the file while this write was uploading; the save was put back and the write is not applied; read the file again and retry")
}

// awaitDocs waits for Docs to read the revision just uploaded, and
// returns the Docs revision the next read will start from.
func (s *Service) awaitDocs(ctx context.Context, id, before string, res *EditResult) string {
	wait := saveWait
	for range docxReads {
		if s.sleep(ctx, wait) != nil {
			break
		}
		if v := s.live(ctx, id); v.err == nil && v.revision != before {
			return v.revision
		}
		wait = time.Second
	}
	res.Warnings = append(res.Warnings, "Docs has not read the new revision yet; a read right away may still show the file as it was")
	return before
}

// docxEdit is edit_document on a Word file.
func (s *Service) docxEdit(ctx context.Context, req EditRequest) (*EditResult, error) {
	mode, err := s.mode(req.Mode)
	if err != nil {
		return nil, err
	}
	for _, op := range req.Ops {
		if !textOpKinds[op.Kind] {
			return nil, Errorf("unsupported", "op %d (%s): on a Word file, edit_document writes text with insert, append, replace, delete and replace_all; %s", op.Seq, op.Kind, wordElsewhere)
		}
	}
	author, date := s.account(ctx), s.wordTime()
	return s.docxWrite(ctx, docxOpts{ref: req.Document, expectRevision: req.ExpectRevision, dryRun: req.DryRun}, func(w *wordMap) (*EditResult, []plan.Forced, error) {
		ro, err := s.resolveOps(ctx, w.f, req.Ops, mode)
		if err != nil {
			return nil, nil, err
		}
		w.fileComments(ro.ops)
		planned, err := plan.Check(ro.ops, plan.Options{Mode: mode, Force: req.Force})
		if err != nil {
			return nil, nil, planError(err)
		}
		res := &EditResult{Mode: string(mode), Applied: len(req.Ops), Changes: planned.Summary, Warnings: append(planned.Warnings, ro.warnings...)}
		if mode == plan.ModeComment {
			for _, p := range planned.Proposals {
				if err := w.comment(p.Range, wordComment(author, date, p.Content)); err != nil {
					return nil, nil, opError(p.Seq, err)
				}
				res.Proposals = append(res.Proposals, p)
			}
			return res, nil, nil
		}
		var tr *ooxml.Track
		if mode == plan.ModeSuggest {
			tr = &ooxml.Track{Author: author, Date: date}
		}
		for i := range ro.ops {
			if err := w.apply(&ro.ops[i], tr); err != nil {
				return nil, nil, err
			}
		}
		return res, planned.Forced, nil
	})
}

// docxAddComment pins a comment to a passage of a Word file, as a Word
// comment.
func (s *Service) docxAddComment(ctx context.Context, req AddCommentRequest, content string) (*AddCommentResult, error) {
	author := s.account(ctx)
	res := &AddCommentResult{Anchored: true, DryRun: req.DryRun}
	edit, err := s.docxWrite(ctx, docxOpts{ref: req.Document, expectRevision: req.ExpectRevision, dryRun: req.DryRun}, func(w *wordMap) (*EditResult, []plan.Forced, error) {
		r, err := s.commentTarget(w.f, *req.Target, res)
		if err != nil {
			return nil, nil, err
		}
		return &EditResult{}, nil, w.comment(r.Rng(), wordComment(author, s.wordTime(), content))
	})
	if err != nil {
		return nil, err
	}
	res.RevisionID, res.Warnings = edit.RevisionID, edit.Warnings
	if strings.TrimSpace(req.Assignee) != "" {
		res.Warnings = append(res.Warnings, "a comment in a Word file cannot be assigned; it was posted without an assignee")
	}
	verb := "posted"
	if req.DryRun {
		verb = "would be posted"
	}
	res.say("a Word comment "+verb, "")
	return res, nil
}

// wordComment is a Word comment by author, with their initials.
func wordComment(author, date, text string) ooxml.NewComment {
	return ooxml.NewComment{Author: author, Initials: initials(author), Date: date, Text: text}
}

// initials are a name's, for a Word comment.
func initials(name string) string {
	var b strings.Builder
	for _, f := range strings.Fields(name) {
		if c, _ := utf8.DecodeRuneInString(f); unicode.IsLetter(c) {
			b.WriteRune(unicode.ToUpper(c))
		}
	}
	return b.String()
}
