package main

import (
	"slices"
	"strings"
	"testing"
)

// The changelog rule is the one with a scar: it used to fail on every
// release pull request, because a release commit moves the entries out
// of [Unreleased] and under the version it is about to tag, and CI runs
// before that tag can exist. These cases pin both halves — an ordinary
// commit must document itself, and a release must not be asked to
// document itself twice.
func TestChangelogSections(t *testing.T) {
	const changelog = `# Changelog

## [Unreleased]

## [0.9.4] - 2026-09-05

### Fixed
- something real
- something else

## [0.9.3] - 2026-09-05

### Added
- older entry
`
	if got := entriesUnder(changelog, "## [Unreleased]"); got != 0 {
		t.Errorf("[Unreleased] has %d entries, want 0 — a release commit leaves it empty", got)
	}
	if got := entriesUnder(changelog, "## [0.9.4]"); got != 2 {
		t.Errorf("the pending version has %d entries, want 2", got)
	}
	if got := entriesUnder(changelog, "## [0.9.3]"); got != 1 {
		t.Errorf("counting ran past the next heading: %d", got)
	}
	if got := entriesUnder(changelog, "## [1.0.0]"); got != 0 {
		t.Errorf("a heading that is not there should count nothing, got %d", got)
	}

	// The first version heading is the one a release is preparing.
	m := versionHeading.FindStringSubmatch(changelog)
	if m == nil || m[1] != "0.9.4" {
		t.Fatalf("first version heading = %v, want 0.9.4", m)
	}
}

func TestEntriesUnderIgnoresProse(t *testing.T) {
	const changelog = `## [Unreleased]

Some prose that is not an entry.
  - an indented bullet inside a paragraph

## [0.1.0] - 2026-01-01
- a real entry
`
	if got := entriesUnder(changelog, "## [Unreleased]"); got != 0 {
		t.Errorf("prose and indented bullets are not entries, got %d", got)
	}
}

func TestDifference(t *testing.T) {
	registered := []string{"a", "b", "c"}
	documented := []string{"b", "c", "d"}
	if got := difference(registered, documented); !slices.Equal(got, []string{"a"}) {
		t.Errorf("missing from the README = %v, want [a]", got)
	}
	if got := difference(documented, registered); !slices.Equal(got, []string{"d"}) {
		t.Errorf("named but not registered = %v, want [d]", got)
	}
	if got := difference(registered, registered); len(got) != 0 {
		t.Errorf("identical lists differ: %v", got)
	}
}

// The two patterns that turn hand-written lists into derived ones. If
// either stops matching, the gate silently checks nothing — so the gate
// asserts a floor on what it found, and these pin the shapes.
func TestPatternsMatchTheRealShapes(t *testing.T) {
	const config = `	def(&s.LogLevel, "log-level", "LOG_LEVEL", string(LogInfo), "log level")
	def(&s.HTTPTimeout, "http-timeout", "HTTP_TIMEOUT", "60s", "per-request timeout")`
	keys := configKey.FindAllStringSubmatch(config, -1)
	if len(keys) != 2 || keys[0][1] != "LOG_LEVEL" || keys[1][1] != "HTTP_TIMEOUT" {
		t.Errorf("config keys = %v", keys)
	}

	const readme = "| `get_document` | metadata |\n| `read_document` | text |\nnot a row\n"
	tools := readmeTool.FindAllStringSubmatch(readme, -1)
	if len(tools) != 2 || tools[0][1] != "get_document" || tools[1][1] != "read_document" {
		t.Errorf("readme tools = %v", tools)
	}
	if strings.Contains(readme, "| `nothing` |") {
		t.Fatal("fixture drifted")
	}
}

// The status rule, exercised over the states this repository cannot
// reach. It always has a tag, so the branches that matter to a sibling
// before its first release would otherwise ship green here and misfire
// there — which is the same argument this file makes about every other
// gate in it.
func TestStatusProblems(t *testing.T) {
	body := func(status, seventeen string) string {
		return "# Architecture\n\n" + status + "\n\n## 1. Mission\n\ntext\n\n## 17. Open decisions\n\n" +
			seventeen + "\n\n## 18. Evidence\n"
	}
	openDecision := "Something is undecided."
	noDecision := "None. Everything is decided."
	claimsOpen := "**Status:** v1.0.0 (2026-09-06). One design decision is open (§17)."
	claimsShut := "**Status:** v1.0.0 (2026-09-06). Phases 0 to 4 are done."
	// The denial, which contains the phrase the claim is detected by.
	claimsNone := "**Status:** v1.0.0 (2026-09-06). No design decision is open (§17)."

	cases := []struct {
		name       string
		status     string
		seventeen  string
		tag, next  string
		wantSubstr string // "" means no problem
	}{
		{"released and current", claimsOpen, openDecision, "1.0.0", "1.0.0", ""},
		{"the version about to be tagged", claimsOpen, openDecision, "0.9.5", "1.0.0", ""},
		{"behind the tag", claimsShut, noDecision, "1.0.0", "1.0.0", ""},
		{"stale version", "**Status:** v0.5.0.", noDecision, "1.0.0", "1.0.0", "says Status v0.5.0"},

		// Before the first release: no tag, nothing under a version
		// heading. None of these can happen in this repository.
		{"unreleased and claims nothing", "This is phase 2.", noDecision, "", "", ""},
		{"unreleased but claims a version", "**Status:** v0.1.0.", noDecision, "", "",
			"there is no tag and no version heading to confirm it"},

		// The half a badge cannot carry.
		{"claims open while §17 says none", claimsOpen, noDecision, "1.0.0", "1.0.0",
			"claims an open decision while §17 has none"},
		{"claims none while §17 lists one", claimsShut, openDecision, "1.0.0", "1.0.0",
			"claims no open decision while §17 lists one"},
		{"denies one and §17 has none", claimsNone, noDecision, "1.0.0", "1.0.0", ""},
		{"denies one while §17 lists one", claimsNone, openDecision, "1.0.0", "1.0.0",
			"claims no open decision while §17 lists one"},

		// git gave us nothing: the message must say so rather than
		// rendering "the released tag is v" with nothing after it.
		{"no tag but a heading exists", "**Status:** v0.5.0.", noDecision, "", "1.0.0",
			"no released tag was found"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := statusProblems(body(c.status, c.seventeen), c.tag, c.next)
			joined := strings.Join(got, "\n")
			switch {
			case c.wantSubstr == "" && len(got) > 0:
				t.Errorf("expected no problem, got:\n%s", joined)
			case c.wantSubstr != "" && !strings.Contains(joined, c.wantSubstr):
				t.Errorf("expected a problem mentioning %q, got:\n%s", c.wantSubstr, joined)
			}
		})
	}
}

// A status line with no **Status:** at all is a problem once something
// has shipped, and not before.
func TestStatusProblemsWithNoStatusLine(t *testing.T) {
	text := "# Architecture\n\nprose\n\n## 17. Open decisions\n\nNone.\n"
	if got := statusProblems(text, "", ""); len(got) > 0 {
		t.Errorf("before the first release a missing status line is fine, got: %v", got)
	}
	got := statusProblems(text, "1.0.0", "1.0.0")
	if len(got) == 0 || !strings.Contains(strings.Join(got, "\n"), "has no **Status:** line") {
		t.Errorf("after a release a missing status line is a problem, got: %v", got)
	}
}
