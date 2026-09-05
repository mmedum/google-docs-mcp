package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-docs-mcp/internal/config"
	"github.com/mmedum/google-docs-mcp/internal/doc/doctest"
	"github.com/mmedum/google-docs-mcp/internal/gapi"
	"github.com/mmedum/google-docs-mcp/internal/gdocs"
	"github.com/mmedum/google-docs-mcp/internal/server"
	"github.com/mmedum/google-docs-mcp/internal/service"
)

const fixtureID = "1SyntheticFixtureDocumentIdXXXXXXXXXXXXXXXXXX"

type fakeAPI struct {
	raw      []byte
	err      error
	batches  []*gapi.BatchUpdateRequest
	comments []*gapi.DriveComment
	deleted  []string
}

func (f *fakeAPI) GetDocument(_ context.Context, id string, _ gapi.GetOptions) (*gapi.DocumentResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	if id != fixtureID {
		return nil, &gapi.APIError{Status: 404, Message: "missing"}
	}
	var d gdocs.Document
	if err := json.Unmarshal(f.raw, &d); err != nil {
		return nil, err
	}
	return &gapi.DocumentResult{Document: &d}, nil
}

func (f *fakeAPI) GetFile(_ context.Context, id string) (*gapi.File, error) {
	return &gapi.File{ID: id, Name: "Quarterly Report", Owners: []*gapi.User{{EmailAddress: "o@example.test"}}}, nil
}

func (f *fakeAPI) BatchUpdate(_ context.Context, id string, req *gapi.BatchUpdateRequest) (*gapi.BatchUpdateResponse, error) {
	f.batches = append(f.batches, req)
	raw := `{"replies":[{"insertComment":{"commentThread":{"commentId":"c1"}}}],"writeControl":{"requiredRevisionId":"rev-0002"},"suggestionResponses":[{"createdSuggestionIds":["suggest.x"]}]}`
	return &gapi.BatchUpdateResponse{DocumentID: id, WriteControl: &gapi.WriteControl{RequiredRevisionID: "rev-0002"}, Raw: json.RawMessage(raw)}, nil
}

func (f *fakeAPI) CreateDocument(_ context.Context, title string) (*gdocs.Document, error) {
	return &gdocs.Document{DocumentID: fixtureID, Title: title, RevisionID: "rev-new", Body: &gdocs.Body{Content: []*gdocs.StructuralElement{
		{StartIndex: 0, EndIndex: 1, SectionBreak: &gdocs.SectionBreak{}},
		{StartIndex: 1, EndIndex: 2, Paragraph: &gdocs.Paragraph{Elements: []*gdocs.ParagraphElement{{StartIndex: 1, EndIndex: 2, TextRun: &gdocs.TextRun{Content: "\n"}}}}},
	}}}, nil
}

func (f *fakeAPI) SearchFiles(_ context.Context, q string, limit int, pageToken string) (*gapi.FileList, error) {
	return &gapi.FileList{Files: []*gapi.File{{ID: "d1", Name: "Doc One"}}}, nil
}

func (f *fakeAPI) Export(_ context.Context, id, mimeType string) ([]byte, error) {
	return []byte("# exported"), nil
}

func (f *fakeAPI) CreateComment(_ context.Context, fileID, content, quote string) (*gapi.DriveComment, error) {
	return &gapi.DriveComment{ID: "dc1", Content: content}, nil
}

func (f *fakeAPI) ListComments(_ context.Context, fileID string, includeDeleted bool) ([]*gapi.DriveComment, error) {
	return f.comments, nil
}

func (f *fakeAPI) GetComment(_ context.Context, fileID, commentID string) (*gapi.DriveComment, error) {
	for _, c := range f.comments {
		if c.ID == commentID {
			return c, nil
		}
	}
	return nil, &gapi.APIError{Status: 404, Message: "missing"}
}

func (f *fakeAPI) CreateReply(_ context.Context, fileID, commentID, content, action string) (*gapi.DriveReply, error) {
	return &gapi.DriveReply{ID: "r1", Content: content, Action: action}, nil
}

func (f *fakeAPI) UpdateComment(_ context.Context, _, commentID, content string) (*gapi.DriveComment, error) {
	return &gapi.DriveComment{ID: commentID, Content: content}, nil
}

func (f *fakeAPI) UpdateReply(_ context.Context, _, _, replyID, content string) (*gapi.DriveReply, error) {
	return &gapi.DriveReply{ID: replyID, Content: content}, nil
}

func (f *fakeAPI) DeleteComment(_ context.Context, fileID, commentID string) error {
	f.deleted = append(f.deleted, commentID)
	return nil
}

func (f *fakeAPI) DeleteReply(_ context.Context, fileID, commentID, replyID string) error {
	f.deleted = append(f.deleted, commentID+"/"+replyID)
	return nil
}

func (f *fakeAPI) ListRevisions(_ context.Context, fileID string) ([]*gapi.Revision, error) {
	return []*gapi.Revision{{ID: "1", ModifiedTime: "2026-09-01T00:00:00Z"}, {ID: "2", ModifiedTime: "2026-09-02T00:00:00Z"}}, nil
}

func (f *fakeAPI) ExportRevision(_ context.Context, fileID, revisionID, mimeType string) ([]byte, error) {
	return []byte("# exported\n\nold line\n"), nil
}

func connect(t *testing.T, api *fakeAPI) *mcp.ClientSession {
	t.Helper()
	return connectWith(t, api, config.Config{DefaultWriteMode: config.WriteDirect})
}

func connectWith(t *testing.T, api *fakeAPI, cfg config.Config) *mcp.ClientSession {
	t.Helper()
	svc := service.New(api, service.Options{Preview: cfg.Preview, ReadOnly: cfg.ReadOnly, Destructive: cfg.EnableDestructive, DefaultWriteMode: cfg.DefaultWriteMode, ExportDir: cfg.ExportDir})
	// Seed the handle memory as a read would, so tools may target handles.
	_, _ = svc.Fetch(context.Background(), fixtureID)
	srv := server.New(server.Deps{Service: svc, Config: cfg, Version: "test"})
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error: %v", name, err)
	}
	return res
}

func textOf(res *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func TestListToolsAndSchemas(t *testing.T) {
	cs := connect(t, &fakeAPI{raw: doctest.RawFixture(t)})
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	readTools := map[string]bool{"get_document": true, "get_outline": true, "read_document": true, "find_in_document": true, "search_documents": true, "export_document": true, "list_suggestions": true,
		"list_comments": true, "list_revisions": true, "diff_revisions": true}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		if tool.Annotations == nil {
			t.Errorf("%s has no annotations", tool.Name)
			continue
		}
		if tool.Annotations.ReadOnlyHint != readTools[tool.Name] {
			t.Errorf("%s readOnlyHint = %v", tool.Name, tool.Annotations.ReadOnlyHint)
		}
		if len(tool.Description) < 150 {
			t.Errorf("%s description too short", tool.Name)
		}
		schema, _ := json.Marshal(tool.InputSchema)
		if tool.Name != "search_documents" && tool.Name != "create_document" && !bytes.Contains(schema, []byte(`"document"`)) {
			t.Errorf("%s schema lacks document: %s", tool.Name, schema)
		}
		if bytes.Contains(schema, []byte("oneOf")) || bytes.Contains(schema, []byte("anyOf")) || bytes.Contains(schema, []byte("$ref")) {
			t.Errorf("%s schema uses a union or reference", tool.Name)
		}
	}
	want := "add_comment,create_document,diff_revisions,edit_document,edit_table,export_document,find_in_document,format_document,get_document,get_outline,insert_object,layout_document,list_comments,list_revisions,list_suggestions,manage_tabs,read_document,reply_comment,review_suggestion,search_documents"
	if strings.Join(names, ",") != want {
		t.Fatalf("tools = %v", names)
	}
	if strings.Contains(want, "delete_") {
		t.Fatal("destructive tools must not register by default")
	}
	// Destructive tools register only with the flag, carrying the interaction hint.
	ds := connectWith(t, &fakeAPI{raw: doctest.RawFixture(t)}, config.Config{EnableDestructive: true, DefaultWriteMode: config.WriteDirect})
	dres, err := ds.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, tool := range dres.Tools {
		if tool.Name == "delete_comment" || tool.Name == "delete_tab" {
			found[tool.Name] = true
			if tool.Annotations == nil || tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint || tool.Meta["anthropic/requiresUserInteraction"] != true {
				t.Errorf("%s annotations = %+v meta = %v", tool.Name, tool.Annotations, tool.Meta)
			}
		}
	}
	if len(found) != 2 || len(dres.Tools) != len(res.Tools)+2 {
		t.Fatalf("destructive tools = %v (%d tools)", found, len(dres.Tools))
	}
	// Read-only servers register only the read tools.
	ro := connectWith(t, &fakeAPI{raw: doctest.RawFixture(t)}, config.Config{ReadOnly: true, DefaultWriteMode: config.WriteDirect})
	res, err = ro.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if !readTools[tool.Name] {
			t.Errorf("read-only server exposes %s", tool.Name)
		}
	}
	if len(res.Tools) != len(readTools) {
		t.Fatalf("read-only tools = %d", len(res.Tools))
	}
}

func TestWriteToolsEndToEnd(t *testing.T) {
	api := &fakeAPI{raw: doctest.RawFixture(t)}
	cs := connectWith(t, api, config.Config{Preview: true, DefaultWriteMode: config.WriteSuggest, ExportDir: t.TempDir()})
	res := call(t, cs, "edit_document", map[string]any{"document": fixtureID, "ops": []map[string]any{
		{"op": "replace", "target": map[string]any{"text": "Second point"}, "content": "Second item"},
		{"op": "append", "content": "- tail"},
	}})
	if res.IsError {
		t.Fatalf("edit: %s", textOf(res))
	}
	txt := textOf(res)
	if !strings.Contains(txt, "applied 2 op(s) in suggest mode") || !strings.Contains(txt, "minimal diff") || !strings.Contains(txt, "suggestion ids: suggest.x") || !strings.Contains(txt, "region after the edit") {
		t.Fatalf("edit text: %s", txt)
	}
	if len(api.batches) != 1 || api.batches[0].WriteControl.WriteMode != "SUGGEST" {
		t.Fatalf("batch: %+v", api.batches)
	}
	res = call(t, cs, "edit_document", map[string]any{"document": fixtureID, "mode": "comment", "dry_run": true, "ops": []map[string]any{{"op": "delete", "target": map[string]any{"handle": "p5"}}}})
	if res.IsError || !strings.Contains(textOf(res), "dry run in comment mode") || !strings.Contains(textOf(res), "Proposed deletion") || len(api.batches) != 1 {
		t.Fatalf("dry run: %s", textOf(res))
	}
	res = call(t, cs, "format_document", map[string]any{"document": fixtureID, "mode": "direct", "ops": []map[string]any{
		{"op": "text_style", "target": map[string]any{"text": "Revenue"}, "bold": true, "color": "#ff0000"},
		{"op": "paragraph_style", "target": map[string]any{"handle": "p11"}, "named_style": "heading_3", "alignment": "center"},
		{"op": "bullets", "target": map[string]any{"from_handle": "p9", "to_handle": "p10"}, "bullets": "none"},
	}})
	if res.IsError || !strings.Contains(textOf(res), "applied 3 op(s) in direct mode") {
		t.Fatalf("format: %s", textOf(res))
	}
	for _, bad := range []map[string]any{
		{"document": fixtureID, "ops": []map[string]any{{"op": "text_style", "target": map[string]any{"handle": "p5"}, "color": "red"}}},
		{"document": fixtureID, "ops": []map[string]any{{"op": "paragraph_style", "target": map[string]any{"handle": "p5"}, "named_style": "HUGE"}}},
		{"document": fixtureID, "ops": []map[string]any{{"op": "paragraph_style", "target": map[string]any{"handle": "p5"}, "alignment": "LEFT"}}},
		{"document": fixtureID, "ops": []map[string]any{{"op": "text_style", "target": map[string]any{"handle": "p5"}, "baseline": "UP"}}},
		{"document": fixtureID, "ops": []map[string]any{{"op": "sparkle", "target": map[string]any{"handle": "p5"}}}},
	} {
		res = call(t, cs, "format_document", bad)
		if !res.IsError || !strings.HasPrefix(textOf(res), "[invalid]") {
			t.Errorf("format validation: %v -> %s", bad, textOf(res))
		}
	}
	res = call(t, cs, "edit_document", map[string]any{"document": fixtureID, "ops": []map[string]any{{"op": "teleport"}}})
	if !res.IsError || !strings.HasPrefix(textOf(res), "[invalid] op 0: unknown op") {
		t.Fatalf("edit validation: %s", textOf(res))
	}
	res = call(t, cs, "create_document", map[string]any{"title": "Fresh", "content": "# Hi"})
	if res.IsError || !strings.Contains(textOf(res), "created \"Fresh\"") {
		t.Fatalf("create: %s", textOf(res))
	}
	res = call(t, cs, "search_documents", map[string]any{"title": "Doc"})
	if res.IsError || !strings.Contains(textOf(res), "Doc One") {
		t.Fatalf("search: %s", textOf(res))
	}
	res = call(t, cs, "find_in_document", map[string]any{"document": fixtureID, "query": "point"})
	if res.IsError || !strings.Contains(textOf(res), "3 match(es)") {
		t.Fatalf("find: %s", textOf(res))
	}
	res = call(t, cs, "export_document", map[string]any{"document": fixtureID, "format": "md"})
	if res.IsError || !strings.Contains(textOf(res), "# exported") {
		t.Fatalf("export: %s", textOf(res))
	}
	res = call(t, cs, "list_suggestions", map[string]any{"document": fixtureID})
	if res.IsError || !strings.Contains(textOf(res), "s1 [p3] replace") {
		t.Fatalf("suggestions: %s", textOf(res))
	}
	res = call(t, cs, "review_suggestion", map[string]any{"document": fixtureID, "action": "accept", "all": true})
	if res.IsError || !strings.Contains(textOf(res), "accepted 1 suggestion(s)") {
		t.Fatalf("review: %s", textOf(res))
	}
}

func TestGetDocumentTool(t *testing.T) {
	cs := connect(t, &fakeAPI{raw: doctest.RawFixture(t)})
	res := call(t, cs, "get_document", map[string]any{"document": "https://docs.google.com/document/d/" + fixtureID + "/edit"})
	if res.IsError {
		t.Fatalf("error: %s", textOf(res))
	}
	if !strings.Contains(textOf(res), "Quarterly Report") || !strings.Contains(textOf(res), "write modes direct/comment") {
		t.Fatalf("text: %s", textOf(res))
	}
	// Read tools carry everything in their text: a client may show the
	// model only the structured form when one is present.
	if res.StructuredContent != nil || !strings.Contains(textOf(res), "revision rev-0001") || !strings.Contains(textOf(res), "2 tab(s)") {
		t.Fatalf("read result: structured=%v text=%s", res.StructuredContent, textOf(res))
	}
}

func TestReadAndOutlineTools(t *testing.T) {
	cs := connect(t, &fakeAPI{raw: doctest.RawFixture(t)})
	res := call(t, cs, "get_outline", map[string]any{"document": fixtureID, "tab": "Main"})
	if res.IsError || !strings.Contains(textOf(res), "[p2] H1 Background {h.bg}") {
		t.Fatalf("outline: %s", textOf(res))
	}
	res = call(t, cs, "read_document", map[string]any{"document": fixtureID, "heading_id": "h.det", "with_handles": true, "include_suggestions": true})
	if res.IsError {
		t.Fatalf("read: %s", textOf(res))
	}
	text := textOf(res)
	if !strings.HasPrefix(text, "<!-- tab 1 body, section \"Details\"") || !strings.Contains(text, "[p8] ## Details {h.det}") || !strings.Contains(text, "| **Name** |") {
		t.Fatalf("read text: %s", text)
	}
	if res.StructuredContent != nil || !strings.Contains(text, "· revision rev-0001 · 5 block(s) -->") {
		t.Fatalf("read result: structured=%v header=%s", res.StructuredContent, text[:80])
	}
	// Handles come by default, since an edit targets them; false drops them.
	res = call(t, cs, "read_document", map[string]any{"document": fixtureID, "heading_id": "h.det"})
	if res.IsError || !strings.Contains(textOf(res), "[p8] ## Details {h.det}") {
		t.Fatalf("read without with_handles: %s", textOf(res))
	}
	res = call(t, cs, "read_document", map[string]any{"document": fixtureID, "heading_id": "h.det", "with_handles": false})
	if res.IsError || strings.Contains(textOf(res), "[p8]") || !strings.Contains(textOf(res), "## Details") {
		t.Fatalf("read with with_handles false: %s", textOf(res))
	}
	res = call(t, cs, "read_document", map[string]any{"document": fixtureID, "format": "raw", "from_handle": "p2", "to_handle": "p2"})
	if res.IsError || !strings.HasPrefix(textOf(res), "[{") {
		t.Fatalf("raw read: %s", textOf(res))
	}
	res = call(t, cs, "read_document", map[string]any{"document": fixtureID, "max_chars": 150})
	if res.IsError || !strings.Contains(textOf(res), "truncated, continue_from") {
		t.Fatalf("budget: %s", textOf(res))
	}
}

func TestToolErrorsAreResultsNotProtocolErrors(t *testing.T) {
	cs := connect(t, &fakeAPI{raw: doctest.RawFixture(t)})
	cases := []struct {
		tool  string
		args  map[string]any
		class string
	}{
		{"get_document", map[string]any{"document": "nope"}, "[invalid]"},
		{"get_document", map[string]any{"document": "1UnknownDocumentIdYYYYYYYYYYYYYYYYYYYYYYYYYY"}, "[not_found]"},
		{"read_document", map[string]any{"document": fixtureID, "heading": "Missing"}, "[not_found]"},
		{"read_document", map[string]any{"document": fixtureID, "segment": "sidebar"}, "[invalid]"},
		{"read_document", map[string]any{"document": fixtureID, "heading_level": 9}, "[invalid]"},
		{"read_document", map[string]any{"document": fixtureID, "occurrence": -1}, "[invalid]"},
		{"read_document", map[string]any{"document": fixtureID, "max_chars": -5}, "[invalid]"},
		{"read_document", map[string]any{"document": fixtureID, "format": "docx"}, "[invalid]"},
		{"get_outline", map[string]any{"document": fixtureID, "tab": "zzz"}, "[not_found]"},
	}
	for _, tc := range cases {
		res := call(t, cs, tc.tool, tc.args)
		if !res.IsError || !strings.HasPrefix(textOf(res), tc.class) {
			t.Errorf("%s %v: isError=%v text=%q", tc.tool, tc.args, res.IsError, textOf(res))
		}
	}
	// Schema validation failure (missing required field) is also a tool error.
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_document", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("protocol error on validation: %v", err)
	}
	if !res.IsError {
		t.Fatal("missing document should be a tool error")
	}
}

func TestAuthFailureSurfacesAsAuthClass(t *testing.T) {
	cs := connect(t, &fakeAPI{err: &gapi.AuthError{Code: "invalid_grant", Msg: "expired"}})
	res := call(t, cs, "get_outline", map[string]any{"document": fixtureID})
	if !res.IsError || !strings.HasPrefix(textOf(res), "[auth]") || !strings.Contains(textOf(res), "login") {
		t.Fatalf("got %q", textOf(res))
	}
}

// Read tools must not declare an output schema: a client that shows the
// model only structuredContent would then hide the content itself.
func TestReadToolsAreTextOnly(t *testing.T) {
	cs := connect(t, &fakeAPI{raw: doctest.RawFixture(t)})
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	readOnly := 0
	for _, tool := range res.Tools {
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			continue
		}
		readOnly++
		if tool.OutputSchema != nil {
			t.Errorf("%s is read-only but declares an output schema", tool.Name)
		}
	}
	if readOnly < 8 {
		t.Fatalf("only %d read-only tools found", readOnly)
	}
}

func TestDumpSchemas(t *testing.T) {
	srv := server.New(server.Deps{Config: config.Config{}, Version: "test"})
	var buf bytes.Buffer
	if err := server.DumpSchemas(context.Background(), srv, &buf, "test"); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Server, Version, SDK string
		Tools                []struct{ Name string }
		ResourceTemplates    []struct{ URITemplate string }
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Server != server.Name || out.SDK != server.SDKVersion || len(out.Tools) != 20 || out.Tools[0].Name != "add_comment" ||
		len(out.ResourceTemplates) != 3 || out.ResourceTemplates[0].URITemplate != "gdocs://{document}" {
		t.Fatalf("dump = %+v", out)
	}
	if !errors.Is(context.Canceled, context.Canceled) {
		t.Fatal("sanity")
	}
}

func TestCommentAndHistoryTools(t *testing.T) {
	api := &fakeAPI{raw: doctest.RawFixture(t), comments: []*gapi.DriveComment{
		{ID: "c1", Content: "Check this", Author: &gapi.User{DisplayName: "Ann"}, QuotedFileContent: &gapi.QuotedText{Value: "Second point"},
			Replies: []*gapi.DriveReply{{ID: "r1", Content: "ok", Author: &gapi.User{DisplayName: "Bob"}, Action: "resolve"}}, Resolved: true},
	}}
	cs := connectWith(t, api, config.Config{EnableDestructive: true, DefaultWriteMode: config.WriteDirect})
	res := call(t, cs, "list_comments", map[string]any{"document": fixtureID})
	if res.IsError || !strings.Contains(textOf(res), "c1 [p7] by Ann [resolved] on “Second point”: Check this") || !strings.Contains(textOf(res), "↳ Bob resolved: ok") {
		t.Fatalf("list_comments: %q", textOf(res))
	}
	res = call(t, cs, "list_comments", map[string]any{"document": fixtureID, "hide_resolved": true})
	if res.IsError || !strings.HasPrefix(textOf(res), "0 comment thread(s) shown (0 open, 1 resolved)") {
		t.Fatalf("hide_resolved: %q", textOf(res))
	}
	res = call(t, cs, "add_comment", map[string]any{"document": fixtureID, "target": map[string]any{"text": "First point"}, "content": "Why?"})
	if res.IsError || !strings.Contains(textOf(res), "comment dc1 posted on p5") || !strings.Contains(textOf(res), "unanchored") {
		t.Fatalf("add_comment: %q", textOf(res))
	}
	res = call(t, cs, "reply_comment", map[string]any{"document": fixtureID, "comment_id": "c1", "action": "reopen"})
	if res.IsError || textOf(res) != "reopened comment c1" {
		t.Fatalf("reply_comment: %q", textOf(res))
	}
	res = call(t, cs, "reply_comment", map[string]any{"document": fixtureID, "comment_id": "c1"})
	if !res.IsError || !strings.HasPrefix(textOf(res), "[invalid]") {
		t.Fatalf("reply without content: %q", textOf(res))
	}
	res = call(t, cs, "delete_comment", map[string]any{"document": fixtureID, "comment_id": "c1", "reply_id": "r1"})
	if res.IsError || textOf(res) != "deleted reply r1 of comment c1" || len(api.deleted) != 1 || api.deleted[0] != "c1/r1" {
		t.Fatalf("delete_comment: %q %v", textOf(res), api.deleted)
	}
	res = call(t, cs, "list_revisions", map[string]any{"document": fixtureID, "limit": 1})
	if res.IsError || !strings.Contains(textOf(res), "2 revision(s), newest first, showing 1") || !strings.Contains(textOf(res), "- 2 at 2026-09-02") {
		t.Fatalf("list_revisions: %q", textOf(res))
	}
	res = call(t, cs, "diff_revisions", map[string]any{"document": fixtureID, "from": "1"})
	if res.IsError || !strings.Contains(textOf(res), "revision 1 → current (md)") || !strings.Contains(textOf(res), "-old line") {
		t.Fatalf("diff_revisions: %q", textOf(res))
	}
	res = call(t, cs, "read_document", map[string]any{"document": fixtureID, "revision": "1", "max_chars": 12})
	if res.IsError || !strings.Contains(textOf(res), "revision 1 (md export; no handles, no revision_id) · truncated -->") || !strings.HasSuffix(textOf(res), "# exported\n\n") {
		t.Fatalf("read at revision: %q", textOf(res))
	}
	// Without the flag the delete tool is unknown to the server.
	plain := connect(t, &fakeAPI{raw: doctest.RawFixture(t)})
	if _, err := plain.CallTool(context.Background(), &mcp.CallToolParams{Name: "delete_comment", Arguments: map[string]any{"document": fixtureID, "comment_id": "c1"}}); err == nil {
		t.Fatal("delete_comment should not exist without the flag")
	}
}

func TestLayoutToolEndToEnd(t *testing.T) {
	api := &fakeAPI{raw: doctest.RawFixture(t)}
	cs := connectWith(t, api, config.Config{DefaultWriteMode: config.WriteDirect})
	res := call(t, cs, "layout_document", map[string]any{"document": fixtureID, "dry_run": true, "ops": []map[string]any{
		{"op": "page", "page_width_pt": 595, "page_height_pt": 842, "margin_left_pt": 36, "landscape": true},
		{"op": "named_style", "style": "heading_1", "color": "#1a73e8", "size_pt": 20},
		{"op": "section", "target": map[string]any{"handle": "p5"}, "columns": 2, "column_separator": "between"},
		{"op": "section_break", "location": map[string]any{"at": "after", "of": map[string]any{"handle": "p5"}}, "section_type": "continuous"},
	}})
	if res.IsError {
		t.Fatalf("layout: %s", textOf(res))
	}
	for _, want := range []string{"op 0 page: page setup of tab 1", "op 1 named_style: heading_1 in tab 1",
		"requests: updateSectionStyle, insertSectionBreak, updateNamedStyle, updateDocumentStyle"} {
		if !strings.Contains(textOf(res), want) {
			t.Errorf("layout text lacks %q:\n%s", want, textOf(res))
		}
	}
	// A4 in points is recognised on the way back out, in get_document.
	res = call(t, cs, "layout_document", map[string]any{"document": fixtureID, "ops": []map[string]any{{"op": "page"}}})
	if !res.IsError || !strings.Contains(textOf(res), "page changes nothing") {
		t.Fatalf("empty page op: %s", textOf(res))
	}
	res = call(t, cs, "layout_document", map[string]any{"document": fixtureID, "ops": []map[string]any{{"op": "nonsense"}}})
	if !res.IsError || !strings.HasPrefix(textOf(res), "[invalid] op 0: unknown op") {
		t.Fatalf("unknown layout op: %s", textOf(res))
	}
}

func TestObjectAndNamedRangeToolsEndToEnd(t *testing.T) {
	api := &fakeAPI{raw: doctest.RawFixture(t)}
	cs := connectWith(t, api, config.Config{DefaultWriteMode: config.WriteDirect})
	res := call(t, cs, "insert_object", map[string]any{"document": fixtureID, "action": "replace",
		"object": "kix.img1", "url": "https://example.test/new.png", "crop": true, "dry_run": true})
	if res.IsError || !strings.Contains(textOf(res), "requests: replaceImage") {
		t.Fatalf("replace image: %s", textOf(res))
	}
	// A floating image is reachable only through delete_object: no range
	// covers it, so no edit_document delete can name it.
	res = call(t, cs, "insert_object", map[string]any{"document": fixtureID, "action": "delete", "object": "kix.pos1", "dry_run": true})
	if res.IsError || !strings.Contains(textOf(res), "requests: deletePositionedObject") {
		t.Fatalf("delete floating object: %s", textOf(res))
	}
	res = call(t, cs, "insert_object", map[string]any{"document": fixtureID, "action": "sideways", "object": "kix.pos1"})
	if !res.IsError || !strings.Contains(textOf(res), "use insert, replace or delete") {
		t.Fatalf("unknown action: %s", textOf(res))
	}
	res = call(t, cs, "edit_document", map[string]any{"document": fixtureID, "dry_run": true, "ops": []map[string]any{
		{"op": "create_named_range", "name": "intro", "target": map[string]any{"handle": "p5"}},
	}})
	if res.IsError || !strings.Contains(textOf(res), "requests: createNamedRange") {
		t.Fatalf("create named range: %s", textOf(res))
	}
	// A named range is a target, and unlike a handle it survives edits.
	res = call(t, cs, "edit_document", map[string]any{"document": fixtureID, "dry_run": true, "ops": []map[string]any{
		{"op": "replace", "target": map[string]any{"named_range": "key finding"}, "content": "Second thought"},
	}})
	if res.IsError || !strings.Contains(textOf(res), `named range "key finding"`) {
		t.Fatalf("target a named range: %s", textOf(res))
	}
	res = call(t, cs, "edit_table", map[string]any{"document": fixtureID, "dry_run": true, "ops": []map[string]any{
		{"op": "style_columns", "table": "tbl1", "column_numbers": []int{1}, "width_pt": 120},
		{"op": "style_rows", "table": "tbl1", "row_numbers": []int{1}, "min_height_pt": 24, "prevent_overflow": true},
	}})
	if res.IsError || !strings.Contains(textOf(res), "requests: updateTableRowStyle, updateTableColumnProperties") {
		t.Fatalf("table line styles: %s", textOf(res))
	}
}

func TestCommentEditAndSuggestionDiscard(t *testing.T) {
	api := &fakeAPI{raw: doctest.RawFixture(t), comments: []*gapi.DriveComment{
		{ID: "c1", Content: "Check this", Author: &gapi.User{DisplayName: "Ann"},
			Replies: []*gapi.DriveReply{{ID: "r1", Content: "ok", Author: &gapi.User{DisplayName: "Bob"}}}},
	}}
	cs := connectWith(t, api, config.Config{DefaultWriteMode: config.WriteDirect})
	res := call(t, cs, "reply_comment", map[string]any{"document": fixtureID, "comment_id": "c1", "action": "edit", "content": "rewritten"})
	if res.IsError || !strings.Contains(textOf(res), "rewrote comment c1") {
		t.Fatalf("edit comment: %s", textOf(res))
	}
	res = call(t, cs, "reply_comment", map[string]any{"document": fixtureID, "comment_id": "c1", "action": "edit", "reply_id": "r1", "content": "still ok"})
	if res.IsError || !strings.Contains(textOf(res), "rewrote reply r1 of comment c1") {
		t.Fatalf("edit reply: %s", textOf(res))
	}
	res = call(t, cs, "reply_comment", map[string]any{"document": fixtureID, "comment_id": "c1", "action": "edit", "reply_id": "nope", "content": "x"})
	if !res.IsError || !strings.Contains(textOf(res), "has no reply nope") {
		t.Fatalf("edit unknown reply: %s", textOf(res))
	}
	// Discarding needs preview, like accepting and rejecting.
	res = call(t, cs, "review_suggestion", map[string]any{"document": fixtureID, "action": "discard", "all": true})
	if !res.IsError || !strings.Contains(textOf(res), "Developer Preview") {
		t.Fatalf("discard without preview: %s", textOf(res))
	}
	pv := connectWith(t, &fakeAPI{raw: doctest.RawFixture(t)}, config.Config{DefaultWriteMode: config.WriteDirect, Preview: true})
	res = call(t, pv, "review_suggestion", map[string]any{"document": fixtureID, "action": "discard", "all": true})
	if res.IsError || !strings.Contains(textOf(res), "discarded 1 suggestion(s)") {
		t.Fatalf("discard: %s", textOf(res))
	}
	res = call(t, pv, "review_suggestion", map[string]any{"document": fixtureID, "action": "shred", "all": true})
	if !res.IsError || !strings.Contains(textOf(res), "accept, reject or discard") {
		t.Fatalf("unknown review action: %s", textOf(res))
	}
}

func TestStructureToolsEndToEnd(t *testing.T) {
	api := &fakeAPI{raw: doctest.RawFixture(t)}
	cs := connectWith(t, api, config.Config{EnableDestructive: true, DefaultWriteMode: config.WriteDirect})
	res := call(t, cs, "edit_table", map[string]any{"document": fixtureID, "dry_run": true, "ops": []map[string]any{
		{"op": "insert_rows", "table": "tbl1", "row": 2, "count": 1},
		{"op": "set_cells", "table": "tbl1", "cells": []map[string]any{{"cell": "r2c2", "content": "2"}}},
	}})
	// The cells are named in the grid the row insertion leaves, so they
	// are written in a batch of their own once it exists.
	if res.IsError || !strings.Contains(textOf(res), "op 0 insert_rows: tbl1 (2×2)") || !strings.Contains(textOf(res), "requests: insertTableRow") ||
		!strings.Contains(textOf(res), "then op 1: a later batch runs set_cells on tbl1") {
		t.Fatalf("edit_table dry run: %q", textOf(res))
	}
	res = call(t, cs, "edit_table", map[string]any{"document": fixtureID, "ops": []map[string]any{{"op": "shuffle", "table": "tbl1"}}})
	if !res.IsError || !strings.HasPrefix(textOf(res), "[invalid] op 0: unknown op") {
		t.Fatalf("bad table op: %q", textOf(res))
	}
	res = call(t, cs, "insert_object", map[string]any{"document": fixtureID, "kind": "date", "date": "2026-09-03", "date_format": "iso", "location": map[string]any{"at": "after", "of": map[string]any{"text": "Step one"}}, "dry_run": true})
	if res.IsError || !strings.Contains(textOf(res), "requests: insertDate") || strings.Contains(textOf(res), "startIndex") {
		t.Fatalf("insert_object: %q", textOf(res))
	}
	res = call(t, cs, "insert_object", map[string]any{"document": fixtureID, "kind": "date", "date": "yesterday", "location": map[string]any{"at": "end"}})
	if !res.IsError || !strings.HasPrefix(textOf(res), "[invalid] date") {
		t.Fatalf("bad date: %q", textOf(res))
	}
	res = call(t, cs, "manage_tabs", map[string]any{"document": fixtureID, "action": "rename", "tab": "Notes", "title": "Renamed"})
	if res.IsError || !strings.HasPrefix(textOf(res), "renamed tab t.1 to \"Renamed\"") {
		t.Fatalf("manage_tabs: %q", textOf(res))
	}
	res = call(t, cs, "manage_tabs", map[string]any{"document": fixtureID, "action": "delete", "tab": "Notes"})
	if !res.IsError || !strings.Contains(textOf(res), "delete_tab") {
		t.Fatalf("delete through manage_tabs: %q", textOf(res))
	}
	res = call(t, cs, "delete_tab", map[string]any{"document": fixtureID, "tab": "Notes"})
	if res.IsError || !strings.HasPrefix(textOf(res), "deleted tab t.1") {
		t.Fatalf("delete_tab: %q", textOf(res))
	}
	res = call(t, cs, "edit_document", map[string]any{"document": fixtureID, "dry_run": true, "ops": []map[string]any{{"op": "delete_header", "target": map[string]any{"segment": "header"}}}})
	if res.IsError || !strings.Contains(textOf(res), "requests: deleteHeader") {
		t.Fatalf("delete_header: %q", textOf(res))
	}
}

func TestResources(t *testing.T) {
	cs := connect(t, &fakeAPI{raw: doctest.RawFixture(t)})
	ctx := context.Background()
	tmpls, err := cs.ListResourceTemplates(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var uris []string
	for _, rt := range tmpls.ResourceTemplates {
		uris = append(uris, rt.URITemplate)
	}
	if strings.Join(uris, " ") != "gdocs://{document} gdocs://{document}/outline gdocs://{document}/tabs/{tab}" {
		t.Fatalf("templates = %v", uris)
	}

	read := func(uri string) (string, error) {
		t.Helper()
		res, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
		if err != nil {
			return "", err
		}
		if len(res.Contents) != 1 || res.Contents[0].MIMEType != "text/markdown" || res.Contents[0].URI != uri {
			t.Fatalf("%s: contents = %+v", uri, res.Contents)
		}
		return res.Contents[0].Text, nil
	}
	whole, err := read("gdocs://" + fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<!-- \"Quarterly Report\" · revision rev-0001 · 2 tab(s) -->\n", "<!-- tab 1 \"Main\" -->\n\n", "\n\n<!-- tab 2 \"Notes\" -->\n\n", "# Background", "| **Name** |"} {
		if !strings.Contains(whole, want) {
			t.Errorf("whole document lacks %q:\n%s", want, whole)
		}
	}
	if strings.Contains(whole, "[p2]") || strings.Contains(whole, "{++") {
		t.Errorf("whole document shows handles or suggestions:\n%s", whole)
	}
	outline, err := read("gdocs://" + fixtureID + "/outline")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(outline, "[p2] H1 Background {h.bg}") {
		t.Errorf("outline: %s", outline)
	}
	tab, err := read("gdocs://" + fixtureID + "/tabs/Notes")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tab, "<!-- tab 2 body · revision rev-0001 · ") {
		t.Errorf("tab: %s", tab)
	}
	for _, uri := range []string{"gdocs://1NoSuchDocumentIdXXXXXXXXXXXXXXXXXXXXXXXXXXX/outline", "gdocs://" + fixtureID + "/tabs/Missing", "gdocs://" + fixtureID + "/nope", "gdocs://" + fixtureID + "/tabs/Notes/extra"} {
		if _, err := read(uri); err == nil {
			t.Errorf("%s: expected an error", uri)
		} else if !strings.Contains(err.Error(), "not found") && !strings.Contains(err.Error(), "Resource not found") {
			t.Errorf("%s: %v", uri, err)
		}
	}
}

// toolArgs is one call's worth of arguments for every registered tool.
// It exists so a test can drive the whole surface rather than the two or
// three tools someone thought of: TestEveryToolHasArgs fails when a tool
// is added without an entry, so the logging guarantee below cannot
// quietly stop covering the newest tool. Whether a call succeeds against
// the fake does not matter here — a refusal is logged too, and a refusal
// is exactly where a message tends to quote the document.
var toolArgs = map[string]map[string]any{
	"add_comment":       {"document": fixtureID, "content": "a note", "target": map[string]any{"text": "Revenue"}},
	"create_document":   {"title": "scratch"},
	"delete_comment":    {"document": fixtureID, "comment_id": "c1"},
	"delete_tab":        {"document": fixtureID, "tab": "Tab 1"},
	"diff_revisions":    {"document": fixtureID, "from": "1"},
	"edit_document":     {"document": fixtureID, "mode": "direct", "ops": []any{map[string]any{"op": "append", "content": leakCanary}}},
	"edit_table":        {"document": fixtureID, "mode": "direct", "ops": []any{map[string]any{"op": "set_cells", "table": "tbl1", "cells": map[string]any{"r1c1": leakCanary}}}},
	"export_document":   {"document": fixtureID, "format": "md"},
	"find_in_document":  {"document": fixtureID, "query": "Revenue"},
	"format_document":   {"document": fixtureID, "mode": "direct", "ops": []any{map[string]any{"op": "text_style", "target": map[string]any{"text": "Revenue"}, "bold": true}}},
	"get_document":      {"document": fixtureID},
	"get_outline":       {"document": fixtureID},
	"insert_object":     {"document": fixtureID, "mode": "direct", "kind": "date", "date": "2026-09-05"},
	"layout_document":   {"document": fixtureID, "mode": "direct", "ops": []any{map[string]any{"op": "page", "page_size": "A4"}}},
	"list_comments":     {"document": fixtureID},
	"list_revisions":    {"document": fixtureID},
	"list_suggestions":  {"document": fixtureID},
	"manage_tabs":       {"document": fixtureID, "action": "add", "title": "Appendix"},
	"read_document":     {"document": fixtureID},
	"reply_comment":     {"document": fixtureID, "comment_id": "c1", "content": "a reply"},
	"review_suggestion": {"document": fixtureID, "action": "accept", "ids": []any{"suggest.x"}},
	"search_documents":  {"title": "scratch"},
}

// leakCanary is text a call sends in, so the logging test can look for
// it coming back out of the log.
const leakCanary = "Revenue grew substantially in Q3."

// TestEveryToolHasArgs keeps toolArgs complete. Without it the surface
// tests below silently shrink to whatever was registered when they were
// written — the failure mode where a guarantee is still asserted, but
// about less and less of the server.
func TestEveryToolHasArgs(t *testing.T) {
	cs := connectWith(t, &fakeAPI{raw: doctest.RawFixture(t)},
		config.Config{DefaultWriteMode: config.WriteDirect, EnableDestructive: true})
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) == 0 {
		t.Fatal("no tools registered")
	}
	for _, tool := range tools.Tools {
		if _, ok := toolArgs[tool.Name]; !ok {
			t.Errorf("tool %s has no entry in toolArgs; add one so the surface tests cover it", tool.Name)
		}
	}
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	for name := range toolArgs {
		if !names[name] {
			t.Errorf("toolArgs names %s, which is not registered", name)
		}
	}
}

// TestDebugLogsCarryNoDocumentData is the guarantee a person needs
// before pasting a log into a bug report: at the loudest level the
// server offers, the log says a call happened and how it went, and
// nothing about the document it touched. The SDK would log whole
// JSON-RPC frames if it were handed a logger, which is why it is not.
func TestDebugLogsCarryNoDocumentData(t *testing.T) {
	api := &fakeAPI{raw: doctest.RawFixture(t)}
	svc := service.New(api, service.Options{DefaultWriteMode: config.WriteDirect})
	_, _ = svc.Fetch(context.Background(), fixtureID)

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	srv := server.New(server.Deps{Service: svc, Config: config.Config{DefaultWriteMode: config.WriteDirect, EnableDestructive: true}, Logger: logger, Version: "test"})
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ss.Close() }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()

	// Every registered tool, not a chosen few: whether a call succeeds
	// against the fake is irrelevant, since a refusal is logged too and a
	// refusal is where a message is most tempted to quote the document.
	names := make([]string, 0, len(toolArgs))
	for name := range toolArgs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: toolArgs[name]}); err != nil {
			t.Fatalf("%s: protocol error: %v", name, err)
		}
	}

	got := logs.String()
	if !strings.Contains(got, "mcp call") || !strings.Contains(got, "tool=read_document") {
		t.Fatalf("debug logging should record that a call happened:\n%s", got)
	}
	for _, name := range names {
		if !strings.Contains(got, "tool="+name) {
			t.Errorf("no log line for %s; the leak check never saw it", name)
		}
	}
	// The document's id, its title, its text, the text a call sent, and a
	// heading id.
	for _, leak := range []string{fixtureID, "Quarterly Report", "Revenue grew", leakCanary, "h.bg"} {
		if strings.Contains(got, leak) {
			t.Errorf("debug log leaked %q:\n%s", leak, got)
		}
	}
}
