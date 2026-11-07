package main

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
)

// parityReleaseWorkflow is the workflow a release tag starts.
const parityReleaseWorkflow = "release.yml"

// The release attests every file it publishes: the archives, the bundle
// and the checksum file goreleaser writes. actions/attest v4.2.1, which
// attest-build-provenance v4.2.2 runs, splits subject-path at commas and
// newlines and trims each glob
// (https://github.com/actions/attest/blob/508db95dd578ae2727ebd6217d5ba78e4fbda05d/src/subject.ts#L303),
// so a space-separated list is one glob that matches nothing.
func TestTheReleaseAttestsEveryFileItPublishes(t *testing.T) {
	t.Chdir("../..")
	flows, err := readWorkflows()
	if err != nil {
		t.Fatal(err)
	}
	release, ok := workflowNamed(flows, parityReleaseWorkflow)
	if !ok {
		t.Fatal("no " + parityReleaseWorkflow)
	}
	cfg, err := os.ReadFile(parityGoreleaser)
	if err != nil {
		t.Fatal(err)
	}
	if p := releaseAttestProblems(release, string(cfg)); p != nil {
		t.Errorf("the release: %q", p)
	}
}

// releaseAttestProblems holds a release workflow's build-provenance step
// to the files the goreleaser config publishes.
func releaseAttestProblems(release workflowFile, cfg string) []string {
	var problems []string
	attested := false
	for _, s := range release.steps {
		if s.action() != "actions/attest-build-provenance" {
			continue
		}
		attested = true
		subjects, ok := s.with["subject-path"]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s:%d: the attestation names no subject-path", release.path, s.line))
			continue
		}
		problems = append(problems, releaseSubjectProblems(subjects.value, checksumNameTemplate(cfg))...)
	}
	if !attested {
		problems = append(problems, release.path+" runs no build-provenance attestation")
	}
	return problems
}

// releaseSubjectProblems holds the attestation's subject-path: a comma-
// or newline-separated list naming the archives, the checksum file and
// the bundle. A space-separated value is one literal glob.
func releaseSubjectProblems(subjects, checksum string) []string {
	var problems []string
	parts := strings.FieldsFunc(subjects, func(r rune) bool { return r == ',' || r == '\n' })
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	if slices.ContainsFunc(parts, func(p string) bool { return strings.ContainsAny(p, " \t") }) {
		problems = append(problems, fmt.Sprintf("subject-path %q separates with a space, which is read as one "+
			"glob; separate with commas or newlines", subjects))
	}
	// Without a name_template goreleaser names the checksum file for the
	// project and version, which a fixed subject-path does not match.
	if checksum == "" {
		problems = append(problems, "checksum.name_template is empty, so nothing holds the subject-path to the "+
			"file every other artifact is verified through")
	}
	// An entry ends with the file it covers: dist/*.mcpb.sigstore.json
	// attests a signature, not the bundle.
	for _, want := range []string{".tar.gz", ".zip", ".mcpb", checksum} {
		if want != "" && !slices.ContainsFunc(parts, func(p string) bool { return strings.HasSuffix(p, want) }) {
			problems = append(problems, fmt.Sprintf("subject-path %q does not name %s, so it carries no attestation",
				subjects, want))
		}
	}
	return problems
}

// checksumNameTemplate is a goreleaser config's top-level
// checksum.name_template, or "".
func checksumNameTemplate(cfg string) string {
	in, child := false, -1
	for _, raw := range strings.Split(cfg, "\n") {
		t := strings.TrimSpace(raw)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		n := indent(raw)
		if n == 0 {
			key, _ := yamlCutComment(t)
			in, child = key == "checksum:", -1
			continue
		}
		if !in {
			continue
		}
		if child < 0 {
			child = n
		}
		if v, ok := strings.CutPrefix(t, "name_template:"); ok && n == child {
			value, _ := workflowValue(strings.TrimSpace(v))
			return value
		}
	}
	return ""
}

func TestReleaseSubjectProblems(t *testing.T) {
	for name, subjects := range map[string]string{
		"commas":                        "dist/*.tar.gz,dist/*.zip,dist/*.mcpb,dist/checksums.txt",
		"newlines":                      "dist/*.tar.gz\ndist/*.zip\ndist/checksums.txt\ndist/*.mcpb",
		"commas and a space after each": "dist/*.tar.gz, dist/*.zip, dist/checksums.txt, dist/*.mcpb",
	} {
		if p := releaseSubjectProblems(subjects, "checksums.txt"); len(p) != 0 {
			t.Errorf("%s: subjects refused: %v", name, p)
		}
	}
	want := []string{
		"checksum.name_template is empty, so nothing holds the subject-path to the file every other artifact is verified through",
		`subject-path "dist/*.tar.gz,dist/*.zip.sigstore.json,dist/*.mcpb" does not name .zip, so it carries no attestation`,
	}
	if p := releaseSubjectProblems("dist/*.tar.gz,dist/*.zip.sigstore.json,dist/*.mcpb", ""); !slices.Equal(p, want) {
		t.Errorf("problems\n got %q\nwant %q", p, want)
	}
}

// The step is read from the workflow as written: a literal block is a list
// and a folded one is one line of space-separated globs.
func TestReleaseAttestProblems(t *testing.T) {
	const cfg = "release:\n  name_template: x\nchecksum:\n  # the file the signature covers\n  name_template: 'checksums.txt' # fixed\n"
	step := func(with string) workflowFile {
		return parseWorkflow("release.yml", "jobs:\n  r:\n    steps:\n      - uses: actions/attest-build-provenance@x\n"+with)
	}
	for _, tc := range []struct {
		name string
		w    workflowFile
		cfg  string
		want []string
	}{
		{"a literal block", step("        with:\n          subject-path: |\n            dist/*.tar.gz\n            dist/*.zip\n            dist/*.mcpb\n            dist/checksums.txt\n"), cfg, nil},
		{"a folded block", step("        with:\n          subject-path: >\n            dist/*.tar.gz\n            dist/*.zip\n            dist/*.mcpb\n            dist/checksums.txt\n"), cfg, []string{
			`subject-path "dist/*.tar.gz dist/*.zip dist/*.mcpb dist/checksums.txt" separates with a space, which is read as one glob; separate with commas or newlines`,
			`subject-path "dist/*.tar.gz dist/*.zip dist/*.mcpb dist/checksums.txt" does not name .tar.gz, so it carries no attestation`,
			`subject-path "dist/*.tar.gz dist/*.zip dist/*.mcpb dist/checksums.txt" does not name .zip, so it carries no attestation`,
			`subject-path "dist/*.tar.gz dist/*.zip dist/*.mcpb dist/checksums.txt" does not name .mcpb, so it carries no attestation`,
		}},
		{"another checksum file", step("        with:\n          subject-path: dist/*.tar.gz,dist/*.zip,dist/*.mcpb,dist/checksums.txt\n"),
			"checksum:\n  name_template: '{{ .ProjectName }}_checksums.txt'\n", []string{
				`subject-path "dist/*.tar.gz,dist/*.zip,dist/*.mcpb,dist/checksums.txt" does not name {{ .ProjectName }}_checksums.txt, so it carries no attestation`,
			}},
		{"no checksum name, only another block's", step("        with:\n          subject-path: dist/*.tar.gz,dist/*.zip,dist/*.mcpb\n"),
			"release:\n  name_template: x\nchecksum:\n  algorithm: sha256\n", []string{
				"checksum.name_template is empty, so nothing holds the subject-path to the file every other artifact is verified through",
			}},
		{"a checksum name only under another key of checksum", step("        with:\n          subject-path: dist/*.tar.gz,dist/*.zip,dist/*.mcpb\n"),
			"checksum:\n  extra_files:\n    - glob: x\n      name_template: other.txt\n", []string{
				"checksum.name_template is empty, so nothing holds the subject-path to the file every other artifact is verified through",
			}},
		{"a tab before each comment", step("        with:\n          subject-path: dist/*.tar.gz,dist/*.zip,dist/*.mcpb,dist/checksums.txt\n"),
			"checksum:\t# signed\n  name_template: checksums.txt\t# fixed\n", nil},
		{"no subject-path", step(""), cfg, []string{"release.yml:4: the attestation names no subject-path"}},
		{"no attestation", parseWorkflow("release.yml", "jobs:\n  r:\n    steps:\n      - run: true\n"), cfg,
			[]string{"release.yml runs no build-provenance attestation"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := releaseAttestProblems(tc.w, tc.cfg); !slices.Equal(got, tc.want) {
				t.Errorf("problems:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}
}
