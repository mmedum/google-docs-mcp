package main

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mmedum/google-docs-mcp/v3/internal/config"
)

// The changelog fixtures: a released v0.9.0, a v0.9.1 with and without
// entries, and an [Unreleased] section with and without them. This
// repository has no 0.9 tag, so a git call that reads the wrong checkout
// finds neither.
const (
	clReleased   = "## [0.9.0] - 2026-01-01\n\n### Added\n\n- The first tool.\n"
	clNext       = "## [0.9.1] - 2026-02-01\n\n### Added\n\n- A second tool.\n\n"
	clNextEmpty  = "## [0.9.1] - 2026-02-01\n\n"
	clUnreleased = "# Changelog\n\n## [Unreleased]\n\n"
	clWritten    = clUnreleased + "### Added\n\n- A second tool.\n\n"
	clEmpty      = "Go source changed and CHANGELOG.md [Unreleased] has no entry; " +
		"write one under [Unreleased], or under the version heading this release tags"
)

// The CHANGELOG rule: a change to Go source is written up under
// [Unreleased], or, on the release commit before its tag, under the
// version that commit releases.
func TestTheChangelogRule(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		changelog     string
		sourceChanged bool
		// tagged are the versions that have a tag.
		tagged []string
		want   []string
	}{
		{"an undocumented source change", clUnreleased + clReleased, true, []string{"0.9.0"}, []string{clEmpty}},
		{"a documented source change", clWritten + clReleased, true, []string{"0.9.0"}, nil},
		{"the release commit before its tag", clUnreleased + clNext + clReleased, true, []string{"0.9.0"}, nil},
		{"a release commit with nothing under its version", clUnreleased + clNextEmpty + clReleased, true,
			[]string{"0.9.0"}, []string{"Go source changed and CHANGELOG.md [0.9.1], the untagged version this commit " +
				"releases, has no entry; write one under [Unreleased], or under the version heading this release tags"}},
		{"an undocumented source change after the release is tagged", clUnreleased + clNext + clReleased, true,
			[]string{"0.9.0", "0.9.1"}, []string{clEmpty}},
		{"prose and an indented bullet, which are no entry", clUnreleased + "Some prose.\n  - an aside\n\n" + clReleased, true,
			[]string{"0.9.0"}, []string{clEmpty}},
		{"no change to Go source", clUnreleased + clReleased, false, []string{"0.9.0"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := changelogRule(tc.changelog, tc.sourceChanged, func(v string) bool { return slices.Contains(tc.tagged, v) })
			if !slices.Equal(got, tc.want) {
				t.Errorf("changelogRule() = %q, want %q", got, tc.want)
			}
		})
	}
}

// What changelogRule is told comes from git: whether Go source differs
// from the last tag, which is not always the commit before HEAD, and
// whether a version is tagged. Each row is a repository whose v0.9.0 tag
// is followed by one more commit.
// Outside a git checkout the CHANGELOG rule has no tag to measure from.
// It reached no verdict, so it says so and exits 2, as the docs gate does.
func TestTheChangelogRuleNeedsAGitCheckout(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	t.Chdir(dir)
	put(t, dir, changelogPath, clUnreleased+clReleased)
	_, err := checkChangelog()
	if !errors.As(err, new(couldNotRun)) || exitCode(err) != 2 {
		t.Errorf("got %v; want a git failure that could not run", err)
	}
}

func TestTheChangelogRuleReadsGit(t *testing.T) {
	for _, tc := range []struct {
		name      string
		changelog string
		// source and readme say which files the commit after v0.9.0
		// changes, and later commits a change to the README alone after it.
		source, readme, later bool
		// tag tags that commit, and edit then changes Go source uncommitted.
		tag, edit bool
		want      []string
	}{
		{name: "an undocumented source change after the release is tagged", changelog: clUnreleased + clNext + clReleased,
			source: true, tag: true, edit: true, want: []string{clEmpty}},
		{name: "an undocumented source change two commits after the tag", changelog: clUnreleased + clReleased,
			source: true, later: true, want: []string{clEmpty}},
		{name: "the release commit before its tag", changelog: clUnreleased + clNext + clReleased, source: true},
		{name: "a change to no Go source", changelog: clUnreleased + clReleased, readme: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := initRepo(t)
			put(t, dir, "main.go", "package main\n")
			put(t, dir, "README.md", "# Fixture\n")
			put(t, dir, changelogPath, clUnreleased+clReleased)
			mustGit(t, "add", "-A")
			mustGit(t, "commit", "-q", "-m", "Release 0.9.0")
			mustGit(t, "tag", "-a", "v0.9.0", "-m", "v0.9.0")

			if tc.source {
				put(t, dir, "main.go", "package main\n\nfunc main() {}\n")
			}
			if tc.readme {
				put(t, dir, "README.md", "# Fixture\n\nMore words.\n")
			}
			put(t, dir, changelogPath, tc.changelog)
			mustGit(t, "add", "-A")
			mustGit(t, "commit", "-q", "-m", "The next change")
			if tc.later {
				put(t, dir, "README.md", "# Fixture\n\nA later change.\n")
				mustGit(t, "commit", "-q", "-am", "A later change")
			}
			if tc.tag {
				mustGit(t, "tag", "-a", "v0.9.1", "-m", "v0.9.1")
			}
			if tc.edit {
				put(t, dir, "main.go", "package main\n\nfunc main() { main() }\n")
			}

			got, err := checkChangelog()
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("checkChangelog() = %q, want %q", got, tc.want)
			}
		})
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

// The configuration page names every variable the server reads as a
// whole word, the removed one and the two read outside Define included,
// names no other, and says what a profile name is.
func TestConfigDocProblems(t *testing.T) {
	doc := string(repoFile(t, configDocPath))
	if got := configDocProblems(config.Vars(), doc); len(got) != 0 {
		t.Errorf("the committed page: %q", got)
	}
	for _, tc := range []struct {
		name, old, new string
		want           []string
	}{
		{"a variable left out", "`GDOCS_HTTP_TIMEOUT`", "`the timeout`",
			[]string{"docs/configuration.md does not document GDOCS_HTTP_TIMEOUT"}},
		{"a removed variable left out", "`GDOCS_REQUIRE_PROMPT`", "`the prompt setting`",
			[]string{"docs/configuration.md does not document GDOCS_REQUIRE_PROMPT"}},
		{"a variable read outside Define left out", "`GDOCS_REFRESH_TOKEN`", "`the refresh token`",
			[]string{"docs/configuration.md does not document GDOCS_REFRESH_TOKEN"}},
		{"a name kept only inside a longer one", "`GDOCS_CONFIG_DIR`", "`GDOCS_CONFIG_DIRECTORY`", []string{
			"docs/configuration.md does not document GDOCS_CONFIG_DIR",
			"docs/configuration.md documents GDOCS_CONFIG_DIRECTORY, which the code does not read",
		}},
		{"a variable the code does not read", "`GDOCS_HTTP_TIMEOUT`", "`GDOCS_HTTP_TIMEOUT` and `GDOCS_RETRIES`",
			[]string{"docs/configuration.md documents GDOCS_RETRIES, which the code does not read"}},
		{"the profile rule in other words", "- and _, starting", "`-` and `_`, starting",
			[]string{`docs/configuration.md does not say a profile name is "up to 64 characters from a to z, ` +
				`0 to 9, - and _, starting with a letter or digit"`}},
	} {
		if !strings.Contains(doc, tc.old) {
			t.Fatalf("%s: the page does not say %q", tc.name, tc.old)
		}
		if got := configDocProblems(config.Vars(), strings.ReplaceAll(doc, tc.old, tc.new)); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The pattern that turns the README's hand-written table into a derived
// list. If it stops matching, the gate silently checks nothing, so this
// pins the shape.
func TestPatternsMatchTheRealShapes(t *testing.T) {
	const readme = "| `get_document` | metadata |\n| `read_document` | text |\nnot a row\n"
	tools := readmeTool.FindAllStringSubmatch(readme, -1)
	if len(tools) != 2 || tools[0][1] != "get_document" || tools[1][1] != "read_document" {
		t.Errorf("readme tools = %v", tools)
	}
	if strings.Contains(readme, "| `nothing` |") {
		t.Fatal("fixture drifted")
	}
}

// The README's scope list against what login requests. The setup step is
// followed exactly once, by somebody who cannot tell it failed until a
// login is refused, which is why it needs a gate rather than a proofread.
func TestScopesAgree(t *testing.T) {
	wanted := []string{
		"https://www.googleapis.com/auth/documents",
		"https://www.googleapis.com/auth/drive",
	}
	cases := []struct {
		name string
		text string
		want string
	}{
		{"both, abbreviated as the README writes them", "add `.../auth/documents` and `.../auth/drive`", ""},
		{"both, written in full", "add https://www.googleapis.com/auth/documents and https://www.googleapis.com/auth/drive", ""},
		{"one missing", "add `.../auth/documents`", "does not list the OAuth scope"},
		{"none at all", "add the two scopes below", "does not list the OAuth scope"},
		{"one the code never requests", "`.../auth/documents`, `.../auth/drive`, `.../auth/drive.metadata`", "which login never requests"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := strings.Join(scopesAgree(c.text, wanted), "\n")
			if c.want == "" {
				if got != "" {
					t.Errorf("expected no problem; got %s", got)
				}
				return
			}
			if !strings.Contains(got, c.want) {
				t.Errorf("no problem containing %q; got %q", c.want, got)
			}
		})
	}
}

// A derivation that reads nothing must say so rather than pass.
func TestScopesAgreeRefusesAnEmptyDerivation(t *testing.T) {
	got := scopesAgree("anything at all", []string{"https://www.googleapis.com/auth/documents"})
	if len(got) != 1 || !strings.Contains(got[0], "not reading the code") {
		t.Errorf("a one-scope derivation should be refused; got %v", got)
	}
}

// A rule under another server's name is not this server's to check, and
// a tool named in prose is not a rule.
func TestPermissionRuleToolsReadsOnlyThisServersRules(t *testing.T) {
	const readme = "Call `mcp__google-docs__get_document` first.\n" +
		"```json\n" +
		`"ask": ["mcp__google-docs__delete_tab", "mcp__google-docs__delete_coment",` + "\n" +
		`  "mcp__google-drive__share_file"]` + "\n" +
		"```\n"
	got := permissionRuleTools(readme)
	want := []string{"delete_tab", "delete_coment"}
	if !slices.Equal(got, want) {
		t.Errorf("permissionRuleTools\n got %v\nwant %v", got, want)
	}
}

// Every rule names a registered tool, and a README with no rule at all
// fails too, since it would not say how to be asked.
func TestPermissionProblems(t *testing.T) {
	registered := []string{"delete_comment", "delete_tab", "get_document"}
	for _, tc := range []struct {
		name, readme string
		want         []string
	}{
		{"every rule names a tool", `"ask": ["mcp__google-docs__delete_tab", "mcp__google-docs__delete_comment"]`, nil},
		{"a rule names no tool", `"ask": ["mcp__google-docs__delete_tab", "mcp__google-docs__delete_coment"]`,
			[]string{"README gives a permission rule for delete_coment, which the server does not register"}},
		{"no rule at all", "Approve each call in your client.",
			[]string{`README gives no Claude Code permission rule ("mcp__google-docs__<tool>")`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := permissionProblems(tc.readme, registered); !slices.Equal(got, tc.want) {
				t.Errorf("permissionProblems = %q, want %q", got, tc.want)
			}
		})
	}
}
