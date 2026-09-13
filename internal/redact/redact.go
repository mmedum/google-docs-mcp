// Package redact keeps a real document and a real person out of the
// artifacts the drivers write. It carries no build tag: the live driver
// and the eval harness are both behind tags and run only by hand, so
// rules that lived beside either of them were tested almost never, and
// the eval harness had no redaction at all.
package redact

import (
	"regexp"
	"strings"
)

// Transcript keeps a real document and a real person out of the transcript,
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
	// Address matches an address at a real-looking domain. Exported
	// because `doctor` needs the same rule with a different replacement:
	// it masks to "…@domain" rather than removing the whole thing, and a
	// second copy of the pattern is a second thing to widen.
	docURL = regexp.MustCompile(`/document/d/[A-Za-z0-9_-]+`)
	gdocs  = regexp.MustCompile(`gdocs://[A-Za-z0-9_-]{20,}`)
	// Every prefix that precedes a revision id, in one rule. There were
	// three, and the third existed only because the second had been
	// missed — `diff_revisions` renders `revision A → B` and nothing
	// caught B. A rule per prefix makes the next prefix a fourth rule;
	// an alternation makes "which prefixes precede an id?" one line to
	// read. `- ` is list_revisions, which writes the id bare.
	revAny = regexp.MustCompile(`(?m)(^- |revision |→ )[A-Za-z0-9_-]{20,}`)

	Address = regexp.MustCompile(`[A-Za-z0-9._%+\-…]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)

	// The two line-leading person positions, and the mid-line one.
	personLine = regexp.MustCompile(`(?m)^(owner |\s*↳ ).*$`)
	byPerson   = regexp.MustCompile(`(?m) by .*$`)
)

// Transcript applies every rule above. See the block comment on the
// rules for why names are caught by position and everything else by
// shape.
func Transcript(s string) string {
	s = docURL.ReplaceAllString(s, "/document/d/<scratch>")
	s = gdocs.ReplaceAllString(s, "gdocs://<scratch>")
	s = revAny.ReplaceAllString(s, "${1}<rev>")
	s = Address.ReplaceAllString(s, "<address>")
	s = personLine.ReplaceAllString(s, "${1}<person>")
	return byPerson.ReplaceAllString(s, " by <person>")
}

// Clip redacts and then truncates, in that order. Truncating first
// lets a value straddling the cut stop matching its shape rule and
// survive, which a test in this package holds.
func Clip(s string, n int) string {
	s = Transcript(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// Account is an address with the local part removed and the domain kept.
//
// The domain is the half a diagnosis uses: shared drives are a Workspace
// feature and a personal account cannot create one, so @gmail.com and a
// Workspace domain are two different sets of behaviour to explain. The
// local part answers nothing — it is never an input to any command here.
func Account(addr string) string {
	local, domain, ok := strings.Cut(addr, "@")
	// Not an address: left alone rather than mangled, so a strange value
	// stays legible to whoever is debugging it.
	if !ok || local == "" || domain == "" {
		return addr
	}
	return "…@" + domain
}

// Accounts masks every address in free text, for text this server did
// not write: a 403 names the account it refused, and that message is
// repeated into an error string that reaches stderr, which the MCP stdio
// transport says clients may capture and forward, and a tool response.
//
// One place rather than at each print, because a print added later is
// then safe without its author knowing the rule, and a writer wrapper
// could split an address across two Write calls and miss it.
func Accounts(s string) string { return Address.ReplaceAllStringFunc(s, Account) }
