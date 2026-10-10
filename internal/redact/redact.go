// Package redact keeps a real document and a real person out of the
// artifacts the drivers write. It carries no build tag: the live driver
// and the eval harness are both behind tags and run only by hand, so
// rules that lived beside either of them were tested almost never, and
// the eval harness had no redaction at all.
package redact

import (
	"bytes"
	"cmp"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
)

// Transcript keeps a real document and a real person out of the transcript,
// which people paste into issues and chat.
//
// Two kinds of thing, caught two ways. An id, a URL and an address have
// a shape, so a pattern finds them wherever they appear. **A person's
// name does not.** There is no shape for "Ann Petersen", so names are
// caught by position where the renderers in internal/service and
// internal/render put one after a label: `owner X`, a reply line, and
// ` by X` from Info.text, list_revisions, search_documents,
// list_comments and list_suggestions. A person chip prints a name with
// no label before it, so a driver passes the names it knows to Clip,
// which replaces them wherever they appear (see Names).
//
// A person is redacted to the end of the line, and everything after the
// name goes with it — a comment's quote and body, a suggestion's diff, a
// revision's `(kept)`. That is a real cost, paid deliberately: a display
// name has no reliable terminator. Directory names carry parentheses
// ("Ann Petersen (Acme Corp)"), so stopping at the first `(` to save a
// trailing timestamp left the organization in the transcript — which is
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

// Names replaces each name given wherever it appears. A person chip
// prints a display name with no position before it, `@Ann Petersen` in
// markdown and bare in plain text, so the rules above cannot catch it;
// a driver passes the names it has read, the signed-in account's first.
// Longest first, so a name that holds another goes whole.
func Names(s string, names []string) string {
	names = slices.SortedFunc(slices.Values(names), func(a, b string) int { return cmp.Compare(len(b), len(a)) })
	for _, n := range names {
		if strings.TrimSpace(n) != "" {
			s = strings.ReplaceAll(s, n, "<person>")
		}
	}
	return s
}

// Clip redacts, replaces the names given, and then truncates, in that
// order. Truncating first lets a value straddling the cut stop matching
// its shape rule and survive, which a test in this package holds. Names
// go after the rules, so a name that is also an address's local part
// cannot break the address before its rule sees it.
func Clip(s string, n int, names ...string) string {
	s = Names(Transcript(s), names)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// TranscriptJSON applies Transcript to every string in a JSON document
// and returns it indented. Applied to the encoded text instead, the rules
// anchored to a line's start never fire, since encoding writes a newline
// as \n, and the rule that redacts to the end of a line takes a string's
// closing quote with it.
func TranscriptJSON(raw []byte) ([]byte, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	return json.MarshalIndent(transcribe(v), "", "  ")
}

// transcribe redacts every string inside a decoded JSON value.
func transcribe(v any) any {
	switch x := v.(type) {
	case string:
		return Transcript(x)
	case []any:
		for i := range x {
			x[i] = transcribe(x[i])
		}
	case map[string]any:
		for k := range x {
			x[k] = transcribe(x[k])
		}
	}
	return v
}

// Account is an address with the local part removed and the domain kept.
//
// The domain is the half a diagnosis uses: shared drives are a Workspace
// feature and a personal account cannot create one, so @gmail.com and a
// Workspace domain are two different sets of behavior to explain. The
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
