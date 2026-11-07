package main

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
)

// The workflows the pins and parity rows edit, named once.
const (
	fxCI      = workflowDir + "/" + parityCI
	fxRelease = workflowDir + "/" + parityReleaseWorkflow
	fxCodeQL  = workflowDir + "/codeql.yml"
	fxPublish = workflowDir + "/publish-mcp.yml"
)

// parityFiles are the files parity reads, by the path a row edits.
var parityFiles = []string{
	fxMakefile, parityGoreleaser, pinsPreCommit, fxCI, fxRelease, fxPublish, fxCodeQL, workflowDir + "/assign-issues.yml",
}

// parityRepoRoot is the repository, made absolute as the package loads,
// before a test can change directory.
var parityRepoRoot = func() string {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		panic(err)
	}
	return root
}()

// parityRepo is the repository's own parity inputs, as text.
func parityRepo(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{}
	for _, f := range parityFiles {
		data, err := os.ReadFile(filepath.Join(parityRepoRoot, filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		files[f] = string(data)
	}
	return files
}

// parityRepoTags is what the tree's //go:build lines need, read once: every
// row reads the same tree.
var parityRepoTags = sync.OnceValues(func() ([]parityTagNeed, error) {
	files, err := docsFiles(parityRepoRoot)
	if err != nil {
		return nil, err
	}
	return parityTreeTags(parityRepoRoot, files)
})

// parityRun runs the gate over files, with the tree's tags and those of
// the extra Go files, and returns its problems, sorted.
func parityRun(t *testing.T, files, extraGo map[string]string) []string {
	t.Helper()
	r := parityCheck(parityInputsOf(t, files, extraGo), commands)
	sort.Strings(r.problems)
	return r.problems
}

// parityInputsOf is the gate's inputs read from files, with the tree's
// tags and those of the extra Go files.
func parityInputsOf(t *testing.T, files, extraGo map[string]string) parityInputs {
	t.Helper()
	tree, err := parityRepoTags()
	if err != nil {
		t.Fatal(err)
	}
	needs := slices.Clone(tree)
	if extraGo != nil {
		more, err := parityTreeTags(docsTree(t, extraGo), slices.Sorted(maps.Keys(extraGo)))
		if err != nil {
			t.Fatal(err)
		}
		needs = append(needs, more...)
	}
	var flows []workflowFile
	for _, f := range parityFiles {
		if text, ok := files[f]; ok && strings.HasPrefix(f, workflowDir) {
			flows = append(flows, parseWorkflow(f, text))
		}
	}
	return parityInputs{mk: parseMakefile(files[fxMakefile]), flows: flows,
		goreleaser: parseGoreleaser(files[parityGoreleaser]), hooks: parsePreCommit(files[pinsPreCommit]), needs: needs,
		prereqOnly: parityPrereqOnly}
}

type parityCase struct {
	name  string
	edits []docsEdit
	// extraGo are Go files whose //go:build lines join the tree's.
	extraGo map[string]string
	want    []string
}

func checkParityCases(t *testing.T, cases []parityCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := parityRun(t, applyEdits(t, parityRepo(t), tc.edits...), tc.extraGo)
			if !slices.Equal(got, tc.want) {
				t.Errorf("problems:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}
}

// dropCheck removes one prerequisite from the Makefile's `check:` line.
// Editing the whole file by substring would hit the target's own
// definition first and leave `check:` untouched.
func dropCheck(t *testing.T, files map[string]string, target string) {
	t.Helper()
	for _, line := range strings.Split(files[fxMakefile], "\n") {
		if !strings.HasPrefix(line, "check:") {
			continue
		}
		fields := strings.Fields(line)
		kept := slices.DeleteFunc(slices.Clone(fields), func(f string) bool { return f == target })
		if len(kept) == len(fields) {
			t.Fatalf("`check:` does not list %q, so removing it proves nothing", target)
		}
		files[fxMakefile] = strings.Replace(files[fxMakefile], line, strings.Join(kept, " "), 1)
		return
	}
	t.Fatal("no `check:` line in the Makefile")
}

// TestParityGate is the gate run against the real files, so a drift
// introduced later fails here as well as in CI, and the failures below
// prove more than that it fails on everything.
func TestParityGate(t *testing.T) {
	if got := parityRun(t, parityRepo(t), nil); got != nil {
		t.Errorf("the repository as it stands:\n  %s", strings.Join(got, "\n  "))
	}
	needs, err := parityRepoTags()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, n := range needs {
		got = append(got, n.file+": "+n.String())
	}
	want := []string{
		"internal/evals/claude_test.go: -tags=evals",
		"internal/gapi/preview_integration_test.go: -tags=integration",
		"internal/livecheck/comments_test.go: -tags=live",
	}
	if !slices.Equal(got, want) {
		t.Errorf("the tree's tags = %q, want %q", got, want)
	}
	t.Chdir("../..")
	var out strings.Builder
	if err := parity(&out, nil); err != nil {
		t.Errorf("parity = %v\n%s", err, out.String())
	}
}

// TestParityCatchesDivergence is the point of the gate: the two lists
// live in different files, and whichever one you are editing is the one
// you remember. Each row is a drift, named.
func TestParityCatchesDivergence(t *testing.T) {
	const tagged = "internal/gapi/preview_integration_test.go builds only with -tags=integration (//go:build integration) and "
	const gitleaks = "`make gitleaks` runs \"go run github.com/zricethezav/gitleaks/v8@v8.30.1 git --config .gitleaks.toml --redact --no-banner .\""
	stale := "      - run: go run ./scripts/gates staleness ./google-docs-mcp\n"
	for _, tc := range []struct {
		name   string
		remove string
		edits  []docsEdit
		want   []string
	}{
		{name: "CI does not vet the build-tagged code", edits: []docsEdit{
			replace(fxCI, "      - run: go vet -tags=integration ./...\n", ""),
		}, want: []string{
			"`make vet` runs \"go vet -tags=integration ./...\" and ci.yml never does",
			tagged + "ci.yml vets no pass that builds it; it compiles only on a maintainer's machine",
		}},
		{name: "make check does not vet the build-tagged code", edits: []docsEdit{
			replace(fxMakefile, "\t$(GO) vet -tags=live ./...\n", ""),
		}, want: []string{
			".github/workflows/ci.yml:79 runs `go vet -tags=live ./...` and `make check` does not",
			"internal/livecheck/comments_test.go builds only with -tags=live (//go:build live) and `make check` vets no pass " +
				"that builds it; a contributor cannot see it break",
		}},
		{name: "make check does not scan for vulnerabilities", remove: "vuln", want: []string{
			".github/workflows/ci.yml:86 runs `go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...` and `make check` does not",
		}},
		{name: "make check does not check the licenses", remove: "licenses", want: []string{
			".github/workflows/ci.yml:88 runs `go run github.com/google/go-licenses@v1.6.0 check ./... " +
				"--allowed_licenses=Apache-2.0,BSD-2-Clause,BSD-3-Clause,MIT,ISC` and `make check` does not",
		}},
		{name: "a gate runs in CI and not in make check", remove: "staleness", want: []string{
			"the staleness gate runs in make check by the dispatch, and no target `make check` runs calls it",
		}},
		{name: "a gate runs in make check and not in CI", edits: []docsEdit{replace(fxCI, stale, "      - run: true\n")},
			want: []string{"the staleness gate runs in make check by the dispatch, and ci.yml never calls it"}},
		{name: "a gate nothing runs", remove: "staleness", edits: []docsEdit{replace(fxCI, stale, "      - run: true\n")},
			want: []string{
				"the staleness gate runs in make check by the dispatch, and ci.yml never calls it",
				"the staleness gate runs in make check by the dispatch, and no target `make check` runs calls it",
			}},
		{name: "the linter's action gone from CI", edits: []docsEdit{
			replace(fxCI, "      - uses: golangci/golangci-lint-action@", "      - uses: some/other-action@"),
		}, want: []string{
			"`make lint` runs \"go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run\", " +
				"and ci.yml neither runs it nor uses golangci/golangci-lint-action, which stands in for it",
		}},
		{name: "a check only CI runs", edits: []docsEdit{
			replace(fxCI, "      - name: licenses\n", "      - run: go run honnef.co/go/tools/cmd/staticcheck@v0.6.1 ./...\n      - name: licenses\n"),
		}, want: []string{".github/workflows/ci.yml:87 runs `go run honnef.co/go/tools/cmd/staticcheck@v0.6.1 ./...` and `make check` does not"}},
		{name: "CI runs a tool at another version", edits: []docsEdit{
			replace(fxCI, "govulncheck@v1.7.0", "govulncheck@v1.7.1"),
		}, want: []string{
			".github/workflows/ci.yml:86 runs `go run golang.org/x/vuln/cmd/govulncheck@v1.7.1 ./...` and `make check` does not",
			"`make vuln` runs \"go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...\" and ci.yml never does",
		}},
		{name: "make check formats with another command", edits: []docsEdit{
			replace(fxMakefile, `[ -z "$$out" ] || { echo "$$out"; exit 1; }`, `test -z "$$out"`),
		}, want: []string{"`make fmt` runs \"out=$(gofmt -l .); test -z \\\"$out\\\"\" and ci.yml never does"}},
		{name: "release.yml writes no release notes", edits: []docsEdit{
			replace(fxRelease, `run: go run ./scripts/gates release-notes "${REF_NAME}" > "${RUNNER_TEMP}/release-notes.md"`, "run: true"),
		}, want: []string{
			"the release-notes gate runs in release by the dispatch, and neither .goreleaser.yaml nor a workflow but ci.yml calls it",
		}},
		{name: "the registry entry is never written", edits: []docsEdit{
			replace(fxPublish, `go run ./scripts/gates registry-publish "${TAG}" dist/checksums.txt > server.json`, "true"),
		}, want: []string{
			"the registry-publish gate runs in release by the dispatch, and neither .goreleaser.yaml nor a workflow but ci.yml calls it",
		}},
		{name: "goreleaser packs no bundle", edits: []docsEdit{
			replace(parityGoreleaser, "post: go run ./scripts/gates mcpb-pack dist", "post: true"),
		}, want: []string{
			"the mcpb-pack gate runs in release by the dispatch, and neither .goreleaser.yaml nor a workflow but ci.yml calls it",
		}},
		// The footer is release-note prose, which the release does not run.
		{name: "goreleaser names the bundle's gate only in its footer", edits: []docsEdit{
			replace(parityGoreleaser, "post: go run ./scripts/gates mcpb-pack dist", "post: true"),
			replace(parityGoreleaser, "  footer: |\n", "  footer: |\n    go run ./scripts/gates mcpb-pack dist 1.0.0 dist/x.mcpb\n"),
		}, want: []string{
			"the mcpb-pack gate runs in release by the dispatch, and neither .goreleaser.yaml nor a workflow but ci.yml calls it",
		}},
		{name: "goreleaser runs the bundle's gate through an alias", edits: []docsEdit{
			replace(parityGoreleaser, "post: go run ./scripts/gates mcpb-pack dist {{ .Version }} dist/google-docs-mcp_{{ .Version }}.mcpb", "post: *pack"),
		}, want: []string{
			".goreleaser.yaml:48: post: an anchor, an alias or a tag; write the value out",
			"the mcpb-pack gate runs in release by the dispatch, and neither .goreleaser.yaml nor a workflow but ci.yml calls it",
		}},
		{name: "a release gate in make check", edits: []docsEdit{
			replace(fxMakefile, "\t@$(GO) run ./scripts/gates docs\n", "\t@$(GO) run ./scripts/gates docs\n\t$(GO) run ./scripts/gates release-notes 1.0.0\n"),
		}, want: []string{"the release-notes gate runs in release by the dispatch, and `make check` runs it"}},
		{name: "a manual gate nothing runs", edits: []docsEdit{
			replace(fxMakefile, "\t@$(GO) run ./scripts/gates api-diff\n", "\t@true\n"),
		}, want: []string{
			"the api-diff gate is manual by the dispatch, and no Makefile target or hook runs it: " +
				"a manual gate nobody can find is a gate that never fires",
		}},
		// A pre-commit hook is a place a manual gate is run from.
		{name: "a manual gate a pre-commit hook runs", edits: []docsEdit{
			replace(fxMakefile, "\t@$(GO) run ./scripts/gates api-diff\n", "\t@true\n"),
			replace(pinsPreCommit, "        entry: go vet ./...\n", "        entry: go run ./scripts/gates api-diff\n"),
		}},
		// A hook runs its entry, and its name is a label.
		{name: "a manual gate named only in a hook's name", edits: []docsEdit{
			replace(fxMakefile, "\t@$(GO) run ./scripts/gates api-diff\n", "\t@true\n"),
			replace(pinsPreCommit, "        name: go vet\n", "        name: go run ./scripts/gates api-diff\n"),
		}, want: []string{
			"the api-diff gate is manual by the dispatch, and no Makefile target or hook runs it: " +
				"a manual gate nobody can find is a gate that never fires",
		}},
		// An excuse is for a command ci.yml runs.
		{name: "a CI-only excuse for a command CI no longer runs", edits: []docsEdit{
			replace(fxCI, "      - run: go build ./...\n", ""),
			replace(fxCI, "      - run: CGO_ENABLED=0 go build -trimpath -o google-docs-mcp ./cmd/google-docs-mcp\n", ""),
		}, want: []string{"go build is excused as CI-only, and ci.yml runs no go build; drop the excuse"}},
		{name: "a manual gate in make check", edits: []docsEdit{
			replace(fxMakefile, "\t@$(GO) run ./scripts/gates docs\n", "\t@$(GO) run ./scripts/gates docs\n\t$(GO) run ./scripts/gates api-diff\n"),
		}, want: []string{"the api-diff gate is manual by the dispatch, and `make check` runs it; record it as inCheck"}},
		{name: "a gate the dispatch does not have", edits: []docsEdit{
			replace(fxMakefile, "\t@$(GO) run ./scripts/gates docs\n", "\t@$(GO) run ./scripts/gates docs\n\t$(GO) run ./scripts/gates ghost\n"),
		}, want: []string{"something runs `gates ghost`, which the dispatch does not have"}},
		// Every workflow but ci.yml is read for gate calls, not a list of them.
		{name: "a gate the dispatch does not have, in another workflow", edits: []docsEdit{
			replace(fxCodeQL, "          languages: go\n", "          languages: go\n      - run: go run ./scripts/gates ghost\n"),
		}, want: []string{"something runs `gates ghost`, which the dispatch does not have"}},
		{name: "a prerequisite a second check rule adds", edits: []docsEdit{
			replace(fxMakefile, ".PHONY: clean\n", "check: ghostcheck\n\n.PHONY: clean\n"),
		}, want: []string{"check: names ghostcheck, which the Makefile does not define"}},
		{name: "the secret scan gone from CI", edits: []docsEdit{replace(fxCI, "run: make gitleaks\n", "run: true\n")},
			want: []string{gitleaks + " and ci.yml never does"}},
		{name: "the secret scan gone from make check", remove: "gitleaks", want: []string{
			".github/workflows/ci.yml:112 runs `go run github.com/zricethezav/gitleaks/v8@v8.30.1 git --config .gitleaks.toml " +
				"--redact --no-banner .` and `make check` does not",
		}},
		// A variable set on the line, or a flag, changes what the recipe runs.
		{name: "CI makes the secret scan with its version set", edits: []docsEdit{
			replace(fxCI, "run: make gitleaks\n", "run: make gitleaks GITLEAKS=github.com/zricethezav/gitleaks/v8@v8.30.1\n"),
		}, want: []string{gitleaks + " and ci.yml never does"}},
		// A redirection changes where the output goes, not what runs.
		{name: "CI makes the secret scan with its output redirected", edits: []docsEdit{
			replace(fxCI, "run: make gitleaks\n", "run: make gitleaks 2>&1 > gitleaks.log\n"),
		}},
		// make runs what a target depends on first.
		{name: "CI makes a target whose prerequisite runs a check make check does not", edits: []docsEdit{
			replace(fxMakefile, ".PHONY: check\n", "extra: static\nstatic:\n\t$(GO) run honnef.co/go/tools/cmd/staticcheck@v0.6.1 ./...\n\n.PHONY: check\n"),
			replace(fxCI, "run: make gitleaks\n", "run: make gitleaks extra\n"),
		}, want: []string{".github/workflows/ci.yml:112 runs `go run honnef.co/go/tools/cmd/staticcheck@v0.6.1 ./...` and `make check` does not"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			files := applyEdits(t, parityRepo(t), tc.edits...)
			if tc.remove != "" {
				dropCheck(t, files, tc.remove)
			}
			if got := parityRun(t, files, nil); !slices.Equal(got, tc.want) {
				t.Errorf("problems:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}
}

// make carries on past a failure that a - prefix or .IGNORE tells it to
// ignore, so a command `make check` runs that way passes where CI, which
// runs it bare, fails. The command is still CI's in the same words.
func TestParityRefusesACommandWhoseFailureMakeIgnores(t *testing.T) {
	const vuln = "\t$(GO) run $(GOVULNCHECK) ./...\n"
	checkParityCases(t, []parityCase{
		{name: "a - before a check's command", edits: []docsEdit{replace(fxMakefile, vuln, "\t-$(GO) run $(GOVULNCHECK) ./...\n")},
			want: []string{"`make vuln` runs \"go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...\" behind a - prefix: " +
				"make ignores this command's failure"}},
		{name: "a - among the other prefixes, before a gate call", edits: []docsEdit{
			replace(fxMakefile, "\t@$(GO) run ./scripts/gates docs\n", "\t@ -$(GO) run ./scripts/gates docs\n"),
		}, want: []string{"`make docs` runs \"go run ./scripts/gates docs\" behind a - prefix: make ignores this command's failure"}},
		{name: "a - before a command of a prerequisite check reaches", edits: []docsEdit{
			replace(fxMakefile, "\t$(GO) test -race ", "\t-$(GO) test -race "),
		}, want: []string{"`make test` runs \"go test -race -coverpkg=./internal/... -coverprofile=cov.out -covermode=atomic ./...\" " +
			"behind a - prefix: make ignores this command's failure"}},
		{name: "a - before a command make check does not run", edits: []docsEdit{
			replace(fxMakefile, "\t$(GO) test -run XXX ", "\t-$(GO) test -run XXX "),
		}},
		{name: ".IGNORE naming a check target", edits: []docsEdit{
			replace(fxMakefile, ".PHONY: check\n", ".IGNORE: bench vuln\n\n.PHONY: check\n"),
		}, want: []string{".IGNORE names vuln: make ignores the failure of every command `make vuln` runs"}},
		{name: ".IGNORE with no prerequisite", edits: []docsEdit{replace(fxMakefile, ".PHONY: check\n", ".IGNORE:\n\n.PHONY: check\n")},
			want: []string{".IGNORE names no target: make ignores the failure of every command"}},
		{name: ".IGNORE naming a target make check does not run", edits: []docsEdit{
			replace(fxMakefile, ".PHONY: check\n", ".IGNORE: bench\n\n.PHONY: check\n"),
		}},
	})
}

// A pull-request gate runs in ci.yml's pr job and stays out of `make
// check`, which has no merge-base to measure from.
func TestParityHoldsThePullRequestGates(t *testing.T) {
	ack := `run: go run ./scripts/gates schema-ack "$PR_BASE_REF" "$PR_HEAD"`
	const gone = "the schema-ack gate runs on pull requests by the dispatch, and ci.yml's pr job never calls it"
	checkParityCases(t, []parityCase{
		{"the pull-request gate is gone from CI", []docsEdit{replace(fxCI, ack, "run: true")}, nil, []string{gone}},
		{"the pull-request gate commented out", []docsEdit{replace(fxCI, "        "+ack, "        # "+ack)}, nil, []string{gone}},
		{"the pull-request gate moved out of the pr job", []docsEdit{
			replace(fxCI, ack, "run: true"),
			replace(fxCI, "run: go run ./scripts/gates docs\n", "run: go run ./scripts/gates docs\n      - run: go run ./scripts/gates schema-ack origin/main HEAD\n"),
		}, nil, []string{gone}},
		{"the pull-request gate in make check", []docsEdit{
			replace(fxMakefile, "check: fmt", "check: schema-ack fmt"),
		}, nil, []string{
			"the schema-ack gate runs on pull requests by the dispatch, and `make check` runs it, where there is no merge-base",
		}},
	})
}

// Commenting a step out, naming a gate in a step's name or env, or quoting
// it in an echo is not the same as running it; a flag before the gates
// package does not hide one.
func TestParityReadsWhatRuns(t *testing.T) {
	const notInCI = "the staleness gate runs in make check by the dispatch, and ci.yml never calls it"
	stale := "      - run: go run ./scripts/gates staleness ./google-docs-mcp\n"
	checkParityCases(t, []parityCase{
		{"a gate's step commented out", []docsEdit{
			replace(fxCI, stale, "      # - run: go run ./scripts/gates staleness ./google-docs-mcp\n"),
		}, nil, []string{notInCI}},
		{"a gate named in a step's name", []docsEdit{
			replace(fxCI, stale, "      - name: go run ./scripts/gates staleness ./google-docs-mcp\n        run: true\n"),
		}, nil, []string{notInCI}},
		{"a gate named in a step's env", []docsEdit{
			replace(fxCI, stale, "      - name: check\n        run: true\n        env:\n          NOTE: go run ./scripts/gates staleness\n"),
		}, nil, []string{notInCI}},
		{"a gate quoted in an echo", []docsEdit{
			replace(fxCI, stale, "      - run: echo \"go run ./scripts/gates staleness ./google-docs-mcp\"\n"),
		}, nil, []string{notInCI}},
		{"a gate inside a run block, after another command", []docsEdit{
			replace(fxCI, stale, "      - run: |\n          set -e\n          go run ./scripts/gates staleness ./google-docs-mcp\n"),
		}, nil, nil},
		{"a gate named by the step after a run block", []docsEdit{
			replace(fxCI, stale, "      - run: |\n          set -e\n      - name: go run ./scripts/gates staleness ./google-docs-mcp\n        run: true\n"),
		}, nil, []string{notInCI}},
		{"a gate run past its build flags, and in a command substitution", []docsEdit{
			replace(fxCI, "run: go run ./scripts/gates staleness", "run: go run -trimpath ./scripts/gates staleness"),
			replace(fxRelease, `run: go run ./scripts/gates release-notes "${REF_NAME}" > "${RUNNER_TEMP}/release-notes.md"`,
				`run: NOTES="$(go run ./scripts/gates release-notes "${REF_NAME}")"`),
		}, nil, nil},
		{"a target named only in a Makefile comment", []docsEdit{
			replace(fxMakefile, "check: fmt", "# check: vuln\ncheck: fmt"),
			replace(fxMakefile, " vuln licenses", " licenses"),
		}, nil, []string{
			".github/workflows/ci.yml:86 runs `go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...` and `make check` does not",
		}},
		{"a recipe spaced and quoted otherwise than CI's line", []docsEdit{
			replace(fxMakefile, "\t$(GO) vet ./...\n", "\t$(GO)  vet './...'\n"),
		}, nil, nil},
		{"a recipe line's prefixes, which are not part of the command", []docsEdit{
			replace(fxMakefile, "\t$(GO) vet -tags=integration ./...\n", "\t@+$(GO) vet -tags=integration ./...\n"),
		}, nil, nil},
		{"a Makefile line the reader does not read", []docsEdit{
			replace(fxMakefile, "GO       ?= go\n", "ifndef GO\nGO       ?= go\nendif\n"),
		}, nil, []string{
			`Makefile:3: "ifndef GO" is a directive or an assignment, which this reader does not read`,
			`Makefile:5: "endif" is a directive or an assignment, which this reader does not read`,
		}},
		{"a step the workflow reader does not read", []docsEdit{
			replace(fxCI, stale, "      - {name: staleness, run: go run ./scripts/gates staleness ./google-docs-mcp}\n"),
		}, nil, []string{
			".github/workflows/ci.yml:152: an item of steps: is not a block mapping; write it one key a line",
			notInCI,
		}},
		{"a hook the reader does not read", []docsEdit{
			replace(pinsPreCommit, "        entry: go vet ./...\n", "        entry: >-\n          go vet ./...\n"),
		}, nil, []string{".pre-commit-config.yaml:19: entry: a block scalar; " +
			"write the command on the key's line, unquoted or quoted with no escape"}},
		{"a gate named only in a recipe's comment", []docsEdit{
			replace(fxMakefile, "\t@$(GO) run ./scripts/gates docs\n", "\t@$(GO) run ./scripts/gates pins # was: $(GO) run ./scripts/gates docs\n"),
		}, nil, []string{"the docs gate runs in make check by the dispatch, and no target `make check` runs calls it"}},
		{"a command beside a gate call on one recipe line", []docsEdit{
			replace(fxMakefile, "\t@$(GO) run ./scripts/gates docs\n", "\t@$(GO) run ./scripts/gates docs && $(GO) vet -tags=foo ./...\n"),
		}, nil, []string{"`make docs` runs \"go vet -tags=foo ./...\" and ci.yml never does"}},
		{"a command beside a gate call on one CI line", []docsEdit{
			replace(fxCI, "run: go run ./scripts/gates docs\n", "run: go run ./scripts/gates docs && go vet ./...\n"),
		}, nil, nil},
	})
}

// A gate both sides call takes the same arguments on both, the Makefile's
// variables expanded. An omitted [BIN] is the binary the gate defaults
// to, and no other omitted argument is.
func TestParityHoldsAGateCallsArguments(t *testing.T) {
	checkParityCases(t, []parityCase{
		{"a coverage floor the Makefile sets apart from CI's", []docsEdit{
			replace(fxMakefile, "COVER_MIN ?= 80", "COVER_MIN ?= 85"),
		}, nil, []string{
			".github/workflows/ci.yml:60 calls the coverage gate with `cov.out 80` and `make check` never does",
			"`make cover` calls the coverage gate with `cov.out 85` and ci.yml never does",
		}},
		// Only the binary compares as a path; every other argument is a word.
		{"a profile written another way", []docsEdit{
			replace(fxMakefile, "coverage cov.out $(COVER_MIN)", "coverage ./cov.out $(COVER_MIN)"),
		}, nil, []string{
			".github/workflows/ci.yml:60 calls the coverage gate with `cov.out 80` and `make check` never does",
			"`make cover` calls the coverage gate with `./cov.out 80` and ci.yml never does",
		}},
		{"a binary other than the one the gate defaults to", []docsEdit{
			replace(fxMakefile, "\t@$(GO) run ./scripts/gates smoke $(BIN)\n", "\t@$(GO) run ./scripts/gates smoke ./old/google-docs-mcp\n"),
		}, nil, []string{
			".github/workflows/ci.yml:150 calls the smoke gate with `./google-docs-mcp` and `make check` never does",
			"`make smoke` calls the smoke gate with `./old/google-docs-mcp` and ci.yml never does",
		}},
		{"a gate call whose output is redirected", []docsEdit{
			replace(fxCI, "run: go run ./scripts/gates smoke ./google-docs-mcp\n",
				"run: go run ./scripts/gates smoke ./google-docs-mcp 2>&1 | tee smoke.log\n"),
		}, nil, nil},
		{"the default binary left out in CI", []docsEdit{
			replace(fxCI, "run: go run ./scripts/gates smoke ./google-docs-mcp\n", "run: go run ./scripts/gates smoke\n"),
		}, nil, nil},
		{"an argument CI gives a gate that make check does not", []docsEdit{
			replace(fxCI, "run: go run ./scripts/gates leaks\n", "run: go run ./scripts/gates leaks history\n"),
		}, nil, []string{
			".github/workflows/ci.yml:154 calls the leaks gate with `history` and `make check` never does",
			"`make leaks` calls the leaks gate with no arguments and ci.yml never does",
		}},
	})
}

// TestParityRefusesToCompareNothing keeps the check from passing because
// it read the wrong file: two empty lists are equal, so each count has a
// floor.
func TestParityRefusesToCompareNothing(t *testing.T) {
	r := parityCheck(parityInputs{mk: parseMakefile("check:\n"), flows: []workflowFile{parseWorkflow(fxCI, "jobs:\n")},
		prereqOnly: parityPrereqOnly}, map[string]command{})
	want := []string{
		"go build is excused as CI-only, and ci.yml runs no go build; drop the excuse",
		"build is excused as a prerequisite and the Makefile does not define it; drop the excuse",
		"read 0 check: prerequisites, want at least 15: the reader is not seeing its input",
		"read 0 ci.yml command lines, want at least 20: the reader is not seeing its input",
		"read 0 gates in check, want at least 10: the reader is not seeing its input",
		"read 0 distinct gate calls, want at least 15: the reader is not seeing its input",
		"read 0 //go:build lines that need -tags, want at least 3: the reader is not seeing its input",
	}
	if !slices.Equal(r.problems, want) {
		t.Errorf("problems:\n  %s\nwant:\n  %s", strings.Join(r.problems, "\n  "), strings.Join(want, "\n  "))
	}
}

// A prerequisite excuse that outlived its target is refused. The excuse
// goes in through a copy of the table, so no test writes package state.
func TestParityRefusesAStaleExcuse(t *testing.T) {
	in := parityInputsOf(t, parityRepo(t), nil)
	in.prereqOnly = maps.Clone(parityPrereqOnly)
	in.prereqOnly["gone"] = "a target that was renamed"
	got := parityCheck(in, commands).problems
	want := []string{"gone is excused as a prerequisite and the Makefile does not define it; drop the excuse"}
	if !slices.Equal(got, want) {
		t.Errorf("problems = %q, want %q", got, want)
	}
}

// Every excuse carries a reason, the same rule the coverage floor and the
// leak allowlist keep.
func TestParityExcusesHaveReasons(t *testing.T) {
	for _, excuses := range []map[string]string{parityPrereqOnly, parityCIOnly} {
		for name, reason := range excuses {
			if strings.TrimSpace(reason) == "" {
				t.Errorf("%q is excused from parity with no reason", name)
			}
		}
	}
}

func TestParityHoldsTheTreesTagsToAVetPass(t *testing.T) {
	const vet = "\t$(GO) vet -tags=integration ./...\n"
	const ciVet = "      - run: go vet -tags=integration ./...\n"
	checkParityCases(t, []parityCase{
		{"a file an OS tag and a custom tag build, which no pass vets", nil,
			map[string]string{"internal/e2e/e2e_test.go": "//go:build e2e && linux\n\npackage e2e\n"},
			[]string{
				"internal/e2e/e2e_test.go builds only with -tags=e2e (//go:build e2e && linux) and `make check` vets no pass that builds it; a contributor cannot see it break",
				"internal/e2e/e2e_test.go builds only with -tags=e2e (//go:build e2e && linux) and ci.yml vets no pass that builds it; it compiles only on a maintainer's machine",
			}},
		{"two tags set by one pass, the flag anywhere among vet's arguments", []docsEdit{
			replace(fxMakefile, vet, vet+"\t$(GO) vet -v -tags=soak,slow ./...\n"),
			replace(fxCI, ciVet, ciVet+"      - run: go vet -v -tags=soak,slow ./...\n"),
		}, map[string]string{"internal/soak/soak_test.go": "//go:build soak && slow\n\npackage soak\n"}, nil},
		{"a file either of two tags builds, with one of them vetted", nil,
			map[string]string{"internal/probe/probe.go": "//go:build integration || nightly\n\npackage probe\n"}, nil},
		{"a test that turns vet off, which is not a vet pass", []docsEdit{
			replace(fxMakefile, vet, "\t$(GO) test -vet=off -tags=integration ./...\n"),
		}, nil, []string{
			".github/workflows/ci.yml:78 runs `go vet -tags=integration ./...` and `make check` does not",
			"`make vet` runs \"go test -vet=off -tags=integration ./...\" and ci.yml never does",
			"internal/gapi/preview_integration_test.go builds only with -tags=integration (//go:build integration) and `make check` vets no pass that builds it; a contributor cannot see it break",
		}},
		// A pass compiles a file when the file's line holds with exactly the
		// pass's tags: one more tag can keep it out.
		{"a file another tag keeps out, vetted only by a pass with that tag", []docsEdit{
			replace(fxMakefile, vet, "\t$(GO) vet -tags=integration,evals ./...\n"),
		}, map[string]string{"internal/probe/probe.go": "//go:build integration && !evals\n\npackage probe\n"}, []string{
			".github/workflows/ci.yml:78 runs `go vet -tags=integration ./...` and `make check` does not",
			"`make vet` runs \"go vet -tags=integration,evals ./...\" and ci.yml never does",
			"internal/probe/probe.go builds only with -tags=integration (//go:build integration && !evals) and `make check` vets no pass that builds it; a contributor cannot see it break",
		}},
	})
}

// The tags each file needs: none where the go command sets them, and each
// smallest custom set otherwise, once per //go:build expression.
func TestParityTreeTags(t *testing.T) {
	files := map[string]string{
		"a/live.go":         "//go:build live && unix\n\npackage a\n",
		"a/other.go":        "//go:build !windows\n\npackage a\n",
		"a/windows.go":      "//go:build windows\n\npackage a\n",
		"a/solaris.go":      "//go:build solaris || illumos || aix\n\npackage a\n",
		"a/e2e.go":          "//go:build e2e && !experimental\n\npackage a\n",
		"a/race.go":         "//go:build evals || !race\n\npackage a\n",
		"a/arch.go":         "//go:build amd64.v2 && go1.22\n\npackage a\n",
		"a/exp.go":          "//go:build goexperiment.synctest\n\npackage a\n",
		"a/instrumented.go": "//go:build msan && asan\n\npackage a\n",
		"a/racy.go":         "//go:build race\n\npackage a\n",
		"a/mixed.go":        "//go:build (a && b) || c\n\npackage a\n",
		"a/live2.go":        "//go:build live\n\npackage a\n",
		"a/ignored.go":      "//go:build ignore\n\npackage a\n",
		"a/both.go":         "//go:build integration && slow\n\npackage a\n",
		"a/either.go":       "//go:build alpha || beta\n\npackage a\n",
		"a/plain.go":        "package a\n",
		"top.go":            "//go:build top\n\npackage a\n",
		"a/notes.txt":       "//go:build prose\n",
		"a/testdata/x.go":   "//go:build fixture\n\npackage x\n",
		".hidden/x.go":      "//go:build hidden\n\npackage x\n",
	}
	needs, err := parityTreeTags(docsTree(t, files), slices.Sorted(maps.Keys(files)))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, n := range needs {
		got = append(got, n.file+": "+n.String())
	}
	want := []string{
		"a/both.go: -tags=integration,slow",
		"a/e2e.go: -tags=e2e",
		"a/either.go: -tags=alpha or -tags=beta",
		"a/live.go: -tags=live",
		"a/live2.go: -tags=live",
		"a/mixed.go: -tags=c or -tags=a,b",
		"top.go: -tags=top",
	}
	if !slices.Equal(got, want) {
		t.Errorf("parityTreeTags = %q, want %q", got, want)
	}
}

// The tags are the checkout's as git lists it: a scratch file git ignores
// cannot change the verdict.
func TestParityReadsTheTagsOfTheCheckout(t *testing.T) {
	initRepo(t)
	putAll(t, ".", map[string]string{
		".gitignore":                  "/scratch/\n",
		fxMakefile:                    "check:\n",
		fxCI:                          "jobs:\n",
		parityGoreleaser:              "version: 2\n",
		pinsPreCommit:                 "repos:\n",
		"internal/probe/live_test.go": "//go:build live\n\npackage probe\n",
		"scratch/soak_test.go":        "//go:build soak\n\npackage scratch\n",
	})
	in, err := readParityInputs()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, n := range in.needs {
		got = append(got, n.file+": "+n.String())
	}
	if want := []string{"internal/probe/live_test.go: -tags=live"}; !slices.Equal(got, want) {
		t.Errorf("the checkout's tags = %q, want %q", got, want)
	}
}

func TestParityVetPassesReadTheTagsFlagAnywhere(t *testing.T) {
	got := parityVetPasses([]string{
		"go vet -tags=integration ./...",
		"go vet -v -tags a,b ./... && go test -tags=evals ./...",
		`go vet --tags="x" ./...; go vet ./...`,
		"go vet --tags y ./...",
		"make vet",
		"go test -vet=off -tags=integration ./...",
		"go vet ./... -tags=z",
		"go vet -vettool /usr/local/bin/shadow -tags=sh ./...",
		"go vet -c 2 -printf.funcs Wrapf -tags c1 ./...",
		"go vet -tags=old -tags=new ./...",
	})
	want := [][]string{{"integration"}, {"a", "b"}, {"x"}, {"y"}, {"sh"}, {"c1"}, {"new"}}
	if !slices.EqualFunc(got, want, slices.Equal[[]string]) {
		t.Errorf("parityVetPasses = %q, want %q", got, want)
	}
}
