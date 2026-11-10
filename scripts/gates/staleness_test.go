package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mmedum/google-docs-mcp/v3/internal/config"
	"github.com/mmedum/google-docs-mcp/v3/internal/server"
	"github.com/mmedum/google-docs-mcp/v3/internal/tools"
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
	clEmpty      = "what users get changed but CHANGELOG.md [Unreleased] is empty"
)

// The CHANGELOG rule: a change to the binary's source is written up under
// [Unreleased], or, on the release commit before its tag, under the
// version that commit releases, a prerelease's included. A heading in a
// fence is an example and cuts no release.
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
		{"a prerelease's commit before its tag", clUnreleased + strings.Replace(clNext, "0.9.1", "1.0.0-rc.1", 1) + clReleased,
			true, []string{"0.9.0"}, nil},
		{"a release commit with nothing under its version", clUnreleased + clNextEmpty + clReleased, true,
			[]string{"0.9.0"},
			[]string{"what users get changed but CHANGELOG.md [0.9.1] (untagged, so this is a release commit) is empty"}},
		{"an undocumented source change after the release is tagged", clUnreleased + clNext + clReleased, true,
			[]string{"0.9.0", "0.9.1"}, []string{clEmpty}},
		{"prose and an indented bullet, which are no entry", clUnreleased + "Some prose.\n  - an aside\n\n" + clReleased, true,
			[]string{"0.9.0"}, []string{clEmpty}},
		{"a release cut only in a fence", clUnreleased + "```\n" + clNext + "```\n\n" + clReleased, true,
			[]string{"0.9.0"}, []string{clEmpty}},
		{"no change to the binary's source", clUnreleased + clReleased, false, []string{"0.9.0"}, nil},
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

// stalenessRepo is a git checkout of a module whose binary, cmd/fixture,
// imports internal/a, which embeds a file, and not internal/unused, with a
// release tag v0.9.0 on its one commit. A case changes a stalenessCopy of
// it.
func stalenessRepo(t *testing.T) string {
	t.Helper()
	isolateGit(t)
	t.Setenv("GOWORK", "off")
	dir := t.TempDir()
	putAll(t, dir, map[string]string{
		changelogPath: clUnreleased + clReleased,
		"go.mod": textLines("module example.com/fixture", "", "go 1.21", "",
			"require example.com/dep v1.0.0", "", "replace example.com/dep => ./dep"),
		"dep/go.mod": textLines("module example.com/dep", "", "go 1.21"),
		"dep/dep.go": textLines("package dep"),
		"cmd/fixture/main.go": textLines("package main", "", `import (`, `	_ "example.com/dep"`,
			`	_ "example.com/fixture/internal/a"`, ")", "", "func main() {}"),
		"tools.go":              textLines("package fixture"),
		"internal/a/a.go":       textLines("package a", "", `import _ "embed"`, "", "//go:embed data/x.txt", "var X string"),
		"internal/a/b.go":       textLines("package a"),
		"internal/a/data/x.txt": "x\n",
		"internal/a/notes.md":   "notes\n",
		"internal/unused/u.go":  textLines("package unused"),
		"internal/a/a_test.go":  textLines("package a"),
		"internal/a/testdata/x": "x\n",
		"scripts/gates/main.go": textLines("package main", "", "func main() {}"),
		"docs/x.md":             "x\n",
	})
	stalenessGit(t, dir, "init", "-q", "-b", "main")
	stalenessGit(t, dir, "add", "-A")
	stalenessGit(t, dir, "commit", "-q", "-m", "first")
	stalenessGit(t, dir, "tag", "v0.9.0")
	return dir
}

// stalenessCopy is a copy of the checkout at dir, its .git included, for
// one case to change on its own.
func stalenessCopy(t *testing.T, dir string) string {
	t.Helper()
	cp := t.TempDir()
	if err := os.CopyFS(cp, os.DirFS(dir)); err != nil {
		t.Fatal(err)
	}
	return cp
}

// stalenessGit runs git in dir, isolated as the test's isolateGit left it.
func stalenessGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	mustGit(t, append([]string{"-C", dir}, args...)...)
}

// stalenessWrite writes the files with their new contents, a file with no
// contents removed.
func stalenessWrite(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		if body == "" {
			stalenessGit(t, dir, "rm", "-q", name)
		} else {
			put(t, dir, name, body)
		}
	}
}

// stalenessChange writes the files as stalenessWrite does and commits them.
func stalenessChange(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	stalenessWrite(t, dir, files)
	stalenessGit(t, dir, "add", "-A")
	stalenessGit(t, dir, "commit", "-q", "-m", "change")
}

// A committed change to what users get needs an entry: any file of a
// package the binary imports but a test, a file one embeds, the bundle's
// manifest and launcher, and go.mod but its require and tool lines. A test, a fixture,
// a directory that is no package, a package it does not import and a
// dependency bump do not, and nor does what is not committed, which CI
// never sees.
func TestTheChangelogRuleCountsWhatShips(t *testing.T) {
	const goMod = "go.mod"
	base := stalenessRepo(t)
	raw, err := os.ReadFile(filepath.Join(base, goMod))
	if err != nil {
		t.Fatal(err)
	}
	gomod := string(raw)
	bump := strings.Replace(gomod, "require example.com/dep v1.0.0", "require (\n\texample.com/dep v1.1.0 // a bump\n)", 1)
	for _, tc := range []struct {
		name string
		// files are committed, and after is written after the commit.
		files, after map[string]string
		ships        bool
	}{
		{"a package the binary imports", map[string]string{"internal/a/b.go": "package a\n\n// changed\n"}, nil, true},
		{"a file added to one", map[string]string{"internal/a/c.go": "package a\n"}, nil, true},
		{"a file removed from one", map[string]string{"internal/a/b.go": ""}, nil, true},
		{"a file moved out of one", map[string]string{"internal/a/b.go": "", "docs/b.go": "package a\n"}, nil, true},
		{"the command", map[string]string{"cmd/fixture/main.go": "package main\n\nfunc main() { println() }\n"}, nil, true},
		{"a file a package embeds", map[string]string{"internal/a/data/x.txt": "y\n"}, nil, true},
		{"a package's assembly", map[string]string{"internal/a/b_amd64.s": "// assembly\n"}, nil, true},
		{"a package's .syso", map[string]string{"internal/a/rsrc_windows_amd64.syso": "object\n"}, nil, true},
		{"a file beside a package's Go that it does not embed", map[string]string{"internal/a/notes.md": "more\n"}, nil, true},
		{"the bundle's manifest", map[string]string{manifestPath: "{\"name\": \"x\"}\n"}, nil, true},
		{"the bundle's launcher", map[string]string{launcherPath: "#!/bin/sh\nexec true\n"}, nil, true},
		{"the root README", map[string]string{"README.md": "# changed\n"}, nil, false},
		{"a file in a directory of a package that is no package", map[string]string{"internal/a/notes/x.txt": "y\n"}, nil, false},
		{"assembly under a package's testdata", map[string]string{"internal/a/testdata/x_amd64.s": "// assembly\n"}, nil, false},
		{"go.mod's go line", map[string]string{goMod: strings.Replace(gomod, "go 1.21", "go 1.22", 1)}, nil, true},
		{"go.mod's toolchain", map[string]string{goMod: gomod + "\ntoolchain go1.21.0\n"}, nil, true},
		{"go.mod's replace", map[string]string{goMod: strings.Replace(gomod, "dep =>", "dep v1.0.0 =>", 1)}, nil, true},
		{"a dependency bump", map[string]string{goMod: bump}, nil, false},
		// The go.mod grammar reads ( as a token of its own (https://go.dev/ref/mod#go-mod-file-lexical).
		{"a dependency bump in a require( block", map[string]string{goMod: strings.Replace(gomod,
			"require example.com/dep v1.0.0", "require(\n\texample.com/dep v1.1.0\n)", 1)}, nil, false},
		// A tool directive names a tool `go tool` runs, which the binary does not
		// build (https://go.dev/ref/mod#go-mod-file-tool).
		{"a tool directive", map[string]string{goMod: gomod + "\ntool example.com/dep\n"}, nil, false},
		{"a tool( block", map[string]string{goMod: gomod + "\ntool(\n\texample.com/dep\n)\n"}, nil, false},
		{"a test", map[string]string{"internal/a/a_test.go": "package a\n\n// changed\n"}, nil, false},
		{"a fixture", map[string]string{"internal/a/testdata/x": "y\n"}, nil, false},
		{"a package the binary does not import", map[string]string{"internal/unused/u.go": "package unused\n\n// changed\n"}, nil, false},
		// A dependency's package sits at a path of its own module's, never this one's.
		{"a package at the path of a dependency's", map[string]string{"tools.go": "package fixture\n\n// changed\n"}, nil, false},
		{"a script", map[string]string{"scripts/gates/main.go": "package main\n\nfunc main() { println() }\n"}, nil, false},
		{"a doc", map[string]string{"docs/x.md": "y\n"}, nil, false},
		{"a change not committed", nil, map[string]string{"internal/a/b.go": "package a\n\n// changed\n"}, false},
		{"go.mod as committed, its go line changed after", map[string]string{goMod: bump},
			map[string]string{goMod: strings.Replace(bump, "go 1.21", "go 1.22", 1)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := stalenessCopy(t, base)
			if tc.files != nil {
				stalenessChange(t, dir, tc.files)
			}
			stalenessWrite(t, dir, tc.after)
			got, err := checkChangelog(dir)
			if err != nil {
				t.Fatal(err)
			}
			var want []string
			if tc.ships {
				want = []string{clEmpty}
			}
			if !slices.Equal(got, want) {
				t.Errorf("checkChangelog() = %q, want %q", got, want)
			}
		})
	}
}

// The base is the newest release tag by semver, a prerelease included: a
// change two commits after it still counts, and a release tagged after
// the change takes it out of the next release's notes.
func TestTheChangelogRuleMeasuresFromTheNewestRelease(t *testing.T) {
	base := stalenessRepo(t)
	for _, tc := range []struct {
		name string
		// then runs in the checkout after the shipped change is committed.
		then func(t *testing.T, dir string)
		want []string
	}{
		{"a change two commits after the tag", func(t *testing.T, dir string) {
			stalenessChange(t, dir, map[string]string{"docs/x.md": "later\n"})
		}, []string{clEmpty}},
		{"a change a release then tags", func(t *testing.T, dir string) { stalenessGit(t, dir, "tag", "v0.9.1") }, nil},
		{"a change a prerelease then tags", func(t *testing.T, dir string) { stalenessGit(t, dir, "tag", "v1.0.0-rc.1") }, nil},
		{"a change older than the newest tag by date, not by version", func(t *testing.T, dir string) {
			stalenessGit(t, dir, "tag", "v0.8.9")
		}, []string{clEmpty}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := stalenessCopy(t, base)
			stalenessChange(t, dir, map[string]string{"internal/a/b.go": "package a\n\n// changed\n"})
			tc.then(t, dir)
			got, err := checkChangelog(dir)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("checkChangelog() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A maintenance branch measures from its own release, not from a newer
// one on another line of history: what only the newer release changed is
// no change of the branch's, and a fix the branch picks up from it is.
func TestTheChangelogRuleMeasuresFromTheReleaseHEADReaches(t *testing.T) {
	const fix = "package a\n\n// fixed in 1.0.0\n"
	base := stalenessRepo(t)
	stalenessChange(t, base, map[string]string{"internal/a/b.go": fix})
	stalenessGit(t, base, "tag", "v1.0.0")
	for _, tc := range []struct {
		name string
		// change is what the maintenance branch commits after v0.9.0.
		change map[string]string
		want   []string
	}{
		{"a maintenance branch that changes only the docs", map[string]string{"docs/x.md": "later\n"}, nil},
		// With the docs beside it, the commit is not v1.0.0's own.
		{"a maintenance branch that picks up the newer release's fix",
			map[string]string{"internal/a/b.go": fix, "docs/x.md": "back-ported\n"}, []string{clEmpty}},
		// A version a tag names on any line is released, so its notes are no
		// release commit's.
		{"the newer release's fix and notes, squashed onto the branch", map[string]string{"internal/a/b.go": fix,
			changelogPath: clUnreleased + strings.Replace(clNext, "0.9.1", "1.0.0", 1) + clReleased}, []string{clEmpty}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := stalenessCopy(t, base)
			stalenessGit(t, dir, "switch", "-q", "-c", "release-0.9", "v0.9.0")
			stalenessChange(t, dir, tc.change)
			got, err := checkChangelog(dir)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("checkChangelog() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A listing that names no command is not reading the binary.
func TestTheChangelogRuleNeedsTheBinary(t *testing.T) {
	dir := stalenessRepo(t)
	stalenessChange(t, dir, map[string]string{"cmd/fixture/main.go": "", "cmd/README.md": "moved\n"})
	_, err := checkChangelog(dir)
	want := "could not run: the CHANGELOG rule: go list -deps ./cmd/... named no package of this module, so it is not reading the binary"
	if !errors.As(err, new(couldNotRun)) || err == nil || err.Error() != want {
		t.Errorf("checkChangelog() = %v, want %q", err, want)
	}
}

// Outside a git checkout the CHANGELOG rule has no tag to measure from.
// It reached no verdict, so it says so and exits 2, as the docs gate does,
// and a git that cannot answer is never "source changed".
func TestTheChangelogRuleNeedsAGitCheckout(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	_, err := checkChangelog(dir)
	if !errors.As(err, new(couldNotRun)) || exitCode(err) != 2 ||
		!strings.HasPrefix(err.Error(), "could not run: the CHANGELOG rule: git -C "+dir+" tag") {
		t.Errorf("outside git: %v; want a git tag failure that could not run", err)
	}
	// A configuration git diff cannot read, and git tag does not.
	dir = stalenessRepo(t)
	stalenessGit(t, dir, "config", "diff.renames", "bogus")
	_, err = checkChangelog(dir)
	if !errors.As(err, new(couldNotRun)) || err == nil ||
		!strings.HasPrefix(err.Error(), "could not run: the CHANGELOG rule: git -C "+dir+" diff v0.9.0..HEAD") {
		t.Errorf("a diff git cannot make: %v; want a git diff failure that could not run", err)
	}
}

// With no release tag there is nothing to measure from, so the rule says
// why rather than measure from some other commit. A shallow clone fetches
// no tag, and the rule names it.
func TestTheChangelogRuleNeedsARelease(t *testing.T) {
	const noTag = "could not run: the CHANGELOG rule: HEAD reaches no release tag (vX.Y.Z) to measure from"
	base := stalenessRepo(t)
	stalenessChange(t, base, map[string]string{"internal/a/b.go": "package a\n\n// changed\n"})
	untagged := stalenessCopy(t, base)
	stalenessGit(t, untagged, "tag", "-d", "v0.9.0")
	shallow := t.TempDir()
	from := filepath.ToSlash(base)
	if !strings.HasPrefix(from, "/") {
		from = "/" + from
	}
	stalenessGit(t, shallow, "clone", "-q", "--depth=1", "--no-tags", "file://"+from, ".")
	for dir, want := range map[string]string{
		untagged: noTag,
		shallow:  noTag + "; the checkout is a shallow clone: check it out with fetch-depth: 0",
	} {
		_, err := checkChangelog(dir)
		if !errors.As(err, new(couldNotRun)) || err == nil || err.Error() != want {
			t.Errorf("checkChangelog() = %v, want %q", err, want)
		}
	}
}

// The CHANGELOG rule not running holds back no other check: the gate
// still holds the README and the configuration page, a real finding still
// fails it, and with none it reports only that the rule could not run.
func TestStalenessRunsEveryOtherCheckWithoutARelease(t *testing.T) {
	isolateGit(t)
	dump := stalenessDump(t)
	files := map[string]string{}
	for _, name := range []string{"README.md", configDocPath, changelogPath} {
		data, err := os.ReadFile(filepath.Join(parityRepoRoot, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = string(data)
	}
	const unrun = "could not run: the CHANGELOG rule: HEAD reaches no release tag (vX.Y.Z) to measure from"
	for _, tc := range []struct {
		name   string
		readme func(string) string
		err    string
		code   int
	}{
		{"nothing else wrong", func(s string) string { return s }, unrun, 2},
		{"a table row for a tool the binary does not register", func(s string) string {
			row := "| `get_document` |"
			if !strings.Contains(s, row) {
				t.Fatalf("README.md has no row starting %q", row)
			}
			return strings.Replace(s, row, "| `ghost_tool` | x |\n"+row, 1)
		}, "README's tool table names tools that are not registered: ghost_tool\n" + unrun, 1},
		// A fence holds an example, which documents nothing and lists nothing.
		{"a tool and a scope named only in a fenced example", func(s string) string {
			for _, old := range []string{"`delete_tab`", "`.../auth/drive.readonly`"} {
				if !strings.Contains(s, old) {
					t.Fatalf("README.md does not say %s", old)
				}
				s = strings.ReplaceAll(s, old, "that one")
			}
			return s + "\n```text\n`delete_tab` .../auth/drive.readonly\n```\n"
		}, "README does not list the OAuth scope https://www.googleapis.com/auth/drive.readonly, which login requests; " +
			"an External app's consent screen built from it will refuse that mode\n" +
			"README does not document: delete_tab\n" + unrun, 1},
		{"a table row and a scope in a fenced example", func(s string) string {
			return s + "\n```text\n| `ghost_tool` | x |\n.../auth/drive.file\n```\n"
		}, unrun, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, body := range files {
				if name == "README.md" {
					body = tc.readme(body)
				}
				put(t, dir, name, body)
			}
			stalenessGit(t, dir, "init", "-q", "-b", "main")
			stalenessGit(t, dir, "add", "-A")
			stalenessGit(t, dir, "commit", "-q", "-m", "first")
			t.Chdir(dir)
			in, err := readStaleness(dump)
			if err != nil {
				t.Fatal(err)
			}
			err = stalenessCheck(in)
			if err == nil || err.Error() != tc.err || exitCode(err) != tc.code {
				t.Errorf("staleness() = %v (exit %d); want %q (exit %d)", err, exitCode(err), tc.err, tc.code)
			}
		})
	}
}

// stalenessDump is the surface the binary dumps, made in process.
func stalenessDump(t *testing.T) []byte {
	t.Helper()
	var dump bytes.Buffer
	srv := server.New(server.Deps{Config: tools.FullSurface(config.Config{}), Version: "test"})
	if err := server.DumpSchemas(context.Background(), srv, nil, &dump, "test"); err != nil {
		t.Fatal(err)
	}
	return dump.Bytes()
}

// A finding of the CHANGELOG rule fails the gate beside the others.
func TestStalenessReportsTheChangelogRule(t *testing.T) {
	var d schemaDump
	if err := json.Unmarshal(stalenessDump(t), &d); err != nil {
		t.Fatal(err)
	}
	in := stalenessInputs{changelog: []string{clEmpty}, readme: string(repoFile(t, "README.md")),
		conf: string(repoFile(t, configDocPath))}
	for _, tool := range d.Tools {
		in.registered = append(in.registered, tool.Name)
	}
	if err := stalenessCheck(in); err == nil || err.Error() != clEmpty || exitCode(err) != 1 {
		t.Errorf("stalenessCheck() = %v, want %q (exit 1)", err, clEmpty)
	}
}

// The newest release is the highest version by semver, a prerelease
// included, and a tag that is not a version is no release.
func TestStalenessNewestRelease(t *testing.T) {
	for want, tags := range map[string][]string{
		"2.0.0-rc.1":  {"v1.9.0", "v2.0.0-rc.1"},
		"2.0.0":       {"v2.0.0-rc.1", "v2.0.0", "v1.10.0"},
		"1.10.0":      {"v1.9.0", "v1.10.0", "latest", "v11", "2.0.0"},
		"2.0.0-rc.10": {"v2.0.0-rc.2", "v2.0.0-rc.10"},
		// A prerelease identifier may hold a hyphen (https://semver.org/#spec-item-9).
		"2.0.0-rc-1": {"v1.9.0", "v2.0.0-rc-1"},
		"":           {"latest"},
	} {
		if got := stalenessNewestRelease(tags); got != want {
			t.Errorf("stalenessNewestRelease(%q) = %q, want %q", tags, got, want)
		}
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
		// An example in a fence documents nothing, and names nothing the code
		// must read.
		{"a variable named only in an example", "`GDOCS_HTTP_TIMEOUT`", "`the timeout`\n\n```sh\nGDOCS_HTTP_TIMEOUT=30s GDOCS_RETRIES=3 google-docs-mcp\n```\n",
			[]string{"docs/configuration.md does not document GDOCS_HTTP_TIMEOUT"}},
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
