package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-docs-mcp/v2/internal/config"
	"github.com/mmedum/google-docs-mcp/v2/internal/gapi"
	"github.com/mmedum/google-docs-mcp/v2/internal/gdocs"
	"github.com/mmedum/google-docs-mcp/v2/internal/mdfile"
	"github.com/mmedum/google-docs-mcp/v2/internal/plan"
	"github.com/mmedum/google-docs-mcp/v2/internal/render"
)

// mdNotes is a synthetic Markdown file in the shape Docs leaves one that
// holds a comment made in its editor: markers around the passage and the
// gdoc_comments block ending the file.
const mdNotes = "# Notes\n\nIntro with **bold** text.\n\n" +
	"## Plan\n\nWe <!-- gdoc_comment_begin: c1 -->ship in May<!-- gdoc_comment_end: c1 -->.\n\n" +
	"![Chart](https://example.com/c.png)\n\n" +
	"## Risks\n\nNone yet.\n\n" + mdBlock

// mdBlock is the block that ends mdNotes.
const mdBlock = "---\ngdoc_comments:\n  c1:\n    status: ACTIVE\n    token: AbC123opaque\n    head:\n" +
	"      author: Ada Example\n      time: '2026-01-02T03:04:05.000Z'\n      body: is May real?\n" +
	"      quote: |-\n        ship in May\n---\n"

// mdService is a service over the Markdown file, with the Docs view of
// it listing the comment the file holds.
func mdService(t *testing.T, mode config.WriteMode) (*Service, *fakeAPI) {
	t.Helper()
	text := mdNotes
	view, err := json.Marshal(gdocs.Document{DocumentID: fixtureID, Comments: []gdocs.CommentThread{{
		CommentID: "AAAeditor", Status: "OPEN", PlainTextQuote: "ship in May",
		HeadPost: gdocs.Post{PostID: "AAAeditor", Content: "is May real?", CreateTime: "2026-01-02T03:04:05.000Z",
			Author: gdocs.PostAuthor{DisplayName: "Ada Example", User: "users/1"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	api := &fakeAPI{raw: view, md: &text, mdOldRevs: map[string]string{"rev0": "# Notes\n\nIntro.\n"}}
	return New(api, Options{DefaultWriteMode: mode, Sleep: noSleep}), api
}

// noSleep makes a write's waits for the editor instant.
func noSleep(context.Context, time.Duration) error { return nil }

func TestMarkdownFileInfo(t *testing.T) {
	svc, api := mdService(t, config.WriteSuggest)
	info, err := svc.Info(context.Background(), fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	if info.Kind != KindMarkdownFile || info.RevisionID != "rev1" || info.Bytes != len(mdNotes) || info.Stats.Headings != 3 ||
		strings.Join(info.Capabilities.WriteModes, ",") != "direct" || info.URL != "https://drive.google.com/file/d/"+fixtureID+"/view" {
		t.Errorf("Info = %+v", info)
	}
	for _, want := range []string{"Markdown file (text/markdown)", "1 comment(s) made in the Docs editor", "takes no suggestions, so a write here needs mode direct"} {
		if !strings.Contains(info.Text, want) {
			t.Errorf("Info text lacks %q:\n%s", want, info.Text)
		}
	}
	// A file this account may not change offers no write mode.
	api.file = &gapi.File{Capabilities: &gapi.FileCapabilities{CanEdit: false}}
	if info, _ := New(api, Options{}).Info(context.Background(), fixtureID); len(info.Capabilities.WriteModes) != 0 {
		t.Errorf("unwritable file offers %v", info.Capabilities.WriteModes)
	}
	// A Google Doc says it is one.
	doc, _ := newService(t)
	if info, err := doc.Info(context.Background(), fixtureID); err != nil || info.Kind != KindDocument {
		t.Errorf("a Google Doc's Info: %+v, %v", info, err)
	}
}

func TestMarkdownFileUnderTheOlderType(t *testing.T) {
	// Drive keeps a .md uploaded as text/x-markdown under that type, and
	// the Docs API reads it as Markdown and refuses its writes the same
	// way (§18). It is a Markdown file here, and an edit keeps its type.
	svc, api := mdService(t, config.WriteSuggest)
	api.file = &gapi.File{MimeType: "text/x-markdown"}
	ctx := context.Background()
	info, err := svc.Info(ctx, fixtureID)
	if err != nil || info.Kind != KindMarkdownFile {
		t.Fatalf("Info = %+v, %v; want a markdown_file", info, err)
	}
	if !strings.Contains(info.Text, "Markdown file (text/x-markdown)") {
		t.Errorf("get_document names another type:\n%s", info.Text)
	}
	res, err := svc.Edit(ctx, EditRequest{Document: fixtureID, Mode: "direct", Ops: []EditOp{
		{Kind: plan.OpReplace, Target: &Target{Text: "**bold**"}, Content: "**strong**"},
	}})
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if len(api.uploads) != 1 || !strings.Contains(api.uploads[0], "**strong**") {
		t.Errorf("uploads = %q, want one carrying the edit", api.uploads)
	}
	if w := strings.Join(res.Warnings, "\n"); strings.Contains(w, "Drive typed the file") {
		t.Errorf("an upload that kept the file's type warns it was retyped: %s", w)
	}
}

func TestSearchFindsAMarkdownFileUnderTheOlderType(t *testing.T) {
	svc, api := mdService(t, config.WriteSuggest)
	api.file = &gapi.File{MimeType: "text/x-markdown"}
	api.searchFiles = []*gapi.File{{ID: fixtureID, Name: "notes.md", MimeType: "text/x-markdown"}}
	ctx := context.Background()
	res, err := svc.Search(ctx, SearchRequest{Kind: "any"})
	if err != nil || len(res.Hits) != 1 || res.Hits[0].Kind != KindMarkdownFile {
		t.Fatalf("Search = %+v, %v; want one markdown_file hit", res, err)
	}
	// The hit's kind is remembered for the session, so a read of it takes
	// the Markdown path without asking Drive again.
	info, err := svc.Info(ctx, fixtureID)
	if err != nil || info.Kind != KindMarkdownFile {
		t.Fatalf("Info after the search = %+v, %v; want a markdown_file", info, err)
	}
}

func TestMarkdownFileReadIsItsText(t *testing.T) {
	svc, _ := mdService(t, config.WriteDirect)
	ctx := context.Background()
	res, err := svc.Read(ctx, ReadRequest{Document: fixtureID})
	if err != nil {
		t.Fatal(err)
	}
	if res.RevisionID != "rev1" || strings.Contains(res.Text, "gdoc_comment") || !strings.Contains(res.Text, "We ship in May.\n\n![Chart]") ||
		!strings.HasPrefix(res.Text, "<!-- Markdown file, the whole file · revision rev1 · ") {
		t.Errorf("whole read:\n%s", res.Text)
	}
	sec, err := svc.Read(ctx, ReadRequest{Document: fixtureID, Scope: ReadScope{HeadingID: "plan"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := "## Plan\n\nWe ship in May.\n\n![Chart](https://example.com/c.png)\n\n"; !strings.HasSuffix(sec.Text, want) {
		t.Errorf("section read = %q, want it to end %q", sec.Text, want)
	}
	// A budget stops at a line and says where to go on.
	// The first four lines are 36 bytes, the fifth is "## Plan".
	cut, err := svc.Read(ctx, ReadRequest{Document: fixtureID, Options: render.Options{MaxChars: 40}})
	if err != nil {
		t.Fatal(err)
	}
	if !cut.Truncated || cut.ContinueFrom != "L5" || !strings.HasSuffix(cut.Text, "-->\n# Notes\n\nIntro with **bold** text.\n\n") {
		t.Errorf("budgeted read: %+v", cut)
	}
	rest, err := svc.Read(ctx, ReadRequest{Document: fixtureID, Scope: ReadScope{ContinueFrom: cut.ContinueFrom}})
	if err != nil || !strings.Contains(rest.Text, "-->\n## Plan\n") {
		t.Errorf("continued read: %v\n%s", err, rest.Text)
	}
	for _, sc := range []ReadScope{{FromHandle: "p1"}, {Tab: "t.0"}, {Segment: "header"}} {
		if _, err := svc.Read(ctx, ReadRequest{Document: fixtureID, Scope: sc}); classOf(err) != "unsupported" {
			t.Errorf("scope %+v: %v, want unsupported", sc, err)
		}
	}
	if _, err := svc.Read(ctx, ReadRequest{Document: fixtureID, Format: FormatRaw}); classOf(err) != "unsupported" {
		t.Errorf("raw: %v, want unsupported", err)
	}
}

func TestMarkdownFileReadShowsComments(t *testing.T) {
	svc, _ := mdService(t, config.WriteDirect)
	res, err := svc.Read(context.Background(), ReadRequest{Document: fixtureID, IncludeComments: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "We ship in May{>>c:AAAeditor<<}.") || !strings.Contains(res.Text, "- c:AAAeditor [L7] by Ada Example: is May real?") {
		t.Errorf("read with comments:\n%s", res.Text)
	}
}

func TestMarkdownFileOutline(t *testing.T) {
	svc, _ := mdService(t, config.WriteDirect)
	res, err := svc.Outline(context.Background(), fixtureID, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"- [L1] H1 Notes {notes}", "  - [L5] H2 Plan {plan}", "  - [L11] H2 Risks {risks}"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("outline lacks %q:\n%s", want, res.Text)
		}
	}
}

func TestMarkdownFileEditUploadsAMinimalChange(t *testing.T) {
	svc, api := mdService(t, config.WriteSuggest)
	res, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Mode: "direct", Ops: []EditOp{
		{Kind: plan.OpReplace, Target: &Target{Text: "**bold**"}, Content: "**strong**"},
		{Kind: plan.OpAppend, Content: "## Next\n\nMore."},
		{Kind: plan.OpInsert, Location: &Location{At: "before", Of: &Target{HeadingID: "risks"}}, Content: "Budget: small."},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := "# Notes\n\nIntro with **strong** text.\n\n" +
		"## Plan\n\nWe <!-- gdoc_comment_begin: c1 -->ship in May<!-- gdoc_comment_end: c1 -->.\n\n" +
		"![Chart](https://example.com/c.png)\n\nBudget: small.\n\n" +
		"## Risks\n\nNone yet.\n\n## Next\n\nMore.\n\n" +
		mdBlock
	if len(api.uploads) != 1 || api.uploads[0] != want {
		t.Fatalf("uploads = %q\nwant one of %q", api.uploads, want)
	}
	if res.RevisionID != "rev2" || res.Applied != 3 || !strings.Contains(strings.Join(res.Warnings, "\n"), "Drive revision rev1") {
		t.Errorf("result: %+v", res)
	}
}

func TestMarkdownFileModes(t *testing.T) {
	svc, api := mdService(t, config.WriteSuggest)
	op := []EditOp{{Kind: plan.OpAppend, Content: "x"}}
	for _, mode := range []string{"", "suggest"} {
		if _, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Mode: mode, Ops: op}); classOf(err) != "unavailable" {
			t.Errorf("mode %q: %v, want unavailable", mode, err)
		}
	}
	if len(api.uploads) != 0 {
		t.Errorf("a refused mode uploaded %d time(s)", len(api.uploads))
	}
}

func TestMarkdownFileGuard(t *testing.T) {
	svc, api := mdService(t, config.WriteDirect)
	inComment := []EditOp{{Kind: plan.OpReplace, Target: &Target{Text: "ship in May"}, Content: "ship in June"}}
	_, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Ops: inComment})
	if classOf(err) != "blocked" || !strings.Contains(err.Error(), `the text of a comment on "ship in May"`) {
		t.Errorf("edit inside a comment: %v", err)
	}
	image := []EditOp{{Kind: plan.OpDelete, Target: &Target{Text: "![Chart](https://example.com/c.png)"}}}
	if _, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Ops: image}); classOf(err) != "blocked" {
		t.Errorf("deleting an image: %v", err)
	}
	if len(api.uploads) != 0 {
		t.Fatalf("a blocked edit uploaded")
	}
	// Forced, the person is asked and the markers stay around the new text.
	if _, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Ops: inComment, Force: true}); err == nil {
		t.Error("a forced edit with no way to ask went ahead")
	}
	res, err := svc.Edit(accepting(context.Background()), EditRequest{Document: fixtureID, Ops: inComment, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(api.uploads[0], "<!-- gdoc_comment_begin: c1 -->ship in June<!-- gdoc_comment_end: c1 -->") || !strings.Contains(strings.Join(res.Warnings, "\n"), "forced") {
		t.Errorf("forced edit: %q, %v", api.uploads, res.Warnings)
	}
}

func TestMarkdownFileRefusesWhatItCannotTake(t *testing.T) {
	svc, api := mdService(t, config.WriteDirect)
	tests := []struct {
		name  string
		ops   []EditOp
		class string
	}{
		{"formatting", []EditOp{{Kind: plan.OpTextStyle, Target: &Target{Text: "Notes"}}}, "unsupported"},
		{"a handle", []EditOp{{Kind: plan.OpDelete, Target: &Target{Handle: "p1"}}}, "unsupported"},
		{"forged markup", []EditOp{{Kind: plan.OpAppend, Content: "<!-- gdoc_comment_begin: c9 -->x"}}, "invalid"},
		{"a block written in", []EditOp{{Kind: plan.OpAppend, Content: "---\ngdoc_comments:\n  c9:\n---"}}, "invalid"},
		{"text that is not there", []EditOp{{Kind: plan.OpDelete, Target: &Target{Text: "absent"}}}, "not_found"},
		{"two ops on one passage", []EditOp{
			{Kind: plan.OpReplace, Target: &Target{Text: "Intro with"}, Content: "Opening with"},
			{Kind: plan.OpReplace, Target: &Target{Text: "with **bold**"}, Content: "and"},
		}, "invalid"},
	}
	for _, tc := range tests {
		if _, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Ops: tc.ops}); classOf(err) != tc.class {
			t.Errorf("%s: %v, want %s", tc.name, err, tc.class)
		}
	}
	if len(api.uploads) != 0 {
		t.Errorf("refused edits uploaded %d time(s)", len(api.uploads))
	}
	ctx := context.Background()
	for name, err := range map[string]error{
		"review": func() error {
			_, err := svc.Review(ctx, ReviewRequest{Document: fixtureID, Action: "accept", All: true})
			return err
		}(),
		"tabs": func() error {
			_, err := svc.ManageTabs(ctx, TabRequest{Document: fixtureID, Action: "add", Title: "x"})
			return err
		}(),
		"pdf export": func() error { _, err := svc.Export(ctx, ExportRequest{Document: fixtureID, Format: "pdf"}); return err }(),
	} {
		if classOf(err) != "unsupported" {
			t.Errorf("%s: %v, want unsupported", name, err)
		}
	}
}

func TestMarkdownFileUploadsNothingForNoChange(t *testing.T) {
	svc, api := mdService(t, config.WriteDirect)
	res, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Ops: []EditOp{{Kind: plan.OpReplace, Target: &Target{Text: "None yet."}, Content: "None yet."}}})
	if err != nil || len(api.uploads) != 0 || res.RevisionID != "rev1" {
		t.Errorf("no-op edit: %+v, %v, %d upload(s)", res, err, len(api.uploads))
	}
	dry, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, DryRun: true, Ops: []EditOp{{Kind: plan.OpReplace, Target: &Target{Text: "None yet."}, Content: "One: scope."}}})
	if err != nil || len(api.uploads) != 0 || !strings.Contains(dry.Preview, "-None yet.\n+One: scope.") {
		t.Errorf("dry run: %+v, %v, %d upload(s)", dry, err, len(api.uploads))
	}
}

func TestMarkdownFileConcurrency(t *testing.T) {
	op := []EditOp{{Kind: plan.OpReplace, Target: &Target{Text: "None yet."}, Content: "Some."}}
	moved := func(api *fakeAPI, onCalls ...int) func(int) {
		return func(n int) {
			for _, c := range onCalls {
				if n == c {
					text := strings.Replace(*api.md, "Intro", "Introduction", 1)
					api.md = &text
					api.uploads = append(api.uploads, "somebody else")
				}
			}
		}
	}
	// Calls: 1 asks the type, 2 reads, 3 is the check before the upload.
	svc, api := mdService(t, config.WriteDirect)
	api.onGetFile = moved(api, 3)
	res, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Ops: op})
	if err != nil || len(api.uploads) != 2 || !strings.Contains(api.uploads[1], "Introduction with") || !strings.Contains(api.uploads[1], "Some.") || res.RevisionID != "rev3" {
		t.Fatalf("one move is planned again: %+v, %v, %q", res, err, api.uploads)
	}
	// Each try reads the file, then checks it just before the upload:
	// calls 3, 5 and 7 are those checks.
	svc, api = mdService(t, config.WriteDirect)
	api.onGetFile = moved(api, 3, 5, 7)
	if _, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Ops: op}); classOf(err) != "conflict" || len(api.uploads) != 3 {
		t.Errorf("three moves: %v, %q", err, api.uploads)
	}
	svc, api = mdService(t, config.WriteDirect)
	if _, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Ops: op, ExpectRevision: "rev0"}); classOf(err) != "conflict" || len(api.uploads) != 0 {
		t.Errorf("expect_revision: %v", err)
	}
	api.uploadErr = &gapi.APIError{Status: 503, Message: "backend"}
	api.uploadErr = errors.Join(gapi.ErrAmbiguous, api.uploadErr)
	if _, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Ops: op}); classOf(err) != "ambiguous_outcome" {
		t.Errorf("an upload whose outcome is unknown: %v", err)
	}
}

func TestMarkdownFileComments(t *testing.T) {
	svc, api := mdService(t, config.WriteDirect)
	api.comments = []*gapi.DriveComment{{ID: "AAAdrive", Content: "through the API", Author: &gapi.User{DisplayName: "Bo Example"}}}
	res, err := svc.ListComments(context.Background(), ListCommentsRequest{Document: fixtureID})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Threads) != 2 || res.Open != 2 {
		t.Fatalf("threads: %+v", res.Threads)
	}
	ed, dr := res.Threads[0], res.Threads[1]
	if ed.ID != "AAAeditor" || !ed.Anchored || ed.Handle != "L7" || dr.ID != "AAAdrive" || dr.Anchored {
		t.Errorf("threads: %+v", res.Threads)
	}
	// Drive cannot reach the editor's comment (the fake answers 404 for
	// any id it does not list), so the reply is written into the file.
	if _, err := svc.Reply(context.Background(), ReplyRequest{Document: fixtureID, CommentID: "AAAeditor", Content: "Agreed."}); err != nil {
		t.Fatalf("reply to the editor's comment: %v", err)
	}
	if want := "        ship in May\n    replies:\n    - author: \"Ada Example\"\n"; len(api.uploads) != 1 || !strings.Contains(api.uploads[0], want) ||
		!strings.Contains(api.uploads[0], "      body: \"Agreed.\"\n---\n") {
		t.Errorf("uploads = %q, want a reply under c1", api.uploads)
	}
	// Resolving cannot be written into the file: it breaks the file for
	// Google.
	if _, err := svc.Reply(context.Background(), ReplyRequest{Document: fixtureID, CommentID: "AAAeditor", Action: "resolve"}); classOf(err) != "unsupported" {
		t.Errorf("resolving the editor's comment: %v", err)
	}
	// A comment made through Drive is Drive's to answer.
	if _, err := svc.Reply(context.Background(), ReplyRequest{Document: fixtureID, CommentID: "AAAdrive", Content: "Noted."}); err != nil || len(api.posted) != 1 {
		t.Errorf("reply to a Drive comment: %v, %v", err, api.posted)
	}
}

func TestMarkdownFileRevisions(t *testing.T) {
	svc, _ := mdService(t, config.WriteDirect)
	d, err := svc.DiffRevisions(context.Background(), DiffRequest{Document: fixtureID, From: "rev0"})
	if err != nil {
		t.Fatal(err)
	}
	if d.To != "current" || !strings.Contains(d.Text, "-Intro.") || !strings.Contains(d.Text, "+Intro with **bold** text.") || strings.Contains(d.Text, "gdoc_comment") {
		t.Errorf("diff:\n%s", d.Text)
	}
	r, err := svc.ReadRevision(context.Background(), fixtureID, "rev0", "md", 1000)
	if err != nil || !strings.HasSuffix(r.Text, "# Notes\n\nIntro.\n") {
		t.Errorf("old revision: %v\n%s", err, r.Text)
	}
}

func TestMarkdownFileRetypedIsRefused(t *testing.T) {
	svc, api := mdService(t, config.WriteDirect)
	if _, err := svc.Info(context.Background(), fixtureID); err != nil {
		t.Fatal(err)
	}
	api.file = &gapi.File{MimeType: "text/plain"}
	if _, err := svc.Read(context.Background(), ReadRequest{Document: fixtureID}); classOf(err) != "unsupported" || !strings.Contains(err.Error(), "text/plain") {
		t.Errorf("a file retyped text/plain: %v", err)
	}
}

func TestMarkdownFileFrontMatter(t *testing.T) {
	text := "---\ntitle: Plan\n---\n\n# Plan\n\nBody.\n"
	api := &fakeAPI{md: &text}
	svc := New(api, Options{DefaultWriteMode: config.WriteDirect, Sleep: noSleep})
	res, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Ops: []EditOp{
		{Kind: plan.OpInsert, Location: &Location{At: "start"}, Content: "Draft."},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// Front matter has to stay first to be front matter.
	if want := "---\ntitle: Plan\n---\n\nDraft.\n\n# Plan\n\nBody.\n"; len(api.uploads) != 1 || api.uploads[0] != want {
		t.Errorf("uploads = %q, want %q", api.uploads, want)
	}
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "front matter, which the Docs editor rewrites") {
		t.Errorf("no front matter warning: %v", res.Warnings)
	}
}

func TestMarkdownFileWithin(t *testing.T) {
	svc, api := mdService(t, config.WriteDirect)
	ctx := context.Background()
	// "yet" is in Risks only; "Notes" is the H1 and nowhere in Plan.
	for _, within := range []string{"risks", "heading:Risks"} {
		if _, err := svc.Edit(ctx, EditRequest{Document: fixtureID, DryRun: true, Ops: []EditOp{
			{Kind: plan.OpReplace, Target: &Target{Text: "yet", Within: within}, Content: "so far"}}}); err != nil {
			t.Errorf("within %q: %v", within, err)
		}
	}
	if _, err := svc.Edit(ctx, EditRequest{Document: fixtureID, Ops: []EditOp{
		{Kind: plan.OpDelete, Target: &Target{Text: "Notes", Within: "plan"}}}}); classOf(err) != "not_found" {
		t.Errorf("text outside the section: %v", err)
	}
	if _, err := svc.Edit(ctx, EditRequest{Document: fixtureID, Ops: []EditOp{
		{Kind: plan.OpDelete, Target: &Target{Text: "yet", Within: "nowhere"}}}}); classOf(err) != "not_found" {
		t.Errorf("an unknown section: %v", err)
	}
	if len(api.uploads) != 0 {
		t.Errorf("uploads: %q", api.uploads)
	}
}

func TestSearchKinds(t *testing.T) {
	svc, api := newService(t)
	ctx := context.Background()
	tests := []struct {
		kind, query string
	}{
		{"", "mimeType = 'application/vnd.google-apps.document' and trashed = false"},
		{"markdown_file", "(mimeType = 'text/markdown' or mimeType = 'text/x-markdown') and trashed = false"},
		{"any", "(mimeType = 'application/vnd.google-apps.document' or mimeType = 'text/markdown' or mimeType = 'text/x-markdown') and trashed = false"},
	}
	for _, tc := range tests {
		if _, err := svc.Search(ctx, SearchRequest{Kind: tc.kind}); err != nil || api.searchQ != tc.query {
			t.Errorf("kind %q: query %q, %v; want %q", tc.kind, api.searchQ, err, tc.query)
		}
	}
	if _, err := svc.Search(ctx, SearchRequest{Kind: "sheet"}); classOf(err) != "invalid" {
		t.Errorf("an unknown kind: %v", err)
	}
}

func TestCreateMarkdownFile(t *testing.T) {
	api := &fakeAPI{}
	svc := New(api, Options{DefaultWriteMode: config.WriteDirect, Sleep: noSleep})
	ctx := context.Background()
	res, err := svc.Create(ctx, CreateRequest{Title: "Plan", Kind: "markdown_file", Content: "# Plan\n\nBody.\n"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != KindMarkdownFile || res.Title != "Plan.md" || api.created[0] != "Plan.md text/markdown" || *api.md != "# Plan\n\nBody.\n" ||
		res.URL != "https://drive.google.com/file/d/"+fixtureID+"/view" {
		t.Errorf("create: %+v, created %v", res, api.created)
	}
	// The kind is known without asking Drive again, and the file reads.
	if info, err := svc.Info(ctx, res.ID); err != nil || info.Kind != KindMarkdownFile {
		t.Errorf("Info of the new file: %+v, %v", info, err)
	}
	if res, _ := svc.Create(ctx, CreateRequest{Title: "notes.markdown", Kind: "markdown_file"}); res == nil || res.Title != "notes.markdown" {
		t.Errorf("a .markdown name was renamed: %+v", res)
	}
	if _, err := svc.Create(ctx, CreateRequest{Title: "x", Kind: "markdown_file", Content: "<!-- gdoc_comment_end: c1 -->"}); classOf(err) != "invalid" {
		t.Errorf("markup in new content: %v", err)
	}
	if _, err := svc.Create(ctx, CreateRequest{Title: "x", Kind: "sheet"}); classOf(err) != "invalid" {
		t.Errorf("an unknown kind: %v", err)
	}
}

func TestMarkdownFileReadWarnsAboveTheText(t *testing.T) {
	text := "---\ntitle: Plan\n---\n\n# Plan\n"
	svc := New(&fakeAPI{md: &text}, Options{Sleep: noSleep})
	res, err := svc.Read(context.Background(), ReadRequest{Document: fixtureID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(res.Text, "front matter, which the Docs editor rewrites as a rule and a heading when a person saves the file there -->\n"+text) {
		t.Errorf("read:\n%s", res.Text)
	}
}

func TestMarkdownFileRegexReplaceAll(t *testing.T) {
	svc, api := mdService(t, config.WriteDirect)
	ctx := context.Background()
	res, err := svc.Edit(ctx, EditRequest{Document: fixtureID, Ops: []EditOp{
		{Kind: plan.OpReplaceAll, Regex: true, Params: plan.Params{Find: `(first|None) (yet)`, Replace: "${2} ${1}"}}}})
	if err != nil || len(api.uploads) != 1 || !strings.Contains(api.uploads[0], "\n\nyet None.\n") {
		t.Fatalf("regex replace_all: %+v, %v, %q", res, err, api.uploads)
	}
	for name, p := range map[string]plan.Params{
		"a group the pattern lacks":  {Find: `(None) yet`, Replace: "$1pt"},
		"a pattern matching nothing": {Find: `x*`, Replace: "y"},
	} {
		if _, err := svc.Edit(ctx, EditRequest{Document: fixtureID, Ops: []EditOp{{Kind: plan.OpReplaceAll, Regex: true, Params: p}}}); classOf(err) != "invalid" {
			t.Errorf("%s: %v, want invalid", name, err)
		}
	}
	if len(api.uploads) != 1 {
		t.Errorf("a refused replace_all uploaded")
	}
}

func TestMarkdownFileSectionBody(t *testing.T) {
	svc, api := mdService(t, config.WriteDirect)
	no := false
	if _, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Ops: []EditOp{
		{Kind: plan.OpReplace, Target: &Target{HeadingID: "risks", IncludeHeading: &no}, Content: "Two risks."}}}); err != nil {
		t.Fatal(err)
	}
	if len(api.uploads) != 1 || !strings.Contains(api.uploads[0], "## Risks\n\nTwo risks.\n\n---\ngdoc_comments:") {
		t.Errorf("the text under a heading: %q", api.uploads)
	}
}

func TestMarkdownFileStopsAtTheDocsModel(t *testing.T) {
	svc, api := mdService(t, config.WriteDirect)
	ctx := context.Background()
	// A path that does not branch on the kind is refused where the Docs
	// model is fetched, before anything is sent.
	if _, err := svc.Fetch(ctx, fixtureID); classOf(err) != "unsupported" {
		t.Errorf("Fetch of a Markdown file: %v", err)
	}
	if _, err := svc.Reply(ctx, ReplyRequest{Document: fixtureID, SuggestionID: "suggest.1", Content: "x"}); classOf(err) != "unsupported" {
		t.Errorf("a reply on a suggestion: %v", err)
	}
	if api.getCalls != 0 || len(api.batches) != 0 {
		t.Errorf("the Docs API was asked %d time(s), sent %d batch(es)", api.getCalls, len(api.batches))
	}
}

func TestInfoAsksDriveOnce(t *testing.T) {
	md, api := mdService(t, config.WriteDirect)
	if _, err := md.Info(context.Background(), fixtureID); err != nil || api.fileGets != 1 {
		t.Errorf("Info of a Markdown file: %v after %d files.get, want 1", err, api.fileGets)
	}
	doc, dapi := newService(t)
	if _, err := doc.Info(context.Background(), fixtureID); err != nil || dapi.fileGets != 1 {
		t.Errorf("Info of a Google Doc: %v after %d files.get, want 1", err, dapi.fileGets)
	}
}

func TestImportedCommentAuthorsAreLabeled(t *testing.T) {
	text := mdNotes
	view, err := json.Marshal(gdocs.Document{DocumentID: fixtureID, Comments: []gdocs.CommentThread{{
		CommentID: "AAAimported", Status: "OPEN", PlainTextQuote: "ship in May",
		HeadPost: gdocs.Post{Content: "is May real?", Author: gdocs.PostAuthor{DisplayName: "Anyone"}, FromImportedDocument: true},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	svc := New(&fakeAPI{raw: view, md: &text}, Options{Sleep: noSleep})
	res, err := svc.ListComments(context.Background(), ListCommentsRequest{Document: fixtureID})
	if err != nil || len(res.Threads) != 1 || res.Threads[0].Author != "Anyone (as the file names them)" || !res.Threads[0].Anchored {
		t.Errorf("an imported comment: %+v, %v", res, err)
	}
}

func TestMarkdownFileFillsAnEmptySection(t *testing.T) {
	text := "# Plan\n\n## Goals\n\n## Risks\n\nNone.\n"
	api := &fakeAPI{md: &text}
	svc := New(api, Options{DefaultWriteMode: config.WriteDirect, Sleep: noSleep})
	no := false
	if _, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Ops: []EditOp{
		{Kind: plan.OpReplace, Target: &Target{HeadingID: "goals", IncludeHeading: &no}, Content: "Ship it."}}}); err != nil {
		t.Fatal(err)
	}
	if want := "# Plan\n\n## Goals\n\nShip it.\n\n## Risks\n\nNone.\n"; len(api.uploads) != 1 || api.uploads[0] != want {
		t.Errorf("uploads = %q, want %q", api.uploads, want)
	}
}

func TestMarkdownFileReadsALongLineInPieces(t *testing.T) {
	text := strings.Repeat("word ", 30) + "\nnext\n" // a 150-character line
	svc := New(&fakeAPI{md: &text}, Options{Sleep: noSleep})
	ctx := context.Background()
	var got strings.Builder
	from := ""
	for range 10 {
		res, err := svc.Read(ctx, ReadRequest{Document: fixtureID, Scope: ReadScope{ContinueFrom: from}, Options: render.Options{MaxChars: 60}})
		if err != nil {
			t.Fatal(err)
		}
		got.WriteString(res.Text[strings.Index(res.Text, "-->\n")+4:])
		if !res.Truncated {
			if got.String() != text {
				t.Errorf("pieces joined = %q, want %q", got.String(), text)
			}
			return
		}
		if res.ContinueFrom == from {
			t.Fatalf("continue_from %s does not move", from)
		}
		from = res.ContinueFrom
	}
	t.Fatal("ten reads did not reach the end")
}

func TestMarkdownFileTakesTwoAppends(t *testing.T) {
	svc, api := mdService(t, config.WriteDirect)
	if _, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Ops: []EditOp{
		{Kind: plan.OpAppend, Content: "## Notes"}, {Kind: plan.OpAppend, Content: "- item"}}}); err != nil {
		t.Fatal(err)
	}
	if len(api.uploads) != 1 || !strings.Contains(api.uploads[0], "None yet.\n\n## Notes\n\n- item\n\n---\ngdoc_comments:") {
		t.Errorf("two appends: %q", api.uploads)
	}
}

func TestMarkdownFileRefusesGrowingPastTheUploadCeiling(t *testing.T) {
	text := strings.Repeat("x", maxUpload-10) + "\n"
	api := &fakeAPI{md: &text}
	svc := New(api, Options{DefaultWriteMode: config.WriteDirect, Sleep: noSleep})
	_, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Ops: []EditOp{{Kind: plan.OpAppend, Content: strings.Repeat("y", 100)}}})
	if classOf(err) != "unsupported" || len(api.uploads) != 0 {
		t.Errorf("an edit past 5 MB: %v, %d upload(s)", err, len(api.uploads))
	}
}

func TestMarkdownFileListsCommentsTheViewLeavesOut(t *testing.T) {
	text := "We <!-- gdoc_comment_begin: c7 -->ship<!-- gdoc_comment_end: c7 --> it.\n"
	view, _ := json.Marshal(gdocs.Document{DocumentID: fixtureID})
	svc := New(&fakeAPI{raw: view, md: &text}, Options{Sleep: noSleep})
	res, err := svc.ListComments(context.Background(), ListCommentsRequest{Document: fixtureID})
	if err != nil || len(res.Threads) != 1 || res.Threads[0].ID != "c7" || !res.Threads[0].Anchored || res.Open != 1 {
		t.Errorf("a marker pair the view does not list: %+v, %v", res, err)
	}
}

func TestMarkdownKindAfterADriveFailure(t *testing.T) {
	svc, api := mdService(t, config.WriteDirect)
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	api.fileErr = &gapi.APIError{Status: 503, Message: "backend"}
	api.fileErr = errors.Join(gapi.ErrServer, api.fileErr)
	first := svc.isMarkdown(context.Background(), fixtureID)
	second := svc.isMarkdown(context.Background(), fixtureID)
	if first || second || api.fileGets != 1 {
		t.Errorf("a failing Drive: %t then %t, asked %d times, want false twice and once", first, second, api.fileGets)
	}
	api.fileErr = nil
	now = now.Add(2 * kindRetryAfter)
	if !svc.isMarkdown(context.Background(), fixtureID) || api.fileGets != 2 {
		t.Errorf("after the guess expired: %d files.get", api.fileGets)
	}
}

func TestMarkdownFileLineCounts(t *testing.T) {
	text := "# A\n\nbody\n"
	svc := New(&fakeAPI{md: &text}, Options{Sleep: noSleep})
	out, err := svc.Outline(context.Background(), fixtureID, "")
	if err != nil || !strings.Contains(out.Text, ": 3 lines, ") {
		t.Errorf("outline: %v\n%s", err, out.Text)
	}
	res, err := svc.Read(context.Background(), ReadRequest{Document: fixtureID})
	if err != nil || !strings.Contains(res.Text, " · 3 line(s) -->") {
		t.Errorf("read: %v\n%s", err, res.Text)
	}
}

// viewWith is the fake's live session: the editor's comment on "ship in
// May", plus whatever a test adds, each with an anchor.
func viewWith(t *testing.T, extra ...gdocs.CommentThread) []byte {
	t.Helper()
	threads := append([]gdocs.CommentThread{{CommentID: "AAAeditor", AnchorID: "kix.a1", Status: "OPEN", PlainTextQuote: "ship in May",
		HeadPost: gdocs.Post{Content: "is May real?", CreateTime: "2026-01-02T03:04:05.000Z", Author: gdocs.PostAuthor{DisplayName: "Ada Example"}}}}, extra...)
	anchors := map[string]gdocs.CommentAnchor{}
	for _, th := range threads {
		anchors[th.AnchorID] = gdocs.CommentAnchor{AnchorID: th.AnchorID, Ranges: []*gdocs.Range{{StartIndex: 1, EndIndex: 4}}}
	}
	raw, err := json.Marshal(gdocs.Document{DocumentID: fixtureID, RevisionID: "live1", Comments: threads,
		Tabs: []*gdocs.Tab{{TabProperties: &gdocs.TabProperties{TabID: "t.0"}, DocumentTab: &gdocs.DocumentTab{CommentAnchors: anchors}}}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestMarkdownFileWaitsForTheEditor(t *testing.T) {
	op := []EditOp{{Kind: plan.OpReplace, Target: &Target{Text: "None yet."}, Content: "Some."}}
	unsaved := gdocs.CommentThread{CommentID: "AAAnew", AnchorID: "kix.a2", Status: "OPEN", PlainTextQuote: "Intro",
		HeadPost: gdocs.Post{Content: "made in the editor, not saved yet"}}
	tests := []struct {
		name string
		view []byte
	}{
		{"a comment the file does not have", viewWith(t, unsaved)},
		{"a resolve the file does not show", func() []byte {
			v := viewWith(t)
			return []byte(strings.Replace(string(v), `"status":"OPEN"`, `"status":"RESOLVED"`, 1))
		}()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, api := mdService(t, config.WriteDirect)
			api.raw = tc.view
			waits := 0
			svc.opts.Sleep = func(context.Context, time.Duration) error { waits++; return nil }
			_, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Ops: op})
			if classOf(err) != "conflict" || len(api.uploads) != 0 || waits != writeAttempts-1 {
				t.Errorf("while the editor holds unsaved work: %v, %d upload(s), %d wait(s)", err, len(api.uploads), waits)
			}
			// The editor saves, and the write goes ahead.
			api.raw = viewWith(t)
			if _, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Ops: op}); err != nil || len(api.uploads) != 1 {
				t.Errorf("after the save: %v, %d upload(s)", err, len(api.uploads))
			}
		})
	}
	// Forced, the person is asked, and the write goes ahead saying what it discards.
	svc, api := mdService(t, config.WriteDirect)
	api.raw = viewWith(t, unsaved)
	res, err := svc.Edit(accepting(context.Background()), EditRequest{Document: fixtureID, Ops: op, Force: true})
	if err != nil || len(api.uploads) != 1 || !strings.Contains(strings.Join(res.Warnings, "\n"), "a comment made in the editor") {
		t.Errorf("forced: %+v, %v", res, err)
	}
}

func TestMarkdownFilePlansAgainWhenTheEditorMoves(t *testing.T) {
	svc, api := mdService(t, config.WriteDirect)
	api.raw = viewWith(t)
	// The live session's revision moves between the plan and the upload
	// once: the write plans again and then goes ahead.
	api.onGetDocument = func(n int) {
		if n == 2 {
			api.raw = []byte(strings.Replace(string(api.raw), `"revisionId":"live1"`, `"revisionId":"live2"`, 1))
		}
	}
	if _, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Ops: []EditOp{{Kind: plan.OpReplace, Target: &Target{Text: "None yet."}, Content: "Some."}}}); err != nil || len(api.uploads) != 1 {
		t.Errorf("an editor that moved once: %v, %d upload(s)", err, len(api.uploads))
	}
	if api.getCalls < 4 {
		t.Errorf("the live session was read %d times; a second plan needs four", api.getCalls)
	}
}

func TestMarkdownFileKeepsASaveThatSlipsIn(t *testing.T) {
	svc, api := mdService(t, config.WriteDirect)
	saved := false
	api.beforeUpload = func() {
		if saved {
			return
		}
		// Somebody's save lands between the check and the upload.
		saved = true
		theirs := strings.Replace(*api.md, "Intro with", "Opening with", 1)
		api.uploads = append(api.uploads, theirs)
		api.mdOldRevs["rev2"] = theirs
	}
	res, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Ops: []EditOp{{Kind: plan.OpReplace, Target: &Target{Text: "None yet."}, Content: "Some."}}})
	if err != nil {
		t.Fatal(err)
	}
	last := *api.md
	if !strings.Contains(last, "Opening with") || !strings.Contains(last, "Some.") || len(api.uploads) != 3 || res.RevisionID != "rev4" {
		t.Errorf("after the repair the file is %q (uploads %d, revision %s); want their save and the edit", last, len(api.uploads), res.RevisionID)
	}
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "it was kept, and the change made again on top of it") {
		t.Errorf("warnings: %v", res.Warnings)
	}
	// When the change no longer fits the save, the save is put back.
	svc, api = mdService(t, config.WriteDirect)
	saved = false
	api.beforeUpload = func() {
		if saved {
			return
		}
		saved = true
		theirs := strings.Replace(*api.md, "None yet.", "Removed in the editor.", 1)
		api.uploads = append(api.uploads, theirs)
		api.mdOldRevs["rev2"] = theirs
	}
	_, err = svc.Edit(context.Background(), EditRequest{Document: fixtureID, Ops: []EditOp{{Kind: plan.OpReplace, Target: &Target{Text: "None yet."}, Content: "Some."}}})
	if classOf(err) != "conflict" || !strings.Contains(*api.md, "Removed in the editor.") || strings.Contains(*api.md, "Some.") {
		t.Errorf("a change that no longer fits: %v, the file is %q", err, *api.md)
	}
}

func TestMarkdownFileCommentMode(t *testing.T) {
	svc, api := mdService(t, config.WriteSuggest)
	res, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Mode: "comment", Ops: []EditOp{
		{Kind: plan.OpReplace, Target: &Target{Text: "None yet."}, Content: "Two risks."},
		{Kind: plan.OpAppend, Content: "## Next"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(api.uploads) != 1 {
		t.Fatalf("uploads: %d", len(api.uploads))
	}
	got := mdfile.Parse(api.uploads[0])
	if got.Text() != mdfile.Parse(mdNotes).Text() {
		t.Errorf("comment mode changed the text:\n%s", got.Text())
	}
	cs := got.Comments()
	if len(cs) != 3 || cs[1].Body != "Proposed change to “None yet.”:\n\nTwo risks." || got.Passage(cs[1].Key) != "None yet." ||
		cs[2].Body != "Proposed insertion at the end of the file:\n\n## Next" || cs[1].Author != "Ada Example" {
		t.Errorf("comments written: %+v", cs)
	}
	if res.Mode != "comment" || len(res.Proposals) != 2 {
		t.Errorf("result: %+v", res)
	}
}

func TestMarkdownFileAddComment(t *testing.T) {
	svc, api := mdService(t, config.WriteDirect)
	ctx := context.Background()
	dry, err := svc.AddComment(ctx, AddCommentRequest{Document: fixtureID, Target: &Target{Text: "None yet."}, Content: "Really?", DryRun: true})
	if err != nil || len(api.uploads) != 0 || !dry.DryRun || dry.Quote != "None yet." {
		t.Fatalf("dry run: %+v, %v", dry, err)
	}
	if _, err := svc.AddComment(ctx, AddCommentRequest{Document: fixtureID, Target: &Target{Text: "None yet."}, Content: "Really?"}); err != nil {
		t.Fatal(err)
	}
	got := mdfile.Parse(api.uploads[0])
	if cs := got.Comments(); len(cs) != 2 || cs[1].Body != "Really?" || cs[1].Author != "Ada Example" || got.Passage(cs[1].Key) != "None yet." {
		t.Errorf("comments: %+v", cs)
	}
	for name, req := range map[string]AddCommentRequest{
		"no passage":  {Document: fixtureID, Content: "x"},
		"an assignee": {Document: fixtureID, Content: "x", Target: &Target{Text: "None yet."}, Assignee: "bo@example.test"},
	} {
		if _, err := svc.AddComment(ctx, req); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// copyDocID is the fake's working copy of the Markdown file.
const copyDocID = "1SyntheticWorkingCopyIdXXXXXXXXXXXXXXXXXXXXXX"

// mdNotesExport is mdNotes's working copy as Docs exports it.
const mdNotesExport = "# Notes\n\nIntro with **bold** text.\n\n## Plan\n\nWe ship in May.\n\n![Chart](https://example.com/c.png)\n\n## Risks\n\nNone yet.\n"

func workingCopy(t *testing.T, export string) (*Service, *fakeAPI) {
	t.Helper()
	svc, api := mdService(t, config.WriteDirect)
	api.files = map[string]*gapi.File{copyDocID: {ID: copyDocID, Name: "notes (working copy)", MimeType: gapi.DocumentMimeType,
		AppProperties: map[string]string{"md_source": fixtureID, "md_base": "rev1"}}}
	api.mdOldRevs["rev1"] = mdNotes
	api.exports = map[string]string{copyDocID: export}
	return svc, api
}

func TestCreateWorkingCopy(t *testing.T) {
	text := "---\ntitle: Plan\n---\n\n# Plan\n\nWe <!-- gdoc_comment_begin: c1 -->ship<!-- gdoc_comment_end: c1 -->.\n\n" +
		"---\ngdoc_comments:\n  c1:\n    status: ACTIVE\n    head:\n      author: Ada\n      body: hm\n      quote: ship\n---\n"
	api := &fakeAPI{md: &text}
	svc := New(api, Options{DefaultWriteMode: config.WriteDirect, Sleep: noSleep})
	res, err := svc.Create(context.Background(), CreateRequest{FromFile: fixtureID})
	if err != nil {
		t.Fatal(err)
	}
	if len(api.createdMeta) != 1 {
		t.Fatalf("created %d files", len(api.createdMeta))
	}
	meta := api.createdMeta[0]
	if meta.MimeType != gapi.DocumentMimeType || meta.Name != "notes (working copy)" || meta.AppProperties["md_source"] != fixtureID || meta.AppProperties["md_base"] != "rev1" {
		t.Errorf("metadata: %+v", meta)
	}
	// The copy holds the text: no front matter, no comment markup.
	if want := "\n# Plan\n\nWe ship."; api.createdData[0] != want {
		t.Errorf("content = %q, want %q", api.createdData[0], want)
	}
	if res.Kind != KindDocument || !strings.Contains(strings.Join(res.Warnings, "\n"), "write_markdown_file") {
		t.Errorf("result: %+v", res)
	}
	if _, err := svc.Create(context.Background(), CreateRequest{FromFile: fixtureID, Content: "x"}); classOf(err) != "invalid" {
		t.Errorf("content with from_file: %v", err)
	}
}

func TestWriteBackCarriesTheCopysChanges(t *testing.T) {
	export := strings.Replace(mdNotesExport, "None yet.", "Two risks.", 1)
	svc, api := workingCopy(t, export)
	res, err := svc.WriteBack(context.Background(), WriteBackRequest{Document: copyDocID})
	if err != nil {
		t.Fatal(err)
	}
	// The file keeps its own **bold**, its comment markers and its block;
	// only the changed block comes over.
	if want := strings.Replace(mdNotes, "None yet.", "Two risks.", 1); len(api.uploads) != 1 || api.uploads[0] != want {
		t.Errorf("uploads = %q\nwant %q", api.uploads, want)
	}
	if len(api.propsSet) != 1 || api.propsSet[0]["md_base"] != res.RevisionID || res.RevisionID != "rev2" {
		t.Errorf("the copy was told %v; the write is %s", api.propsSet, res.RevisionID)
	}
}

func TestWriteBackConflicts(t *testing.T) {
	export := strings.Replace(mdNotesExport, "None yet.", "Copy says three.", 1)
	export = strings.Replace(export, "Intro with", "Opening with", 1)
	svc, api := workingCopy(t, export)
	// The file changed the same block since the copy was made.
	changed := strings.Replace(mdNotes, "None yet.", "The file says two.", 1)
	api.md = &changed
	if _, err := svc.WriteBack(context.Background(), WriteBackRequest{Document: copyDocID}); classOf(err) != "conflict" || len(api.uploads) != 0 {
		t.Fatalf("a conflict: %v, %d upload(s)", err, len(api.uploads))
	}
	res, err := svc.WriteBack(context.Background(), WriteBackRequest{Document: copyDocID, Force: true})
	if err != nil || len(api.uploads) != 1 {
		t.Fatalf("forced: %v", err)
	}
	if got := api.uploads[0]; !strings.Contains(got, "The file says two.") || !strings.Contains(got, "Opening with") || strings.Contains(got, "Copy says three.") {
		t.Errorf("forced write-back = %q", got)
	}
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "file's version was kept") {
		t.Errorf("warnings: %v", res.Warnings)
	}
}

func TestWriteBackWithoutItsBase(t *testing.T) {
	svc, api := workingCopy(t, strings.Replace(mdNotesExport, "None yet.", "Two risks.", 1))
	delete(api.mdOldRevs, "rev1")
	if _, err := svc.WriteBack(context.Background(), WriteBackRequest{Document: copyDocID}); classOf(err) != "conflict" {
		t.Errorf("a base gone from history: %v", err)
	}
	res, err := svc.WriteBack(context.Background(), WriteBackRequest{Document: copyDocID, Force: true})
	if err != nil || len(api.uploads) != 1 || !strings.Contains(api.uploads[0], "Two risks.") || !strings.Contains(strings.Join(res.Warnings, "\n"), "gone from the file's history") {
		t.Errorf("forced without a base: %+v, %v", res, err)
	}
}

func TestWriteBackImagesWithoutAnAddress(t *testing.T) {
	export := strings.Replace(mdNotesExport, "None yet.", "None yet.\n\n![photo](data:image/png;base64,AAAA)", 1)
	svc, api := workingCopy(t, export)
	res, err := svc.WriteBack(context.Background(), WriteBackRequest{Document: copyDocID})
	if err != nil || len(api.uploads) != 1 || !strings.Contains(api.uploads[0], "None yet.\n\n![photo]()\n\n---") || strings.Contains(api.uploads[0], "base64") {
		t.Fatalf("an image with no address: %v, %q", err, api.uploads)
	}
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "no address") {
		t.Errorf("warnings: %v", res.Warnings)
	}
}

func TestWriteBackRefusesWhatIsNotACopy(t *testing.T) {
	svc, api := workingCopy(t, mdNotesExport)
	api.files[copyDocID].AppProperties = nil
	if _, err := svc.WriteBack(context.Background(), WriteBackRequest{Document: copyDocID}); classOf(err) != "invalid" {
		t.Errorf("a Doc that is no copy: %v", err)
	}
	if _, err := svc.WriteBack(context.Background(), WriteBackRequest{Document: fixtureID}); classOf(err) != "invalid" {
		t.Errorf("the Markdown file itself: %v", err)
	}
}

func TestInfoNamesAWorkingCopy(t *testing.T) {
	svc, api := newService(t)
	f, _ := api.GetFile(context.Background(), fixtureID)
	f.AppProperties = map[string]string{"md_source": "1SyntheticMarkdownSourceXXXXXXXXXXXXXXXXXXXXX"}
	api.file = f
	info, err := svc.Info(context.Background(), fixtureID)
	// The text names the file by its link, which a transcript's redactor
	// knows; the structured result carries the id.
	if err != nil || info.WorkingCopyOf != "1SyntheticMarkdownSourceXXXXXXXXXXXXXXXXXXXXX" ||
		!strings.Contains(info.Text, "working copy of the Markdown file https://drive.google.com/file/d/1SyntheticMarkdownSourceXXXXXXXXXXXXXXXXXXXXX/view: write_markdown_file") {
		t.Errorf("Info of a working copy: %v\n%s", err, info.Text)
	}
}

// A Markdown file's lists nest by their own indentation; list_level, a
// Google Doc's, is refused rather than ignored.
func TestMarkdownRefusesListLevel(t *testing.T) {
	svc, api := mdService(t, config.WriteDirect)
	level := 1
	_, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Mode: "direct", Ops: []EditOp{
		{Kind: plan.OpAppend, Content: "- item", Params: plan.Params{ListLevel: &level}}}})
	var se *Error
	if !errors.As(err, &se) || se.Class != "invalid" {
		t.Fatalf("got %v, want invalid", err)
	}
	if len(api.uploads) != 0 {
		t.Fatalf("%d uploads", len(api.uploads))
	}
}
