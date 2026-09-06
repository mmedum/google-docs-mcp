package livecheck

import (
	"strings"
	"testing"
)

// Every input below is a line one of this project's renderers actually
// emits, taken from the format strings rather than invented — which is
// the whole basis for redacting by position. An earlier version of this
// table was an approximation of those renderers and had already drifted
// from them: it asserted that a comment line's trailing timestamp
// survives, and no renderer produces a line that ends at the timestamp,
// so the rule meant to preserve it could never fire.
//
// The five positions: internal/service/ops.go (Info.text), history.go
// (list_revisions), search.go (search_documents), suggestions.go
// (list_suggestions) and internal/render/comments.go.
func TestScrubKeepsPeopleAndDocumentsOut(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"get_document's owner line, name and address",
			"owner Ann Petersen <ann.petersen@acme-corp.example>\n",
			"owner <person>\n",
		},
		{
			"get_document's last modified line",
			"last modified 2026-09-01T10:00:00Z by Ann Petersen\n",
			"last modified 2026-09-01T10:00:00Z by <person>\n",
		},
		{
			// render/comments.go: the author is followed by a timestamp,
			// an optional [resolved], the quoted text and the body.
			"a comment thread line",
			"- c9 [t3] by Ann Petersen (2026-09-01T10:00:00Z) on “second point”: please cite this\n",
			"- c9 [t3] by <person>\n",
		},
		{
			"a comment reply line",
			"    ↳ Ann Petersen resolved: agreed\n",
			"    ↳ <person>\n",
		},
		{
			// history.go writes the revision id bare, with no "revision"
			// in front of it, so the shape rule never saw this one.
			"a revision listing line",
			"- 1SyntheticRevisionIdXXXXXXXXXX at 2026-09-01T10:00:00Z by Ann Petersen (kept)\n",
			"- <rev> at 2026-09-01T10:00:00Z by <person>\n",
		},
		{
			// The fifth position, which the redactor's own comment did
			// not name until this table went looking for it.
			"a suggestion listing line",
			"- s1 [p3] replace by Ann Petersen {--old--} {++new++}\n",
			"- s1 [p3] replace by <person>\n",
		},
		{
			// A short comment id on the same line shape as a revision.
			// The length bound is what keeps them apart.
			"a comment id is not a revision id",
			"- c9 [t3] by Ann Petersen\n",
			"- c9 [t3] by <person>\n",
		},
		{
			// The reason a person is redacted to end of line rather than
			// to the first "(": a directory renders the organisation
			// inside the name, and stopping early left it behind.
			"a name carrying an organisation",
			"- c9 [t3] by Ann Petersen (Acme Corp): see above\n",
			"- c9 [t3] by <person>\n",
		},
		{
			"document url and revision, the shapes that always worked",
			"https://docs.google.com/document/d/1SyntheticDocumentIdXXXXXXXXXXXX/edit revision 1SyntheticRevisionIdXXXXXXXXXX\n",
			"https://docs.google.com/document/d/<scratch>/edit revision <rev>\n",
		},
		{
			"a gdocs resource uri",
			"gdocs://1SyntheticDocumentIdXXXXXXXXXXXX/outline\n",
			"gdocs://<scratch>/outline\n",
		},
		{
			"ordinary prose is not a person",
			"the paragraph was replaced and the table rebuilt\n",
			"the paragraph was replaced and the table rebuilt\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := scrub(c.in); got != c.want {
				t.Errorf("scrub(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
		})
	}
}

// The cost of redacting to end of line, asserted rather than left as a
// remark: a comment's quote and body, and a suggestion's diff, do not
// survive. If someone later finds a terminator that is safe for a
// display name, this is the test that should change.
func TestScrubLosesWhatFollowsAPerson(t *testing.T) {
	got := scrub("- s1 [p3] replace by Ann Petersen {--old text--} {++new text++}\n")
	for _, lost := range []string{"old text", "new text"} {
		if strings.Contains(got, lost) {
			t.Errorf("expected %q to be lost with the name; got %q", lost, got)
		}
	}
}

// The guarantee stated over a whole transcript rather than per line,
// because a pasted transcript is the artifact the promise is about.
func TestScrubLeavesNothingIdentifying(t *testing.T) {
	transcript := `=== get_document (isError=false) ===
Quarterly Revenue Plan
https://docs.google.com/document/d/1SyntheticDocumentIdXXXXXXXXXXXX/edit
revision 1SyntheticRevisionIdXXXXXXXXXX
owner Ann Petersen <ann.petersen@acme-corp.example>
last modified 2026-09-01T10:00:00Z by Bo Nilsen <bo@acme-corp.example>
=== list_comments (isError=false) ===
- c9 [t3] by Ann Petersen (2026-09-01T10:00:00Z) on “point”: please cite
    ↳ Bo Nilsen resolved: agreed
`
	got := scrub(transcript)
	for _, forbidden := range []string{
		"Ann Petersen", "Bo Nilsen",
		"ann.petersen@acme-corp.example", "bo@acme-corp.example",
		"acme-corp",
		"1SyntheticDocumentIdXXXXXXXXXXXX", "1SyntheticRevisionIdXXXXXXXXXX",
	} {
		if strings.Contains(got, forbidden) {
			t.Errorf("scrubbed transcript still carries %q:\n%s", forbidden, got)
		}
	}
	// The title is document content and is deliberately NOT redacted: a
	// step asserts on it, and the driver only ever reads documents it
	// created. Asserted so that stops being an accident.
	if !strings.Contains(got, "Quarterly Revenue Plan") {
		t.Error("the title should survive; steps assert on it and the driver writes its own documents")
	}
}

// shown truncates after redacting, so a long result cannot smuggle a
// value past the cut.
//
// The case matters more than it looks. An earlier version used a line
// the position rules already cover and a cut inside the name, and it
// passed under the very bug it named: truncating first gives "owner
// An…", which contains neither the name nor the domain. What
// discriminates is a value only the *shape* rules catch, cut in the
// middle — truncate first and the address stops looking like an address,
// so nothing matches it and the fragment survives.
func TestShownRedactsBeforeTruncating(t *testing.T) {
	line := "granted to ann@acme-corp.example today"
	if got := shown(line, 23); strings.Contains(got, "acme-cor") {
		t.Errorf("shown truncated before redacting: %q", got)
	}
}
