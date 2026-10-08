package service

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mmedum/google-docs-mcp/v2/internal/gapi"
	"github.com/mmedum/google-docs-mcp/v2/internal/gdocs"
	"github.com/mmedum/google-docs-mcp/v2/internal/plan"
)

func TestListCommentsLocatesAndRenders(t *testing.T) {
	svc, api := writable(t, false)
	api.comments = []*gapi.DriveComment{
		{ID: "c1", Content: "Check this", Author: &gapi.User{DisplayName: "Ann"}, CreatedTime: "2026-09-01T00:00:00Z", QuotedFileContent: &gapi.QuotedText{Value: "Second point"},
			Replies: []*gapi.DriveReply{{ID: "r1", Content: "ok", Author: &gapi.User{DisplayName: "Bob"}, Action: "resolve", CreatedTime: "2026-09-02T00:00:00Z"}}, Resolved: true},
		{ID: "c2", Content: "Ambiguous", QuotedFileContent: &gapi.QuotedText{Value: "point"}},
		{ID: "c3", Content: "gone", Deleted: true},
	}
	res, err := svc.ListComments(context.Background(), ListCommentsRequest{Document: fixtureID, IncludeDeleted: true})
	if err != nil || len(res.Threads) != 3 || res.Open != 1 || res.Resolved != 1 {
		t.Fatalf("list: %+v %v", res, err)
	}
	c1 := res.Threads[0]
	if c1.Handle != "p7" || c1.Start != 94 || c1.End != 106 || len(c1.Replies) != 1 || c1.Replies[0].Action != "resolve" || c1.Anchored {
		t.Fatalf("c1: %+v", c1)
	}
	if res.Threads[1].Handle != "" {
		t.Fatalf("ambiguous quote should stay unlocated: %+v", res.Threads[1])
	}
	for _, want := range []string{"3 comment thread(s) shown (1 open, 1 resolved)", "- c1 [p7] by Ann (2026-09-01T00:00:00Z) [resolved] on “Second point”: Check this", "    ↳ Bob resolved (2026-09-02T00:00:00Z): ok", "- c3 [deleted]: gone"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("text lacks %q:\n%s", want, res.Text)
		}
	}
	res, err = svc.ListComments(context.Background(), ListCommentsRequest{Document: fixtureID, HideResolved: true})
	if err != nil || len(res.Threads) != 2 {
		t.Fatalf("hide resolved: %+v %v", res, err)
	}
	api.listErr = &gapi.APIError{Status: 403, Message: "no"}
	if _, err := svc.ListComments(context.Background(), ListCommentsRequest{Document: fixtureID}); classOf(err) != "forbidden" {
		t.Fatalf("list error: %v", err)
	}
}

func TestListCommentsSaysWhoAThreadIsAssignedTo(t *testing.T) {
	svc, api := writable(t, false)
	api.comments = []*gapi.DriveComment{{ID: "c1", Content: "Please check", AssigneeEmailAddress: "jane@example.com"}}
	res, err := svc.ListComments(context.Background(), ListCommentsRequest{Document: fixtureID})
	if err != nil {
		t.Fatal(err)
	}
	if res.Threads[0].Assignee != "jane@example.com" {
		t.Errorf("assignee: %+v", res.Threads[0])
	}
	if want := "- c1 assigned to jane@example.com: Please check"; !strings.Contains(res.Text, want) {
		t.Errorf("text lacks %q:\n%s", want, res.Text)
	}
}

func TestPreviewAnchorsLocateComments(t *testing.T) {
	svc, api := writable(t, true)
	// Splice a comment thread and its anchor into the fixture.
	var w gdocs.Document
	if err := json.Unmarshal(api.raw, &w); err != nil {
		t.Fatal(err)
	}
	w.Comments = []gdocs.CommentThread{{CommentID: "c9", AnchorID: "a1", PlainTextQuote: "point", HeadPost: gdocs.Post{Content: "hi", Author: gdocs.PostAuthor{DisplayName: "Ann"}}, Status: "OPEN"}}
	w.Tabs[0].DocumentTab.CommentAnchors = map[string]gdocs.CommentAnchor{"a1": {AnchorID: "a1", Ranges: []*gdocs.Range{{StartIndex: 69, EndIndex: 80}}}}
	api.raw, _ = json.Marshal(&w)
	api.comments = []*gapi.DriveComment{{ID: "c9", Content: "hi", QuotedFileContent: &gapi.QuotedText{Value: "point"}}}
	res, err := svc.ListComments(context.Background(), ListCommentsRequest{Document: fixtureID})
	if err != nil || len(res.Threads) != 1 {
		t.Fatalf("list: %+v %v", res, err)
	}
	if c := res.Threads[0]; !c.Anchored || c.Handle != "p5" || c.Start != 69 || c.End != 80 || c.Tab != "t.0" {
		t.Fatalf("anchored thread: %+v", c)
	}
	// The guard sees the same anchor.
	f := fetched(t, svc)
	threads, err := svc.comments(context.Background(), f)
	if err != nil || len(threads) != 1 || threads[0].Handle != "p5" || len(threads[0].Replies) != 0 {
		t.Fatalf("guard threads: %+v %v", threads, err)
	}
	as := f.anchorsIn(f.Doc.Tabs[0].Body, 70, 75, threads)
	if len(as) != 1 || as[0].Kind != "comment" || as[0].ID != "c9" {
		t.Fatalf("anchors: %+v", as)
	}
}

func TestAddComment(t *testing.T) {
	svc, api := writable(t, false)
	ctx := context.Background()
	res, err := svc.AddComment(ctx, AddCommentRequest{Document: fixtureID, Target: &Target{Text: "First point"}, Content: " Why? ", Assignee: "x@example.test"})
	if err != nil || res.ID != "dc1" || res.Handle != "p5" || res.Quote != "First point" || res.Anchored || len(res.Warnings) != 2 {
		t.Fatalf("drive add: %+v %v", res, err)
	}
	if api.comments[0].Content != "Why?" || api.comments[0].QuotedFileContent.Value != "First point" {
		t.Fatalf("posted: %+v", api.comments[0])
	}
	res, err = svc.AddComment(ctx, AddCommentRequest{Document: fixtureID, Content: "General"})
	if err != nil || res.Handle != "" || res.Quote != "" || len(res.Warnings) != 0 || !strings.Contains(res.Text, "unanchored") {
		t.Fatalf("document-level: %+v %v", res, err)
	}
	for _, tc := range []struct {
		req   AddCommentRequest
		class string
	}{
		{AddCommentRequest{Document: fixtureID, Content: " "}, "invalid"},
		{AddCommentRequest{Document: fixtureID, Content: strings.Repeat("x", 2049)}, "invalid"},
		{AddCommentRequest{Document: fixtureID, Content: "x", Target: &Target{Text: "nowhere"}}, "not_found"},
		{AddCommentRequest{Document: fixtureID, Content: "x", ExpectRevision: "old"}, "conflict"},
	} {
		if _, err := svc.AddComment(ctx, tc.req); classOf(err) != tc.class {
			t.Errorf("%+v: %v", tc.req, err)
		}
	}

	// Preview: anchored through insertComment with a revision guard.
	svc, api = writable(t, true)
	api.replies = []string{`{"replies":[{"insertComment":{"commentThread":{"commentId":"pc1"}}}],"writeControl":{"requiredRevisionId":"rev-0002"}}`}
	res, err = svc.AddComment(ctx, AddCommentRequest{Document: fixtureID, Target: &Target{Handle: "p5"}, Content: "Anchored", Assignee: "a@example.test"})
	if err != nil || res.ID != "pc1" || !res.Anchored || res.RevisionID != "rev-0002" || len(res.Warnings) != 0 {
		t.Fatalf("preview add: %+v %v", res, err)
	}
	b := api.batches[0]
	if b.WriteControl.RequiredRevisionID != "rev-0001" || !strings.Contains(string(b.Requests[0]), `"assigneeEmailAddress":"a@example.test"`) || !strings.Contains(string(b.Requests[0]), `"startIndex":69`) {
		t.Fatalf("batch: %+v %s", b.WriteControl, b.Requests[0])
	}
	if !strings.Contains(res.Text, "comment pc1 posted on p5") {
		t.Fatalf("text: %s", res.Text)
	}
}

// Google can answer an insertComment batch with 200, no thread, and a
// state saying every comment failed. Reporting that as posted would hand
// back a comment with no id.
func TestAddCommentRefusesACommentGoogleDidNotSave(t *testing.T) {
	for _, tc := range []struct {
		name, reply, class string
	}{
		{"all failed", `{"replies":[{}],"commentUpdateState":"ALL_FAILED_UNKNOWN_REASON"}`, "server"},
		{"no thread named", `{"replies":[{}],"commentUpdateState":"ALL_SAVED"}`, "ambiguous_outcome"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, api := writable(t, true)
			api.replies = []string{tc.reply}
			res, err := svc.AddComment(context.Background(), AddCommentRequest{Document: fixtureID, Target: &Target{Handle: "p5"}, Content: "Anchored"})
			if classOf(err) != tc.class {
				t.Fatalf("got %+v, %v; want a [%s] error", res, err, tc.class)
			}
		})
	}
}

func TestCommentModeRefusesCommentsGoogleDidNotSave(t *testing.T) {
	svc, api := writable(t, true)
	api.replies = []string{`{"replies":[{},{}],"commentUpdateState":"ALL_FAILED_UNKNOWN_REASON"}`}
	res, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Mode: "comment", Ops: []EditOp{
		{Kind: plan.OpReplace, Target: &Target{Text: "Second point"}, Content: "Second item"},
		{Kind: plan.OpDelete, Target: &Target{Handle: "p5"}},
	}})
	if classOf(err) != "server" {
		t.Fatalf("got %+v, %v; want a [server] error", res, err)
	}
}

// withSuggestionThread gives the fixture's suggestion s1 the thread the
// comments view returns for it, with one reply already on it.
func withSuggestionThread(t *testing.T, raw []byte) []byte {
	t.Helper()
	var w map[string]json.RawMessage
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatal(err)
	}
	w["suggestions"] = json.RawMessage(`[{"suggestionId":"s1","status":"OPEN",
		"headPost":{"postId":"h1","author":{"displayName":"Ann"}},
		"replies":[{"postId":"p1","content":"Why this word?","author":{"displayName":"Bob"},"createTime":"2026-10-01T09:00:00Z"}]}]`)
	return mustJSON(w)
}

func TestReplyToASuggestionsThread(t *testing.T) {
	svc, api := writable(t, true)
	api.raw = withSuggestionThread(t, api.raw)
	api.replies = []string{`{"replies":[{"addCommentReply":{"post":{"postId":"p2"}}}],"commentUpdateState":"ALL_SAVED"}`}
	res, err := svc.Reply(context.Background(), ReplyRequest{Document: fixtureID, SuggestionID: "s1", Content: " It reads better. "})
	if err != nil {
		t.Fatal(err)
	}
	if res.SuggestionID != "s1" || res.ReplyID != "p2" || res.CommentID != "" || res.Text != "replied to suggestion s1 (reply p2)" {
		t.Errorf("result: %+v", res)
	}
	if len(api.batches) != 1 || len(api.posted) != 0 {
		t.Fatalf("want one Docs batch and no Drive reply; got %d batches, Drive posts %v", len(api.batches), api.posted)
	}
	want := `{"addCommentReply":{"post":{"content":"It reads better."},"suggestionId":"s1"}}`
	if got := string(api.batches[0].Requests[0]); got != want {
		t.Errorf("request:\n got %s\nwant %s", got, want)
	}
}

func TestReplyToASuggestionIsRefused(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		view  bool
		req   ReplyRequest
		class string
	}{
		{"both ids", true, ReplyRequest{Document: fixtureID, CommentID: "c1", SuggestionID: "s1", Content: "x"}, "invalid"},
		{"resolve", true, ReplyRequest{Document: fixtureID, SuggestionID: "s1", Action: "resolve"}, "invalid"},
		{"edit", true, ReplyRequest{Document: fixtureID, SuggestionID: "s1", Action: "edit", Content: "x"}, "invalid"},
		{"no content", true, ReplyRequest{Document: fixtureID, SuggestionID: "s1"}, "invalid"},
		{"unknown suggestion", true, ReplyRequest{Document: fixtureID, SuggestionID: "s9", Content: "x"}, "not_found"},
		{"comments view refused", false, ReplyRequest{Document: fixtureID, SuggestionID: "s1", Content: "x"}, "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, api := writable(t, tc.view)
			api.raw = withSuggestionThread(t, api.raw)
			if _, err := svc.Reply(ctx, tc.req); classOf(err) != tc.class {
				t.Errorf("got %v; want a [%s] error", err, tc.class)
			}
			if len(api.batches) != 0 {
				t.Errorf("a refused reply sent %d batches", len(api.batches))
			}
		})
	}
}

func TestListSuggestionsShowsTheThreadsReplies(t *testing.T) {
	svc, api := writable(t, true)
	api.raw = withSuggestionThread(t, api.raw)
	res, err := svc.ListSuggestions(context.Background(), fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	want := []CommentReply{{ID: "p1", Author: "Bob", Content: "Why this word?", Created: "2026-10-01T09:00:00Z"}}
	if len(res.Suggestions) != 1 || !reflect.DeepEqual(res.Suggestions[0].Replies, want) {
		t.Fatalf("suggestions: %+v", res.Suggestions)
	}
	if line := "    ↳ Bob (2026-10-01T09:00:00Z): Why this word?"; !strings.Contains(res.Text, line) {
		t.Errorf("the listing does not show the reply; want %q in:\n%s", line, res.Text)
	}
}

func TestReplyAndDeleteComment(t *testing.T) {
	svc, api := writable(t, false)
	ctx := accepting(context.Background())
	api.comments = []*gapi.DriveComment{{ID: "c1", Content: "x"}, {ID: "c2", Content: "y", Resolved: true,
		Replies: []*gapi.DriveReply{{ID: "r7", Content: "done"}}}, {ID: "c3", Deleted: true}}
	res, err := svc.Reply(ctx, ReplyRequest{Document: fixtureID, CommentID: "c1", Content: "thanks"})
	if err != nil || res.Action != "reply" || res.ReplyID != "r1" || res.Resolved || api.posted[0] != "c1:thanks:" {
		t.Fatalf("reply: %+v %v %v", res, err, api.posted)
	}
	res, err = svc.Reply(ctx, ReplyRequest{Document: fixtureID, CommentID: "c1", Action: "resolve"})
	if err != nil || !res.Resolved || api.posted[1] != "c1::resolve" || res.Text != "resolved comment c1" {
		t.Fatalf("resolve: %+v %v %v", res, err, api.posted)
	}
	res, err = svc.Reply(ctx, ReplyRequest{Document: fixtureID, CommentID: "c2", Action: "reopen", Content: "not done"})
	if err != nil || res.Resolved || api.posted[2] != "c2:not done:reopen" {
		t.Fatalf("reopen: %+v %v %v", res, err, api.posted)
	}
	for _, tc := range []struct {
		req   ReplyRequest
		class string
	}{
		{ReplyRequest{Document: "bad", CommentID: "c1", Content: "x"}, "invalid"},
		{ReplyRequest{Document: fixtureID, Content: "x"}, "invalid"},
		{ReplyRequest{Document: fixtureID, CommentID: "c1"}, "invalid"},
		{ReplyRequest{Document: fixtureID, CommentID: "c1", Action: "delete"}, "invalid"},
		{ReplyRequest{Document: fixtureID, CommentID: "c1", Action: "reopen"}, "invalid"},
		{ReplyRequest{Document: fixtureID, CommentID: "c2", Action: "resolve"}, "invalid"},
		{ReplyRequest{Document: fixtureID, CommentID: "c3", Content: "x"}, "not_found"},
		{ReplyRequest{Document: fixtureID, CommentID: "c9", Content: "x"}, "not_found"},
	} {
		if _, err := svc.Reply(ctx, tc.req); classOf(err) != tc.class {
			t.Errorf("%+v: %v", tc.req, err)
		}
	}

	// Deletion is refused unless enabled.
	if _, err := svc.DeleteComment(ctx, DeleteCommentRequest{Document: fixtureID, CommentID: "c1"}); classOf(err) != "forbidden" {
		t.Fatalf("delete without flag: %v", err)
	}
	svc = New(api, Options{Destructive: true, DefaultWriteMode: "direct"})
	dres, err := svc.DeleteComment(ctx, DeleteCommentRequest{Document: fixtureID, CommentID: "c1"})
	if err != nil || dres.Text != "deleted comment c1" || api.deleted[0] != "c1" {
		t.Fatalf("delete: %+v %v %v", dres, err, api.deleted)
	}
	dres, err = svc.DeleteComment(ctx, DeleteCommentRequest{Document: fixtureID, CommentID: "c2", ReplyID: "r7"})
	if err != nil || dres.ReplyID != "r7" || api.deleted[1] != "c2/r7" {
		t.Fatalf("delete reply: %+v %v %v", dres, err, api.deleted)
	}
	if _, err := svc.DeleteComment(ctx, DeleteCommentRequest{Document: fixtureID, CommentID: "c9"}); classOf(err) != "not_found" {
		t.Fatalf("delete missing: %v", err)
	}
	ro := New(api, Options{Destructive: true, ReadOnly: true})
	if _, err := ro.DeleteComment(ctx, DeleteCommentRequest{Document: fixtureID, CommentID: "c1"}); classOf(err) != "forbidden" {
		t.Fatalf("read-only delete: %v", err)
	}
}

func TestReadWithComments(t *testing.T) {
	svc, api := writable(t, false)
	api.comments = []*gapi.DriveComment{
		{ID: "c1", Content: "Check this", Author: &gapi.User{DisplayName: "Ann"}, QuotedFileContent: &gapi.QuotedText{Value: "Second point"}, Replies: []*gapi.DriveReply{{ID: "r1", Content: "ok"}}},
		{ID: "c2", Content: "In the notes", QuotedFileContent: &gapi.QuotedText{Value: "Second tab text"}},
		{ID: "c3", Content: "unlocated"},
		{ID: "c4", Content: "gone", Deleted: true, QuotedFileContent: &gapi.QuotedText{Value: "First point"}},
	}
	res, err := svc.Read(context.Background(), ReadRequest{Document: fixtureID, IncludeComments: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"- Second point{>>c:c1<<}", "\n\ncomments:\n- c:c1 [p7] by Ann: Check this (1 reply)", "(2 more elsewhere or unlocated; use list_comments)"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("read lacks %q:\n%s", want, res.Text)
		}
	}
	if strings.Contains(res.Text, "c4") || strings.Contains(res.Text, "First point{>>") {
		t.Errorf("deleted comment rendered:\n%s", res.Text)
	}
	// The lookup happens once per fetch: a second read reuses it.
	calls := 0
	api.listErr = nil
	before := len(api.comments)
	res, err = svc.Read(context.Background(), ReadRequest{Document: fixtureID, IncludeComments: true, Scope: ReadScope{Tab: "Notes"}})
	if err != nil || !strings.Contains(res.Text, "Second tab text{>>c:c2<<}") || !strings.Contains(res.Text, "- c:c2 [tab2/p2]") || len(api.comments) != before || calls != 0 {
		t.Fatalf("second tab read: %v\n%s", err, res.Text)
	}
	res, err = svc.Read(context.Background(), ReadRequest{Document: fixtureID, IncludeComments: true, Scope: ReadScope{HeadingID: "h.sum"}})
	if err != nil || !strings.Contains(res.Text, "comments: none in this range") {
		t.Fatalf("empty range: %v\n%s", err, res.Text)
	}
	api.comments = nil
	svc.Invalidate(fixtureID)
	res, err = svc.Read(context.Background(), ReadRequest{Document: fixtureID, IncludeComments: true})
	if err != nil || !strings.HasSuffix(res.Text, "<!-- no comments -->") {
		t.Fatalf("no comments: %v\n%s", err, res.Text)
	}
	api.listErr = &gapi.APIError{Status: 403, Message: "no"}
	svc.Invalidate(fixtureID)
	if _, err := svc.Read(context.Background(), ReadRequest{Document: fixtureID, IncludeComments: true}); classOf(err) != "forbidden" {
		t.Fatalf("comment lookup error should surface: %v", err)
	}
}
