package main

import (
	"io"
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
