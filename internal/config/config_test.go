package config

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
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
