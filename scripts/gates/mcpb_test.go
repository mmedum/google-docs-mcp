package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The standard says: break the manifest all six ways and watch each be
// refused before you believe the check. A check nobody has watched fail
// is a check nobody knows the shape of — doing this in a sibling
// repository found a packer that verified the entry point and the Linux
// files and never the win32 override's command, so a typo in the .exe
// path would have packed cleanly, installed cleanly, and been caught by
// nothing.
//
// Each case below breaks exactly one thing in a manifest that is
// otherwise correct, and asserts the sentence that names it.

func testLauncher() []string {
	return []string{"google-docs-mcp-linux-amd64", "google-docs-mcp-linux-arm64"}
}

// repoFile reads a path relative to the repository root.
//
// The gates run from the root, which they chdir to themselves; a test
// runs from its own package directory. One helper rather than the same
// fallback written at each call site.
func repoFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(repoPath(t, path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

// repoPath resolves a repository-relative path from wherever the test is
// running, for the callers that need the PATH rather than the bytes.
func repoPath(t *testing.T, path string) string {
	t.Helper()
	if _, err := os.Stat("../../" + path); err == nil {
		return "../../" + path
	}
	return path
}

// good is the committed manifest, which every case starts from.
func good(t *testing.T) manifest {
	t.Helper()
	var m manifest
	if err := json.Unmarshal(repoFile(t, manifestPath), &m); err != nil {
		t.Fatalf("the committed manifest is not valid JSON: %v", err)
	}
	return m
}

func TestTheCommittedManifestIsValid(t *testing.T) {
	if problems := validateManifest(good(t), bundleFiles, testLauncher()); len(problems) > 0 {
		t.Fatalf("the committed manifest does not describe the bundle:\n%s", strings.Join(problems, "\n"))
	}
}

func TestTheSixWaysAManifestBreaks(t *testing.T) {
	cases := []struct {
		name   string
		breaks func(m *manifest)
		want   string
	}{
		{
			name:   "an entry point nobody stages",
			breaks: func(m *manifest) { m.Server.EntryPoint = "server/google-docs-mcp-mac" },
			want:   "entry_point",
		},
		{
			name: "a platform command nobody stages",
			breaks: func(m *manifest) {
				over := m.Server.MCPConfig.PlatformOverrides["win32"]
				over.Command = "${__dirname}/server/google-docs-mcp-windows.exe"
				m.Server.MCPConfig.PlatformOverrides["win32"] = over
			},
			want: "platform_overrides.win32.command",
		},
		{
			name: "an env value spending a key nobody declared",
			breaks: func(m *manifest) {
				// Composed, not the whole value: a check that only looked
				// at values that ARE a reference would miss this one and
				// the server would start with ${user_config.dir}
				// unsubstituted.
				m.Server.MCPConfig.Env["GCAL_CONFIG_DIR"] = "${user_config.dir}/google-docs-mcp"
			},
			want: "user_config does not declare",
		},
		{
			name: "an override for a platform the bundle does not claim",
			breaks: func(m *manifest) {
				m.Server.MCPConfig.PlatformOverrides["freebsd"] = m.Server.MCPConfig.PlatformOverrides["linux"]
			},
			want: "compatibility.platforms does not claim",
		},
		{
			name: "a platform running another platform's binary",
			breaks: func(m *manifest) {
				// Deleting the override passes every check above: win32
				// then runs the default command, which is the macOS
				// universal binary, and that file really is in the bundle.
				delete(m.Server.MCPConfig.PlatformOverrides, "win32")
			},
			want: `platform "win32" runs`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := good(t)
			c.breaks(&m)
			problems := validateManifest(m, bundleFiles, testLauncher())
			if !mentions(problems, c.want) {
				t.Fatalf("breaking %s was not refused; problems: %v", c.name, problems)
			}
		})
	}
}

// TestTheLauncherNamesAreThePackers is the sixth way, and the one no
// manifest can see: the names live in a shell script nothing else reads.
func TestTheLauncherNamesAreThePackers(t *testing.T) {
	// The 3.0.0 names: the family's layout is -linux-amd64.
	problems := validateManifest(good(t), bundleFiles,
		[]string{"google-docs-mcp-linux-arm64", "google-docs-mcp-linux-x64"})
	if !mentions(problems, "the launcher runs") {
		t.Fatalf("a launcher running a binary nobody stages was not refused; problems: %v", problems)
	}
	// And the real launcher agrees with the real staged list, read from
	// the file rather than from the list above.
	names := launcherNamesIn(launcherSource(t))
	if len(names) != 2 {
		t.Fatalf("the launcher execs %d binaries, want 2: %v", len(names), names)
	}
	if problems := validateManifest(good(t), bundleFiles, names); len(problems) > 0 {
		t.Fatalf("the committed launcher and the packer disagree:\n%s", strings.Join(problems, "\n"))
	}
}

// TestTheLauncherWritesItsRefusalToStderr: a line of English on stdout
// corrupts the JSON-RPC session before the client's first request
// completes, so an unknown architecture has to fail the other way.
func TestTheLauncherWritesItsRefusalToStderr(t *testing.T) {
	body := launcherSource(t)
	if !strings.Contains(body, ">&2") {
		t.Fatal("the launcher does not write its refusal to stderr")
	}
	if !strings.Contains(body, "exit 1") {
		t.Fatal("the launcher does not exit non-zero on an unknown architecture")
	}
	// exec, not a call: the server talks MCP over this process's stdio,
	// and a shell left in the middle owns the pipes.
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "google-docs-mcp-linux") && !strings.Contains(line, "exec ") {
			t.Fatalf("the launcher runs a binary without exec: %q", strings.TrimSpace(line))
		}
	}
}

// TestTheCommittedManifestCarriesThePlaceholder: a manifest in the tree
// must not be able to claim a version that shipped.
func TestTheCommittedManifestCarriesThePlaceholder(t *testing.T) {
	if got := good(t).Version; got != placeholderVersion {
		t.Fatalf("the committed manifest says %q, want the placeholder %q", got, placeholderVersion)
	}
}

// TestTheManifestSaysItCannotLogYouIn is the first-run failure: an
// install that appears to work and then answers every call with "not
// signed in".
func TestTheManifestSaysItCannotLogYouIn(t *testing.T) {
	if !strings.Contains(strings.ToLower(good(t).LongDescription), "does not log you in") {
		t.Fatal("long_description does not say the bundle cannot log the user in")
	}
}

// TestPackRefusesThePlaceholderAsAVersion: the packer writes the real
// version, and a build that passed the placeholder through would ship a
// bundle claiming 0.0.0-dev.
func TestPackRefusesThePlaceholderAsAVersion(t *testing.T) {
	err := packMCPB(t.TempDir(), placeholderVersion, t.TempDir()+"/out.mcpb")
	if err == nil || !strings.Contains(err.Error(), "placeholder") {
		t.Fatalf("packing with the placeholder version gave %v", err)
	}
}

func mentions(problems []string, want string) bool {
	for _, p := range problems {
		if strings.Contains(p, want) {
			return true
		}
	}
	return false
}

func launcherSource(t *testing.T) string {
	t.Helper()
	return string(repoFile(t, launcherPath))
}

// TestTheManifestIsTheJSONThePackerWrites: the version goes in through a
// decode and an encode, so a manifest cannot be corrupted by a
// substitution over text.
func TestTheManifestIsTheJSONThePackerWrites(t *testing.T) {
	var document map[string]any
	if err := json.Unmarshal(repoFile(t, manifestPath), &document); err != nil {
		t.Fatalf("the committed manifest is not valid JSON: %v", err)
	}
	document["version"] = "1.2.3"
	out, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"version": "1.2.3"`) {
		t.Fatal("the stamped manifest does not carry the version")
	}
}

// What the manifest claims about ITSELF: which document defines the
// format it says it is, and whether that reference can move under it.
// Neither `$schema` nor `support` was read until now.
//
// Every case below is well formed against the staged tree, and the last
// passes every claim that holds the document against itself — a 0.2
// manifest citing a 0.2 schema is stale and entirely consistent, which
// is why the floor is a separate claim.
func TestTheWaysAManifestMisdeclaresItself(t *testing.T) {
	const pinned = "https://raw.githubusercontent.com/anthropics/mcpb/v2.1.2/schemas/mcpb-manifest-v0.3.schema.json"

	// A commit SHA is the other ref that cannot move, and the stronger
	// of the two: refusing it would push somebody back to a branch.
	bySHA := good(t)
	bySHA.Schema = "https://raw.githubusercontent.com/anthropics/mcpb/" +
		"0123456789abcdef0123456789abcdef01234567/schemas/mcpb-manifest-v0.3.schema.json"
	if problems := declarationProblems(bySHA); len(problems) > 0 {
		t.Fatalf("a schema pinned to a commit was refused:\n%s", strings.Join(problems, "\n"))
	}

	cases := []struct {
		name   string
		breaks func(m *manifest)
		want   string
	}{
		{"no $schema", func(m *manifest) { m.Schema = "" }, "no $schema"},
		{
			"the unpinned dist path",
			func(m *manifest) {
				m.Schema = "https://raw.githubusercontent.com/anthropics/mcpb/main/dist/mcpb-manifest.schema.json"
			},
			"not upstream's published",
		},
		{
			"served from a branch",
			func(m *manifest) {
				m.Schema = "https://raw.githubusercontent.com/anthropics/mcpb/main/schemas/mcpb-manifest-v0.3.schema.json"
			},
			"can be re-pointed",
		},
		{
			// What refusing branch NAMES passes: a tag upstream moves
			// as it releases, which reads as pinned.
			"a partial tag",
			func(m *manifest) {
				m.Schema = "https://raw.githubusercontent.com/anthropics/mcpb/v2.1/schemas/mcpb-manifest-v0.3.schema.json"
			},
			"can be re-pointed",
		},
		{
			"the right file from another host",
			func(m *manifest) { m.Schema = "https://example.invalid/schemas/mcpb-manifest-v0.3.schema.json" },
			"not upstream's published",
		},
		{
			"a version that disagrees with the schema it cites",
			func(m *manifest) { m.Schema = pinned; m.ManifestVersion = "0.4" },
			"cannot claim one version",
		},
		{
			"stale, and entirely self-consistent",
			func(m *manifest) {
				m.Schema = "https://raw.githubusercontent.com/anthropics/mcpb/v2.1.2/schemas/mcpb-manifest-v0.2.schema.json"
				m.ManifestVersion = "0.2"
			},
			"still a manifest a version behind",
		},
		{"no support URL", func(m *manifest) { m.Support = "" }, "no support URL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := good(t)
			tc.breaks(&m)
			problems := declarationProblems(m)
			if len(problems) == 0 {
				t.Fatalf("%s was accepted", tc.name)
			}
			if !strings.Contains(strings.Join(problems, "\n"), tc.want) {
				t.Fatalf("wanted %q, got:\n%s", tc.want, strings.Join(problems, "\n"))
			}
		})
	}
}

// The floor compares numbers, not text: "0.10" sorts before "0.3" as a
// string, a bug that waits for the tenth minor version.
func TestTheFloorComparesVersionsNumerically(t *testing.T) {
	for _, c := range []struct {
		version, floor string
		want           bool
	}{
		{"0.3", "0.3", false},
		{"0.2", "0.3", true},
		{"0.10", "0.3", false},
		{"0.9", "1.0", true},
		{"nonsense", "0.3", true},
	} {
		if got := olderThan(c.version, c.floor); got != c.want {
			t.Errorf("olderThan(%q, %q) = %v, want %v", c.version, c.floor, got, c.want)
		}
	}
}

// The committed manifest meets the floor, stated rather than read from
// the file: a test taking its expected value from the thing under test
// passes on whatever it finds there.
func TestTheCommittedManifestMeetsTheFloor(t *testing.T) {
	if got := good(t).ManifestVersion; got != "0.3" {
		t.Fatalf("the committed manifest declares %q; this repository has checked 0.3", got)
	}
}

// edited is the committed manifest with one edit applied to its decoded
// document, read back the way the gate reads it.
func edited(t *testing.T, edit func(map[string]any)) manifest {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(repoFile(t, manifestPath), &doc); err != nil {
		t.Fatal(err)
	}
	edit(doc)
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// dig is the object at a path in a decoded manifest.
func dig(doc map[string]any, path ...string) map[string]any {
	for _, key := range path {
		doc = doc[key].(map[string]any)
	}
	return doc
}

// Each rule the bundle's settings and claims are held to, broken once in
// the committed manifest, and the shape that ships passing all of them.
func TestTheBundleSettingsRefuseEachBreak(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
		want string
	}{
		{name: "the shape that ships", edit: func(map[string]any) {}},
		{"a switch the bundle turns on", func(d map[string]any) {
			dig(d, "user_config", "enable_destructive")["default"] = true
		}, "user_config.enable_destructive is a boolean defaulting to true; GDOCS_ENABLE_DESTRUCTIVE is a switch " +
			"the server defaults to false"},
		{"a switch asked as text", func(d map[string]any) {
			dig(d, "user_config", "read_only")["type"] = "string"
			dig(d, "user_config", "read_only")["default"] = "false"
		}, `user_config.read_only is a string defaulting to "false"; GDOCS_READ_ONLY is a switch`},
		{"a switch with no default", func(d map[string]any) {
			delete(dig(d, "user_config", "enable_destructive"), "default")
		}, "user_config.enable_destructive is a boolean defaulting to nothing"},
		{"an optional key with no default", func(d map[string]any) {
			delete(dig(d, "user_config", "export_dir"), "default")
		}, "user_config.export_dir is optional with no default"},
		{"a setting the bundle no longer asks for", func(d map[string]any) {
			delete(dig(d, "user_config"), "export_dir")
			delete(dig(d, "server", "mcp_config", "env"), "GDOCS_EXPORT_DIR")
		}, "user_config does not ask for export_dir, which sets GDOCS_EXPORT_DIR"},
		{"a setting spent under another key", func(d map[string]any) {
			dig(d, "server", "mcp_config", "env")["GDOCS_PROFILE"] = "${user_config.default_write_mode}"
		}, `mcp_config.env.GDOCS_PROFILE is "${user_config.default_write_mode}", want "${user_config.profile}"`},
		{"a key no setting has", func(d map[string]any) {
			dig(d, "user_config")["verbose"] = map[string]any{
				"type": "boolean", "title": "Verbose", "description": "d", "default": false}
		}, "user_config asks for verbose, which is no setting's key"},
		{"a variable the bundle does not ask for", func(d map[string]any) {
			dig(d, "server", "mcp_config", "env")["GDOCS_LOG_LEVEL"] = "debug"
		}, "mcp_config.env sets GDOCS_LOG_LEVEL, which the bundle does not ask for"},
		{"a default that configures the server", func(d map[string]any) {
			dig(d, "user_config", "default_write_mode")["default"] = "direct"
		}, "the bundle's defaults configure the server otherwise than no environment does"},
		{"a default the server refuses", func(d map[string]any) {
			dig(d, "user_config", "profile")["default"] = "Not A Profile"
		}, "the environment the bundle's defaults give the server is refused: config: invalid: profile"},
		{"no privacy policy", func(d map[string]any) { delete(d, "privacy_policies") },
			"privacy_policies is empty"},
		{"an empty privacy policy list", func(d map[string]any) { d["privacy_policies"] = []any{} },
			"privacy_policies is empty"},
		{"no Claude Desktop floor", func(d map[string]any) { delete(dig(d, "compatibility"), "claude_desktop") },
			`compatibility.claude_desktop is ""; the family's bundles claim ">=0.10.0"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := edited(t, tc.edit)
			problems := append(claimProblems(m), bundleSettingProblems(m, serverVars())...)
			if tc.want == "" {
				if len(problems) > 0 {
					t.Fatalf("the shape that ships was refused:\n%s", strings.Join(problems, "\n"))
				}
				return
			}
			if !mentions(problems, tc.want) {
				t.Errorf("problems:\n%s\nwant one containing %q", strings.Join(problems, "\n"), tc.want)
			}
		})
	}
}

// Every variable the server reads has a verdict: asked for, or left out
// with a reason. A new one with neither fails, and so does a verdict on
// one the server no longer reads.
func TestEverySettingHasABundleVerdict(t *testing.T) {
	known := serverVars()
	if len(known) < 13 {
		t.Fatalf("the server reads %d variables; this would be checking a stub", len(known))
	}
	m := good(t)
	if p := bundleSettingProblems(m, known); len(p) != 0 {
		t.Fatalf("the committed manifest fails: %v", p)
	}
	got := bundleSettingProblems(m, append(slices.Clone(known), "GDOCS_NEW"))
	if !mentions(got, "the server reads GDOCS_NEW, and the bundle neither asks for it nor says why not") {
		t.Errorf("a setting with no verdict passed: %v", got)
	}
	without := slices.DeleteFunc(slices.Clone(known), func(s string) bool { return s == "GDOCS_HTTP_TIMEOUT" })
	got = bundleSettingProblems(m, without)
	if !mentions(got, "the bundle has a verdict on GDOCS_HTTP_TIMEOUT, which the server does not read") {
		t.Errorf("a verdict on a setting the server does not read passed: %v", got)
	}
}

// The launcher's install hint names the module go.mod declares, read
// under Windows line endings too. The path moves with each major
// version, so a hint left behind sends somebody to an old one.
func TestTheLauncherInstallHintIsGoModsModule(t *testing.T) {
	const hint = `echo "no binary." "Install with: go install example.com/m/v3/cmd/google-docs-mcp@latest" >&2`
	for _, goMod := range []string{"module example.com/m/v3\n\ngo 1.27\n", "module example.com/m/v3\r\n\r\ngo 1.27\r\n"} {
		if problems := installHintProblems(hint, goMod); len(problems) > 0 {
			t.Errorf("go.mod %q: %v", goMod, problems)
		}
	}
	for _, tc := range []struct{ name, launcher, goMod, want string }{
		{"a hint without the major version", strings.Replace(hint, "/v3", "", 1), "module example.com/m/v3\n",
			`the launcher says "go install example.com/m/cmd/google-docs-mcp@latest"`},
		{"a hint a major version behind", strings.Replace(hint, "/v3", "/v2", 1), "module example.com/m/v3\r\n",
			`the launcher says "go install example.com/m/v2/cmd/google-docs-mcp@latest"`},
		{"no hint at all", "exit 1", "module example.com/m/v3\n", "gives no install hint"},
		{"no module line", hint, "go 1.27\n", "go.mod names no module"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := installHintProblems(tc.launcher, tc.goMod); !mentions(got, tc.want) {
				t.Errorf("problems %q, want %q", got, tc.want)
			}
		})
	}
	// And the committed launcher against the committed go.mod.
	if problems := installHintProblems(launcherSource(t), string(repoFile(t, "go.mod"))); len(problems) > 0 {
		t.Errorf("the committed launcher: %v", problems)
	}
}

// The packed bundle has the family's layout: the binaries and the
// launcher executable, the license and the README readable and the
// repository's own, and nothing else. The modes are literals, not
// staged.mode(), so the assertion does not check the code against
// itself, and the fixture stages its binaries 0600, so a packer that
// copied the source's mode fails here.
func TestThePackedBundleHasTheFamilyLayout(t *testing.T) {
	dist := t.TempDir()
	for dir, name := range map[string]string{
		"google-docs-mcp-universal_darwin_all": "google-docs-mcp",
		"google-docs-mcp_linux_amd64_v1":       "google-docs-mcp",
		"google-docs-mcp_linux_arm64_v8.0":     "google-docs-mcp",
		"google-docs-mcp_windows_amd64_v1":     "google-docs-mcp.exe",
	} {
		full := filepath.Join(dist, dir)
		if err := os.MkdirAll(full, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(full, name), []byte("binary for "+dir), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(t.TempDir(), "b.mcpb")
	// The packer reads the manifest, the launcher, the license and the
	// README from the repository root, as the release runs it.
	t.Chdir("../..")
	if err := packMCPB(dist, "1.2.3", out); err != nil {
		t.Fatalf("pack: %v", err)
	}
	z, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = z.Close() }()

	want := map[string]os.FileMode{
		"manifest.json":                      0o644,
		"LICENSE":                            0o644,
		"README.md":                          0o644,
		"server/launch-linux.sh":             0o755,
		"server/google-docs-mcp-darwin":      0o755,
		"server/google-docs-mcp.exe":         0o755,
		"server/google-docs-mcp-linux-amd64": 0o755,
		"server/google-docs-mcp-linux-arm64": 0o755,
	}
	var names []string
	for _, f := range z.File {
		names = append(names, f.Name)
		mode, ok := want[f.Name]
		if !ok {
			t.Errorf("the bundle carries %s, which is not the family's layout", f.Name)
			continue
		}
		if got := f.Mode().Perm(); got != mode {
			t.Errorf("%s is packed %v, want %v", f.Name, got, mode)
		}
		if f.Name != "LICENSE" && f.Name != "README.md" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		if repo, err := os.ReadFile(f.Name); err != nil || !bytes.Equal(got, repo) {
			t.Errorf("the packed %s is not the repository's (%v)", f.Name, err)
		}
	}
	if len(names) != len(want) {
		t.Errorf("the bundle carries %v; want the %d files of the family's layout", names, len(want))
	}
}
