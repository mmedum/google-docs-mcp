package gapi

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// TestAPermissionDenialDoesNotRepeatTheAccount: Google names the account
// in the message of a 403, and that message is repeated verbatim into an
// error string. The MCP stdio transport says clients may capture and
// forward a server's stderr, and the protocol's logging section says log
// messages must not carry personal identifying information.
func TestAPermissionDenialDoesNotRepeatTheAccount(t *testing.T) {
	body := []byte(`{"error":{"code":403,"status":"PERMISSION_DENIED",` +
		`"message":"The user someone.private@example.com does not have permission."}}`)
	e := parseAPIError(403, "GET", "/v1/documents/x", body)
	if strings.Contains(e.Error(), "someone.private@example.com") {
		t.Errorf("the address was repeated verbatim: %s", e.Error())
	}
	if !strings.Contains(e.Error(), "…@example.com") {
		t.Errorf("the domain should survive so the account is still identifiable: %s", e.Error())
	}
	// A body that is not an error envelope is kept verbatim as the
	// message, which is the worse of the two paths.
	raw := parseAPIError(500, "GET", "/x", []byte("upstream refused someone.private@example.com"))
	if strings.Contains(raw.Error(), "someone.private@example.com") {
		t.Errorf("an unparsed body was kept verbatim: %s", raw.Error())
	}
	// A message with no address in it is untouched.
	plain := parseAPIError(404, "GET", "/x", []byte(`{"error":{"message":"Document not found."}}`))
	if plain.Message != "Document not found." {
		t.Errorf("message = %q, want it unchanged", plain.Message)
	}
}

type failingTransport struct{ err error }

func (f failingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if f.err == nil {
		<-r.Context().Done()
		return nil, r.Context().Err()
	}
	return nil, f.err
}

// TestTransportErrorsDoNotCarryTheURL: net/http wraps a transport failure
// in a *url.Error that repeats the whole request URL, so a search term in
// q= and the document id in the path reached the debug log and the error.
// A canceled call took the same route without even the network wrapper.
func TestTransportErrorsDoNotCarryTheURL(t *testing.T) {
	const term = "canaryterm"
	for _, tc := range []struct {
		label  string
		err    error
		cancel bool
	}{
		{"connection reset", errors.New("connection reset by peer"), false},
		{"oauth2 failure", errors.New("oauth2: token expired and refresh token is not set"), false},
		{"canceled", nil, true},
	} {
		var logs bytes.Buffer
		c := New(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "tok"}), Options{
			BaseTransport: failingTransport{err: tc.err},
			Logger:        slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
			Retry:         RetryPolicy{MaxAttempts: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond},
		})
		run := func(call func(context.Context) error) error {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel {
				time.AfterFunc(10*time.Millisecond, cancel)
			}
			return call(ctx)
		}
		calls := map[string]func(context.Context) error{
			"search": func(ctx context.Context) error {
				_, err := c.SearchFiles(ctx, "fullText contains '"+term+"'", 10, "")
				return err
			},
			"get": func(ctx context.Context) error {
				_, err := c.GetDocument(ctx, "docid"+term, GetOptions{})
				return err
			},
		}
		for name, call := range calls {
			err := run(call)
			if err == nil {
				t.Fatalf("%s %s: succeeded against a failing transport", tc.label, name)
			}
			if strings.Contains(err.Error(), term) {
				t.Errorf("%s %s: the error repeats the query or id: %v", tc.label, name, err)
			}
		}
		if strings.Contains(logs.String(), term) {
			t.Errorf("%s: the debug log repeats the query or id: %s", tc.label, logs.String())
		}
	}
}
