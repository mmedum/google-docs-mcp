package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mmedum/google-docs-mcp/internal/gapi"
)

func TestStripDataURIs(t *testing.T) {
	in := "![][image1]\n\n[image1]: <data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAEAAAABACAYAAACqaXHe> and data:text/plain;base64,short"
	got := stripDataURIs(in)
	if got != "![][image1]\n\n[image1]: <data:image/png;base64,<omitted>> and data:text/plain;base64,short" {
		t.Fatalf("stripped: %q", got)
	}
	if s, cut := clipUTF8("héllo", 2); s != "h" || !cut {
		t.Fatalf("clip: %q %v", s, cut)
	}
}

func TestListRevisions(t *testing.T) {
	svc, api := newService(t)
	api.revisions = []*gapi.Revision{
		{ID: "1", ModifiedTime: "2026-09-01T00:00:00Z", LastModifyingUser: &gapi.User{DisplayName: "Ann"}},
		{ID: "2", ModifiedTime: "2026-09-02T00:00:00Z", KeepForever: true},
		{ID: "3", ModifiedTime: "2026-09-03T00:00:00Z"},
	}
	res, err := svc.ListRevisions(context.Background(), fixtureID, 2)
	if err != nil || res.Total != 3 || len(res.Revisions) != 2 || res.Revisions[0].ID != "3" || !res.Revisions[1].KeepForever {
		t.Fatalf("revisions: %+v %v", res, err)
	}
	if !strings.Contains(res.Text, "showing 2") || !strings.Contains(res.Text, "- 2 at 2026-09-02T00:00:00Z (kept)") {
		t.Fatalf("text:\n%s", res.Text)
	}
	res, err = svc.ListRevisions(context.Background(), fixtureID, 0)
	if err != nil || len(res.Revisions) != 3 || res.Revisions[2].ModifiedBy != "Ann" {
		t.Fatalf("default limit: %+v %v", res, err)
	}
	if _, err := svc.ListRevisions(context.Background(), "nope", 0); classOf(err) != "invalid" {
		t.Fatalf("bad id: %v", err)
	}
	api.revisionErr = &gapi.APIError{Status: 403, Message: "no"}
	if _, err := svc.ListRevisions(context.Background(), fixtureID, 0); classOf(err) != "forbidden" {
		t.Fatalf("api error: %v", err)
	}
}

func TestDiffRevisionsAndReadRevision(t *testing.T) {
	svc, api := newService(t)
	api.revisionExports = map[string]string{"1": "# Title\n\nold line\nsame\n", "2": "# Title\n\nnew line\nsame\n"}
	ctx := context.Background()
	res, err := svc.DiffRevisions(ctx, DiffRequest{Document: fixtureID, From: "1", To: "2"})
	if err != nil || res.Stats.Added != 1 || res.Stats.Removed != 1 || res.Stats.Hunks != 1 || res.Truncated {
		t.Fatalf("diff: %+v %v", res, err)
	}
	if !strings.Contains(res.Text, "-old line\n+new line") || !strings.HasPrefix(res.Text, "revision 1 → 2 (md): +1 −1 lines in 1 hunk(s)") {
		t.Fatalf("diff text:\n%s", res.Text)
	}
	// To empty diffs against the current export (the fake exports a fixed markdown body).
	res, err = svc.DiffRevisions(ctx, DiffRequest{Document: fixtureID, From: "1", Format: "txt"})
	if err != nil || res.To != "current" || res.Format != "txt" || api.exported != "text/plain" {
		t.Fatalf("diff to current: %+v %v (exported %s)", res, err, api.exported)
	}
	res, err = svc.DiffRevisions(ctx, DiffRequest{Document: fixtureID, From: "1", To: "1"})
	if err != nil || !strings.Contains(res.Text, "no differences") {
		t.Fatalf("identical: %+v %v", res, err)
	}
	for _, tc := range []struct {
		req   DiffRequest
		class string
	}{
		{DiffRequest{Document: "bad", From: "1"}, "invalid"},
		{DiffRequest{Document: fixtureID}, "invalid"},
		{DiffRequest{Document: fixtureID, From: "1", Format: "pdf"}, "invalid"},
		{DiffRequest{Document: fixtureID, From: "9"}, "invalid"},
	} {
		if _, err := svc.DiffRevisions(ctx, tc.req); classOf(err) != tc.class {
			t.Errorf("%+v: %v", tc.req, err)
		}
	}
	rr, err := svc.ReadRevision(ctx, fixtureID, "2", "", 12)
	if err != nil || rr.Text != "# Title\n\nnew" || !rr.Truncated || rr.Revision != "2" || rr.RevisionID != "" || !strings.Contains(rr.Scope, "no handles") {
		t.Fatalf("read revision: %+v %v", rr, err)
	}
	if _, err := svc.ReadRevision(ctx, fixtureID, "2", "raw", 0); classOf(err) != "invalid" {
		t.Fatalf("raw at revision: %v", err)
	}
	var se *Error
	if _, err := svc.ReadRevision(ctx, fixtureID, "9", "md", 0); !errors.As(err, &se) || se.Class != "invalid" {
		t.Fatalf("missing revision: %v", err)
	}
}
