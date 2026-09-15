package main

import (
	"io"
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
// SHA whose tool is left to float. The two named cases are the two that
// actually shipped unpinned in a sibling server, one of them failing a
// release.
func TestUnpinnedTools(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantBad bool
	}{
		{
			"cosign with no release input — this failed a sibling's release",
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

const fakeSHA = "0000000000000000000000000000000000000000"
