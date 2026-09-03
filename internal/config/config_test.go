package config

import (
	"errors"
	"flag"
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
	if c.Profile != "default" || c.LogLevel != LogInfo || c.LogFormat != LogText || c.Preview || c.ReadOnly || c.EnableDestructive {
		t.Fatalf("defaults wrong: %+v", c)
	}
	if c.DefaultWriteMode != WriteDirect || c.HTTPTimeout != 60*time.Second || c.ExportDir != "" {
		t.Fatalf("defaults wrong: %+v", c)
	}
	if got := c.AvailableWriteModes(); len(got) != 2 || got[0] != WriteDirect {
		t.Fatalf("modes = %v", got)
	}
}

func TestEnvThenFlagPrecedence(t *testing.T) {
	env := map[string]string{"GDOCS_LOG_LEVEL": "debug", "GDOCS_PREVIEW": "yes", "GDOCS_PROFILE": "Work"}
	c, err := build(t, env, "--log-level=warn")
	if err != nil {
		t.Fatal(err)
	}
	if c.LogLevel != LogWarn {
		t.Fatalf("flag should override env: %v", c.LogLevel)
	}
	if !c.Preview || c.DefaultWriteMode != WriteSuggest || c.Profile != "work" {
		t.Fatalf("env not applied: %+v", c)
	}
	if got := c.AvailableWriteModes(); len(got) != 3 || got[0] != WriteSuggest {
		t.Fatalf("modes = %v", got)
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
		{"suggest without preview", map[string]string{"GDOCS_DEFAULT_WRITE_MODE": "suggest"}, "Developer Preview"},
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
	c, err := build(t, map[string]string{"GDOCS_PREVIEW": "1", "GDOCS_DEFAULT_WRITE_MODE": "comment", "GDOCS_EXPORT_DIR": "/tmp/exports/", "GDOCS_ENABLE_DESTRUCTIVE": "on"})
	if err != nil {
		t.Fatal(err)
	}
	if c.DefaultWriteMode != WriteComment || c.ExportDir != "/tmp/exports" || !c.EnableDestructive {
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
