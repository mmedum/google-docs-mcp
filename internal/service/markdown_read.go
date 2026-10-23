package service

import (
	"cmp"
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mmedum/google-docs-mcp/v3/internal/doc"
	"github.com/mmedum/google-docs-mcp/v3/internal/gapi"
	"github.com/mmedum/google-docs-mcp/v3/internal/mdfile"
	"github.com/mmedum/google-docs-mcp/v3/internal/render"
)

// lineRef is where a read continues: a line, and a character in it when
// a line longer than the budget was cut.
var lineRef = regexp.MustCompile(`^[Ll](\d+)(?::(\d+))?$`)

// mdScope resolves a read scope in a Markdown file.
func mdScope(f *mdfile.File, sc ReadScope) (mdSpan, error) {
	if err := noTabOrSegment(sc.Tab, sc.Segment); err != nil {
		return mdSpan{}, err
	}
	if sc.FromHandle != "" || sc.ToHandle != "" {
		return mdSpan{}, noHandles()
	}
	span := mdSpan{start: 0, end: len(f.Text()), desc: "the whole file"}
	if sc.HeadingID != "" || sc.Heading != "" {
		var err error
		if span, err = mdSection(f, sc.HeadingID, sc.Heading, sc.HeadingLevel, sc.Occurrence, nil); err != nil {
			return mdSpan{}, err
		}
	}
	if sc.ContinueFrom != "" {
		m := lineRef.FindStringSubmatch(strings.TrimSpace(sc.ContinueFrom))
		if m == nil {
			return mdSpan{}, Errorf("invalid", "continue_from %q: a Markdown file continues from a line, L<number>, or L<number>:<character>, as a truncated read names it", sc.ContinueFrom)
		}
		n, _ := strconv.Atoi(m[1])
		at := f.LineStart(n)
		if m[2] != "" {
			col, _ := strconv.Atoi(m[2])
			at = f.Cut(at, col)
		}
		if at < span.start || at >= span.end {
			return mdSpan{}, Errorf("invalid", "continue_from %s is outside %s", sc.ContinueFrom, span.desc)
		}
		span.start, span.desc = at, fmt.Sprintf("%s, from line %d", span.desc, n)
	}
	return span, nil
}

// mdRead is read_document for a Markdown file: the file's own text
// without Docs' comment markup, budgeted at a line. The budget counts
// characters, as a Google Doc's does.
func (s *Service) mdRead(ctx context.Context, req ReadRequest) (*ReadResult, error) {
	format := strings.ToLower(strings.TrimSpace(req.Format))
	if format == FormatRaw {
		return nil, mdUnsupported("raw is a Google Doc's JSON", "markdown and text both return the file's text")
	}
	if format != "" && format != FormatMarkdown && format != FormatText {
		return nil, Errorf("invalid", "format %q; use markdown, text or raw", req.Format)
	}
	var m *mdFile
	var threads []mdThread
	var warn string
	var err error
	if req.IncludeComments {
		m, threads, warn, err = s.mdWithComments(ctx, req.Document, false, false)
	} else {
		m, err = s.fetchMarkdown(ctx, req.Document, nil)
	}
	if err != nil {
		return nil, err
	}
	span, err := mdScope(m.md, req.Scope)
	if err != nil {
		return nil, err
	}
	end := span.end
	if req.Options.MaxChars > 0 {
		end = min(end, m.md.Cut(span.start, req.Options.MaxChars))
	}
	text := m.md.Text()[span.start:end]
	footer := ""
	if req.IncludeComments {
		byKey := map[string]string{}
		for _, t := range threads {
			if t.key != "" {
				byKey[t.key] = t.ID
			}
		}
		text = m.md.WithMarks(span.start, end, func(key string) string {
			if id, ok := byKey[key]; ok {
				key = id
			}
			return "{>>c:" + key + "<<}"
		})
		footer = mdCommentFooter(threads, span.start, end, warn)
	}
	res := &ReadResult{RevisionID: m.revision(), Segment: "body", Scope: "Markdown file, " + span.desc, unit: "line",
		Blocks: mdfile.Lines(text), Chars: utf8.RuneCountInString(text), Truncated: end < span.end}
	if res.Truncated {
		res.ContinueFrom = continueFrom(m.md, end)
	}
	// Warnings go above the text, as comments, so that what follows the
	// header is the file's text and nothing else.
	var warnings strings.Builder
	for _, w := range m.warnings {
		fmt.Fprintf(&warnings, "<!-- warning: %s -->\n", w)
	}
	res.Text = res.header() + warnings.String() + text + footer
	return res, nil
}

// continueFrom names where a read cut at end goes on: the line, and the
// character in it when the cut fell inside a line longer than the
// budget, so that continuing always moves forward.
func continueFrom(f *mdfile.File, end int) string {
	line := f.LineOf(end)
	at := "L" + strconv.Itoa(line)
	if start := f.LineStart(line); end > start {
		at += ":" + strconv.Itoa(utf8.RuneCountInString(f.Text()[start:end]))
	}
	return at
}

// mdThread is a comment thread on a Markdown file and the key of its
// entry in the file's block, when it has one.
type mdThread struct {
	CommentThread
	key string
}

// mdWithComments reads a Markdown file and its comments: the Docs
// comments view holds the ones made in the editor (Drive cannot see
// those), Drive's list, when asked for, the ones made through its API,
// which are in no editor's view of the file. The three calls need only
// the id, so they run together.
func (s *Service) mdWithComments(ctx context.Context, ref string, drive, includeDeleted bool) (*mdFile, []mdThread, string, error) {
	id, err := parseRef(ref)
	if err != nil {
		return nil, nil, "", err
	}
	type view struct {
		res *gapi.DocumentResult
		err error
	}
	type listed struct {
		list []*gapi.DriveComment
		err  error
	}
	viewCh, listCh := make(chan view, 1), make(chan listed, 1)
	go func() {
		res, err := s.api.GetDocument(ctx, id, gapi.GetOptions{SuggestionsViewMode: gapi.SuggestionsInline, CommentsViewMode: gapi.CommentsIncluded})
		viewCh <- view{res, err}
	}()
	if drive {
		go func() {
			list, err := s.api.ListComments(ctx, id, includeDeleted)
			listCh <- listed{list, err}
		}()
	} else {
		listCh <- listed{}
	}
	m, err := s.fetchMarkdown(ctx, id, nil)
	v, l := <-viewCh, <-listCh
	switch {
	case err != nil:
		return nil, nil, "", err
	case l.err != nil:
		return nil, nil, "", wrapAPI(l.err, "comments")
	}
	threads, warn := mdThreads(m.md, v.res, v.err)
	for _, t := range driveThreads(l.list) {
		threads = append(threads, mdThread{CommentThread: t})
	}
	return m, threads, warn, nil
}

// mdThreads matches each thread of the Docs comments view to its entry
// in the file, by its text and time, which is how it is located. When
// the view was refused the entries are listed from the file alone, and
// the warning says so.
func mdThreads(f *mdfile.File, view *gapi.DocumentResult, viewErr error) ([]mdThread, string) {
	entries := f.Comments()
	var out []mdThread
	if viewErr != nil {
		for _, e := range entries {
			out = append(out, entryThread(f, e))
		}
		return out, "the Docs comments view was refused (" + messageOf(wrapAPI(viewErr, "file")) + "), so comments are listed from the file, their authors as it names them"
	}
	used := make([]bool, len(entries))
	for _, t := range viewThreads(view.Document) {
		th := mdThread{CommentThread: t}
		if i := matchEntry(entries, used, t); i >= 0 {
			used[i] = true
			th.key = entries[i].Key
			locate(&th, f, entries[i])
		}
		out = append(out, th)
	}
	// An entry, or a marker pair, the view does not list is still in the
	// file and still marked in a read, so it is listed from the file.
	for i, e := range entries {
		if !used[i] && e.Start >= 0 {
			out = append(out, entryThread(f, e))
		}
	}
	return out, ""
}

func entryThread(f *mdfile.File, e mdfile.Comment) mdThread {
	author := e.Author
	if !e.Token {
		author = authorFromFile(author)
	}
	t := mdThread{key: e.Key, CommentThread: CommentThread{ID: e.Key, Author: author, Content: e.Body, Quote: e.Quote,
		Resolved: e.Status == "RESOLVED", Created: e.Time, Replies: []CommentReply{}}}
	locate(&t, f, e)
	return t
}

func locate(t *mdThread, f *mdfile.File, e mdfile.Comment) {
	if e.Start < 0 {
		return
	}
	t.Anchored, t.Handle = true, "L"+strconv.Itoa(f.LineOf(e.Start))
	t.Start, t.End = int64(e.Start), int64(e.End)
}

// matchEntry finds the file's entry for a Docs thread: the same head
// text and quote, or, for a thread Google stores, the same time.
func matchEntry(entries []mdfile.Comment, used []bool, t CommentThread) int {
	quote := doc.Normalize(t.Quote)
	for i, e := range entries {
		if !used[i] && e.Body == t.Content && doc.Normalize(e.Quote) == quote {
			return i
		}
	}
	for i, e := range entries {
		if !used[i] && e.Time != "" && e.Time == t.Created {
			return i
		}
	}
	return -1
}

// mdCommentFooter lists the threads a read marked, in the footer the
// Docs path writes, and counts the rest.
func mdCommentFooter(threads []mdThread, start, end int, warn string) string {
	plain := make([]CommentThread, len(threads))
	for i, t := range threads {
		plain[i] = t.CommentThread
	}
	var in []render.Mark
	other := 0
	for _, mk := range commentMarks(plain) {
		if mk.Thread.Handle != "" && int(mk.End) >= start && int(mk.End) <= end {
			in = append(in, mk)
			continue
		}
		other++
	}
	out := render.CommentFooter(in, other)
	if warn != "" {
		out += "\nwarning: " + warn
	}
	return out
}

// mdListComments is list_comments for a Markdown file.
func (s *Service) mdListComments(ctx context.Context, req ListCommentsRequest) (*CommentsResult, error) {
	m, threads, warn, err := s.mdWithComments(ctx, req.Document, true, req.IncludeDeleted)
	if err != nil {
		return nil, err
	}
	plain := make([]CommentThread, len(threads))
	for i, t := range threads {
		plain[i] = t.CommentThread
	}
	res := tallyThreads(plain, m.revision(), req.HideResolved)
	res.Text += "\nOn a Markdown file, comments made in the Docs editor live in the file itself and Drive cannot see them: they can be read here, not replied to, resolved or deleted through any API. " +
		"Comments made through Drive's API (unanchored) are listed too; the Docs editor does not show those."
	if warn != "" {
		res.Text += "\nwarning: " + warn
	}
	return res, nil
}

// mdFind is find_in_document for a Markdown file: lines instead of
// handles.
func (s *Service) mdFind(ctx context.Context, req FindRequest) (*FindResult, error) {
	if err := noTabOrSegment(req.Tab, req.Segment); err != nil {
		return nil, err
	}
	m, err := s.fetchMarkdown(ctx, req.Document, nil)
	if err != nil {
		return nil, err
	}
	limit, contextChars := findBounds(req)
	text := m.md.Text()
	var spans []mdfile.Span
	if req.Regex {
		re, err := compileFind(req.Query, req.MatchCase)
		if err != nil {
			return nil, err
		}
		for _, loc := range re.FindAllStringIndex(text, -1) {
			if loc[0] < loc[1] {
				spans = append(spans, mdfile.Span{Start: loc[0], End: loc[1]})
			}
		}
	} else {
		spans = m.md.FindAll(req.Query, req.MatchCase)
	}
	res := &FindResult{Query: req.Query, RevisionID: m.revision(), Matches: []FindMatch{}, Total: len(spans)}
	for _, sp := range spans {
		if len(res.Matches) == limit {
			res.Truncated = true
			break
		}
		line := m.md.LineOf(sp.Start)
		res.Matches = append(res.Matches, FindMatch{Handle: "L" + strconv.Itoa(line), Match: text[sp.Start:sp.End],
			Offset: utf8.RuneCountInString(text[m.md.LineStart(line):sp.Start]), Context: doc.OneLine(snippet(text, sp.Start, sp.End, contextChars))})
	}
	res.Text = findText(res, "the Markdown file")
	return res, nil
}

// mdExport is export_document for a Markdown file: the file is already
// markdown, and Drive converts a blob to nothing else.
func (s *Service) mdExport(ctx context.Context, req ExportRequest, format string) (*ExportResult, error) {
	if format != "md" && format != "txt" {
		return nil, mdUnsupported("Drive exports only Google's own formats and converts a Markdown file to nothing", "md and txt return the file's text")
	}
	m, err := s.fetchMarkdown(ctx, req.Document, nil)
	if err != nil {
		return nil, err
	}
	limit := req.MaxChars
	if limit <= 0 {
		limit = DefaultMaxChars
	}
	text := m.md.Text()
	res := &ExportResult{Format: format, MimeType: gapi.MarkdownMimeType, Inline: true, Bytes: len(text)}
	res.Text, res.Truncated = clipUTF8(text, limit)
	return res, nil
}

// mdWhole is the gdocs:// resource for a Markdown file.
func (s *Service) mdWhole(ctx context.Context, ref string, maxChars int) (*WholeResult, error) {
	m, err := s.fetchMarkdown(ctx, ref, nil)
	if err != nil {
		return nil, err
	}
	text := m.md.Text()
	res := &WholeResult{Title: m.title(), RevisionID: m.revision(), Tabs: 1, Text: text}
	if maxChars > 0 {
		if end := m.md.Cut(0, maxChars); end < len(text) {
			res.Text, res.Truncated = text[:end]+fmt.Sprintf("\n\n<!-- truncated; continue with read_document continue_from %s -->", continueFrom(m.md, end)), true
		}
	}
	return res, nil
}

// mdRevisionText is one revision's visible text: an old one from Drive's
// revision download, the current one from the file.
func (s *Service) mdRevisionText(ctx context.Context, id, revision string) (string, error) {
	var data []byte
	var err error
	if revision == "" {
		data, err = s.api.DownloadFile(ctx, id)
	} else {
		data, err = s.api.DownloadRevision(ctx, id, revision)
	}
	if err != nil {
		return "", wrapAPI(err, "revision "+cmp.Or(revision, "current"))
	}
	return mdfile.Parse(string(data)).Text(), nil
}

// mdDiff is diff_revisions for a Markdown file: the files' own text at
// two revisions, comment markup left out, downloaded together.
func (s *Service) mdDiff(ctx context.Context, id string, req DiffRequest, from, to string) (*DiffResult, error) {
	type got struct {
		text string
		err  error
	}
	oldCh := make(chan got, 1)
	go func() {
		text, err := s.mdRevisionText(ctx, id, from)
		oldCh <- got{text, err}
	}()
	newText, err := s.mdRevisionText(ctx, id, to)
	old := <-oldCh
	switch {
	case old.err != nil:
		return nil, old.err
	case err != nil:
		return nil, err
	}
	return diffResult(old.text, newText, from, cmp.Or(to, "current"), "md", req), nil
}

// mdReadRevision is read_document with revision on a Markdown file.
func (s *Service) mdReadRevision(ctx context.Context, id, revision string, maxChars int) (*ReadResult, error) {
	text, err := s.mdRevisionText(ctx, id, revision)
	if err != nil {
		return nil, err
	}
	res := &ReadResult{Revision: revision, Scope: "revision " + revision + " of the Markdown file (its text; no revision_id)", Segment: "body"}
	res.Text, res.Truncated = clipUTF8(text, maxChars)
	res.Chars = len(res.Text)
	res.Text = res.header() + res.Text
	return res, nil
}
