//go:build integration

package gapi

// How Google answers a batchUpdate pinned to a revision the document has
// moved past, which APIError.Unwrap maps to ErrConflict. The tools never
// send one on purpose: expect_revision is checked before the write, so
// only a person's edit between the read and the write reaches this
// answer.
//
//	GDOCS_INTEGRATION=1 go test -tags=integration ./internal/gapi -run TestRawStaleRevision -v
import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestRawStaleRevision(t *testing.T) {
	c := probeClient(t)
	ctx := context.Background()
	id := probeDoc(t, c, ctx, "stale", "google-docs-mcp stale revision probe (safe to delete)",
		[]json.RawMessage{insertAt(t, "First line\n", 1)}, nil)
	before, err := c.GetDocument(ctx, id, GetOptions{})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	old := before.Document.RevisionID
	if _, err := c.BatchUpdate(ctx, id, &BatchUpdateRequest{Requests: []json.RawMessage{insertAt(t, "Second line\n", 1)}}); err != nil {
		t.Fatalf("moving the revision on: %v", err)
	}
	_, err = c.BatchUpdate(ctx, id, &BatchUpdateRequest{
		Requests:     []json.RawMessage{insertAt(t, "Third line\n", 1)},
		WriteControl: &WriteControl{RequiredRevisionID: old},
	})
	if err == nil {
		t.Fatal("a write pinned to a revision the document moved past was applied")
	}
	t.Logf("answer: %v", err)
	var api *APIError
	if !errors.As(err, &api) {
		t.Fatalf("not a Google error: %v", err)
	}
	t.Logf("status %d, rpc %q, reason %q, message %q", api.Status, api.RPC, api.Reason, api.Message)
	if !errors.Is(err, ErrConflict) {
		t.Errorf("classified as %v, want %v", api.Unwrap(), ErrConflict)
	}
	t.Logf("=== scratch document left behind; Drive search title:\"safe to delete\" finds it ===")
}
