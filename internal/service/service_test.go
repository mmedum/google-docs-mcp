package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-docs-mcp/internal/config"
	"github.com/mmedum/google-docs-mcp/internal/doc"
	"github.com/mmedum/google-docs-mcp/internal/doc/doctest"
	"github.com/mmedum/google-docs-mcp/internal/gapi"
	"github.com/mmedum/google-docs-mcp/internal/gdocs"
	"github.com/mmedum/google-docs-mcp/internal/render"
)

const fixtureID = "1SyntheticFixtureDocumentIdXXXXXXXXXXXXXXXXXX"

type fakeAPI struct {
	raw      []byte
	getCalls int
	getErr   error
	fileErr  error
	file     *gapi.File
	lastOpts gapi.GetOptions

	batches   []*gapi.BatchUpdateRequest
	batchErrs []error
	replies   []string
	created   []string
	searchQ   string
	exported  string
	comments  []*gapi.DriveComment
	listErr   error
}

func (f *fakeAPI) BatchUpdate(_ context.Context, id string, req *gapi.BatchUpdateRequest) (*gapi.BatchUpdateResponse, error) {
	f.batches = append(f.batches, req)
	if n := len(f.batches) - 1; n < len(f.batchErrs) && f.batchErrs[n] != nil {
		return nil, f.batchErrs[n]
	}
	raw := `{"replies":[],"writeControl":{"requiredRevisionId":"rev-0002"}}`
	if n := len(f.batches) - 1; n < len(f.replies) && f.replies[n] != "" {
		raw = f.replies[n]
	}
	var out gapi.BatchUpdateResponse
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	out.DocumentID = id
	out.Raw = json.RawMessage(raw)
	return &out, nil
}

func (f *fakeAPI) CreateDocument(_ context.Context, title string) (*gdocs.Document, error) {
	f.created = append(f.created, title)
	// A new document is a section break and one empty paragraph.
	return &gdocs.Document{DocumentID: fixtureID, Title: title, RevisionID: "rev-new", Body: &gdocs.Body{Content: []*gdocs.StructuralElement{
		{StartIndex: 0, EndIndex: 1, SectionBreak: &gdocs.SectionBreak{}},
		{StartIndex: 1, EndIndex: 2, Paragraph: &gdocs.Paragraph{Elements: []*gdocs.ParagraphElement{{StartIndex: 1, EndIndex: 2, TextRun: &gdocs.TextRun{Content: "\n"}}}}},
	}}}, nil
}

func (f *fakeAPI) SearchFiles(_ context.Context, q string, limit int, pageToken string) (*gapi.FileList, error) {
	f.searchQ = q
	return &gapi.FileList{Files: []*gapi.File{{ID: "d1", Name: "Doc One", ModifiedTime: "2026-09-01T00:00:00Z", WebViewLink: "https://docs.google.com/document/d/d1/edit", Owners: []*gapi.User{{EmailAddress: "o@example.test"}}}}, NextPageToken: "next"}, nil
}

func (f *fakeAPI) Export(_ context.Context, id, mimeType string) ([]byte, error) {
	f.exported = mimeType
	if mimeType == "application/pdf" {
		return []byte("%PDF-1.4 fake"), nil
	}
	return []byte("# Quarterly Report\n\nexported body text that is long enough to truncate"), nil
}

func (f *fakeAPI) CreateComment(_ context.Context, fileID, content, quote string) (*gapi.DriveComment, error) {
	c := &gapi.DriveComment{ID: fmt.Sprintf("dc%d", len(f.comments)+1), Content: content, QuotedFileContent: &gapi.QuotedText{Value: quote}}
	f.comments = append(f.comments, c)
	return c, nil
}

func (f *fakeAPI) ListComments(_ context.Context, fileID string, includeDeleted bool) ([]*gapi.DriveComment, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.comments, nil
}

func (f *fakeAPI) GetDocument(_ context.Context, id string, o gapi.GetOptions) (*gapi.DocumentResult, error) {
	f.getCalls++
	f.lastOpts = o
	if f.getErr != nil {
		return nil, f.getErr
	}
	if id != fixtureID {
		return nil, &gapi.APIError{Status: 404, Message: "not found"}
	}
	var d gdocs.Document
	if err := json.Unmarshal(f.raw, &d); err != nil {
		return nil, err
	}
	return &gapi.DocumentResult{Document: &d}, nil
}

func (f *fakeAPI) GetFile(_ context.Context, id string) (*gapi.File, error) {
	if f.fileErr != nil {
		return nil, f.fileErr
	}
	if f.file != nil {
		return f.file, nil
	}
	return &gapi.File{ID: id, Name: "Quarterly Report", ModifiedTime: "2026-09-01T10:00:00Z", CreatedTime: "2026-08-01T10:00:00Z",
		Owners:            []*gapi.User{{DisplayName: "Owner", EmailAddress: "owner@example.test"}},
		LastModifyingUser: &gapi.User{EmailAddress: "editor@example.test"},
		WebViewLink:       "https://docs.google.com/document/d/" + id + "/edit",
		Capabilities:      &gapi.FileCapabilities{CanEdit: true, CanComment: true}}, nil
}

func newService(t *testing.T) (*Service, *fakeAPI) {
	t.Helper()
	api := &fakeAPI{raw: doctest.RawFixture(t)}
	svc := New(api, Options{Preview: false, DefaultWriteMode: config.WriteDirect})
	return svc, api
}

func TestFetchCacheAndHandles(t *testing.T) {
	svc, api := newService(t)
	now := time.Unix(1000, 0)
	svc.now = func() time.Time { return now }
	f, err := svc.Fetch(context.Background(), "https://docs.google.com/document/d/"+fixtureID+"/edit")
	if err != nil {
		t.Fatal(err)
	}
	if f.Doc.Title != "Quarterly Report" || api.lastOpts.SuggestionsViewMode != gapi.SuggestionsInline || api.lastOpts.CommentsViewMode != "" {
		t.Fatalf("fetch options wrong: %+v", api.lastOpts)
	}
	if _, err := svc.Fetch(context.Background(), fixtureID); err != nil || api.getCalls != 1 {
		t.Fatalf("second fetch should hit the cache: calls=%d err=%v", api.getCalls, err)
	}
	now = now.Add(10 * time.Second)
	if _, err := svc.Fetch(context.Background(), fixtureID); err != nil || api.getCalls != 2 {
		t.Fatalf("expired cache should refetch: calls=%d", api.getCalls)
	}
	svc.Invalidate(fixtureID)
	if _, err := svc.Fetch(context.Background(), fixtureID); err != nil || api.getCalls != 3 {
		t.Fatalf("invalidate should refetch: calls=%d", api.getCalls)
	}
	m, ok := svc.Handles(fixtureID)
	if !ok || m.RevisionID != "rev-0001" || m.Text["p2"] != "Background" || m.Text["tbl1:r2c1/p1"] != "Alpha" {
		t.Fatalf("handle memory wrong: %v %+v", ok, m)
	}
	if _, ok := svc.Handles("other"); ok {
		t.Fatal("unknown doc should have no memory")
	}
}

func TestFetchPreviewRequestsComments(t *testing.T) {
	api := &fakeAPI{raw: doctest.RawFixture(t)}
	svc := New(api, Options{Preview: true})
	if _, err := svc.Fetch(context.Background(), fixtureID); err != nil {
		t.Fatal(err)
	}
	if api.lastOpts.CommentsViewMode != gapi.CommentsIncluded {
		t.Fatalf("preview should request comments: %+v", api.lastOpts)
	}
}

func TestFetchErrors(t *testing.T) {
	svc, api := newService(t)
	_, err := svc.Fetch(context.Background(), "not-an-id")
	var se *Error
	if !errors.As(err, &se) || se.Class != "invalid" {
		t.Fatalf("bad id: %v", err)
	}
	_, err = svc.Fetch(context.Background(), "1UnknownDocumentIdYYYYYYYYYYYYYYYYYYYYYYYYYY")
	if !errors.As(err, &se) || se.Class != "not_found" || !strings.Contains(se.Message, "shared") {
		t.Fatalf("not found: %v", err)
	}
	for _, tc := range []struct {
		err   error
		class string
		hint  string
	}{
		{&gapi.APIError{Status: 401, Message: "x"}, "auth", "login"},
		{&gapi.APIError{Status: 403, Reason: "ACCESS_TOKEN_SCOPE_INSUFFICIENT", Message: "x"}, "forbidden", "scope"},
		{&gapi.APIError{Status: 403, Message: "x"}, "forbidden", "access"},
		{&gapi.APIError{Status: 429, Message: "x"}, "rate_limited", "retry"},
		{&gapi.APIError{Status: 500, Message: "x"}, "server", "retry"},
		{gapi.ErrNetwork, "network", "reach"},
		{errors.New("weird"), "unexpected", "weird"},
	} {
		api.getErr = tc.err
		svc.Invalidate(fixtureID)
		_, err := svc.Fetch(context.Background(), fixtureID)
		if !errors.As(err, &se) || se.Class != tc.class || !strings.Contains(se.Message, tc.hint) {
			t.Errorf("%v: got %v", tc.err, err)
		}
		if !errors.Is(err, tc.err) && tc.class != "unexpected" {
			t.Errorf("%v: underlying error not wrapped", tc.err)
		}
	}
}

func TestInfo(t *testing.T) {
	svc, api := newService(t)
	info, err := svc.Info(context.Background(), fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	if info.Title != "Quarterly Report" || len(info.Tabs) != 2 || info.Tabs[0].Headings != 3 || info.Owner != "Owner <owner@example.test>" || info.LastModifiedBy != "editor@example.test" {
		t.Fatalf("info wrong: %+v", info)
	}
	if info.CanEdit == nil || !*info.CanEdit || info.Stats.Tables != 1 || info.Capabilities.DefaultWriteMode != "direct" || len(info.Capabilities.WriteModes) != 2 {
		t.Fatalf("info wrong: %+v", info)
	}
	api.fileErr = &gapi.APIError{Status: 403, Reason: "ACCESS_TOKEN_SCOPE_INSUFFICIENT", Message: "scope"}
	info, err = svc.Info(context.Background(), fixtureID)
	if err != nil || len(info.Warnings) != 1 || !strings.Contains(info.Warnings[0], "Drive metadata unavailable") {
		t.Fatalf("drive failure should be a warning: %+v %v", info, err)
	}
	api.fileErr = nil
	api.file = &gapi.File{ID: fixtureID, Trashed: true}
	info, _ = svc.Info(context.Background(), fixtureID)
	if len(info.Warnings) != 1 || !strings.Contains(info.Warnings[0], "trash") {
		t.Fatalf("trashed warning missing: %+v", info.Warnings)
	}
}

func TestOutline(t *testing.T) {
	svc, _ := newService(t)
	res, err := svc.Outline(context.Background(), fixtureID, "")
	if err != nil || len(res.Tabs) != 2 || !strings.Contains(res.Text, "{h.bg}") {
		t.Fatalf("outline: %+v %v", res, err)
	}
	res, err = svc.Outline(context.Background(), fixtureID, "Notes")
	if err != nil || len(res.Tabs) != 1 || res.Tabs[0].Number != 2 {
		t.Fatalf("outline tab filter: %+v %v", res, err)
	}
	_, err = svc.Outline(context.Background(), fixtureID, "nope")
	var se *Error
	if !errors.As(err, &se) || se.Class != "not_found" {
		t.Fatalf("missing tab: %v", err)
	}
}

func TestResolveScope(t *testing.T) {
	d := doctest.Fixture(t)
	cases := []struct {
		name  string
		scope ReadScope
		tab   int
		seg   string
		from  int
		to    int
		class string
	}{
		{"whole body", ReadScope{}, 1, "body", 0, 16, ""},
		{"tab by title", ReadScope{Tab: "notes"}, 2, "body", 0, 3, ""},
		{"heading id", ReadScope{HeadingID: "h.det"}, 1, "body", 8, 13, ""},
		{"heading id other tab", ReadScope{HeadingID: "h.notes"}, 2, "body", 1, 3, ""},
		{"heading text", ReadScope{Heading: "background"}, 1, "body", 2, 13, ""},
		{"heading with level", ReadScope{Heading: "Details", HeadingLevel: 2}, 1, "body", 8, 13, ""},
		{"handle range", ReadScope{FromHandle: "p5", ToHandle: "p7"}, 1, "body", 5, 8, ""},
		{"from only", ReadScope{FromHandle: "p12"}, 1, "body", 13, 16, ""},
		{"continue", ReadScope{ContinueFrom: "tbl1"}, 1, "body", 11, 16, ""},
		{"header", ReadScope{Segment: "header"}, 1, "header1", 0, 1, ""},
		{"footnote by number", ReadScope{Segment: "footnote1"}, 1, "footnote1", 0, 1, ""},
		{"missing tab", ReadScope{Tab: "zzz"}, 0, "", 0, 0, "not_found"},
		{"missing heading id", ReadScope{HeadingID: "h.zzz"}, 0, "", 0, 0, "not_found"},
		{"missing heading", ReadScope{Heading: "Nonexistent"}, 0, "", 0, 0, "not_found"},
		{"heading wrong level", ReadScope{Heading: "Background", HeadingLevel: 3}, 0, "", 0, 0, "not_found"},
		{"heading occurrence too high", ReadScope{Heading: "Background", Occurrence: 2}, 0, "", 0, 0, "not_found"},
		{"heading in header", ReadScope{Segment: "header", Heading: "x"}, 0, "", 0, 0, "invalid"},
		{"bad segment", ReadScope{Segment: "sidebar"}, 0, "", 0, 0, "invalid"},
		{"missing footer", ReadScope{Segment: "footer"}, 0, "", 0, 0, "not_found"},
		{"footnote out of range", ReadScope{Segment: "footnote9"}, 0, "", 0, 0, "not_found"},
		{"bad handle", ReadScope{FromHandle: "p99"}, 0, "", 0, 0, "not_found"},
		{"cell handle", ReadScope{FromHandle: "tbl1:r1c1/p1"}, 0, "", 0, 0, "invalid"},
		{"reversed range", ReadScope{FromHandle: "p7", ToHandle: "p5"}, 0, "", 0, 0, "invalid"},
		{"foreign handle", ReadScope{FromHandle: "tab2/p1"}, 0, "", 0, 0, "not_found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := ResolveScope(d, tc.scope)
			if tc.class != "" {
				var se *Error
				if !errors.As(err, &se) || se.Class != tc.class {
					t.Fatalf("want class %s, got %v", tc.class, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if res.Tab.Number != tc.tab || res.Segment.Label() != tc.seg || res.From != tc.from || res.To != tc.to {
				t.Fatalf("got tab %d %s [%d,%d) %q", res.Tab.Number, res.Segment.Label(), res.From, res.To, res.Description)
			}
			if res.Description == "" {
				t.Fatal("description missing")
			}
		})
	}
}

func TestResolveScopeAmbiguousHeading(t *testing.T) {
	d := doctest.Fixture(t)
	// Duplicate a heading text in tab 1 to force ambiguity.
	body := d.Tabs[0].Body
	body.Blocks[13].Paragraph.Runs[0].Text = "Background\n"
	_, err := ResolveScope(d, ReadScope{Heading: "Background"})
	var se *Error
	if !errors.As(err, &se) || se.Class != "ambiguous" || !strings.Contains(se.Message, "p12") {
		t.Fatalf("got %v", err)
	}
	res, err := ResolveScope(d, ReadScope{Heading: "Background", Occurrence: 2})
	if err != nil || res.From != 13 {
		t.Fatalf("occurrence 2: %+v %v", res, err)
	}
}

func TestRead(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	res, err := svc.Read(ctx, ReadRequest{Document: fixtureID, Scope: ReadScope{HeadingID: "h.det"}, Options: render.Options{WithHandles: true}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Text, "[p8] ## Details {h.det}") || res.Blocks != 5 || res.Segment != "body" || res.TabNumber != 1 || res.Truncated {
		t.Fatalf("read section: %+v", res)
	}
	res, err = svc.Read(ctx, ReadRequest{Document: fixtureID, Format: "text", Scope: ReadScope{Segment: "header"}})
	if err != nil || res.Text != "Confidential draft" || res.Segment != "header1" {
		t.Fatalf("read header text: %+v %v", res, err)
	}
	res, err = svc.Read(ctx, ReadRequest{Document: fixtureID, Format: "raw", Scope: ReadScope{FromHandle: "p2", ToHandle: "p3"}})
	if err != nil {
		t.Fatal(err)
	}
	var elems []gdocs.StructuralElement
	if err := json.Unmarshal([]byte(res.Text), &elems); err != nil || len(elems) != 2 || elems[0].Paragraph.ParagraphStyle.HeadingID != "h.bg" {
		t.Fatalf("raw: %v %d", err, len(elems))
	}
	res, err = svc.Read(ctx, ReadRequest{Document: fixtureID, Format: "raw", Options: render.Options{MaxChars: 400}})
	if err != nil || !res.Truncated || res.ContinueFrom == "" {
		t.Fatalf("raw budget: %+v %v", res, err)
	}
	res, err = svc.Read(ctx, ReadRequest{Document: fixtureID, Options: render.Options{MaxChars: 100}})
	if err != nil || !res.Truncated || res.ContinueFrom == "" {
		t.Fatalf("markdown budget: %+v %v", res, err)
	}
	cont, err := svc.Read(ctx, ReadRequest{Document: fixtureID, Scope: ReadScope{ContinueFrom: res.ContinueFrom}})
	if err != nil || cont.Blocks == 0 || strings.Contains(cont.Text, "Quarterly Report") {
		t.Fatalf("continue: %+v %v", cont, err)
	}
	if _, err := svc.Read(ctx, ReadRequest{Document: fixtureID, Format: "pdf"}); err == nil {
		t.Fatal("bad format should fail")
	}
	if _, err := svc.Read(ctx, ReadRequest{Document: fixtureID, Scope: ReadScope{Tab: "nope"}}); err == nil {
		t.Fatal("bad scope should fail")
	}
	res, err = svc.Read(ctx, ReadRequest{Document: fixtureID, Format: "raw", Scope: ReadScope{Segment: "footnote1"}})
	if err != nil || !strings.Contains(res.Text, "See appendix.") {
		t.Fatalf("raw footnote: %+v %v", res, err)
	}
}

func TestErrorType(t *testing.T) {
	e := Errorf("stale", "handle %s moved", "p1")
	if e.Error() != "[stale] handle p1 moved" || e.Unwrap() != nil {
		t.Fatalf("got %q", e.Error())
	}
	if doc.Normalize(" x ") != "x" {
		t.Fatal("sanity")
	}
}
