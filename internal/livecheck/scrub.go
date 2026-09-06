// This file carries the transcript redactor. It has no build tag on
// purpose: the rest of the package is behind `live` and runs only by
// hand with credentials, so a test for the redactor there would run
// almost never, and the thing that keeps a real person out of a pasted
// transcript has to be checked by the same `make check` as everything
// else.
package livecheck

import "regexp"

// scrub keeps a real document and a real person out of the transcript,
// which people paste into issues and chat.
//
// Two kinds of thing, caught two ways. An id, a URL and an address have
// a shape, so a pattern finds them wherever they appear. **A person's
// name does not.** There is no shape for "Ann Petersen", so names are
// caught by position, and position works only because the renderers in
// internal/service and internal/render wrote every one of them: `owner
// X`, a reply line, and ` by X` from Info.text, list_revisions,
// search_documents, list_comments and list_suggestions.
//
// A person is redacted to the end of the line, and everything after the
// name goes with it — a comment's quote and body, a suggestion's diff, a
// revision's `(kept)`. That is a real cost, paid deliberately: a display
// name has no reliable terminator. Directory names carry parentheses
// ("Ann Petersen (Acme Corp)"), so stopping at the first `(` to save a
// trailing timestamp left the organisation in the transcript — which is
// the half hard rule 1 is actually about. Over-redaction is the safe
// direction here, and nothing downstream depends on it: a step parses
// the untouched text (see `shown`).
var (
	docURL  = regexp.MustCompile(`/document/d/[A-Za-z0-9_-]+`)
	gdocs   = regexp.MustCompile(`gdocs://[A-Za-z0-9_-]{20,}`)
	revText = regexp.MustCompile(`revision [A-Za-z0-9_-]{20,}`)
	address = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)

	// A revision id reaches the transcript two ways and only one says
	// "revision" first: list_revisions renders `- <id> at <when>`, which
	// revText never saw. Comment and thread ids share that line shape
	// but are short, which is what the length bound is for.
	revLine = regexp.MustCompile(`(?m)^- [A-Za-z0-9_-]{20,}`)

	// The two line-leading person positions, and the mid-line one.
	personLine = regexp.MustCompile(`(?m)^(owner |\s*↳ ).*$`)
	byPerson   = regexp.MustCompile(`(?m) by .*$`)
)

func scrub(s string) string {
	s = docURL.ReplaceAllString(s, "/document/d/<scratch>")
	s = gdocs.ReplaceAllString(s, "gdocs://<scratch>")
	s = revText.ReplaceAllString(s, "revision <rev>")
	s = revLine.ReplaceAllString(s, "- <rev>")
	s = address.ReplaceAllString(s, "<address>")
	s = personLine.ReplaceAllString(s, "${1}<person>")
	return byPerson.ReplaceAllString(s, " by <person>")
}

// shown prepares a result for the transcript: everything logged goes
// through here, so the scrubbing happens once and a step still parses the
// ids and URLs it needs out of the untouched text.
func shown(s string, n int) string {
	s = scrub(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
