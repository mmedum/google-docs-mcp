package main

import (
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"
)

// The gate itself, over this repository's own snapshot and verdicts.
func TestAPICoverageGate(t *testing.T) {
	if err := apiCoverage(io.Discard, nil); err != nil {
		t.Errorf("api coverage gate: %v", err)
	}
}

func published() map[string]apiMethod {
	return map[string]apiMethod{
		"docs\tdocuments.get":       {Method: "documents.get", Verb: "GET", Path: "v1/documents/{id}"},
		"drive\tfiles.list":         {Method: "files.list", Verb: "GET", Path: "files"},
		"drive\tpermissions.create": {Method: "permissions.create", Verb: "POST", Path: "files/{id}/permissions"},
	}
}

func TestCoverageProblems(t *testing.T) {
	ok := []verdict{
		{API: "docs", Method: "documents.get", Verdict: "used", Reason: "GetDocument", line: 1},
		{API: "drive", Method: "files.list", Verdict: "used", Reason: "SearchFiles", line: 2},
		{API: "drive", Method: "permissions.create", Verdict: "out", Reason: "sharing is Drive's", line: 3},
	}
	calls := []string{"GetDocument", "SearchFiles"}

	cases := []struct {
		name     string
		verdicts []verdict
		calls    []string
		want     string
	}{
		{"a matched set", ok, calls, ""},
		{
			name:     "Google published a method nobody has judged",
			verdicts: ok[:2],
			calls:    calls,
			want:     "is published and has no verdict",
		},
		{
			name:     "a client call no row accounts for",
			verdicts: ok,
			calls:    append(slices.Clone(calls), "DeleteEverything"),
			want:     "calls an API and no row names it as used",
		},
		{
			name: "a verdict on a method that is gone",
			verdicts: append(slices.Clone(ok),
				verdict{API: "drive", Method: "files.telepathy", Verdict: "out", Reason: "invented", line: 9}),
			calls: calls,
			want:  "is not a published method any more",
		},
		{
			name: "the same method judged twice",
			verdicts: append(slices.Clone(ok),
				verdict{API: "docs", Method: "documents.get", Verdict: "out", Reason: "changed my mind", line: 9}),
			calls: calls,
			want:  "already has a verdict on line",
		},
		{
			name: "used, but naming something that is not a call",
			verdicts: []verdict{
				{API: "docs", Method: "documents.get", Verdict: "used", Reason: "FetchDocument", line: 1},
				{API: "drive", Method: "files.list", Verdict: "used", Reason: "SearchFiles", line: 2},
				{API: "drive", Method: "permissions.create", Verdict: "out", Reason: "sharing is Drive's", line: 3},
			},
			calls: calls,
			want:  "is not a call in internal/gapi",
		},
		{
			name: "out with no reason at all",
			verdicts: []verdict{
				{API: "docs", Method: "documents.get", Verdict: "used", Reason: "GetDocument", line: 1},
				{API: "drive", Method: "files.list", Verdict: "used", Reason: "SearchFiles", line: 2},
				{API: "drive", Method: "permissions.create", Verdict: "out", Reason: "  ", line: 3},
			},
			calls: calls,
			want:  "is out with no reason given",
		},
		{
			name: "a verdict that is neither",
			verdicts: []verdict{
				{API: "docs", Method: "documents.get", Verdict: "used", Reason: "GetDocument", line: 1},
				{API: "drive", Method: "files.list", Verdict: "used", Reason: "SearchFiles", line: 2},
				{API: "drive", Method: "permissions.create", Verdict: "maybe", Reason: "later", line: 3},
			},
			calls: calls,
			want:  "is neither used nor out",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := strings.Join(coverageProblems(published(), c.verdicts, c.calls), "\n")
			if c.want == "" {
				if got != "" {
					t.Errorf("expected no problem; got:\n%s", got)
				}
				return
			}
			if !strings.Contains(got, c.want) {
				t.Errorf("no problem containing %q; got:\n%s", c.want, got)
			}
		})
	}
}

// Drive nests replies under comments, so a reader that looks one level
// down reports coverage of an API it has not seen.
func TestWalkResourcesSeesNesting(t *testing.T) {
	doc := []byte(`{"comments": {
		"methods": {"list": {"id": "drive.comments.list", "httpMethod": "GET", "path": "files/{fileId}/comments"}},
		"resources": {"replies": {"methods": {
			"list": {"id": "drive.replies.list", "httpMethod": "GET", "path": "files/{fileId}/comments/{commentId}/replies"}}}}}}`)
	var resources map[string]json.RawMessage
	if err := json.Unmarshal(doc, &resources); err != nil {
		t.Fatal(err)
	}
	var got []apiMethod
	if err := walkResources(resources, &got); err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, m := range got {
		names = append(names, m.Method)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"comments.list", "replies.list"}) {
		t.Errorf("methods = %v, want the nested one too", names)
	}
	for _, m := range got {
		if m.Verb == "" || m.Path == "" {
			t.Errorf("%s carries no verb or path; a method that moves would look unchanged", m.Method)
		}
	}
}

// The client's own calls, read out of internal/gapi.
func TestClientCallsReadsTheClient(t *testing.T) {
	calls, err := clientCalls(mustModuleRoot(t) + "/internal/gapi")
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) < 10 {
		t.Fatalf("found %d calls; the derivation is not reading the client", len(calls))
	}
	for _, want := range []string{"GetDocument", "BatchUpdate", "ListComments"} {
		if !slices.Contains(calls, want) {
			t.Errorf("%s is missing from %v", want, calls)
		}
	}
	// ShortID is exported and takes no context: not an API call.
	if slices.Contains(calls, "ShortID") {
		t.Error("ShortID counted as an API call; the signature rule is not being applied")
	}
}
