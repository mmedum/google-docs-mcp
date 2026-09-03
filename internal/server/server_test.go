package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	raw     []byte
	err     error
	batches []*gapi.BatchUpdateRequest
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
	return &gapi.DocumentResult{Document: &d, Raw: f.raw}, nil
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
	return &gdocs.Document{DocumentID: fixtureID, Title: title, RevisionID: "rev-new"}, nil
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
	return nil, nil
}

func connect(t *testing.T, api *fakeAPI) *mcp.ClientSession {
	t.Helper()
	return connectWith(t, api, config.Config{DefaultWriteMode: config.WriteDirect})
}

func connectWith(t *testing.T, api *fakeAPI, cfg config.Config) *mcp.ClientSession {
	t.Helper()
	svc := service.New(api, service.Options{Preview: cfg.Preview, ReadOnly: cfg.ReadOnly, DefaultWriteMode: cfg.DefaultWriteMode, WriteModes: cfg.AvailableWriteModes(), ExportDir: cfg.ExportDir})
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
	readTools := map[string]bool{"get_document": true, "get_outline": true, "read_document": true, "find_in_document": true, "search_documents": true, "export_document": true, "list_suggestions": true}
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
	want := "create_document,edit_document,export_document,find_in_document,format_document,get_document,get_outline,list_suggestions,read_document,review_suggestion,search_documents"
	if strings.Join(names, ",") != want {
		t.Fatalf("tools = %v", names)
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
	sc, _ := json.Marshal(res.StructuredContent)
	var info service.Info
	if err := json.Unmarshal(sc, &info); err != nil || info.RevisionID != "rev-0001" || len(info.Tabs) != 2 {
		t.Fatalf("structured: %s %v", sc, err)
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
	sc, _ := json.Marshal(res.StructuredContent)
	if !bytes.Contains(sc, []byte(`"revision_id":"rev-0001"`)) || !bytes.Contains(sc, []byte(`"blocks":5`)) {
		t.Fatalf("read structured: %s", sc)
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

func TestDumpSchemas(t *testing.T) {
	srv := server.New(server.Deps{Config: config.Config{}, Version: "test"})
	var buf bytes.Buffer
	if err := server.DumpSchemas(context.Background(), srv, &buf, "test"); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Server, Version, SDK string
		Tools                []struct{ Name string }
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Server != server.Name || out.SDK != server.SDKVersion || len(out.Tools) != 11 || out.Tools[0].Name != "create_document" {
		t.Fatalf("dump = %+v", out)
	}
	if !errors.Is(context.Canceled, context.Canceled) {
		t.Fatal("sanity")
	}
}
