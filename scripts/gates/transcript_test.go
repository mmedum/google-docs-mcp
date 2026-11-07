package main

import (
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A driver file is one the live or evals tag builds, its OS and
// architecture tags left free as parity leaves them: `live && linux` is
// the driver on Linux, and its writes are held. A file that builds
// without the tag is a unit test beside the driver.
func TestTranscriptReadsEveryDriverFile(t *testing.T) {
	const body = "package livecheck\n\nimport \"testing\"\n\nfunc step(t *testing.T, got string) { t.Logf(\"%s\", got) }\n"
	const unredacted = ":7:52: got reaches the transcript unredacted; wrap it in shown or clip, " +
		"or add it to loggedPlain with the reason it is safe"
	for _, tc := range []struct {
		name, build string
		held        bool
	}{
		{"the live tag", "//go:build live", true},
		{"the live tag and an OS tag", "//go:build live && linux", true},
		{"the evals tag", "//go:build evals", true},
		{"both tags", "//go:build live && evals", true},
		{"a unit test beside the driver", "// a unit test", false},
		{"a file another tag builds", "//go:build integration", false},
		{"a file the tag is not needed to build", "//go:build live || !live", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "x_test.go")
			put(t, filepath.Dir(file), "x_test.go", tc.build+"\n\n"+body)
			n, got, err := transcriptProblems(file)
			if err != nil {
				t.Fatal(err)
			}
			var want []string
			if tc.held {
				want = []string{filepath.ToSlash(file) + unredacted}
			}
			if !slices.Equal(got, want) || n != len(want) {
				t.Errorf("problems = %q after %d writes, want %q", got, n, want)
			}
		})
	}
}

// The gate reads every file of the checkout that the live or evals tag
// builds, wherever it sits: a driver file in a package of its own is held
// as one in internal/livecheck is. A file git ignores is not the
// checkout's, and the go command skips a testdata directory. Too few
// writes read is a gate not reading the drivers.
func TestTranscriptReadsTheWholeCheckout(t *testing.T) {
	driver := func(build string) string {
		return build + "\n\npackage x\n\nimport \"testing\"\n\nfunc step(t *testing.T, got string) { t.Logf(\"%s\", got) }\n"
	}
	const steps = "internal/livecheck/steps_test.go"
	const unredacted = ":7:52: got reaches the transcript unredacted; wrap it in shown or clip, " +
		"or add it to loggedPlain with the reason it is safe"
	found := []string{
		"internal/evals/both_test.go" + unredacted,
		"internal/livecheck/more/x_test.go" + unredacted,
		"internal/probe/x_test.go" + unredacted,
	}
	for _, tc := range []struct {
		name  string
		steps bool
		want  []string
	}{
		{"with the driver's steps", true, found},
		{"too few writes", false, append(slices.Clone(found),
			"read 3 transcript writes, want at least 15: the reader is not seeing its input")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initRepo(t)
			files := map[string]string{
				"go.mod":                            "module example.com/fixture\n\ngo 1.21\n",
				".gitignore":                        "/scratch/\n",
				"internal/livecheck/more/x_test.go": driver("//go:build live"),
				"internal/probe/x_test.go":          driver("//go:build evals"),
				"internal/evals/both_test.go":       driver("//go:build live && evals"),
				"internal/gapi/x_test.go":           driver("//go:build integration"),
				"internal/probe/testdata/x.go":      "//go:build live\n\npackage x\n\nfunc {\n",
				"scratch/x_test.go":                 driver("//go:build live"),
			}
			if tc.steps {
				files[steps] = "//go:build live\n\npackage livecheck\n\nimport \"testing\"\n\nfunc steps(t *testing.T) {\n" +
					strings.Repeat("\tt.Log(\"step\")\n", 15) + "}\n"
			}
			putAll(t, ".", files)
			err := transcript(io.Discard, nil)
			if want := strings.Join(tc.want, "\n"); err == nil || err.Error() != want {
				t.Errorf("transcript = %v, want\n%s", err, want)
			}
		})
	}
}
