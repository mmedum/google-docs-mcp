package config

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func build(t *testing.T, env map[string]string, args ...string) (Config, error) {
	t.Helper()
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	s := Define(fs, func(k string) string { return env[k] })
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	return s.Build()
}

func TestDefaults(t *testing.T) {
	c, err := build(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Profile != "default" || c.LogLevel != LogInfo || c.LogFormat != LogText || c.ReadOnly || c.EnableDestructive {
		t.Fatalf("defaults wrong: %+v", c)
	}
	if c.DefaultWriteMode != WriteSuggest || c.HTTPTimeout != 60*time.Second || c.ExportDir != "" {
		t.Fatalf("defaults wrong: %+v", c)
	}
	if got := WriteModes(); len(got) != 3 || got[0] != WriteSuggest {
		t.Fatalf("modes = %v", got)
	}
	if len(c.Warnings) != 0 {
		t.Fatalf("warnings = %v", c.Warnings)
	}
}

func TestEnvThenFlagPrecedence(t *testing.T) {
	env := map[string]string{"GDOCS_LOG_LEVEL": "debug", "GDOCS_DEFAULT_WRITE_MODE": "comment", "GDOCS_PROFILE": "Work"}
	c, err := build(t, env, "--log-level=warn")
	if err != nil {
		t.Fatal(err)
	}
	if c.LogLevel != LogWarn {
		t.Fatalf("flag should override env: %v", c.LogLevel)
	}
	if c.DefaultWriteMode != WriteComment || c.Profile != "work" {
		t.Fatalf("env not applied: %+v", c)
	}
}

// An existing configuration that still sets the retired preview switch
// starts, says the switch does nothing, and changes nothing else.
func TestPreviewIsDeprecated(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		args []string
	}{
		{"env", map[string]string{"GDOCS_PREVIEW": "true"}, nil},
		{"env false", map[string]string{"GDOCS_PREVIEW": "false"}, nil},
		{"flag", nil, []string{"--preview=true"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := build(t, tc.env, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			want := "GDOCS_PREVIEW (--preview) is deprecated and ignored: comments and suggestions are generally available"
			if len(c.Warnings) != 1 || c.Warnings[0] != want {
				t.Fatalf("warnings = %q", c.Warnings)
			}
			if c.DefaultWriteMode != WriteSuggest {
				t.Fatalf("default write mode = %q", c.DefaultWriteMode)
			}
		})
	}
}

// The removed GDOCS_REQUIRE_PROMPT starts when off and is refused
// otherwise, saying what to do instead: ignored, it would quietly allow
// what its owner set it to stop. Its flag is gone.
func TestTheRemovedRequirePromptIsRefusedUnlessOff(t *testing.T) {
	for _, v := range []string{"", "false", " FALSE ", "0", "off"} {
		if _, err := build(t, map[string]string{"GDOCS_REQUIRE_PROMPT": v}); err != nil {
			t.Errorf("GDOCS_REQUIRE_PROMPT=%q: %v", v, err)
		}
	}
	for _, v := range []string{"true", "1", "maybe"} {
		_, err := build(t, map[string]string{"GDOCS_REQUIRE_PROMPT": v})
		if err == nil || !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "GDOCS_REQUIRE_PROMPT was removed") ||
			!strings.Contains(err.Error(), "leave GDOCS_ENABLE_DESTRUCTIVE off") {
			t.Errorf("GDOCS_REQUIRE_PROMPT=%q: err = %v", v, err)
		}
		if err != nil && strings.Contains(err.Error(), v) {
			t.Errorf("GDOCS_REQUIRE_PROMPT=%q: the refusal echoes the value: %v", v, err)
		}
	}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	Define(fs, func(string) string { return "" })
	if fs.Lookup("require-prompt") != nil {
		t.Error("--require-prompt is still a flag")
	}
	if got := RemovedVars(); len(got) != 1 || got[0] != "GDOCS_REQUIRE_PROMPT" {
		t.Errorf("RemovedVars() = %v", got)
	}
}

func TestValidation(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"bad level", map[string]string{"GDOCS_LOG_LEVEL": "loud"}, "log level"},
		{"bad format", map[string]string{"GDOCS_LOG_FORMAT": "xml"}, "log format"},
		{"bad bool", map[string]string{"GDOCS_READ_ONLY": "maybe"}, "read-only"},
		{"bad mode", map[string]string{"GDOCS_DEFAULT_WRITE_MODE": "yolo"}, "default write mode"},
		{"relative export dir", map[string]string{"GDOCS_EXPORT_DIR": "exports"}, "absolute"},
		{"bad timeout", map[string]string{"GDOCS_HTTP_TIMEOUT": "soon"}, "http timeout"},
		{"huge timeout", map[string]string{"GDOCS_HTTP_TIMEOUT": "1h"}, "between"},
		{"bad profile", map[string]string{"GDOCS_PROFILE": "../x"}, "profile"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := build(t, tc.env)
			if err == nil || !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestExplicitModesAndDirs(t *testing.T) {
	// What counts as absolute is the platform's business: C:\... on
	// Windows, /tmp/... elsewhere. Build one rather than hardcode a
	// POSIX path the config would rightly refuse.
	dir := filepath.Join(t.TempDir(), "exports")
	// The directory has to exist now: an export dir that does not is a
	// typo, and is refused at startup rather than at the first export.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	c, err := build(t, map[string]string{"GDOCS_DEFAULT_WRITE_MODE": "comment", "GDOCS_EXPORT_DIR": dir + string(filepath.Separator), "GDOCS_ENABLE_DESTRUCTIVE": "on"})
	if err != nil {
		t.Fatal(err)
	}
	if c.DefaultWriteMode != WriteComment || c.ExportDir != dir || !c.EnableDestructive {
		t.Fatalf("got %+v", c)
	}
}

func TestLoggerAndLevels(t *testing.T) {
	var sb strings.Builder
	l := NewLogger(Config{LogLevel: LogDebug, LogFormat: LogJSON}, &sb)
	l.Debug("hello", "k", "v")
	if !strings.Contains(sb.String(), `"msg":"hello"`) {
		t.Fatalf("json log missing: %s", sb.String())
	}
	sb.Reset()
	l = NewLogger(Config{LogLevel: LogWarn, LogFormat: LogText}, &sb)
	l.Info("hidden")
	l.Warn("shown")
	if strings.Contains(sb.String(), "hidden") || !strings.Contains(sb.String(), "shown") {
		t.Fatalf("level filter wrong: %s", sb.String())
	}
	for _, lv := range []LogLevel{LogDebug, LogInfo, LogWarn, LogError} {
		_ = lv.Slog()
	}
}

// A directory that does not exist used to be accepted, because only a
// relative path was refused. The typo then surfaced at the moment
// somebody tried to move a file, a long way from the setting that caused
// it and invisible to `status`.
func TestTheDirectoryMustExistAndBeADirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name, dir string
		ok        bool
	}{
		{"a real directory", dir, true},
		{"unset is allowed and turns the feature off", "", true},
		{"a path that does not exist", filepath.Join(dir, "nope"), false},
		{"a file rather than a directory", file, false},
		{"a relative path", filepath.Join("relative", "path"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := build(t, map[string]string{"GDOCS_EXPORT_DIR": tc.dir})
			if tc.ok {
				if err != nil {
					t.Errorf("%q was refused: %v", tc.dir, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("%q was accepted; the failure would surface at the first file operation", tc.dir)
			}
			if !strings.Contains(err.Error(), "export dir") {
				t.Errorf("the error does not name the setting: %v", err)
			}
		})
	}
}

// Vars is what the bundle gate reads a switch's default from, so each
// claim it makes is held to what Build does. A switch takes true and
// false and refuses anything else, and nothing else does that. Its
// default, and an empty value, each configure what setting nothing does,
// which is what lets a bundle leave a field blank.
func TestVarsDescribeWhatBuildDoes(t *testing.T) {
	vars := Vars()
	if len(vars) != 10 {
		t.Fatalf("Vars has %d settings; Define binds 10 flags", len(vars))
	}
	unset, err := build(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	var switches []string
	for _, v := range vars {
		accepts := func(value string) bool {
			_, err := build(t, map[string]string{v.Name: value})
			return err == nil
		}
		if boolean := accepts("true") && accepts("false") && !accepts("maybe"); boolean != v.Switch {
			t.Errorf("%s: Switch is %t, and Build reads it as a boolean: %t", v.Name, v.Switch, boolean)
		}
		if v.Switch {
			switches = append(switches, v.Name)
		}
		for _, value := range []string{v.Default, ""} {
			c, err := build(t, map[string]string{v.Name: value})
			if err != nil {
				t.Errorf("%s=%q is refused: %v", v.Name, value, err)
				continue
			}
			if !reflect.DeepEqual(c, unset) {
				t.Errorf("%s=%q configures something:\n got %+v\nwant %+v", v.Name, value, c, unset)
			}
		}
	}
	if want := []string{"GDOCS_READ_ONLY", "GDOCS_ENABLE_DESTRUCTIVE"}; !slices.Equal(switches, want) {
		t.Errorf("switches %v, want %v", switches, want)
	}
}

// EnvVars is recorded from the lookups Define makes, so it holds the
// removed setting that no flag binds as well as every one that does.
func TestEnvVarsIsEveryLookup(t *testing.T) {
	got := EnvVars()
	for _, v := range Vars() {
		if !slices.Contains(got, v.Name) {
			t.Errorf("EnvVars lacks %s, which Define binds", v.Name)
		}
	}
	if !slices.Contains(got, "GDOCS_REQUIRE_PROMPT") {
		t.Errorf("EnvVars lacks GDOCS_REQUIRE_PROMPT, which Define reads to refuse it set: %v", got)
	}
	if len(got) != 11 || !slices.IsSorted(got) {
		t.Errorf("EnvVars is %v; want the 10 settings and the removed one, sorted", got)
	}
}
