package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/mmedum/google-docs-mcp/v3/internal/config"
	"github.com/mmedum/google-docs-mcp/v3/internal/gapi"
	"github.com/mmedum/google-docs-mcp/v3/internal/ooxml"
	"github.com/mmedum/google-docs-mcp/v3/internal/plan"
)

// wordService serves internal/ooxml/testdata/brief.docx, a Google Doc
// exported as .docx, with the Docs model documents.get gave for it (spike
// H, ids made synthetic).
func wordService(t *testing.T) (*Service, *fakeAPI) {
	t.Helper()
	return wordFixture(t, "brief")
}

// wordFixture serves internal/ooxml/testdata/<name>.docx with the Docs
// model in <name>.json, which TestRawDocxModel captures.
func wordFixture(t *testing.T, name string) (*Service, *fakeAPI) {
	t.Helper()
	data, err := os.ReadFile("../ooxml/testdata/" + name + ".docx")
	if err != nil {
		t.Fatal(err)
	}
	model, err := os.ReadFile("../ooxml/testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	api := &fakeAPI{raw: model, md: &text, mdOldRevs: map[string]string{}, file: &gapi.File{ID: fixtureID, Name: name + ".docx", MimeType: gapi.WordMimeType}}
	return New(api, Options{DefaultWriteMode: config.WriteSuggest, Sleep: noSleep}), api
}

// uploaded is the file as last uploaded, its paragraphs' text one per
// line, and its part's markup.
func uploaded(t *testing.T, api *fakeAPI, part string) (string, string) {
	t.Helper()
	if len(api.uploads) == 0 {
		t.Fatal("nothing was uploaded")
	}
	data := []byte(api.uploads[len(api.uploads)-1])
	f, err := ooxml.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, zf := range zr.File {
		if zf.Name == part {
			rc, _ := zf.Open()
			b, _ := io.ReadAll(rc)
			_ = rc.Close()
			return f.Text(), string(b)
		}
	}
	return f.Text(), ""
}

// handleOf is the handle of the paragraph with this text, from a read.
func handleOf(t *testing.T, svc *Service, text string) string {
	t.Helper()
	f, err := svc.Fetch(context.Background(), fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	svc.Remember(f)
	for _, b := range f.Doc.Tabs[0].Body.Blocks {
		if b.Paragraph != nil && strings.TrimSuffix(paragraphText(b), "\n") == text {
			return b.Handle
		}
	}
	t.Fatalf("no paragraph %q", text)
	return ""
}

func TestWordInfo(t *testing.T) {
	svc, _ := wordService(t)
	info, err := svc.Info(context.Background(), fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	if info.Kind != "word_file" || strings.Join(info.Capabilities.WriteModes, "/") != "suggest/direct/comment" || !strings.Contains(info.Text, "a Word file (.docx)") {
		t.Fatalf("kind %q, modes %v:\n%s", info.Kind, info.Capabilities.WriteModes, info.Text)
	}
}

// Direct: the text changes in the file, by its minimal difference, and the
// file goes up whole, as a Word file. A dry run sends nothing.
func TestWordEditDirect(t *testing.T) {
	svc, api := wordService(t)
	edit := EditRequest{Document: fixtureID, Mode: "direct", Ops: []EditOp{{Kind: plan.OpReplace, Target: &Target{Text: "Closing line."}, Content: "Closing words."}}}
	dry := edit
	dry.DryRun = true
	res, err := svc.Edit(context.Background(), dry)
	if err != nil {
		t.Fatal(err)
	}
	if len(api.uploads) != 0 || !strings.Contains(res.Preview, "-Closing line.") || !strings.Contains(res.Preview, "+Closing words.") {
		t.Fatalf("dry run: %d uploads, preview:\n%s", len(api.uploads), res.Preview)
	}
	if _, err := svc.Edit(context.Background(), edit); err != nil {
		t.Fatal(err)
	}
	text, doc := uploaded(t, api, "word/document.xml")
	if !strings.HasSuffix(text, "\nClosing words.\n") || strings.Contains(doc, "<w:ins ") {
		t.Fatalf("text:\n%s", text)
	}
}

// Suggest: a tracked change under the signed-in account's name, which
// Docs reads as a suggestion.
func TestWordEditSuggest(t *testing.T) {
	svc, api := wordService(t)
	if _, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Ops: []EditOp{
		{Kind: plan.OpReplace, Target: &Target{Text: "line"}, Content: "words"}}}); err != nil {
		t.Fatal(err)
	}
	text, doc := uploaded(t, api, "word/document.xml")
	if !strings.HasSuffix(text, "\nClosing linewords.\n") || !strings.Contains(doc, `<w:del w:id=`) || !strings.Contains(doc, `w:author="Ada Example"`) {
		t.Fatalf("text:\n%s\n%s", text, doc[len(doc)-800:])
	}
}

// Whole paragraphs: content goes between them, list items continue the
// list placed against at the level list_level gives, a numbered list
// after a bulleted item is a list of its own, a replaced paragraph goes,
// and a heading takes the file's style for it.
func TestWordEditBlocks(t *testing.T) {
	svc, api := wordService(t)
	one := 1
	second, closing := handleOf(t, svc, "Second point"), handleOf(t, svc, "Closing line.")
	if _, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Mode: "direct", Ops: []EditOp{
		{Kind: plan.OpInsert, Location: &Location{At: "after", Of: &Target{Handle: second}}, Content: "- Deeper\n\n1. Counted", Params: plan.Params{ListLevel: &one}},
		{Kind: plan.OpReplace, Target: &Target{Handle: closing}, Content: "## Ending"},
	}}); err != nil {
		t.Fatal(err)
	}
	_, doc := uploaded(t, api, "word/document.xml")
	f, err := ooxml.Open([]byte(api.uploads[0]))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range f.Paras {
		if p.Text == "Deeper" || p.Text == "Counted" || p.Text == "Ending" || p.Text == "Closing line." {
			got = append(got, p.Text+"/"+p.Style+"/"+p.NumID+"/"+string(rune('0'+p.Ilvl)))
		}
	}
	if strings.Join(got, " ") != "Deeper//2/1 Counted//3/0 Ending/Heading2//0" {
		t.Fatalf("paragraphs %v\n%s", got, doc)
	}
	if ordered, ok := f.ListOrdered("3", 0); !ordered || !ok {
		t.Fatal("the numbered list is not numbered")
	}
}

// A cell spanning columns is one cell in a Word file and several in Docs,
// the covered ones empty, while a span of rows is a cell in each row in
// both (internal/ooxml/testdata/merged.docx, read live): paragraphs after
// such cells still line up, in the table and after it.
func TestWordMergedCells(t *testing.T) {
	svc, api := wordFixture(t, "merged")
	if _, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Mode: "direct", Ops: []EditOp{
		{Kind: plan.OpReplace, Target: &Target{Text: "Gamma six"}, Content: "Gamma sixth"},
		{Kind: plan.OpReplace, Target: &Target{Text: "After the table"}, Content: "After the grid"},
	}}); err != nil {
		t.Fatal(err)
	}
	got, _ := uploaded(t, api, "word/document.xml")
	want := "Before the table\nAlpha one\nBeta one\nGamma one\nWide row\nTall cell\nBeta three\nGamma three\n\nBeta four\nGamma four\n" +
		"Block cell\nGamma five\n\nGamma sixth\nAfter the grid\n"
	if got != want {
		t.Fatalf("the file reads %q, want %q", got, want)
	}
}

// A cell a column span covers has no paragraph in the file: an op placed
// in it is refused before anything is written, naming the cell to use.
func TestWordCoveredCell(t *testing.T) {
	svc, api := wordFixture(t, "merged")
	f, err := svc.Fetch(context.Background(), fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	svc.Remember(f)
	_, err = svc.Edit(context.Background(), EditRequest{Document: fixtureID, Mode: "direct", Ops: []EditOp{
		{Kind: plan.OpInsert, Location: &Location{At: "start", Of: &Target{Cell: "tbl1:r2c3"}}, Content: "Into the span"},
	}})
	var se *Error
	if !errors.As(err, &se) || se.Class != "invalid" || !strings.Contains(se.Message, "merged into tbl1:r2c1") {
		t.Fatalf("err = %v, want [invalid] naming tbl1:r2c1", err)
	}
	if len(api.uploads) != 0 {
		t.Fatal("something was uploaded")
	}
}

// Comment mode posts each op as a Word comment proposing it and changes no
// text; add_comment posts one.
func TestWordComments(t *testing.T) {
	svc, api := wordService(t)
	if _, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Mode: "comment", Ops: []EditOp{
		{Kind: plan.OpReplace, Target: &Target{Text: "Closing line."}, Content: "Closing words."}}}); err != nil {
		t.Fatal(err)
	}
	text, comments := uploaded(t, api, "word/comments.xml")
	if !strings.HasSuffix(text, "\nClosing line.\n") || !strings.Contains(comments, `w:author="Ada Example"`) || !strings.Contains(comments, "Proposed change to") {
		t.Fatalf("text:\n%s\ncomments:\n%s", text, comments)
	}
	res, err := svc.AddComment(context.Background(), AddCommentRequest{Document: fixtureID, Target: &Target{Text: "Intro"}, Content: "Why intro?"})
	if err != nil {
		t.Fatal(err)
	}
	_, comments = uploaded(t, api, "word/comments.xml")
	if !res.Anchored || res.Quote != "Intro" || !strings.Contains(comments, "Why intro?") || len(api.uploads) != 2 {
		t.Fatalf("add_comment %+v\n%s", res, comments)
	}
}

// What only the Docs API does is refused before anything is written,
// saying where it goes instead.
func TestWordRefusesDocsOnly(t *testing.T) {
	svc, api := wordService(t)
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"formatting": func() error {
			_, err := svc.Edit(ctx, EditRequest{Document: fixtureID, Mode: "direct", Ops: []EditOp{
				{Kind: plan.OpTextStyle, Target: &Target{Text: "Intro"}, Params: plan.Params{Text: plan.TextStyleSpec{Bold: new(true)}}}}})
			return err
		},
		"a table in content": func() error {
			_, err := svc.Edit(ctx, EditRequest{Document: fixtureID, Mode: "direct", Ops: []EditOp{{Kind: plan.OpAppend, Content: "| a |\n|---|\n| 1 |"}}})
			return err
		},
		"review": func() error {
			_, err := svc.Review(ctx, ReviewRequest{Document: fixtureID, Action: "accept", All: true})
			return err
		},
		"tabs": func() error {
			_, err := svc.ManageTabs(ctx, TabRequest{Document: fixtureID, Action: "add", Title: "New"})
			return err
		},
		"suggestion reply": func() error {
			_, err := svc.Reply(ctx, ReplyRequest{Document: fixtureID, SuggestionID: "suggest.x", Content: "ok"})
			return err
		},
	} {
		if err := call(); classOf(err) != "unsupported" {
			t.Errorf("%s: got %v, want unsupported", name, err)
		}
	}
	if len(api.uploads) != 0 {
		t.Fatalf("%d uploads", len(api.uploads))
	}
}

// replace_all, which a Word file has no request for, changes every match
// as a replace: the case as match_case says, the replacement as given.
func TestWordReplaceAll(t *testing.T) {
	svc, api := wordService(t)
	if _, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Mode: "direct", Ops: []EditOp{
		{Kind: plan.OpReplaceAll, Params: plan.Params{Find: "point", Replace: "$5 item", MatchCase: true}}}}); err != nil {
		t.Fatal(err)
	}
	text, _ := uploaded(t, api, "word/document.xml")
	if want := "Brief\nIntro paragraph with bold and a link.\nPoints\nFirst $5 item\nNested $5 item\nSecond $5 item\nOne\nTwo\n\nA\nB\n1\n2\n\nClosing line.\n"; text != want {
		t.Fatalf("text:\n%s\nwant:\n%s", text, want)
	}
}

// withHeader gives the Docs model the fake serves a default header holding
// text. The file has none: only the body's paragraphs are matched to it.
func withHeader(t *testing.T, api *fakeAPI, text string) {
	t.Helper()
	var d map[string]any
	if err := json.Unmarshal(api.raw, &d); err != nil {
		t.Fatal(err)
	}
	dt := d["tabs"].([]any)[0].(map[string]any)["documentTab"].(map[string]any)
	n := len(utf16.Encode([]rune(text)))
	run := map[string]any{"endIndex": n, "textRun": map[string]any{"content": text, "textStyle": map[string]any{}}}
	para := map[string]any{"endIndex": n, "paragraph": map[string]any{"elements": []any{run}, "paragraphStyle": map[string]any{"namedStyleType": "NORMAL_TEXT"}}}
	dt["headers"] = map[string]any{"kix.header": map[string]any{"headerId": "kix.header", "content": []any{para}}}
	dt["documentStyle"].(map[string]any)["defaultHeaderId"] = "kix.header"
	var err error
	if api.raw, err = json.Marshal(d); err != nil {
		t.Fatal(err)
	}
}

// A Word file's text is written in its body only, so replace_all replaces
// the matches there and warns of those it left in a header; one matching
// only outside the body is refused.
func TestWordReplaceAllBodyOnly(t *testing.T) {
	svc, api := wordService(t)
	withHeader(t, api, "Header point\n")
	res, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Mode: "direct", Ops: []EditOp{
		{Kind: plan.OpReplaceAll, Params: plan.Params{Find: "point", Replace: "item", MatchCase: true}}}})
	if err != nil {
		t.Fatal(err)
	}
	text, _ := uploaded(t, api, "word/document.xml")
	if want := "Brief\nIntro paragraph with bold and a link.\nPoints\nFirst item\nNested item\nSecond item\nOne\nTwo\n\nA\nB\n1\n2\n\nClosing line.\n"; text != want {
		t.Fatalf("text:\n%s\nwant:\n%s", text, want)
	}
	if !slices.ContainsFunc(res.Warnings, func(w string) bool {
		return strings.Contains(w, "1 match(es)") && strings.Contains(w, "outside the body")
	}) {
		t.Errorf("warnings %q, want one for the match left in the header", res.Warnings)
	}
	_, err = svc.Edit(context.Background(), EditRequest{Document: fixtureID, Mode: "direct", Ops: []EditOp{
		{Kind: plan.OpReplaceAll, Params: plan.Params{Find: "Header", Replace: "Top", MatchCase: true}}}})
	var se *Error
	if !errors.As(err, &se) || se.Class != "unsupported" || len(api.uploads) != 1 {
		t.Fatalf("a match only in the header: %v, %d uploads; want unsupported and still 1", err, len(api.uploads))
	}
}

// A Word file's suggestions are tracked changes written into the file, so
// get_document offers suggest mode on one whose comments view Google
// refused; and the one function every Docs API write goes through refuses
// a Word file before sending anything.
func TestWordSuggestWithoutCommentsView(t *testing.T) {
	svc, api := wordService(t)
	api.viewErr = viewRefused
	info, err := svc.Info(context.Background(), fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	if info.Capabilities.CommentsView || !slices.Contains(info.Capabilities.WriteModes, "suggest") {
		t.Fatalf("comments view %v, write modes %q; want no view and suggest offered", info.Capabilities.CommentsView, info.Capabilities.WriteModes)
	}
	f, err := svc.Fetch(context.Background(), fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	var se *Error
	if _, _, err := svc.batchUpdate(context.Background(), f, nil, ""); !errors.As(err, &se) || se.Class != "unsupported" || len(api.batches) != 0 {
		t.Fatalf("a batch for a Word file: %v, %d sent; want unsupported and none", err, len(api.batches))
	}
}

// An edit that takes a Word file past what Drive takes in one upload is
// refused before uploading.
func TestWordEditTooLarge(t *testing.T) {
	svc, api := wordService(t)
	// Text a zip cannot squeeze under the ceiling: random letters, about
	// 4.7 bits each.
	r := rand.New(rand.NewPCG(1, 2))
	b := make([]byte, maxUpload*2)
	for i := range b {
		b[i] = 'a' + byte(r.IntN(26))
	}
	_, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Mode: "direct", Ops: []EditOp{
		{Kind: plan.OpAppend, Content: string(b)}}})
	var se *Error
	if !errors.As(err, &se) || se.Class != "unsupported" || len(api.uploads) != 0 {
		t.Fatalf("a file past the ceiling: %v, %d uploads; want unsupported and none", err, len(api.uploads))
	}
}

// An error from internal/ooxml is classed: an edit it will not make is
// unsupported, any other failure invalid, and an op's names the op.
func TestWordErrorClasses(t *testing.T) {
	for _, c := range []struct {
		err         error
		class, text string
	}{
		{fmt.Errorf("%w: inside a suggestion", ooxml.ErrUnsupported), "unsupported", "op 3: inside a suggestion"},
		{errors.New("two edits reach the same text"), "invalid", "op 3: two edits reach the same text"},
	} {
		var se *Error
		if err := docxError(&plan.Op{Seq: 3}, c.err); !errors.As(err, &se) || se.Class != c.class || se.Message != c.text {
			t.Errorf("%v: got %v, want [%s] %s", c.err, err, c.class, c.text)
		}
	}
	if err := docxError(&plan.Op{Seq: 3}, nil); err != nil {
		t.Errorf("no error: got %v", err)
	}
	var se *Error
	if err := wordError(errors.New("comments.xml has no end tag")); !errors.As(err, &se) || se.Class != "invalid" {
		t.Errorf("a comment's failure: got %v, want invalid", err)
	}
}

// Without the comments view, the guard learns a Word file's comments from
// the file: a direct delete of a commented paragraph is blocked, and goes
// through when forced.
func TestWordGuardsFileComments(t *testing.T) {
	svc, api := wordService(t)
	api.viewErr = viewRefused
	f, err := ooxml.Open([]byte(*api.md))
	if err != nil {
		t.Fatal(err)
	}
	two := slices.IndexFunc(f.Paras, func(p *ooxml.Para) bool { return p.Text == "Two" })
	if _, err := f.Comment(two, 0, two, 3, ooxml.NewComment{Author: "Ada Example", Date: "2026-10-21T12:00:00Z", Text: "Why two?"}); err != nil {
		t.Fatal(err)
	}
	out, _, err := f.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	commented := string(out)
	api.md = &commented
	del := EditRequest{Document: fixtureID, Mode: "direct", Ops: []EditOp{{Kind: plan.OpDelete, Target: &Target{Handle: handleOf(t, svc, "Two")}}}}
	if _, err := svc.Edit(context.Background(), del); classOf(err) != "blocked" || len(api.uploads) != 0 {
		t.Fatalf("got %v with %d uploads, want blocked and none", err, len(api.uploads))
	}
	del.Force = true
	if _, err := svc.Edit(accepting(context.Background()), del); err != nil {
		t.Fatal(err)
	}
	if text, _ := uploaded(t, api, "word/document.xml"); strings.Contains(text, "\nTwo\n") {
		t.Fatalf("forced, the paragraph is still there:\n%s", text)
	}
}

// A change that leaves the file as it is uploads nothing, and says so.
func TestWordEditNoChange(t *testing.T) {
	svc, api := wordService(t)
	res, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Mode: "direct", Ops: []EditOp{
		{Kind: plan.OpReplace, Target: &Target{Text: "Closing line."}, Content: "Closing line."}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(api.uploads) != 0 || !slices.Contains(res.Warnings, "the change leaves the file as it is, so nothing was uploaded") {
		t.Fatalf("%d uploads, warnings %q", len(api.uploads), res.Warnings)
	}
}

// A file too large to upload, or no longer a Word file, is refused from
// its metadata, before anything is written.
func TestWordRefusesFromMetadata(t *testing.T) {
	for name, meta := range map[string]func(*fakeAPI){
		"too large": func(api *fakeAPI) {
			api.files = map[string]*gapi.File{fixtureID: {ID: fixtureID, MimeType: gapi.WordMimeType, Size: "6000000"}}
		},
		"retyped": func(api *fakeAPI) {
			// Drive told the session it was a Word file; an upload since
			// made it a PDF.
			api.onGetFile = func(n int) {
				if n > 1 {
					api.file.MimeType = "application/pdf"
				}
			}
		},
	} {
		svc, api := wordService(t)
		meta(api)
		_, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Mode: "direct", Ops: []EditOp{
			{Kind: plan.OpReplace, Target: &Target{Text: "Closing line."}, Content: "Closing words."}}})
		if classOf(err) != "unsupported" || len(api.uploads) != 0 {
			t.Errorf("%s: got %v with %d uploads, want unsupported and none", name, err, len(api.uploads))
		}
	}
}

// When Docs and the file disagree on a paragraph the write reaches, the
// write waits and then refuses, writing nothing.
func TestWordStale(t *testing.T) {
	svc, api := wordService(t)
	data, err := os.ReadFile("../ooxml/testdata/brief.docx")
	if err != nil {
		t.Fatal(err)
	}
	f, err := ooxml.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range f.Paras {
		if p.Text == "Closing line." {
			if err := f.Replace(i, 8, 12, []ooxml.Run{{Text: "typed"}}, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	out, _, err := f.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	api.md = &text
	_, err = svc.Edit(context.Background(), EditRequest{Document: fixtureID, Mode: "direct", Ops: []EditOp{{Kind: plan.OpReplace, Target: &Target{Text: "Closing line."}, Content: "Closing words."}}})
	if classOf(err) != "conflict" || len(api.uploads) != 0 {
		t.Fatalf("got %v with %d uploads, want conflict and none", err, len(api.uploads))
	}
}

// A save that lands between the check and the upload is put back, and the
// write is not applied.
func TestWordSaveLandsInBetween(t *testing.T) {
	svc, api := wordService(t)
	saved := false
	api.beforeUpload = func() {
		if saved {
			return
		}
		saved = true
		theirs := *api.md
		api.uploads = append(api.uploads, theirs)
		api.mdOldRevs["rev2"] = theirs
	}
	_, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Mode: "direct", Ops: []EditOp{{Kind: plan.OpReplace, Target: &Target{Text: "Closing line."}, Content: "Closing words."}}})
	text, _ := uploaded(t, api, "word/document.xml")
	if classOf(err) != "conflict" || !strings.HasSuffix(text, "\nClosing line.\n") {
		t.Fatalf("got %v; the file ends:\n%s", err, text[len(text)-40:])
	}
}

// A Word file's comments are listed from the Docs comments view, where
// they are, besides any made through Drive; answering one through Drive is
// refused, saying where it is done.
func TestWordListsComments(t *testing.T) {
	svc, api := wordService(t)
	var w map[string]any
	if err := json.Unmarshal(api.raw, &w); err != nil {
		t.Fatal(err)
	}
	w["comments"] = []any{map[string]any{"commentId": "doco.1", "status": "OPEN",
		"headPost": map[string]any{"postId": "doco.1", "content": "From the file.", "author": map[string]any{"displayName": "Probe Author"}, "fromImportedDocument": true}}}
	api.raw, _ = json.Marshal(w)
	res, err := svc.ListComments(context.Background(), ListCommentsRequest{Document: fixtureID})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Threads) != 1 || res.Threads[0].Content != "From the file." || res.Threads[0].Author != "Probe Author (as the file names them)" {
		t.Fatalf("threads %+v", res.Threads)
	}
	if _, err := svc.Reply(context.Background(), ReplyRequest{Document: fixtureID, CommentID: "doco.1", Content: "ok"}); classOf(err) != "unsupported" {
		t.Fatalf("reply through Drive: %v", err)
	}
}
