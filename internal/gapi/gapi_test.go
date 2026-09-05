package gapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/time/rate"
)

func newTestClient(t *testing.T, h http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "tok"}), Options{
		DocsBaseURL: srv.URL, DriveBaseURL: srv.URL + "/drive/v3",
		Retry:       RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond},
		ReadLimiter: rate.NewLimiter(rate.Inf, 1), WriteLimiter: rate.NewLimiter(rate.Inf, 1),
		Sleep:    func(context.Context, time.Duration) error { return nil },
		Timeout:  5 * time.Second,
		AllowURL: func(*url.URL) bool { return true },
	})
	return c, srv
}

// googleError writes an error body shaped like Google's. When a reason
// is given it appears in **both** envelopes and in the two spellings
// Google actually uses — camelCase in the legacy `errors[]`, UPPER_SNAKE
// in the `ErrorInfo` detail — because a fake that sends one spelling in
// both places cannot catch a parser that prefers the wrong envelope. A
// sibling server had exactly that bug, live, with a green suite.
func googleError(w http.ResponseWriter, status int, msg, rpc, reason string) {
	body := map[string]any{"code": status, "message": msg, "status": rpc}
	if reason != "" {
		body["details"] = []map[string]any{{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": upperSnake(reason)}}
		body["errors"] = []map[string]any{{"domain": "global", "reason": reason, "message": msg}}
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": body})
}

// upperSnake spells a camelCase reason the way an ErrorInfo detail does.
func upperSnake(reason string) string {
	var b strings.Builder
	for i, r := range reason {
		switch {
		case r >= 'A' && r <= 'Z':
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r)
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 'a' + 'A')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func TestGetDocumentDecodesAndSendsParams(t *testing.T) {
	var gotPath, gotQuery, gotAuth string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotAuth = r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"documentId":"abc","title":"T","revisionId":"r1","tabs":[{"tabProperties":{"tabId":"t.0","title":"Main"}}],"comments":[{"commentId":"c1","plainTextQuote":"q"}]}`))
	}))
	res, err := c.GetDocument(context.Background(), "abc", GetOptions{SuggestionsViewMode: SuggestionsInline, CommentsViewMode: CommentsIncluded})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/documents/abc" || gotAuth != "Bearer tok" {
		t.Fatalf("path %q auth %q", gotPath, gotAuth)
	}
	for _, want := range []string{"includeTabsContent=true", "suggestionsViewMode=SUGGESTIONS_INLINE", "commentsViewMode=COMMENTS_VIEW_MODE_INCLUDED"} {
		if !strings.Contains(gotQuery, want) {
			t.Fatalf("query %q lacks %q", gotQuery, want)
		}
	}
	if res.Document.Title != "T" || len(res.Document.Tabs) != 1 || len(res.Document.Comments) != 1 || res.Document.Comments[0].CommentID != "c1" {
		t.Fatalf("decode wrong: %+v", res)
	}
}

func TestErrorClassification(t *testing.T) {
	cases := []struct {
		status int
		msg    string
		reason string
		want   error
		class  string
	}{
		{401, "bad token", "", ErrUnauthorized, "auth"},
		{403, "Request had insufficient authentication scopes.", "ACCESS_TOKEN_SCOPE_INSUFFICIENT", ErrMissingScope, "forbidden"},
		{403, "The caller does not have permission", "", ErrForbidden, "forbidden"},
		// Drive reports throttling as 403, not 429, so the reason decides.
		{403, "Rate Limit Exceeded", "userRateLimitExceeded", ErrRateLimited, "rate_limited"},
		{403, "Daily Limit Exceeded", "dailyLimitExceeded", ErrRateLimited, "rate_limited"},
		{403, "The user's Drive storage quota has been exceeded.", "storageQuotaExceeded", ErrForbidden, "forbidden"},
		{404, "Requested entity was not found.", "", ErrNotFound, "not_found"},
		{400, "Invalid requests[0]", "", ErrInvalid, "invalid"},
		{400, "The provided revision id does not match the current revision", "", ErrConflict, "conflict"},
		{418, "teapot", "", ErrUnexpected, "unexpected"},
	}
	for _, tc := range cases {
		c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			googleError(w, tc.status, tc.msg, "X", tc.reason)
		}))
		_, err := c.GetDocument(context.Background(), "abc", GetOptions{})
		if !errors.Is(err, tc.want) {
			t.Errorf("status %d: err %v is not %v", tc.status, err, tc.want)
		}
		if got := Class(err); got != tc.class {
			t.Errorf("status %d: class %q, want %q", tc.status, got, tc.class)
		}
		var ae *APIError
		if !errors.As(err, &ae) || ae.Message != tc.msg || ae.Status != tc.status {
			t.Errorf("status %d: APIError not populated: %v", tc.status, err)
		}
	}
}

func TestNonJSONErrorBody(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
		_, _ = w.Write([]byte("<html>bad gateway</html>"))
	}))
	_, err := c.GetDocument(context.Background(), "abc", GetOptions{})
	if !errors.Is(err, ErrServer) || !strings.Contains(err.Error(), "bad gateway") {
		t.Fatalf("got %v", err)
	}
}

func TestReadRetriesThenSucceeds(t *testing.T) {
	var n int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) < 3 {
			w.Header().Set("Retry-After", "1")
			googleError(w, 429, "quota", "RESOURCE_EXHAUSTED", "")
			return
		}
		_, _ = w.Write([]byte(`{"user":{"emailAddress":"a@b.test"}}`))
	}))
	u, err := c.About(context.Background())
	if err != nil || u.EmailAddress != "a@b.test" {
		t.Fatalf("got %v %v", u, err)
	}
	if n != 3 {
		t.Fatalf("attempts = %d", n)
	}
}

// A throttling 403 backs off like a 429, and the reason reaches the
// message so a refusal that is not about permissions does not read as one.
func TestThrottling403RetriesOnRead(t *testing.T) {
	var n int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) < 3 {
			googleError(w, 403, "Rate Limit Exceeded", "PERMISSION_DENIED", "userRateLimitExceeded")
			return
		}
		_, _ = w.Write([]byte(`{"user":{"emailAddress":"a@b.test"}}`))
	}))
	u, err := c.About(context.Background())
	if err != nil || u.EmailAddress != "a@b.test" || n != 3 {
		t.Fatalf("got %v %v after %d attempts", u, err, n)
	}
}

// The daily project quota is a quota, but no amount of backing off frees
// it, so it classifies as rate limited without being retried.
func TestDailyLimitIsNotRetried(t *testing.T) {
	var n int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		googleError(w, 403, "Daily Limit Exceeded", "PERMISSION_DENIED", "dailyLimitExceeded")
	}))
	_, err := c.About(context.Background())
	if !errors.Is(err, ErrRateLimited) || n != 1 {
		t.Fatalf("err %v after %d attempts", err, n)
	}
}

// A write is not repeated on a throttling 403: only 429 and 503 prove
// nothing was applied, and that rule is older than this classification.
func TestThrottling403DoesNotRetryWrites(t *testing.T) {
	var n int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		googleError(w, 403, "Rate Limit Exceeded", "PERMISSION_DENIED", "rateLimitExceeded")
	}))
	_, err := c.BatchUpdate(context.Background(), "abc", &BatchUpdateRequest{Requests: []json.RawMessage{json.RawMessage(`{}`)}})
	if !errors.Is(err, ErrRateLimited) || n != 1 {
		t.Fatalf("err %v after %d attempts", err, n)
	}
}

// A Drive comment is a create, so the write rule covers it too: only 429
// and 503 prove nothing was posted. Retrying a throttling 403 or a 500
// here would post the comment twice.
func TestDriveWriteRetriesOnlyOn429And503(t *testing.T) {
	for _, tc := range []struct {
		label    string
		status   int
		reason   string
		attempts int32
	}{
		{"throttling 403", 403, "userRateLimitExceeded", 1},
		{"server error", 500, "", 1},
		{"service unavailable", 503, "", 2},
		{"too many requests", 429, "", 2},
	} {
		var n int32
		c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if atomic.AddInt32(&n, 1) == 1 {
				googleError(w, tc.status, "transient", "X", tc.reason)
				return
			}
			_, _ = w.Write([]byte(`{"id":"c1","content":"hi"}`))
		}))
		_, err := c.CreateComment(context.Background(), "abc", "hi", "")
		if n != tc.attempts {
			t.Errorf("%s: attempts %d, want %d (err %v)", tc.label, n, tc.attempts, err)
		}
		if tc.attempts == 1 && err == nil {
			t.Errorf("%s: expected an error", tc.label)
		}
	}
}

// The same condition reaches us camelCase from Drive's legacy envelope and
// UPPER_SNAKE from a google.rpc.ErrorInfo detail, which parseAPIError
// prefers. Both must classify as a quota, not a permission.
func TestThrottlingReasonSpellings(t *testing.T) {
	for _, reason := range []string{"userRateLimitExceeded", "USER_RATE_LIMIT_EXCEEDED", "RATE_LIMIT_EXCEEDED"} {
		c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			googleError(w, 403, "Rate Limit Exceeded", "PERMISSION_DENIED", reason)
		}))
		_, err := c.About(context.Background())
		if !errors.Is(err, ErrRateLimited) {
			t.Errorf("reason %q: %v is not rate limited", reason, err)
		}
	}
}

// Google's reason is what separates "cannot" from "may not", so it is in
// the text the model reads, not only in the field the classifier uses.
func TestErrorMessageCarriesTheReason(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		googleError(w, 403, "This revision cannot be downloaded by the authenticated user.", "PERMISSION_DENIED", "downloadRestrictedForRevision")
	}))
	_, err := c.About(context.Background())
	// Either spelling satisfies the contract, which is that Google's
	// reason reaches the message — asserting one of them would pin the
	// parser's preference between the two envelopes instead.
	if err == nil || !strings.Contains(reasonKey(err.Error()), reasonKey("downloadRestrictedForRevision")) {
		t.Fatalf("got %v", err)
	}
}

func TestReadGivesUpAfterMaxAttempts(t *testing.T) {
	var n int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		googleError(w, 503, "unavailable", "UNAVAILABLE", "")
	}))
	_, err := c.About(context.Background())
	if !errors.Is(err, ErrServer) || n != 3 {
		t.Fatalf("err %v attempts %d", err, n)
	}
}

func TestWriteRetriesOnlyOn429And503(t *testing.T) {
	for _, tc := range []struct {
		status   int
		attempts int32
	}{{429, 2}, {503, 2}, {500, 1}, {502, 1}} {
		var n int32
		c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if atomic.AddInt32(&n, 1) == 1 {
				googleError(w, tc.status, "transient", "X", "")
				return
			}
			_, _ = w.Write([]byte(`{"documentId":"abc","replies":[]}`))
		}))
		_, err := c.BatchUpdate(context.Background(), "abc", &BatchUpdateRequest{Requests: []json.RawMessage{json.RawMessage(`{}`)}})
		if n != tc.attempts {
			t.Errorf("status %d: attempts %d, want %d (err %v)", tc.status, n, tc.attempts, err)
		}
		if tc.attempts == 1 && err == nil {
			t.Errorf("status %d: expected error", tc.status)
		}
	}
}

func TestWriteNetworkFailureIsAmbiguous(t *testing.T) {
	c, srv := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()
	_, err := c.BatchUpdate(context.Background(), "abc", &BatchUpdateRequest{})
	if !errors.Is(err, ErrAmbiguous) || Class(err) != "ambiguous_outcome" {
		t.Fatalf("got %v", err)
	}
	_, err = c.About(context.Background())
	if !errors.Is(err, ErrNetwork) {
		t.Fatalf("read network error should be ErrNetwork: %v", err)
	}
}

func TestBatchUpdateBodyAndWriteControl(t *testing.T) {
	var body map[string]any
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/documents/abc:batchUpdate" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"documentId":"abc","replies":[{}],"writeControl":{"requiredRevisionId":"r2"}}`))
	}))
	res, err := c.BatchUpdate(context.Background(), "abc", &BatchUpdateRequest{
		Requests:     []json.RawMessage{json.RawMessage(`{"insertText":{"text":"x","location":{"index":1}}}`)},
		WriteControl: &WriteControl{RequiredRevisionID: "r1", WriteMode: "SUGGEST"},
	})
	if err != nil {
		t.Fatal(err)
	}
	wc := body["writeControl"].(map[string]any)
	if wc["requiredRevisionId"] != "r1" || wc["writeMode"] != "SUGGEST" {
		t.Fatalf("writeControl = %v", wc)
	}
	if res.WriteControl.RequiredRevisionID != "r2" || len(res.Replies) != 1 {
		t.Fatalf("response = %+v", res)
	}
}

func TestGetFile(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/drive/v3/files/abc") || !strings.Contains(r.URL.RawQuery, "supportsAllDrives=true") {
			t.Errorf("unexpected %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"id":"abc","name":"Doc","owners":[{"emailAddress":"o@b.test"}],"capabilities":{"canEdit":true},"version":"12"}`))
	}))
	f, err := c.GetFile(context.Background(), "abc")
	if err != nil || f.Name != "Doc" || !f.Capabilities.CanEdit || f.Version != "12" {
		t.Fatalf("got %+v %v", f, err)
	}
}

func TestAuthErrors(t *testing.T) {
	c := New(NoCredentials{}, Options{DocsBaseURL: "http://127.0.0.1:1", Retry: RetryPolicy{MaxAttempts: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond}, AllowURL: func(*url.URL) bool { return true }})
	_, err := c.GetDocument(context.Background(), "abc", GetOptions{})
	if !errors.Is(err, ErrUnauthorized) || Class(err) != "auth" || !strings.Contains(err.Error(), "login") {
		t.Fatalf("got %v", err)
	}
	re := &oauth2.RetrieveError{Response: &http.Response{StatusCode: 400}, ErrorCode: "invalid_grant", ErrorDescription: "Token has been expired or revoked."}
	err = wrapTransportError(re)
	var ae *AuthError
	if !errors.As(err, &ae) || ae.Code != "invalid_grant" || !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("got %v", err)
	}
}

func TestHelpers(t *testing.T) {
	// The path keeps its shape and loses the id entirely: six characters
	// of an id is still six characters of an id, and §12 promises a log
	// carries nothing about the document.
	if got := redactPath("https://docs.googleapis.com/v1/documents/1AbCdEfGhIjKlMnOp/x?includeTabsContent=true"); got != "/v1/documents/…/x" {
		t.Fatalf("redactPath = %q", got)
	}
	if got := redactPath("https://www.googleapis.com/drive/v3/files/1AbCdEfGhIjKlMnOp/comments"); strings.Contains(got, "1AbCdE") {
		t.Fatalf("redactPath left part of the id: %q", got)
	}
	if ShortID("abc") != "abc" || ShortID("abcdefgh") != "abcdef…" {
		t.Fatal("ShortID")
	}
	if got := parseRetryAfter("7"); got != 7*time.Second {
		t.Fatalf("retry-after seconds = %v", got)
	}
	if got := parseRetryAfter(time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)); got <= 0 || got > 31*time.Second {
		t.Fatalf("retry-after date = %v", got)
	}
	if parseRetryAfter("garbage") != 0 || parseRetryAfter("") != 0 {
		t.Fatal("bad retry-after should be zero")
	}
	c := New(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "t"}), Options{})
	if c.docs != DefaultDocsBaseURL || c.drive != DefaultDriveBaseURL || c.retry.MaxAttempts != 5 || c.driveWriteLim == nil {
		t.Fatalf("defaults wrong: %+v", c)
	}
	for attempt := 1; attempt <= 6; attempt++ {
		if d := c.backoff(attempt, 0); d <= 0 || d > c.retry.MaxDelay+c.retry.MaxDelay/4 {
			t.Fatalf("backoff(%d) = %v", attempt, d)
		}
	}
	if d := c.backoff(1, time.Minute); d != c.retry.MaxDelay {
		t.Fatalf("retry-after should be capped: %v", d)
	}
}

func TestContextCancellation(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := c.About(ctx)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSearchExportComments(t *testing.T) {
	var seen []string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		switch {
		case r.URL.Path == "/drive/v3/files":
			q := r.URL.Query()
			if q.Get("pageToken") == "" && (q.Get("q") != "name contains 'x'" || q.Get("corpora") != "allDrives" || q.Get("pageSize") != "5") {
				t.Errorf("search query wrong: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"files":[{"id":"a","name":"A"}],"nextPageToken":"n2"}`))
		case strings.HasSuffix(r.URL.Path, "/export"):
			if r.Header.Get("Accept") != "*/*" || r.URL.Query().Get("mimeType") != "text/markdown" {
				t.Errorf("export headers wrong: %v %s", r.Header, r.URL.RawQuery)
			}
			_, _ = w.Write([]byte("# md"))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/comments"):
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["content"] != "hello" || in["quotedFileContent"].(map[string]any)["value"] != "quoted" {
				t.Errorf("comment body wrong: %v", in)
			}
			_, _ = w.Write([]byte(`{"id":"c1","content":"hello","quotedFileContent":{"value":"quoted"}}`))
		case strings.HasSuffix(r.URL.Path, "/comments"):
			if r.URL.Query().Get("pageToken") == "" {
				_, _ = w.Write([]byte(`{"comments":[{"id":"c1","resolved":true}],"nextPageToken":"p2"}`))
			} else {
				_, _ = w.Write([]byte(`{"comments":[{"id":"c2","replies":[{"id":"r1","action":"resolve"}]}]}`))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	ctx := context.Background()
	fl, err := c.SearchFiles(ctx, "name contains 'x'", 5, "")
	if err != nil || len(fl.Files) != 1 || fl.NextPageToken != "n2" {
		t.Fatalf("search: %+v %v", fl, err)
	}
	if _, err := c.SearchFiles(ctx, "q", 0, "tok"); err != nil || !strings.Contains(seen[len(seen)-1], "pageToken=tok") || !strings.Contains(seen[len(seen)-1], "pageSize=20") {
		t.Fatalf("search defaults: %v %v", seen, err)
	}
	data, err := c.Export(ctx, "abc", "text/markdown")
	if err != nil || string(data) != "# md" {
		t.Fatalf("export: %q %v", data, err)
	}
	cm, err := c.CreateComment(ctx, "abc", "hello", "quoted")
	if err != nil || cm.ID != "c1" || cm.QuotedFileContent.Value != "quoted" {
		t.Fatalf("create comment: %+v %v", cm, err)
	}
	all, err := c.ListComments(ctx, "abc", true)
	if err != nil || len(all) != 2 || !all[0].Resolved || all[1].Replies[0].Action != "resolve" {
		t.Fatalf("list comments: %+v %v", all, err)
	}
	if got := QuoteDriveValue(`it's a \ test`); got != `'it\'s a \\ test'` {
		t.Fatalf("quote = %s", got)
	}
	if ExportMimeTypes["pdf"] != "application/pdf" || DocumentMimeType == "" {
		t.Fatal("mime tables")
	}
}

func TestCreateDocument(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/documents" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		_, _ = w.Write([]byte(`{"documentId":"new1","title":"` + in["title"] + `","revisionId":"r1"}`))
	}))
	d, err := c.CreateDocument(context.Background(), "T")
	if err != nil || d.DocumentID != "new1" || d.Title != "T" {
		t.Fatalf("create: %+v %v", d, err)
	}
}

func TestRepliesRevisionsAndDeletes(t *testing.T) {
	var seen []string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/comments/c1/replies"):
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["action"] != "resolve" || in["content"] != "done" || !strings.Contains(r.URL.RawQuery, "fields=") {
				t.Errorf("reply body wrong: %v %s", in, r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"id":"r9","content":"done","action":"resolve"}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/comments/c1"):
			if r.URL.Query().Get("includeDeleted") != "true" {
				t.Errorf("get comment query: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"id":"c1","content":"x","replies":[{"id":"r1"}]}`))
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/comments/c1/replies/r1"):
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["content"] != "fixed reply" {
				t.Errorf("reply patch body: %v", in)
			}
			_, _ = w.Write([]byte(`{"id":"r1","content":"fixed reply"}`))
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/comments/c1"):
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["content"] != "fixed" || !strings.Contains(r.URL.RawQuery, "fields=") {
				t.Errorf("comment patch body: %v %s", in, r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"id":"c1","content":"fixed"}`))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/revisions"):
			if r.URL.Query().Get("pageToken") == "" {
				_, _ = w.Write([]byte(`{"revisions":[{"id":"1","modifiedTime":"2026-09-01T00:00:00Z","exportLinks":{"text/markdown":"https://example.test/x"}}],"nextPageToken":"p2"}`))
			} else {
				_, _ = w.Write([]byte(`{"revisions":[{"id":"7","keepForever":true,"lastModifyingUser":{"displayName":"Ann"}}]}`))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	ctx := context.Background()
	rp, err := c.CreateReply(ctx, "abc", "c1", "done", "resolve")
	if err != nil || rp.ID != "r9" || rp.Action != "resolve" {
		t.Fatalf("reply: %+v %v", rp, err)
	}
	cm, err := c.GetComment(ctx, "abc", "c1")
	if err != nil || cm.ID != "c1" || len(cm.Replies) != 1 {
		t.Fatalf("get comment: %+v %v", cm, err)
	}
	up, err := c.UpdateComment(ctx, "abc", "c1", "fixed")
	if err != nil || up.Content != "fixed" {
		t.Fatalf("update comment: %+v %v", up, err)
	}
	upr, err := c.UpdateReply(ctx, "abc", "c1", "r1", "fixed reply")
	if err != nil || upr.Content != "fixed reply" {
		t.Fatalf("update reply: %+v %v", upr, err)
	}
	if err := c.DeleteComment(ctx, "abc", "c1"); err != nil {
		t.Fatalf("delete comment: %v", err)
	}
	if err := c.DeleteReply(ctx, "abc", "c1", "r1"); err != nil {
		t.Fatalf("delete reply: %v", err)
	}
	revs, err := c.ListRevisions(ctx, "abc")
	if err != nil || len(revs) != 2 || revs[0].ID != "1" || !revs[1].KeepForever || revs[1].LastModifyingUser.DisplayName != "Ann" {
		t.Fatalf("revisions: %+v %v", revs, err)
	}
	want := []string{"POST /drive/v3/files/abc/comments/c1/replies", "GET /drive/v3/files/abc/comments/c1",
		"PATCH /drive/v3/files/abc/comments/c1", "PATCH /drive/v3/files/abc/comments/c1/replies/r1", "DELETE /drive/v3/files/abc/comments/c1", "DELETE /drive/v3/files/abc/comments/c1/replies/r1", "GET /drive/v3/files/abc/revisions", "GET /drive/v3/files/abc/revisions"}
	if strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Fatalf("calls %v", seen)
	}
}

func TestExportRevision(t *testing.T) {
	var polls int32
	var srvURL string
	c, srv := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/files/abc/download"):
			q := r.URL.Query()
			if q.Get("revisionId") != "12" || q.Get("mimeType") != "text/markdown" {
				t.Errorf("download query: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"name":"op1","done":false}`))
		case strings.HasSuffix(r.URL.Path, "/operations/op1"):
			if atomic.AddInt32(&polls, 1) < 2 {
				_, _ = w.Write([]byte(`{"name":"op1","done":false}`))
				return
			}
			_, _ = w.Write([]byte(`{"name":"op1","done":true,"response":{"downloadUri":"` + srvURL + `/content"}}`))
		case r.URL.Path == "/content":
			_, _ = w.Write([]byte("# old"))
		default:
			http.NotFound(w, r)
		}
	}))
	srvURL = srv.URL
	data, err := c.ExportRevision(context.Background(), "abc", "12", "text/markdown")
	if err != nil || string(data) != "# old" || polls != 2 {
		t.Fatalf("export revision: %q %v polls=%d", data, err, polls)
	}
	// A host carrying a port must not match: the allowlist sees
	// url.URL.Host, and a redirect or a response-body URL pointing at
	// googleapis.com:8443 is not the API.
	for _, host := range []string{"docs.googleapis.com", "docs.google.com", "googleapis.com", "lh3.googleusercontent.com", "DOCS.GOOGLE.COM"} {
		if !googleHost(host) {
			t.Errorf("googleHost(%q) should be allowed", host)
		}
	}
	for _, host := range []string{"evil.example", "googleapis.com.evil.example", "docs.google.com.evil.example",
		"docs.googleapis.com:8443", "docs.google.com:443", "googleapis.com:80"} {
		if googleHost(host) {
			t.Errorf("googleHost(%q) should be refused", host)
		}
	}
}

func TestExportRevisionErrors(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name":"op1","done":true,"error":{"code":3,"message":"revision unsupported"}}`))
	}))
	_, err := c.ExportRevision(context.Background(), "abc", "12", "text/markdown")
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "revision unsupported") {
		t.Fatalf("operation error: %v", err)
	}
	// A content URL outside the allowlist is refused before any request.
	c, srv := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name":"op1","done":true,"response":{"downloadUri":"https://evil.example/x"}}`))
	}))
	c.allowURL = func(u *url.URL) bool { return strings.HasPrefix(srv.URL, "http://"+u.Host) }
	_, err = c.ExportRevision(context.Background(), "abc", "12", "text/markdown")
	if !errors.Is(err, ErrUnexpected) || !strings.Contains(err.Error(), "evil.example") {
		t.Fatalf("foreign host: %v", err)
	}
}
