package service

import (
	"context"
	"strings"
	"testing"
)

func TestManageTabs(t *testing.T) {
	svc, api := writable(t, false)
	ctx := context.Background()
	api.replies = []string{`{"replies":[{"addDocumentTab":{"tabProperties":{"tabId":"t.new","title":"Appendix"}}}],"writeControl":{"requiredRevisionId":"rev-0002"}}`}
	res, err := svc.ManageTabs(ctx, TabRequest{Document: fixtureID, Action: "add", Title: "Appendix", Position: 2, Parent: "Notes", Emoji: "📎"})
	if err != nil || res.TabID != "t.new" || res.Title != "Appendix" || res.RevisionID != "rev-0002" || len(res.Warnings) != 0 {
		t.Fatalf("add: %+v %v", res, err)
	}
	req := string(api.batches[0].Requests[0])
	for _, want := range []string{`"title":"Appendix"`, `"index":1`, `"parentTabId":"t.1"`, `"iconEmoji":"📎"`} {
		if !strings.Contains(req, want) {
			t.Errorf("add request lacks %s: %s", want, req)
		}
	}
	if api.batches[0].WriteControl.RequiredRevisionID != "rev-0001" || api.batches[0].WriteControl.WriteMode != "" {
		t.Fatalf("write control: %+v", api.batches[0].WriteControl)
	}
	if res.Text != `added tab "Appendix" (id t.new); revision rev-0002` {
		t.Fatalf("text: %s", res.Text)
	}

	res, err = svc.ManageTabs(ctx, TabRequest{Document: fixtureID, Action: "rename", Tab: "2", Title: "Notes 2"})
	if err != nil || res.TabID != "t.1" || !strings.Contains(string(api.batches[1].Requests[0]), `"fields":"title"`) {
		t.Fatalf("rename: %+v %v %s", res, err, api.batches[1].Requests[0])
	}
	res, err = svc.ManageTabs(ctx, TabRequest{Document: fixtureID, Action: "move", Tab: "Notes", Position: 1})
	if err != nil || !strings.Contains(string(api.batches[2].Requests[0]), `"index":0`) || strings.Contains(string(api.batches[2].Requests[0]), "title") {
		t.Fatalf("move: %+v %v %s", res, err, api.batches[2].Requests[0])
	}
	res, err = svc.ManageTabs(ctx, TabRequest{Document: fixtureID, Action: "move", Tab: "Notes", Parent: "Main"})
	if err != nil || !strings.Contains(string(api.batches[3].Requests[0]), `"parentTabId":"t.0"`) {
		t.Fatalf("nest: %+v %v %s", res, err, api.batches[3].Requests[0])
	}
	for _, tc := range []struct {
		req   TabRequest
		class string
	}{
		{TabRequest{Document: fixtureID, Action: "add"}, "invalid"},
		{TabRequest{Document: fixtureID, Action: "add", Title: "x", Position: -1}, "invalid"},
		{TabRequest{Document: fixtureID, Action: "add", Title: "x", Parent: "zzz"}, "not_found"},
		{TabRequest{Document: fixtureID, Action: "rename", Tab: "Notes"}, "invalid"},
		{TabRequest{Document: fixtureID, Action: "rename", Tab: "zzz", Title: "x"}, "not_found"},
		{TabRequest{Document: fixtureID, Action: "move", Tab: "Notes"}, "invalid"},
		{TabRequest{Document: fixtureID, Action: "move", Tab: "Notes", Parent: "Notes"}, "invalid"},
		{TabRequest{Document: fixtureID, Action: "move", Tab: "Notes", Parent: "none"}, "unsupported"},
		{TabRequest{Document: fixtureID, Action: "split"}, "invalid"},
		{TabRequest{Document: fixtureID, Action: "add", Title: "x", ExpectRevision: "old"}, "conflict"},
		{TabRequest{Document: fixtureID, Action: "delete", Tab: "Notes"}, "invalid"},
		{TabRequest{Document: fixtureID, Action: "rename", Tab: "Notes", Title: "x", Position: 2}, "invalid"},
		{TabRequest{Document: fixtureID, Action: "move", Tab: "Notes", Position: 1, Title: "x"}, "invalid"},
	} {
		if _, err := svc.ManageTabs(ctx, tc.req); classOf(err) != tc.class {
			t.Errorf("%+v: %v", tc.req, err)
		}
	}
	// Adding with content appends to the new tab in a second edit.
	api.batches, api.replies = nil, []string{`{"replies":[{"addDocumentTab":{"tabProperties":{"tabId":"t.1"}}}],"writeControl":{"requiredRevisionId":"rev-0002"}}`}
	res, err = svc.ManageTabs(ctx, TabRequest{Document: fixtureID, Action: "add", Title: "Filled", Content: "Hello tab"})
	if err != nil || len(api.batches) != 2 || !strings.Contains(string(api.batches[1].Requests[0]), `"tabId":"t.1"`) || !strings.Contains(string(api.batches[1].Requests[0]), "Hello tab") {
		t.Fatalf("add with content: %+v %v batches=%d", res, err, len(api.batches))
	}

	// Deletion needs the destructive flag and a second tab.
	if _, err := svc.DeleteTab(ctx, TabRequest{Document: fixtureID, Tab: "Notes"}); classOf(err) != "forbidden" {
		t.Fatalf("delete without flag: %v", err)
	}
	svc = New(api, Options{Destructive: true, DefaultWriteMode: "direct", CacheTTL: 1})
	api.batches, api.replies = nil, nil
	res, err = svc.DeleteTab(ctx, TabRequest{Document: fixtureID, Tab: "Notes"})
	if err != nil || res.TabID != "t.1" || !strings.Contains(string(api.batches[0].Requests[0]), `"deleteTab":{"tabId":"t.1"}`) || !strings.HasPrefix(res.Text, `deleted tab t.1 ("Notes")`) {
		t.Fatalf("delete: %+v %v", res, err)
	}
}
