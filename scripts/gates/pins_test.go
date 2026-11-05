package main

import (
	"io"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The gate itself, run against this repository. It is the command
// `make pins` runs, so a failure here is the failure a maintainer sees.
func TestPinsGate(t *testing.T) {
	if err := pins(io.Discard, nil); err != nil {
		t.Errorf("pins gate: %v", err)
	}
}

func TestPinsShell(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want bool
	}{
		{"workflow level", "on: push\ndefaults:\n  run:\n    shell: bash\n\njobs:\n  a:\n", true},
		{"with a comment inside", "defaults:\n  # why\n  run:\n    shell: bash\n", true},
		{"another key beside shell", "defaults:\n  run:\n    working-directory: .\n    shell: bash\n", true},
		{"comments on both keys", "defaults: # workflow-wide\n  run:\n    shell: bash # for pipefail too\n", true},
		{"a quoted scalar", "defaults:\n  run:\n    shell: \"bash\"\n", true},
		{"four-space indentation", "defaults:\n    run:\n        shell: bash\n", true},
		{"tab indentation", "defaults:\n\trun:\n\t\tshell: bash\n", true},
		{"job level only", "jobs:\n  a:\n    defaults:\n      run:\n        shell: bash\n", false},
		{"absent", "on: push\n\njobs:\n  a:\n    steps:\n      - run: go build ./...\n", false},
		{"a different shell", "defaults:\n  run:\n    shell: pwsh\n", false},
		{"a different shell with a comment", "defaults:\n  run:\n    shell: pwsh # not bash\n", false},
		{"shell outside a run block", "defaults:\n  shell: bash\n", false},
		{"shell as a sibling of run", "defaults:\n  run:\n  shell: bash\n", false},
		{"shell under a key beside run", "defaults:\n  run:\n    working-directory: .\n  other:\n    shell: bash\n", false},
		{"block ends before the shell", "defaults:\n  run:\n    working-directory: .\njobs:\n    shell: bash\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pinsShell(c.yaml); got != c.want {
				t.Errorf("pinsShell = %v, want %v", got, c.want)
			}
		})
	}
}

// The hole the version checks above could never see: an action pinned by
// SHA whose tool is left to float. The two named cases are the two tools
// the release workflow installs.
func TestUnpinnedTools(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantBad bool
	}{
		{
			"cosign with no release input",
			"jobs:\n  a:\n    steps:\n      - name: Install cosign\n        uses: sigstore/cosign-installer@" + fakeSHA + "\n",
			true,
		},
		{
			"cosign pinned",
			"jobs:\n  a:\n    steps:\n      - name: Install cosign\n        uses: sigstore/cosign-installer@" + fakeSHA + "\n        with:\n          cosign-release: v3.1.3\n",
			false,
		},
		{
			"syft with no version input — the same hole one step below",
			"jobs:\n  a:\n    steps:\n      - name: Install syft\n        uses: anchore/sbom-action/download-syft@" + fakeSHA + "\n",
			true,
		},
		{
			"syft pinned",
			"jobs:\n  a:\n    steps:\n      - uses: anchore/sbom-action/download-syft@" + fakeSHA + "\n        with:\n          syft-version: v1.51.1\n",
			false,
		},
		{
			"setup-go pins by reference through the file",
			"jobs:\n  a:\n    steps:\n      - uses: actions/setup-go@" + fakeSHA + "\n        with:\n          go-version-file: go.mod\n",
			false,
		},
		{
			"gitleaks takes its scanner version from the environment",
			"jobs:\n  a:\n    steps:\n      - uses: gitleaks/gitleaks-action@" + fakeSHA + "\n        env:\n          GITLEAKS_VERSION: 8.31.0\n",
			false,
		},
		{
			"a blank line above an unpinned installer lends it no later step's version",
			"jobs:\n  a:\n    steps:\n      - run: echo\n\n      - uses: sigstore/cosign-installer@" + fakeSHA +
				"\n      - uses: anchore/sbom-action/download-syft@" + fakeSHA + "\n        with:\n          cosign-release: v3.1.3\n          syft-version: v1.51.1\n",
			true,
		},
		{
			"an action that installs nothing needs no version",
			"jobs:\n  a:\n    steps:\n      - uses: actions/checkout@" + fakeSHA + "\n",
			false,
		},
		{
			"an unknown action is not quietly trusted",
			"jobs:\n  a:\n    steps:\n      - uses: some-vendor/tool-installer@" + fakeSHA + "\n        with:\n          version: v1.2.3\n",
			true,
		},
		{
			"the next step's pin does not cover this one",
			"jobs:\n  a:\n    steps:\n      - name: Install cosign\n        uses: sigstore/cosign-installer@" + fakeSHA + "\n\n      - uses: anchore/sbom-action/download-syft@" + fakeSHA + "\n        with:\n          syft-version: v1.51.1\n",
			true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, problems := unpinnedTools(workflowFile{name: "test.yml", data: c.yaml})
			if got := len(problems) > 0; got != c.wantBad {
				t.Errorf("found %d problem(s), want bad=%v: %v", len(problems), c.wantBad, problems)
			}
		})
	}
}

// Every action the workflows actually use is classified one way or the
// other. An unclassified action fails the gate, which is the point; this
// says so at the level of the tables rather than the files.
func TestEveryActionIsClassified(t *testing.T) {
	root, err := moduleRoot()
	if err != nil {
		t.Fatalf("module root: %v", err)
	}
	files, err := readWorkflows(root)
	if err != nil {
		t.Fatalf("read workflows: %v", err)
	}
	for _, f := range files {
		for _, m := range usesLine.FindAllStringSubmatch(f.data, -1) {
			action, _, _ := strings.Cut(m[1], "@")
			if strings.HasPrefix(action, "./") || strings.HasPrefix(action, "docker://") {
				continue
			}
			_, installer := installerPins[action]
			_, known := notInstallers[action]
			if !installer && !known {
				t.Errorf("%s: %s is in neither installerPins nor notInstallers", f.name, action)
			}
		}
	}
}

// pinCopy is one copy of a tool's pin: the file that holds it, and a
// pattern whose first group is everything on the line before the version.
type pinCopy struct {
	file    string
	pattern *regexp.Regexp
}

const pinsCI = ".github/workflows/ci.yml"

// The six copies of the three tools the Makefile and CI both run.
var (
	mkGolangci  = pinCopy{"Makefile", regexp.MustCompile(`(?m)^(GOLANGCI_VERSION\s*\?=\s*)\S+`)}
	mkVulncheck = pinCopy{"Makefile", regexp.MustCompile(`(?m)^(GOVULNCHECK_VERSION\s*\?=\s*)\S+`)}
	mkLicenses  = pinCopy{"Makefile", regexp.MustCompile(`(?m)^(GO_LICENSES_VERSION\s*\?=\s*)\S+`)}
	ciGolangci  = pinCopy{pinsCI, regexp.MustCompile(`(?m)^(\s+version:\s*)\S+`)}
	ciVulncheck = pinCopy{pinsCI, regexp.MustCompile(`(golang\.org/x/vuln/cmd/govulncheck@)\S+`)}
	ciLicenses  = pinCopy{pinsCI, regexp.MustCompile(`(github\.com/google/go-licenses@)\S+`)}
)

// set writes version into the one place the copy is, and fails the test
// when the file holds it anywhere else or nowhere.
func (c pinCopy) set(t *testing.T, files map[string]string, version string) {
	t.Helper()
	if n := len(c.pattern.FindAllStringIndex(files[c.file], -1)); n != 1 {
		t.Fatalf("%s matches %s %d times, want once", c.pattern, c.file, n)
	}
	files[c.file] = c.pattern.ReplaceAllString(files[c.file], "${1}"+version)
}

// The Makefile runs three tools CI runs, each at a variable, and the gate
// holds each variable to CI's copy. Every row starts with all six copies
// at v1.0.0.
func TestPinsHoldTheMakefileToCI(t *testing.T) {
	const drift = " in ci.yml; make check runs one and CI the other"
	for _, tc := range []struct {
		name string
		edit func(t *testing.T, files map[string]string)
		want string
	}{
		{"every copy agrees", func(*testing.T, map[string]string) {}, ""},
		{"golangci-lint moved in the Makefile", func(t *testing.T, f map[string]string) { mkGolangci.set(t, f, "v1.0.1") },
			"golangci-lint is v1.0.1 in the Makefile's GOLANGCI_VERSION and v1.0.0" + drift},
		{"golangci-lint moved in CI", func(t *testing.T, f map[string]string) { ciGolangci.set(t, f, "v1.0.1") },
			"golangci-lint is v1.0.0 in the Makefile's GOLANGCI_VERSION and v1.0.1" + drift},
		{"govulncheck moved in the Makefile", func(t *testing.T, f map[string]string) { mkVulncheck.set(t, f, "v1.0.1") },
			"govulncheck is v1.0.1 in the Makefile's GOVULNCHECK_VERSION and v1.0.0" + drift},
		{"govulncheck moved in CI", func(t *testing.T, f map[string]string) { ciVulncheck.set(t, f, "v1.0.1") },
			"govulncheck is v1.0.0 in the Makefile's GOVULNCHECK_VERSION and v1.0.1" + drift},
		{"go-licenses moved in the Makefile", func(t *testing.T, f map[string]string) { mkLicenses.set(t, f, "v1.0.1") },
			"go-licenses is v1.0.1 in the Makefile's GO_LICENSES_VERSION and v1.0.0" + drift},
		{"go-licenses moved in CI", func(t *testing.T, f map[string]string) { ciLicenses.set(t, f, "v1.0.1") },
			"go-licenses is v1.0.0 in the Makefile's GO_LICENSES_VERSION and v1.0.1" + drift},
		{"a leading v on one copy only", func(t *testing.T, f map[string]string) { ciVulncheck.set(t, f, "1.0.0") }, ""},
		{"the Makefile runs a tool at a literal", func(t *testing.T, f map[string]string) {
			f["Makefile"] = strings.Replace(f["Makefile"], "govulncheck@$(GOVULNCHECK_VERSION)", "govulncheck@v1.0.0", 1)
		}, "the Makefile does not run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION), the pin this check holds to CI"},
		{"the Makefile sets no variable", func(t *testing.T, f map[string]string) {
			f["Makefile"] = mkLicenses.pattern.ReplaceAllString(f["Makefile"], "")
		}, "the Makefile does not set GO_LICENSES_VERSION, which it runs go-licenses at"},
		{"CI runs no copy", func(t *testing.T, f map[string]string) {
			f[pinsCI] = ciLicenses.pattern.ReplaceAllString(f[pinsCI], "go-licenses-gone ")
		}, "ci.yml pins no go-licenses, which the Makefile runs at $(GO_LICENSES_VERSION)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := pinsFiles(t)
			for _, c := range []pinCopy{mkGolangci, mkVulncheck, mkLicenses, ciGolangci, ciVulncheck, ciLicenses} {
				c.set(t, files, "v1.0.0")
			}
			tc.edit(t, files)
			root := t.TempDir()
			putAll(t, root, files)
			t.Chdir(root)
			err := pins(io.Discard, nil)
			if got := errorText(err); got != tc.want {
				t.Errorf("pins = %q, want %q", got, tc.want)
			}
		})
	}
}

// pinsFiles is the repository's copy of every file the pins gate reads.
func pinsFiles(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{}
	for _, rel := range []string{"go.mod", "Makefile", ".pre-commit-config.yaml"} {
		files[rel] = string(repoFile(t, rel))
	}
	entries, err := os.ReadDir(repoPath(t, ".github/workflows"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		rel := ".github/workflows/" + e.Name()
		files[rel] = string(repoFile(t, rel))
	}
	return files
}

// errorText is the error's text, empty for none.
func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

const fakeSHA = "0000000000000000000000000000000000000000"
