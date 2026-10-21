package service

import (
	"context"
	"crypto/md5" //nolint:gosec // Drive's md5Checksum is MD5; this compares with it, nothing more
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mmedum/google-docs-mcp/v2/internal/config"
	"github.com/mmedum/google-docs-mcp/v2/internal/doc"
	"github.com/mmedum/google-docs-mcp/v2/internal/gapi"
	"github.com/mmedum/google-docs-mcp/v2/internal/mdfile"
	"github.com/mmedum/google-docs-mcp/v2/internal/plan"
	"github.com/mmedum/google-docs-mcp/v2/internal/render"
)

// A Markdown file that Docs opens in place stays a text/markdown blob:
// the Docs API reads it and refuses every write, and Drive serves and
// replaces its bytes. Such a file is read from its bytes and edited as
// the text it is, with the comment markup Docs keeps in it guarded
// (architecture §7.5).

const (
	// mdMaxWrite is the largest file written: Drive's ceiling for a
	// simple upload is 5 MB.
	mdMaxWrite = 5 << 20
	// mdMaxRead is the largest file read.
	mdMaxRead = 20 << 20
)

// IsMarkdownMessage opens every refusal of an op a Markdown file cannot
// take, so the model learns why once.
const IsMarkdownMessage = "this is a Markdown file, which Docs opens in place and the Docs API cannot write; it is edited as its own text"

// markupRefused is why content holding Docs' comment markup is refused.
const markupRefused = "the content holds Docs' comment markup (a gdoc_comment marker, or a line starting gdoc_comments:), which would forge or end a comment"

func tooLarge(n int) error {
	return Errorf("unsupported", "%d bytes is over the %d this server uploads, Drive's ceiling for a simple upload", n, mdMaxWrite)
}

// mdFile is a Markdown file as one read saw it.
type mdFile struct {
	meta *gapi.File
	md   *mdfile.File
	// warnings are what the read noticed: front matter, a file Drive
	// changed while it was being read.
	warnings []string
}

func (m *mdFile) id() string { return m.meta.ID }

func (m *mdFile) title() string {
	if m.meta.Name != "" {
		return m.meta.Name
	}
	return m.meta.ID
}

func (m *mdFile) url() string {
	if m.meta.WebViewLink != "" {
		return m.meta.WebViewLink
	}
	return doc.FileURL(m.meta.ID)
}

// mime is the type an upload to the file is sent as: its own, so that a
// text/x-markdown file is not retyped, since Drive makes the uploaded
// type the file's. fetchMarkdown refuses any other type, so this is one
// of the two.
func (m *mdFile) mime() string { return m.meta.MimeType }

// revision is what a Markdown file's revision_id is: Drive's head
// revision, which every upload moves.
func (m *mdFile) revision() string { return m.meta.HeadRevisionID }

// kind asks Drive once per id per session what type a document is, and
// returns the metadata when this call is the one that asked, for a caller
// that would fetch it anyway. Drive refusing the id is remembered as a
// Google Doc, which the Docs API then answers for; a failure that may
// pass is not remembered.
func (s *Service) kind(ctx context.Context, ref string) (string, *gapi.File) {
	id, err := parseRef(ref)
	if err != nil {
		return "", nil
	}
	s.mu.Lock()
	k, ok := s.kinds[id]
	s.mu.Unlock()
	if ok && (k.until.IsZero() || s.now().Before(k.until)) {
		return k.mime, nil
	}
	file, err := s.api.GetFile(ctx, id)
	switch {
	case err == nil && file != nil:
		s.rememberKind(id, file.MimeType)
		return file.MimeType, file
	case errors.Is(err, gapi.ErrNotFound) || errors.Is(err, gapi.ErrForbidden):
		s.rememberKind(id, gapi.DocumentMimeType)
	default:
		// Drive may be failing for a while; every call paying a failing
		// round trip, retries included, before the Docs API is asked
		// would make that the Docs API's outage too.
		s.mu.Lock()
		s.kinds[id] = kindMemo{mime: gapi.DocumentMimeType, until: s.now().Add(kindRetryAfter)}
		s.mu.Unlock()
	}
	s.log.DebugContext(ctx, "could not ask Drive for the file's type; reading it as a Google Doc", "class", gapi.Class(err))
	return gapi.DocumentMimeType, nil
}

// kindMemo is what a session remembers of a document's type: its Drive
// type, and, for a guess made because Drive could not be asked, until
// when.
type kindMemo struct {
	mime  string
	until time.Time
}

// kindRetryAfter is how long a guess stands before Drive is asked again.
const kindRetryAfter = time.Minute

// isMarkdown is kind for a caller that only branches on it. A reference
// that does not parse is not a Markdown file; the path it falls through
// to says what is wrong with it.
func (s *Service) isMarkdown(ctx context.Context, ref string) bool {
	mime, _ := s.kind(ctx, ref)
	return gapi.IsMarkdownMimeType(mime)
}

// wordUnsupported refuses a Docs API write to a Word file, which Google
// reads like a Google Doc and refuses every write to (spike G); nil for
// any other document.
func (s *Service) wordUnsupported(ctx context.Context, ref string) error {
	if mime, _ := s.kind(ctx, ref); mime == gapi.WordMimeType {
		return Errorf("unsupported", "%s", wordFileNote)
	}
	return nil
}

// wordFileNote is what a person is told about a Word file.
const wordFileNote = "this is a Word file (.docx): it reads like a Google Doc, but Google refuses every edit to an " +
	"Office file, directly or as a suggestion; to edit it, save it as a Google Doc (File > Save as Google Docs) and edit that"

func (s *Service) rememberKind(id string, mime string) {
	s.mu.Lock()
	s.kinds[id] = kindMemo{mime: mime}
	s.mu.Unlock()
}

// fetchMarkdown reads a Markdown file: its metadata, then its bytes,
// checked against the metadata's checksum so that the two describe one
// state of the file. A file that moves in between is read once more.
// meta, when given, is metadata this call fetched a moment ago.
func (s *Service) fetchMarkdown(ctx context.Context, ref string, meta *gapi.File) (*mdFile, error) {
	id, err := parseRef(ref)
	if err != nil {
		return nil, err
	}
	var m *mdFile
	for attempt := range 2 {
		if meta == nil || attempt > 0 {
			if meta, err = s.api.GetFile(ctx, id); err != nil {
				return nil, wrapAPI(err, "file")
			}
		}
		if !gapi.IsMarkdownMimeType(meta.MimeType) {
			// Retyped since this session asked, by an upload sent as
			// another type: the Docs API refuses a text/plain .md.
			s.rememberKind(id, meta.MimeType)
			return nil, Errorf("unsupported", "the file is %s now, not a Markdown file; Docs opens a .md file in place only while it is typed %s or %s", meta.MimeType, gapi.MarkdownMimeType, gapi.LegacyMarkdownMimeType)
		}
		if size, _ := strconv.ParseInt(meta.Size, 10, 64); size > mdMaxRead {
			return nil, Errorf("unsupported", "the Markdown file is %d bytes; this server reads files up to %d", size, mdMaxRead)
		}
		data, err := s.api.DownloadFile(ctx, id)
		if err != nil {
			return nil, wrapAPI(err, "file")
		}
		m = &mdFile{meta: meta, md: mdfile.Parse(string(data))}
		if meta.MD5Checksum == "" || md5Hex(data) == meta.MD5Checksum {
			break
		}
		if attempt == 1 {
			m.warnings = append(m.warnings, "the file changed while it was being read, so its revision id may be older than its text; read it again before relying on either")
		}
	}
	switch fm := m.md.FrontMatter(); {
	case fm.Present:
		m.warnings = append(m.warnings, "the file starts with front matter, which the Docs editor rewrites as a rule and a heading when a person saves the file there")
	case fm.Mangled:
		m.warnings = append(m.warnings, "the file opens the way the Docs editor rewrites front matter (a rule, then a heading holding \"key: value\"); an earlier revision may still have it intact (list_revisions, read_document revision)")
	}
	return m, nil
}

func md5Hex(data []byte) string {
	sum := md5.Sum(data) //nolint:gosec // compared with Drive's md5Checksum
	return hex.EncodeToString(sum[:])
}

// mdUnsupported refuses something a Markdown file cannot do.
func mdUnsupported(what, instead string) error {
	msg := IsMarkdownMessage + "; " + what
	if instead != "" {
		msg += "; " + instead
	}
	return Errorf("unsupported", "%s", msg)
}

// noHandles is the refusal of a block handle in a Markdown file.
func noHandles() error {
	return mdUnsupported("it has no block handles", "target exact text, heading_id or heading, and continue a long read with continue_from L<line>")
}

// noTabOrSegment refuses the tab and segment a Google Doc has.
func noTabOrSegment(tab, segment string) error {
	switch {
	case tab != "":
		return mdUnsupported("it has no tabs", "leave tab out")
	case segment != "" && segment != "body":
		return mdUnsupported("it has no headers, footers or footnote segments", "its footnotes are text in the body")
	}
	return nil
}

// mdInfo is get_document for a Markdown file.
func (s *Service) mdInfo(ctx context.Context, ref string, meta *gapi.File) (*Info, error) {
	m, err := s.fetchMarkdown(ctx, ref, meta)
	if err != nil {
		return nil, err
	}
	text := m.md.Text()
	comments := 0
	for _, c := range m.md.Comments() {
		if c.Start >= 0 {
			comments++
		}
	}
	info := &Info{ID: m.id(), Title: m.title(), URL: m.url(), RevisionID: m.revision(), Tabs: []TabInfo{}, Kind: KindMarkdownFile,
		Bytes: len(m.md.Source()), Stats: doc.Stats{Headings: len(m.md.Headings()), Words: doc.WordCount(text)}, Warnings: m.warnings}
	info.fromFile(m.meta)
	info.Capabilities = s.mdCapabilities(m)
	var b strings.Builder
	info.writeIdentity(&b, " (Drive's head revision; it moves on every save and upload)")
	fmt.Fprintf(&b, "Markdown file (%s), %d bytes: Docs opens it in place, and this server reads and writes it as its own text through Drive\n", m.mime(), info.Bytes)
	fmt.Fprintf(&b, "%d headings, %d words, %d comment(s) made in the Docs editor\n", info.Stats.Headings, info.Stats.Words, comments)
	info.writeServer(&b)
	if c := info.Capabilities; c.DefaultWriteMode == string(config.WriteSuggest) && len(c.WriteModes) > 0 {
		b.WriteString("a Markdown file takes no suggestions, so a write here needs mode direct, which changes the file's text\n")
	}
	writeWarnings(&b, info.Warnings)
	info.Text = strings.TrimRight(b.String(), "\n")
	return info, nil
}

// mdCapabilities is what this server can do with a Markdown file.
func (s *Service) mdCapabilities(m *mdFile) Capabilities {
	c := Capabilities{DefaultWriteMode: string(s.opts.DefaultWriteMode), ReadOnly: s.opts.ReadOnly, WriteModes: []string{}}
	if !s.opts.ReadOnly && mdWritable(m) == nil {
		c.WriteModes = append(c.WriteModes, string(config.WriteDirect))
	}
	return c
}

// mdWritable says why the file cannot be written, or nil.
func mdWritable(m *mdFile) error {
	if c := m.meta.Capabilities; c != nil && !c.CanModifyContent {
		return Errorf("forbidden", "this account cannot change the file's content (Drive says it may not modify it)")
	}
	for _, r := range m.meta.ContentRestrictions {
		if r != nil && r.ReadOnly {
			msg := "the file's content is locked in Drive"
			if r.Reason != "" {
				msg += ": " + r.Reason
			}
			return Errorf("forbidden", "%s", msg)
		}
	}
	if n := len(m.md.Source()); n > mdMaxWrite {
		return tooLarge(n)
	}
	return nil
}

// mdOutline is get_outline for a Markdown file.
func (s *Service) mdOutline(ctx context.Context, ref, tab string) (*OutlineResult, error) {
	if err := noTabOrSegment(tab, ""); err != nil {
		return nil, err
	}
	m, err := s.fetchMarkdown(ctx, ref, nil)
	if err != nil {
		return nil, err
	}
	text := m.md.Text()
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\nMarkdown file, revision %s: %d lines, %d words\n", m.title(), m.revision(), mdfile.Lines(text), doc.WordCount(text))
	heads := m.md.Headings()
	if len(heads) > 0 && heads[0].Start > m.md.FrontMatter().End {
		fmt.Fprintf(&b, "- (preamble: lines 1–%d before the first heading)\n", heads[0].Line-1)
	}
	if len(heads) == 0 {
		b.WriteString("- (no headings)\n")
	}
	for _, h := range heads {
		fmt.Fprintf(&b, "%s- [L%d] H%d %s {%s} — %d words\n", strings.Repeat("  ", max(h.Level-1, 0)), h.Line, h.Level, h.Text, h.ID, doc.WordCount(text[h.Start:h.End]))
	}
	writeWarnings(&b, m.warnings)
	return &OutlineResult{Text: strings.TrimRight(b.String(), "\n"), RevisionID: m.revision()}, nil
}

// mdSpan is a resolved target: a range of the visible text. A block
// span covers whole lines, a section or the whole text, and content
// placed there stands as blocks of its own.
type mdSpan struct {
	start, end int
	block      bool
	desc       string
}

// pick chooses one of n hits, n > 0, by occurrence counted from 1.
// Several without an occurrence are ambiguous.
func pick(n, occurrence int, what string, ambiguous func() error) (int, error) {
	switch {
	case occurrence > n:
		return 0, Errorf("not_found", "%s occurs %d time(s), not %d", what, n, occurrence)
	case occurrence > 0:
		return occurrence - 1, nil
	case n > 1:
		return 0, ambiguous()
	}
	return 0, nil
}

// mdSection resolves a heading_id, or a heading's text and level, to its
// section; include false leaves the heading out.
func mdSection(f *mdfile.File, headingID, heading string, level, occurrence int, include *bool) (mdSpan, error) {
	var h mdfile.Heading
	if headingID != "" {
		var ok bool
		if h, ok = f.Heading(headingID); !ok {
			return mdSpan{}, Errorf("not_found", "no heading %q; this file's are %s", headingID, headingIDs(f))
		}
	} else {
		want := doc.Normalize(heading)
		var hits []mdfile.Heading
		for _, c := range f.Headings() {
			if strings.EqualFold(doc.Normalize(c.Text), want) && (level == 0 || c.Level == level) {
				hits = append(hits, c)
			}
		}
		if len(hits) == 0 {
			return mdSpan{}, Errorf("not_found", "no heading %q; this file's are %s", heading, headingIDs(f))
		}
		i, err := pick(len(hits), occurrence, fmt.Sprintf("heading %q", heading), func() error {
			lines := make([]string, len(hits))
			for i, h := range hits {
				lines[i] = "line " + strconv.Itoa(h.Line)
			}
			return Errorf("ambiguous", "heading %q matches %d headings (%s); pass its heading_id or occurrence", heading, len(hits), strings.Join(lines, ", "))
		})
		if err != nil {
			return mdSpan{}, err
		}
		h = hits[i]
	}
	if include != nil && !*include {
		return mdSpan{start: h.BodyStart, end: h.End, block: true, desc: fmt.Sprintf("the text under %q", h.Text)}, nil
	}
	return mdSpan{start: h.Start, end: h.End, block: true, desc: fmt.Sprintf("section %q", h.Text)}, nil
}

func headingIDs(f *mdfile.File) string {
	hs := f.Headings()
	if len(hs) == 0 {
		return "none"
	}
	ids := make([]string, 0, min(len(hs), 13))
	for i, h := range hs {
		if i == 12 {
			ids = append(ids, fmt.Sprintf("and %d more (get_outline)", len(hs)-12))
			break
		}
		ids = append(ids, h.ID)
	}
	return strings.Join(ids, ", ")
}

// mdTarget resolves a write target in a Markdown file.
func mdTarget(f *mdfile.File, t Target) (mdSpan, error) {
	switch {
	case t.Handle != "" || t.From != "" || t.To != "":
		return mdSpan{}, noHandles()
	case t.Cell != "":
		return mdSpan{}, mdUnsupported("its tables are text, with no cells to address", "target the table's text")
	case t.NamedRange != "":
		return mdSpan{}, mdUnsupported("it has no named ranges", "target exact text, heading_id or heading")
	}
	if err := noTabOrSegment(t.Tab, t.Segment); err != nil {
		return mdSpan{}, err
	}
	if t.selectorCount() != 1 {
		return mdSpan{}, Errorf("invalid", "a target in a Markdown file is exactly one of text, heading_id or heading")
	}
	if t.Text == "" {
		return mdSection(f, t.HeadingID, t.Heading, t.HeadingLevel, t.Occurrence, t.IncludeHeading)
	}
	within, scope := mdfile.Span{}, "the file"
	if t.Within != "" {
		// A heading_id, or heading:<text> as a Google Doc takes it.
		id, text := t.Within, ""
		if rest, ok := strings.CutPrefix(t.Within, "heading:"); ok {
			id, text = "", rest
		}
		sec, err := mdSection(f, id, text, 0, 0, nil)
		if err != nil {
			return mdSpan{}, Errorf(classOf(err), "within %q: %s", t.Within, messageOf(err))
		}
		within, scope = mdfile.Span{Start: sec.start, End: sec.end}, sec.desc
	}
	hits := f.Find(t.Text, within)
	quoted := doc.Clip(doc.OneLine(t.Text), 60)
	if len(hits) == 0 {
		return mdSpan{}, Errorf("not_found", "%q is not in %s; quote it as a read shows it", quoted, scope)
	}
	i, err := pick(len(hits), t.Occurrence, fmt.Sprintf("%q in %s", quoted, scope), func() error {
		lines := make([]string, 0, min(len(hits), 5))
		for _, h := range hits[:min(len(hits), 5)] {
			lines = append(lines, strconv.Itoa(f.LineOf(h.Start)))
		}
		return Errorf("ambiguous", "%q matches %d times in %s (lines %s); add occurrence or within", quoted, len(hits), scope, strings.Join(lines, ", "))
	})
	if err != nil {
		return mdSpan{}, err
	}
	desc := fmt.Sprintf("%q", quoted)
	if t.Occurrence > 0 {
		desc = fmt.Sprintf("%q (occurrence %d)", quoted, t.Occurrence)
	}
	return mdSpan{start: hits[i].Start, end: hits[i].End, desc: desc}, nil
}

// mdLocation resolves where an insertion goes: the start or end of the
// file, or before or after a target. Before and after a section are
// between blocks; before and after text are inline.
func mdLocation(f *mdfile.File, loc Location) (mdSpan, error) {
	at := strings.ToLower(strings.TrimSpace(loc.At))
	if at == "" {
		at = "end"
		if loc.Of != nil {
			at = "after"
		}
	}
	if loc.Of == nil || loc.Of.selectorCount() == 0 {
		switch at {
		case "start":
			// Front matter has to stay first to be front matter.
			p := f.FrontMatter().End
			return mdSpan{start: p, end: p, block: true, desc: "start of the file"}, nil
		case "end":
			p := len(f.Text())
			return mdSpan{start: p, end: p, block: true, desc: "end of the file"}, nil
		}
		return mdSpan{}, Errorf("invalid", "location %q needs a target (of) to be before or after", at)
	}
	span, err := mdTarget(f, *loc.Of)
	if err != nil {
		return mdSpan{}, err
	}
	p := span.end
	switch at {
	case "before", "start":
		p = span.start
	case "after", "end":
	default:
		return mdSpan{}, Errorf("invalid", "location %q; use start, end, before or after", at)
	}
	return mdSpan{start: p, end: p, block: span.block, desc: at + " " + span.desc}, nil
}

// mdPlanned is a planned write to a Markdown file.
type mdPlanned struct {
	edits []mdfile.Edit
	opOf  []int // the op each edit came from
	// spans are what each op resolved to, a point for an insertion, which
	// is what two ops may not share.
	spans []opSpan
	// summary has one entry per op, in op order, so summary[seq] is op
	// seq's: the ops come numbered from 0.
	summary  []plan.OpSummary
	forced   []render.Forced
	warnings []string
}

type opSpan struct{ op, start, end int }

// mdOpKinds are the ops a Markdown file takes: the ones that change
// text. Any other is refused, whichever tool sent it.
var mdOpKinds = map[plan.OpKind]bool{plan.OpInsert: true, plan.OpAppend: true, plan.OpReplace: true, plan.OpDelete: true, plan.OpReplaceAll: true}

// mdPlan resolves every op against one read and turns it into edits of
// the visible text, then runs the guard over them.
func mdPlan(f *mdfile.File, ops []EditOp, force bool) (*mdPlanned, error) {
	p := &mdPlanned{}
	text := f.Text()
	for _, op := range ops {
		if !mdOpKinds[op.Kind] {
			return nil, mdUnsupported(fmt.Sprintf("op %d (%s) needs the Docs API", op.Seq, op.Kind),
				"write the change as markdown with insert, append, replace, delete or replace_all")
		}
		if mdfile.ContainsMarkup(op.Content) || mdfile.ContainsMarkup(op.Replace) {
			return nil, Errorf("invalid", "op %d: %s", op.Seq, markupRefused)
		}
		var edits []mdfile.Edit
		var desc string
		minimal := false
		switch op.Kind {
		case plan.OpInsert, plan.OpAppend:
			if strings.TrimSpace(op.Content) == "" {
				return nil, Errorf("invalid", "op %d: content is empty", op.Seq)
			}
			loc := Location{At: "end"}
			if op.Kind == plan.OpInsert {
				if op.Location == nil {
					return nil, Errorf("invalid", "op %d: insert needs a location", op.Seq)
				}
				loc = *op.Location
			}
			at, err := mdLocation(f, loc)
			if err != nil {
				return nil, opError(op.Seq, err)
			}
			desc = at.desc
			p.spans = append(p.spans, opSpan{op.Seq, at.start, at.start})
			if at.block {
				edits = []mdfile.Edit{f.BlockInsert(at.start, op.Content)}
			} else {
				edits = []mdfile.Edit{{Start: at.start, End: at.start, Insert: op.Content}}
			}
		case plan.OpReplace, plan.OpDelete:
			if op.Target == nil {
				return nil, Errorf("invalid", "op %d: %s needs a target", op.Seq, op.Kind)
			}
			span, err := mdTarget(f, *op.Target)
			if err != nil {
				return nil, opError(op.Seq, err)
			}
			desc = span.desc
			p.spans = append(p.spans, opSpan{op.Seq, span.start, span.end})
			content := ""
			if op.Kind == plan.OpReplace {
				content = op.Content
				if span.block {
					content = f.BlockReplace(span.start, span.end, content)
				}
			}
			edits, minimal = mdfile.MinimalEdits(text[span.start:span.end], content, span.start), op.Kind == plan.OpReplace
		case plan.OpReplaceAll:
			var err error
			if edits, desc, err = mdReplaceAll(f, op); err != nil {
				return nil, opError(op.Seq, err)
			}
			for _, e := range edits {
				p.spans = append(p.spans, opSpan{op.Seq, e.Start, e.End})
			}
		}
		p.summary = append(p.summary, plan.OpSummary{Seq: op.Seq, Kind: op.Kind, Description: desc, Minimal: minimal})
		for _, e := range edits {
			p.edits = append(p.edits, e)
			p.opOf = append(p.opOf, op.Seq)
		}
	}
	if err := p.checkOverlaps(); err != nil {
		return nil, err
	}
	return p, p.guard(f, force)
}

func opError(seq int, err error) error {
	var e *Error
	if errors.As(err, &e) {
		return &Error{Class: e.Class, Message: fmt.Sprintf("op %d: %s", seq, e.Message), Err: e.Err}
	}
	return err
}

// mdReplaceAll is replace_all over the visible text: plain text
// normalized as targets are, or an RE2 pattern held to the checks a
// Google Doc's regex replace_all is.
func mdReplaceAll(f *mdfile.File, op EditOp) ([]mdfile.Edit, string, error) {
	if op.Find == "" {
		return nil, "", Errorf("invalid", "find is empty")
	}
	text := f.Text()
	var edits []mdfile.Edit
	if op.Regex {
		re, err := compileFind(op.Find, true)
		if err != nil {
			return nil, "", err
		}
		if err := checkTemplate(re, op.Replace); err != nil {
			return nil, "", err
		}
		ms := re.FindAllStringSubmatchIndex(text, matchLimit+1)
		if len(ms) > matchLimit {
			return nil, "", Errorf("invalid", "%q matches more than %d times; narrow the pattern", op.Find, matchLimit)
		}
		for _, m := range ms {
			if m[0] == m[1] {
				return nil, "", Errorf("invalid", "%q matches empty text on line %d; a replacement needs text to replace", op.Find, f.LineOf(m[0]))
			}
			edits = append(edits, mdfile.MinimalEdits(text[m[0]:m[1]], string(re.ExpandString(nil, op.Replace, text, m)), m[0])...)
		}
	} else {
		hits := f.FindAll(op.Find, op.MatchCase)
		if len(hits) > matchLimit {
			return nil, "", Errorf("invalid", "%q matches more than %d times; narrow it", doc.Clip(op.Find, 40), matchLimit)
		}
		for _, h := range hits {
			edits = append(edits, mdfile.MinimalEdits(text[h.Start:h.End], op.Replace, h.Start)...)
		}
	}
	if len(edits) == 0 {
		return nil, "", Errorf("not_found", "%q is not in the file", doc.Clip(op.Find, 60))
	}
	return edits, fmt.Sprintf("every %q", doc.Clip(op.Find, 40)), nil
}

// checkOverlaps refuses two ops whose targets overlap, or an insertion
// inside another op's target, where the order of their changes would be
// a guess. Insertions at one point, or at the edge of another op's
// target, are not: they land in op order, and before what is replaced
// from that point (mdfile.Apply). A replace_all match counts as a target
// of its own.
func (p *mdPlanned) checkOverlaps() error {
	spans := slices.Clone(p.spans)
	sort.SliceStable(spans, func(i, j int) bool {
		a, b := spans[i], spans[j]
		if a.start != b.start {
			return a.start < b.start
		}
		return a.start == a.end && b.start < b.end
	})
	for k := 1; k < len(spans); k++ {
		a, b := spans[k-1], spans[k]
		if a.op != b.op && b.start < a.end {
			return Errorf("invalid", "ops %d and %d reach the same text; make them one op, or separate calls", min(a.op, b.op), max(a.op, b.op))
		}
	}
	return nil
}

// guard refuses a direct edit that would delete commented text, an
// image or a footnote, unless forced; forced, it records what goes.
func (p *mdPlanned) guard(f *mdfile.File, force bool) error {
	hitsByOp := map[int][]mdfile.Hit{}
	var order []int
	for i, e := range p.edits {
		op := p.opOf[i]
		for _, h := range f.Destroys(e.Start, e.End) {
			if _, ok := hitsByOp[op]; !ok {
				order = append(order, op)
			}
			if !slices.Contains(hitsByOp[op], h) {
				hitsByOp[op] = append(hitsByOp[op], h)
			}
		}
	}
	for _, op := range order {
		what, sum := describeHits(f, hitsByOp[op]), p.summary[op]
		if !force {
			return Errorf("blocked", "op %d (%s of %s) would change %s; narrow the target, or pass force: true if the person chose to", op, sum.Kind, sum.Description, what)
		}
		p.warnings = append(p.warnings, fmt.Sprintf("op %d: forced; it changes %s", op, what))
		p.forced = append(p.forced, render.Forced{Seq: op, Kind: string(sum.Kind), Description: sum.Description, Destroys: what})
	}
	return nil
}

// describeHits names what a deletion touches, by what a person would
// recognize: a comment by its passage, an image or footnote by its text.
func describeHits(f *mdfile.File, hs []mdfile.Hit) string {
	parts := make([]string, 0, len(hs))
	for _, h := range hs {
		if h.Kind == "comment" {
			parts = append(parts, fmt.Sprintf("the text of a comment on %q", doc.Clip(doc.OneLine(f.Passage(h.ID)), 50)))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %s", h.Kind, doc.Clip(h.ID, 60)))
	}
	return strings.Join(parts, ", ")
}
