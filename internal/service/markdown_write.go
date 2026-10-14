package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mmedum/google-docs-mcp/v2/internal/doc"
	"github.com/mmedum/google-docs-mcp/v2/internal/gapi"
	"github.com/mmedum/google-docs-mcp/v2/internal/gdocs"
	"github.com/mmedum/google-docs-mcp/v2/internal/mdfile"
	"github.com/mmedum/google-docs-mcp/v2/internal/plan"
	"github.com/mmedum/google-docs-mcp/v2/internal/render"
)

// Every write to a Markdown file is a whole-file upload, and Drive has no
// precondition for one. While a person has the file open in the Docs
// editor, the file is not where their work is: it is in the live session,
// which reaches the file only when the editor saves, and an upload makes
// the editor take the uploaded version (spike D, stage 3). So a write
// checks, writes, verifies and repairs:
//
//   - before: the live session must agree with the file — no comment,
//     reply or resolve made in the editor and not saved yet — or the write
//     waits a moment and, still disagreeing, refuses;
//   - just before the upload: the file and the live session must be where
//     the plan was made, or it is planned again;
//   - after: the revision before the upload must be the one the plan was
//     made on; a save that landed in between is taken from Drive's
//     history and the change made again on top of it, so nothing is
//     overwritten even then;
//   - a write of comment markup must leave a file the Docs API still
//     reads, or the bytes are put back.

// mdChange makes a write's new file from a file as read. It is called
// again on a newer version when a save lands while the write is under
// way, so it may depend on nothing but its argument and what it closed
// over before the write began.
type mdChange func(m *mdFile) (*mdfile.File, *EditResult, []render.Forced, error)

type mdWriteOpts struct {
	ref            string
	expectRevision string
	dryRun         bool
	force          bool
	// comment says the change writes comment markup; posts names their
	// bodies, which the live session must show afterwards.
	comment bool
	posts   []string
}

const (
	// mdWriteAttempts bounds how often a write waits for the editor or
	// plans again.
	mdWriteAttempts = 3
	// mdWait is how long a write waits for the editor to save.
	mdWait = 3 * time.Second
)

// liveFields is the part of the live session a write checks: its
// revision, its comments and where they sit. The comments view honors a
// mask only with the tabs' content asked for (spike D).
const liveFields = "revisionId,comments,tabs(tabProperties(tabId),documentTab(commentAnchors))"

type liveView struct {
	revision string
	doc      *gdocs.Document
	err      error
}

func (s *Service) live(ctx context.Context, id string) liveView {
	res, err := s.api.GetDocument(ctx, id, gapi.GetOptions{SuggestionsViewMode: gapi.SuggestionsInline, CommentsViewMode: gapi.CommentsIncluded, Fields: liveFields})
	if err != nil {
		return liveView{err: err}
	}
	return liveView{revision: res.Document.RevisionID, doc: res.Document}
}

// editorPending is what a person did in the Docs editor that the file does not
// show yet, or "": a comment the live session holds on a passage and the
// file does not, a reply, a resolve or a reopen. A thread the live
// session holds with no passage is an orphan, not news.
func editorPending(f *mdfile.File, v liveView) string {
	if v.doc == nil {
		return ""
	}
	anchored := viewAnchors(v.doc)
	entries := f.Comments()
	used := make([]bool, len(entries))
	for i, t := range viewThreads(v.doc) {
		w := v.doc.Comments[i]
		k := matchEntry(entries, used, t)
		if k < 0 {
			if anchored[w.CommentID] != nil {
				return "a comment made in the editor"
			}
			continue
		}
		used[k] = true
		if t.Resolved != (entries[k].Status == "RESOLVED") {
			return "a resolve or a reopen"
		}
		if len(t.Replies) > len(entries[k].Replies) {
			return "a reply"
		}
	}
	return ""
}

// mdWrite runs one write to a Markdown file. It returns the result of
// the change as finally written.
func (s *Service) mdWrite(ctx context.Context, o mdWriteOpts, change mdChange) (*EditResult, error) {
	id, err := parseRef(o.ref)
	if err != nil {
		return nil, err
	}
	for attempt := 1; ; attempt++ {
		res, again, err := s.mdWriteOnce(ctx, id, o, change, attempt)
		if !again {
			return res, err
		}
	}
}

// mdWriteOnce is one try at a write; again says to plan it again on a
// fresh read.
func (s *Service) mdWriteOnce(ctx context.Context, id string, o mdWriteOpts, change mdChange, attempt int) (*EditResult, bool, error) {
	viewCh := make(chan liveView, 1)
	go func() { viewCh <- s.live(ctx, id) }()
	m, err := s.fetchMarkdown(ctx, id, nil)
	view := <-viewCh
	if err != nil {
		return nil, false, err
	}
	if err := checkRevision(o.expectRevision, m.revision(), "editing"); err != nil {
		return nil, false, err
	}
	if err := mdWritable(m); err != nil {
		return nil, false, err
	}
	after, res, forced, err := change(m)
	if err != nil {
		return nil, false, err
	}
	res.RevisionID, res.DryRun = m.revision(), o.dryRun
	res.Warnings = append(slices.Clone(m.warnings), res.Warnings...)
	if view.err != nil {
		res.Warnings = append(res.Warnings, "could not ask Google whether the file is being edited in Docs ("+messageOf(wrapAPI(view.err, "file"))+")")
	}
	if after.Source() == m.md.Source() {
		res.Warnings = append(res.Warnings, "the change leaves the file as it is, so nothing was uploaded")
		return res, false, nil
	}
	if n := len(after.Source()); n > mdMaxWrite {
		return nil, false, tooLarge(n)
	}
	res.Preview = render.UnifiedDiff(m.md.Text(), after.Text(), 2, 6000).Text
	if o.comment && res.Preview == "" {
		res.Preview = "(the text does not change; the comment is written into the file)"
	}
	forced, again, err := s.awaitEditor(ctx, o, m, view, attempt, forced, res)
	if again || err != nil || o.dryRun {
		return res, again, err
	}
	if len(forced) > 0 && asks(ctx) {
		if err := ask(ctx, render.AskForced(m.id(), m.title(), forced)); err != nil {
			return nil, false, err
		}
	}
	if again, err := s.stillWhereItWas(ctx, id, m, view, attempt); again || err != nil {
		return nil, again, err
	}
	return res, false, s.upload(ctx, id, m, after, o, change, res)
}

// awaitEditor holds a write back while the live session has work the
// file does not show yet: it waits and tries again, then refuses, unless
// forced, which asks the person and names what it discards.
func (s *Service) awaitEditor(ctx context.Context, o mdWriteOpts, m *mdFile, view liveView, attempt int, forced []render.Forced, res *EditResult) ([]render.Forced, bool, error) {
	what := editorPending(m.md, view)
	switch {
	case what == "":
		return forced, false, nil
	case o.force:
		res.Warnings = append(res.Warnings, "forced past "+what+" made in the Docs editor and not saved yet, which this write discards")
		return append(forced, render.Forced{Seq: -1, Kind: "write", Description: "the file", Destroys: what + " made in the Docs editor and not saved yet"}), false, nil
	case attempt < mdWriteAttempts && !o.dryRun:
		s.log.InfoContext(ctx, "the Markdown file has changes in the Docs editor not saved yet; waiting", "what", what)
		return forced, true, s.sleep(ctx, mdWait)
	}
	return forced, false, Errorf("conflict", "the file is being worked on in the Docs editor: %s has not reached the file yet, and writing now would lose it; "+
		"try again in a moment, or pass force: true if the person chose to write anyway", what)
}

// stillWhereItWas is the check just before an upload, which is all Drive
// offers: the file and the live session where the plan was made.
func (s *Service) stillWhereItWas(ctx context.Context, id string, m *mdFile, view liveView, attempt int) (bool, error) {
	nowCh := make(chan liveView, 1)
	go func() { nowCh <- s.live(ctx, id) }()
	now, err := s.api.GetFile(ctx, id)
	viewNow := <-nowCh
	if err != nil {
		return false, wrapAPI(err, "file")
	}
	moved := now.HeadRevisionID != m.meta.HeadRevisionID || now.MD5Checksum != m.meta.MD5Checksum
	edited := view.err == nil && viewNow.err == nil && viewNow.revision != view.revision
	switch {
	case !moved && !edited:
		return false, nil
	case attempt >= mdWriteAttempts:
		return false, Errorf("conflict", "the file kept changing while this write was planned; read it again and retry")
	}
	s.log.InfoContext(ctx, "the Markdown file moved while planning; planning again", "saved", moved, "edited", edited)
	if edited {
		return true, s.sleep(ctx, mdWait)
	}
	return true, nil
}

// upload writes the file, then verifies: that nobody's save was
// overwritten, and that a comment written into it is listed.
func (s *Service) upload(ctx context.Context, id string, m *mdFile, after *mdfile.File, o mdWriteOpts, change mdChange, res *EditResult) error {
	up, err := s.api.UploadFile(ctx, id, []byte(after.Source()), gapi.MarkdownMimeType)
	if err != nil {
		return wrapAPI(err, "file")
	}
	res.RevisionID = up.HeadRevisionID
	res.Warnings = append(res.Warnings, fmt.Sprintf("the text before this write is Drive revision %s (diff_revisions from it shows the change; Drive keeps it at least 30 days unless the file passes 100 revisions)", m.revision()))
	if up.MD5Checksum != "" && up.MD5Checksum != md5Hex([]byte(after.Source())) {
		res.Warnings = append(res.Warnings, "Drive's checksum of the uploaded file is not the checksum of what was sent; read the file before relying on it")
	}
	if up.MimeType != "" && up.MimeType != gapi.MarkdownMimeType {
		res.Warnings = append(res.Warnings, fmt.Sprintf("Drive typed the file %s after the upload", up.MimeType))
	}
	if err := s.repair(ctx, m, up, change, res); err != nil {
		return err
	}
	if !o.comment {
		return nil
	}
	posts := o.posts
	for _, p := range res.Proposals {
		posts = append(posts, p.Content)
	}
	return s.confirmComments(ctx, id, m, posts, res)
}

// repair finds the revision the upload replaced. When it is not the one
// the change was planned on, somebody's save landed in between: it is
// taken from Drive's history and the change made again on top of it,
// or, when the change no longer fits, put back as it was.
func (s *Service) repair(ctx context.Context, m *mdFile, up *gapi.File, change mdChange, res *EditResult) error {
	revs, err := s.api.ListRevisions(ctx, m.id())
	if err != nil {
		res.Warnings = append(res.Warnings, "could not read the file's history to confirm nothing was saved in between: "+messageOf(wrapAPI(err, "revisions")))
		return nil
	}
	prev := ""
	for i, r := range revs {
		if r.ID == up.HeadRevisionID && i > 0 {
			prev = revs[i-1].ID
		}
	}
	if prev == "" || prev == m.revision() {
		return nil
	}
	data, err := s.api.DownloadRevision(ctx, m.id(), prev)
	if err != nil {
		return Errorf("ambiguous_outcome", "a save landed in the file while this write was uploading (revision %s), and it could not be read back to keep it: %s; "+
			"restore it from the file's history", prev, messageOf(wrapAPI(err, "revision "+prev)))
	}
	theirs := &mdFile{meta: withRevision(m.meta, prev, md5Hex(data)), md: mdfile.Parse(string(data))}
	again, _, _, cerr := change(theirs)
	if cerr != nil || again == nil {
		// The change does not fit the save: the save wins.
		if _, err := s.api.UploadFile(ctx, m.id(), data, gapi.MarkdownMimeType); err != nil {
			return Errorf("ambiguous_outcome", "a save landed in the file while this write was uploading (revision %s), the write no longer fits it, and putting the save back failed: %s; "+
				"restore revision %s from the file's history", prev, messageOf(wrapAPI(err, "file")), prev)
		}
		return Errorf("conflict", "a save landed in the file while this write was uploading, and the write no longer fits it (%s); the save was put back and the write is not applied", messageOf(cerr))
	}
	up2, err := s.api.UploadFile(ctx, m.id(), []byte(again.Source()), gapi.MarkdownMimeType)
	if err != nil {
		return Errorf("ambiguous_outcome", "a save landed in the file while this write was uploading (revision %s), and writing the change again on top of it failed: %s; "+
			"read the file, and restore revision %s from its history if the save is missing", prev, messageOf(wrapAPI(err, "file")), prev)
	}
	res.RevisionID = up2.HeadRevisionID
	res.Warnings = append(res.Warnings, fmt.Sprintf("a save landed in the file while this write was uploading (revision %s); it was kept, and the change made again on top of it", prev))
	return nil
}

func withRevision(meta *gapi.File, rev, sum string) *gapi.File {
	cp := *meta
	cp.HeadRevisionID, cp.MD5Checksum = rev, sum
	return &cp
}

// confirmComments waits for the live session to show the comments a
// write put into the file and names their ids. When the Docs API can no
// longer read the file at all, the bytes go back to what they were.
func (s *Service) confirmComments(ctx context.Context, id string, m *mdFile, posts []string, res *EditResult) error {
	for try := range 4 {
		if err := s.sleep(ctx, 2*time.Second); err != nil {
			return err
		}
		v := s.live(ctx, id)
		if errors.Is(v.err, gapi.ErrServer) && try == 3 {
			if _, err := s.api.UploadFile(ctx, id, []byte(m.md.Source()), gapi.MarkdownMimeType); err != nil {
				return Errorf("ambiguous_outcome", "the Docs API cannot read the file since the comment was written into it, and putting it back failed: %s", messageOf(wrapAPI(err, "file")))
			}
			return Errorf("unexpected", "the Docs API could not read the file once the comment was written into it, so the file was put back as it was")
		}
		if v.doc == nil {
			continue
		}
		var ids []string
		for _, t := range viewThreads(v.doc) {
			if slices.Contains(posts, t.Content) {
				ids = append(ids, t.ID)
				continue
			}
			for _, r := range t.Replies {
				if slices.Contains(posts, r.Content) {
					ids = append(ids, t.ID)
				}
			}
		}
		if len(ids) >= len(posts) {
			res.CommentIDs = ids
			return nil
		}
	}
	res.Warnings = append(res.Warnings, "the comment is in the file, and Docs has not listed it yet; list_comments shows it once Docs has read the file again")
	return nil
}

// sleep waits, or stops with the request.
func (s *Service) sleep(ctx context.Context, d time.Duration) error {
	if s.opts.Sleep != nil {
		return s.opts.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// account is the name a comment written into a Markdown file carries:
// the signed-in account's, asked of Drive once.
func (s *Service) account(ctx context.Context) string {
	s.mu.Lock()
	name := s.accountName
	s.mu.Unlock()
	if name != "" {
		return name
	}
	u, err := s.api.About(ctx)
	if err != nil || u == nil {
		return "google-docs-mcp"
	}
	name = u.DisplayName
	if name == "" {
		name = u.EmailAddress
	}
	s.mu.Lock()
	s.accountName = name
	s.mu.Unlock()
	return name
}

// postTime is a comment's time as Docs writes it.
func (s *Service) postTime() string { return s.now().UTC().Format("2006-01-02T15:04:05.000Z") }

// mdEdit is edit_document (and every other write tool's ops) on a
// Markdown file: in direct mode the ops as minimal edits of the file's
// text, in comment mode each op as a comment proposing it.
func (s *Service) mdEdit(ctx context.Context, req EditRequest) (*EditResult, error) {
	mode, err := s.mode(req.Mode)
	if err != nil {
		return nil, err
	}
	o := mdWriteOpts{ref: req.Document, expectRevision: req.ExpectRevision, dryRun: req.DryRun, force: req.Force}
	switch mode {
	case plan.ModeSuggest:
		return nil, Errorf("unavailable", "a Markdown file takes no suggestions: Docs has no suggestion mode for it and the Docs API refuses every write to it; "+
			"use mode comment to propose the change, or mode direct to make it, as the person asked")
	case plan.ModeComment:
		author, at := s.account(ctx), s.postTime()
		o.comment = true
		return s.mdWrite(ctx, o, func(m *mdFile) (*mdfile.File, *EditResult, []render.Forced, error) {
			out, res, err := mdPropose(m.md, req.Ops, author, at)
			return out, res, nil, err
		})
	}
	return s.mdWrite(ctx, o, func(m *mdFile) (*mdfile.File, *EditResult, []render.Forced, error) {
		p, err := mdPlan(m.md, req.Ops, req.Force)
		if err != nil {
			return nil, nil, nil, err
		}
		after, err := m.md.Apply(p.edits)
		if err != nil {
			return nil, nil, nil, Errorf("invalid", "%v; nothing was written", err)
		}
		return after, &EditResult{Mode: string(plan.ModeDirect), Applied: len(req.Ops), Changes: p.summary, Warnings: p.warnings}, p.forced, nil
	})
}

// mdPropose writes each op as a comment proposing it, on the passage it
// would change, and changes no text.
func mdPropose(f *mdfile.File, ops []EditOp, author, at string) (*mdfile.File, *EditResult, error) {
	res := &EditResult{Mode: string(plan.ModeComment), Applied: len(ops)}
	text := f.Text()
	out := f
	add := func(seq int, s, e int, body string) error {
		// A comment sits on text; trailing newlines are not text.
		for e > s && text[e-1] == '\n' {
			e--
		}
		g, _, err := out.AddComment(s, e, mdfile.NewPost{Author: author, Time: at, Body: body})
		if err != nil {
			return Errorf("invalid", "op %d: %v", seq, err)
		}
		out = g
		res.Proposals = append(res.Proposals, plan.Proposal{Seq: seq, Content: body, Quote: text[s:e]})
		return nil
	}
	quote := func(s string) string { return "“" + doc.Clip(doc.OneLine(s), 80) + "”" }
	for _, op := range ops {
		if !mdOpKinds[op.Kind] {
			return nil, nil, mdUnsupported(fmt.Sprintf("op %d (%s) needs the Docs API", op.Seq, op.Kind), "propose the change as markdown with insert, append, replace, delete or replace_all")
		}
		var err error
		switch op.Kind {
		case plan.OpReplace, plan.OpDelete:
			if op.Target == nil {
				return nil, nil, Errorf("invalid", "op %d: %s needs a target", op.Seq, op.Kind)
			}
			span, terr := mdTarget(f, *op.Target)
			if terr != nil {
				return nil, nil, opError(op.Seq, terr)
			}
			body := "Proposed deletion of " + quote(text[span.start:span.end]) + "."
			if op.Kind == plan.OpReplace {
				body = "Proposed change to " + quote(text[span.start:span.end]) + ":\n\n" + op.Content
			}
			err = add(op.Seq, span.start, span.end, body)
		case plan.OpInsert, plan.OpAppend:
			loc := Location{At: "end"}
			if op.Location != nil {
				loc = *op.Location
			}
			s, e, desc, lerr := mdNear(f, loc)
			if lerr != nil {
				return nil, nil, opError(op.Seq, lerr)
			}
			err = add(op.Seq, s, e, "Proposed insertion "+desc+":\n\n"+op.Content)
		case plan.OpReplaceAll:
			hits := f.FindAll(op.Find, op.MatchCase)
			switch {
			case op.Regex:
				return nil, nil, Errorf("unsupported", "op %d: comment mode on a Markdown file proposes a plain replace_all only; use mode direct for a regex", op.Seq)
			case len(hits) == 0:
				return nil, nil, Errorf("not_found", "op %d: %q is not in the file", op.Seq, doc.Clip(op.Find, 60))
			case len(hits) > 20:
				return nil, nil, Errorf("invalid", "op %d: %q matches %d times; comment mode posts a comment per match, at most 20", op.Seq, doc.Clip(op.Find, 40), len(hits))
			}
			for _, h := range hits {
				if err = add(op.Seq, h.Start, h.End, fmt.Sprintf("Proposed: replace %s with %s.", quote(text[h.Start:h.End]), quote(op.Replace))); err != nil {
					break
				}
			}
		}
		if err != nil {
			return nil, nil, err
		}
		res.Changes = append(res.Changes, plan.OpSummary{Seq: op.Seq, Kind: op.Kind, Description: "a comment proposing it"})
	}
	return out, res, nil
}

// mdNear is the passage a proposed insertion is posted on: the target it
// goes before or after, or the first or last line with text.
func mdNear(f *mdfile.File, loc Location) (int, int, string, error) {
	text := f.Text()
	if loc.Of != nil && loc.Of.selectorCount() > 0 {
		span, err := mdTarget(f, *loc.Of)
		if err != nil {
			return 0, 0, "", err
		}
		at := strings.ToLower(strings.TrimSpace(loc.At))
		if at == "" {
			at = "after"
		}
		if span.block {
			// A section: its heading's line.
			end := strings.IndexByte(text[span.start:], '\n')
			if end < 0 {
				end = len(text) - span.start
			}
			return span.start, span.start + end, at + " " + span.desc, nil
		}
		return span.start, span.end, at + " this", nil
	}
	lines := strings.Split(text, "\n")
	pick := func(i int) (int, int) {
		s := f.LineStart(i + 1)
		return s, s + len(lines[i])
	}
	if strings.EqualFold(strings.TrimSpace(loc.At), "start") {
		for i, l := range lines {
			if strings.TrimSpace(l) != "" {
				s, e := pick(i)
				return s, e, "at the start of the file", nil
			}
		}
	} else {
		for i := len(lines) - 1; i >= 0; i-- {
			if strings.TrimSpace(lines[i]) != "" {
				s, e := pick(i)
				return s, e, "at the end of the file", nil
			}
		}
	}
	return 0, 0, "", Errorf("invalid", "the file has no text to put a comment on")
}

// mdAddComment is add_comment on a Markdown file: the comment is written
// into the file, on its passage, under the signed-in account's name, the
// way the Docs editor writes its own, and the editor shows it.
func (s *Service) mdAddComment(ctx context.Context, req AddCommentRequest, content string) (*AddCommentResult, error) {
	if req.Target == nil || req.Target.selectorCount() == 0 {
		return nil, Errorf("invalid", "a comment on a Markdown file sits on a passage; give a target (exact text, heading_id or heading)")
	}
	if strings.TrimSpace(req.Assignee) != "" {
		return nil, mdUnsupported("a comment written into it cannot be assigned", "mention the person in the comment, or assign it in the Docs editor")
	}
	author, at := s.account(ctx), s.postTime()
	res := &AddCommentResult{Anchored: true, DryRun: req.DryRun}
	er, err := s.mdWrite(ctx, mdWriteOpts{ref: req.Document, expectRevision: req.ExpectRevision, dryRun: req.DryRun, comment: true, posts: []string{content}},
		func(m *mdFile) (*mdfile.File, *EditResult, []render.Forced, error) {
			span, err := mdTarget(m.md, *req.Target)
			if err != nil {
				return nil, nil, nil, err
			}
			text := m.md.Text()
			end := span.end
			for end > span.start && text[end-1] == '\n' {
				end--
			}
			out, _, err := m.md.AddComment(span.start, end, mdfile.NewPost{Author: author, Time: at, Body: content})
			if err != nil {
				return nil, nil, nil, Errorf("invalid", "%v", err)
			}
			res.Quote, res.Handle = text[span.start:end], fmt.Sprintf("L%d", m.md.LineOf(span.start))
			return out, &EditResult{Mode: string(plan.ModeComment)}, nil, nil
		})
	if er != nil {
		res.RevisionID, res.Warnings = er.RevisionID, er.Warnings
		if len(er.CommentIDs) > 0 {
			res.ID = er.CommentIDs[0]
		}
	}
	if err != nil {
		return nil, err
	}
	switch {
	case req.DryRun:
		res.Text = fmt.Sprintf("dry run: would write a comment into the file on %s (%q) by %s", res.Handle, doc.Clip(res.Quote, 60), author)
	case res.ID != "":
		res.Text = fmt.Sprintf("comment %s written into the file on %s (%q) by %s", res.ID, res.Handle, doc.Clip(res.Quote, 60), author)
	default:
		res.Text = fmt.Sprintf("comment written into the file on %s (%q) by %s", res.Handle, doc.Clip(res.Quote, 60), author)
	}
	for _, w := range res.Warnings {
		res.Text += "\nwarning: " + w
	}
	return res, nil
}

// mdReply is reply_comment on a comment the Docs editor keeps in a
// Markdown file, which Drive cannot reach: the reply is written into the
// file. Resolving cannot be: a resolve written into the bytes makes the
// Docs API fail on the whole file (spike D), so a person resolves in the
// editor.
func (s *Service) mdReply(ctx context.Context, req ReplyRequest, action, content string) (*ReplyResult, error) {
	commentID := strings.TrimSpace(req.CommentID)
	if action != "reply" {
		return nil, mdUnsupported("a comment it keeps can only be replied to through the file: written into the bytes, a resolve or reopen makes Google fail to read the file, and an edit is not kept",
			"the person can "+action+" it in the Docs editor")
	}
	id, err := parseRef(req.Document)
	if err != nil {
		return nil, err
	}
	v := s.live(ctx, id)
	if v.err != nil {
		return nil, wrapAPI(v.err, "file")
	}
	var thread *CommentThread
	for _, t := range viewThreads(v.doc) {
		if t.ID == commentID {
			thread = &t
		}
	}
	if thread == nil {
		return nil, Errorf("not_found", "no comment %s on the file; ids come from list_comments, and the id of a comment written into a Markdown file changes each time the file is written, until a person saves the file in the Docs editor", commentID)
	}
	author, at := s.account(ctx), s.postTime()
	if _, err := s.mdWrite(ctx, mdWriteOpts{ref: req.Document, comment: true, posts: []string{content}},
		func(m *mdFile) (*mdfile.File, *EditResult, []render.Forced, error) {
			entries := m.md.Comments()
			k := matchEntry(entries, make([]bool, len(entries)), *thread)
			if k < 0 {
				return nil, nil, nil, Errorf("not_found", "comment %s is not in the file's text (it lost its passage), so a reply cannot be written into it", commentID)
			}
			out, err := m.md.AddReply(entries[k].Key, mdfile.NewPost{Author: author, Time: at, Body: content})
			if err != nil {
				return nil, nil, nil, Errorf("invalid", "%v", err)
			}
			return out, &EditResult{Mode: string(plan.ModeComment)}, nil, nil
		}); err != nil {
		return nil, err
	}
	return &ReplyResult{CommentID: commentID, Action: "reply", Resolved: thread.Resolved,
		Text: fmt.Sprintf("replied to comment %s in the file by %s", commentID, author)}, nil
}
