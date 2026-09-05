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
