//go:build integration

package gapi_test

// Spike A: does the Developer Preview really give us suggestion mode,
// anchored comments and the comments view? Runs against the caller's
// own account and creates one clearly named scratch document.
//
//	GDOCS_INTEGRATION=1 go test -tags=integration ./internal/gapi -run TestPreviewSpike -v

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-docs-mcp/internal/auth"
	"github.com/mmedum/google-docs-mcp/internal/credentials"
	"github.com/mmedum/google-docs-mcp/internal/doc"
	"github.com/mmedum/google-docs-mcp/internal/gapi"
	"github.com/mmedum/google-docs-mcp/internal/userconfig"
)

func liveClient(t *testing.T) *gapi.Client {
	t.Helper()
	if os.Getenv("GDOCS_INTEGRATION") == "" {
		t.Skip("set GDOCS_INTEGRATION=1 to run against Google")
	}
	uc, err := userconfig.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	oc, err := auth.LoadClientSecret(uc.ClientSecretPath, auth.Scopes(false))
	if err != nil {
		t.Fatal(err)
	}
	tokenFile, _ := userconfig.TokenFilePath("default")
	store := &credentials.Store{Profile: "default", Keyring: credentials.OSKeyring(), FilePath: tokenFile}
	rt, _, err := store.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	return gapi.New(auth.TokenSource(context.Background(), oc, rt), gapi.Options{Timeout: 60 * time.Second, UserAgent: "google-docs-mcp/spike"})
}

func req(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPreviewSpike(t *testing.T) {
	c := liveClient(t)
	ctx := context.Background()
	report := map[string]any{}
	defer func() {
		out, _ := json.MarshalIndent(report, "", "  ")
		path := filepath.Join(os.Getenv("GDOCS_SPIKE_OUT"), "spike-a.json")
		if os.Getenv("GDOCS_SPIKE_OUT") != "" {
			_ = os.WriteFile(path, out, 0o600)
		}
		t.Logf("report: %s", out)
	}()

	created, err := c.CreateDocument(ctx, "google-docs-mcp spike A (safe to delete)")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := created.DocumentID
	report["document_url"] = doc.DocumentURL(id)

	const body = "Hello spike. This sentence gets a comment. This sentence gets a suggestion.\n"
	res, err := c.BatchUpdate(ctx, id, &gapi.BatchUpdateRequest{Requests: []json.RawMessage{
		req(t, map[string]any{"insertText": map[string]any{"text": body, "location": map[string]any{"index": 1}}}),
	}})
	if err != nil {
		t.Fatalf("seed text: %v", err)
	}
	rev := res.WriteControl.RequiredRevisionID

	// 1. Comments view on documents.get.
	got, err := c.GetDocument(ctx, id, gapi.GetOptions{SuggestionsViewMode: gapi.SuggestionsInline, CommentsViewMode: gapi.CommentsIncluded})
	if err != nil {
		t.Fatalf("get with commentsViewMode: %v", err)
	}
	report["comments_view_accepted"] = true
	report["comments_key_present_before"] = got.Preview.Comments != nil
	tabID := ""
	if len(got.Document.Tabs) > 0 && got.Document.Tabs[0].TabProperties != nil {
		tabID = got.Document.Tabs[0].TabProperties.TabID
	}
	report["tab_id"] = tabID

	// 2. Suggestion mode: insert text as a suggestion at the end of the body text.
	insertAt := int64(1 + doc.UTF16Len(strings.TrimSuffix(body, "\n")))
	res, err = c.BatchUpdate(ctx, id, &gapi.BatchUpdateRequest{
		Requests:     []json.RawMessage{req(t, map[string]any{"insertText": map[string]any{"text": " (suggested addition)", "location": map[string]any{"index": insertAt, "tabId": tabID}}})},
		WriteControl: &gapi.WriteControl{RequiredRevisionID: rev, WriteMode: "SUGGEST"},
	})
	if err != nil {
		report["suggest_mode_error"] = err.Error()
		t.Fatalf("suggest mode batchUpdate: %v", err)
	}
	report["suggest_mode_raw_reply"] = json.RawMessage(res.Raw)
	rev = res.WriteControl.RequiredRevisionID

	got, err = c.GetDocument(ctx, id, gapi.GetOptions{SuggestionsViewMode: gapi.SuggestionsInline, CommentsViewMode: gapi.CommentsIncluded})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := doc.Parse(got.Document)
	if err != nil {
		t.Fatal(err)
	}
	var suggestionIDs []string
	for _, b := range parsed.Tabs[0].Body.Blocks {
		if b.Paragraph == nil {
			continue
		}
		for _, r := range b.Paragraph.Runs {
			if r.IsSuggestedInsertion() {
				suggestionIDs = append(suggestionIDs, r.Inserted...)
				report["suggested_run_text"] = r.Text
			}
		}
	}
	report["suggestion_ids_inline"] = suggestionIDs
	report["suggestions_key_after"] = json.RawMessage(got.Preview.Suggestions)

	// 3. Anchored comment on "This sentence gets a comment."
	start := int64(1 + doc.UTF16Len("Hello spike. "))
	end := start + doc.UTF16Len("This sentence gets a comment.")
	res, err = c.BatchUpdate(ctx, id, &gapi.BatchUpdateRequest{
		Requests: []json.RawMessage{req(t, map[string]any{"insertComment": map[string]any{
			"content": "Spike comment: is this anchored to the sentence?",
			"range":   map[string]any{"startIndex": start, "endIndex": end, "tabId": tabID},
		}})},
		WriteControl: &gapi.WriteControl{RequiredRevisionID: rev},
	})
	if err != nil {
		report["insert_comment_error"] = err.Error()
		t.Fatalf("insertComment: %v", err)
	}
	report["insert_comment_raw_reply"] = json.RawMessage(res.Raw)
	rev = res.WriteControl.RequiredRevisionID

	got, err = c.GetDocument(ctx, id, gapi.GetOptions{SuggestionsViewMode: gapi.SuggestionsInline, CommentsViewMode: gapi.CommentsIncluded})
	if err != nil {
		t.Fatal(err)
	}
	report["comments_after"] = json.RawMessage(got.Preview.Comments)

	// 4. Reject a second suggestion to prove accept/reject works, leaving the first for the UI.
	res, err = c.BatchUpdate(ctx, id, &gapi.BatchUpdateRequest{
		Requests:     []json.RawMessage{req(t, map[string]any{"insertText": map[string]any{"text": " [to be rejected]", "location": map[string]any{"index": 1, "tabId": tabID}}})},
		WriteControl: &gapi.WriteControl{RequiredRevisionID: rev, WriteMode: "SUGGEST"},
	})
	if err != nil {
		t.Fatalf("second suggestion: %v", err)
	}
	rev = res.WriteControl.RequiredRevisionID
	got, err = c.GetDocument(ctx, id, gapi.GetOptions{SuggestionsViewMode: gapi.SuggestionsInline, CommentsViewMode: gapi.CommentsIncluded})
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ = doc.Parse(got.Document)
	rejectID := findSuggestion(parsed, "to be rejected")
	report["reject_target"] = rejectID
	if rejectID != "" {
		res, err = c.BatchUpdate(ctx, id, &gapi.BatchUpdateRequest{
			Requests:     []json.RawMessage{req(t, map[string]any{"rejectSuggestion": map[string]any{"suggestionId": rejectID}})},
			WriteControl: &gapi.WriteControl{RequiredRevisionID: rev},
		})
		if err != nil {
			report["reject_error"] = err.Error()
		} else {
			report["reject_raw_reply"] = json.RawMessage(res.Raw)
			got, _ = c.GetDocument(ctx, id, gapi.GetOptions{SuggestionsViewMode: gapi.SuggestionsInline})
			parsed, _ = doc.Parse(got.Document)
			report["text_after_reject"] = bodyText(parsed)
			report["rejected_still_present"] = findSuggestion(parsed, "to be rejected") != ""
		}
	}
	fmt.Println("spike document:", doc.DocumentURL(id))
}

func findSuggestion(d *doc.Document, contains string) string {
	for _, b := range d.Tabs[0].Body.AllBlocks() {
		if b.Paragraph == nil {
			continue
		}
		for _, r := range b.Paragraph.Runs {
			if r.IsSuggestedInsertion() && strings.Contains(r.Text, contains) {
				return r.Inserted[0]
			}
		}
	}
	return ""
}

func bodyText(d *doc.Document) string {
	var parts []string
	for _, b := range d.Tabs[0].Body.Blocks {
		parts = append(parts, b.Text(doc.ViewInline))
	}
	return strings.Join(parts, "\n")
}

// TestPreviewSpikeReject rejects the "[to be rejected]" suggestion in an
// existing spike document (GDOCS_SPIKE_DOC) and accepts nothing else.
func TestPreviewSpikeReject(t *testing.T) {
	c := liveClient(t)
	ref := os.Getenv("GDOCS_SPIKE_DOC")
	if ref == "" {
		t.Skip("set GDOCS_SPIKE_DOC to the spike document id or URL")
	}
	id, err := doc.ParseID(ref)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	got, err := c.GetDocument(ctx, id, gapi.GetOptions{SuggestionsViewMode: gapi.SuggestionsInline, CommentsViewMode: gapi.CommentsIncluded})
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := doc.Parse(got.Document)
	rejectID := findSuggestion(parsed, "to be rejected")
	if rejectID == "" {
		t.Fatalf("no pending '[to be rejected]' suggestion; body: %q", bodyText(parsed))
	}
	res, err := c.BatchUpdate(ctx, id, &gapi.BatchUpdateRequest{
		Requests:     []json.RawMessage{req(t, map[string]any{"rejectSuggestion": map[string]any{"suggestionId": rejectID}})},
		WriteControl: &gapi.WriteControl{RequiredRevisionID: parsed.RevisionID},
	})
	if err != nil {
		t.Fatalf("rejectSuggestion: %v", err)
	}
	t.Logf("reject reply: %s", res.Raw)
	got, _ = c.GetDocument(ctx, id, gapi.GetOptions{SuggestionsViewMode: gapi.SuggestionsInline, CommentsViewMode: gapi.CommentsIncluded})
	parsed, _ = doc.Parse(got.Document)
	t.Logf("body after reject: %q", bodyText(parsed))
	t.Logf("pending suggestion threads after reject: %s", got.Preview.Suggestions)
	if findSuggestion(parsed, "to be rejected") != "" {
		t.Fatal("rejected suggestion still present")
	}
}
