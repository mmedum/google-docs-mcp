// Package config loads and validates runtime configuration.
//
// Environment variables (GDOCS_*) are the source of truth because every
// MCP client (Claude Code, Claude Desktop, Cursor) passes only command,
// args and env to a stdio server. Each setting also has a flag bound to
// the same name; a flag given explicitly overrides the environment.
// Validation runs once at start so a misconfigured server fails before
// it announces itself.
package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// EnvPrefix is prepended to every environment variable name.
const EnvPrefix = "GDOCS_"

// LogLevel is a typed enum constrained at load time.
type LogLevel string

// Allowed LogLevel values.
const (
	LogDebug LogLevel = "debug"
	LogInfo  LogLevel = "info"
	LogWarn  LogLevel = "warn"
	LogError LogLevel = "error"
)

// Slog returns the slog.Level for this level.
func (l LogLevel) Slog() slog.Level {
	switch l {
	case LogDebug:
		return slog.LevelDebug
	case LogWarn:
		return slog.LevelWarn
	case LogError:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// LogFormat is a typed enum constrained at load time.
type LogFormat string

// Allowed LogFormat values.
const (
	LogText LogFormat = "text"
	LogJSON LogFormat = "json"
)

// WriteMode says how a write lands in the document.
type WriteMode string

// Write modes.
const (
	WriteSuggest WriteMode = "suggest"
	WriteDirect  WriteMode = "direct"
	WriteComment WriteMode = "comment"
)

// Config is the validated runtime configuration.
type Config struct {
	Profile           string
	LogLevel          LogLevel
	LogFormat         LogFormat
	DefaultWriteMode  WriteMode
	ReadOnly          bool
	EnableDestructive bool
	ExportDir         string
	HTTPTimeout       time.Duration
	ClientSecretPath  string
	// Warnings name settings that were accepted but no longer do
	// anything, for the caller to log once at start.
	Warnings []string
}

// WriteModes lists the write modes, in order of preference.
func WriteModes() []WriteMode {
	return []WriteMode{WriteSuggest, WriteDirect, WriteComment}
}

// Settings holds the raw string values before validation. Flags and the
// environment both feed it; Build turns it into a Config.
type Settings struct {
	Profile           string
	LogLevel          string
	LogFormat         string
	Preview           string
	DefaultWriteMode  string
	ReadOnly          string
	EnableDestructive string
	ExportDir         string
	HTTPTimeout       string
	ClientSecretPath  string
	// removed are the removed settings, as the environment gives them,
	// read only to refuse one still set.
	removed map[string]string
	// vars is every variable Define reads, in order.
	vars []Var
}

// Var is one variable the server reads.
type Var struct {
	// Name is the environment variable.
	Name string
	// Default is what the server uses when neither the variable nor its
	// flag is set, written as a person would set it. A switch's is
	// "false", which is also how a bundle's checkbox reaches the
	// environment.
	Default string
	// Switch marks a setting that is on or off.
	Switch bool
	// Removed marks a setting a release took out, read only to refuse it.
	Removed bool
}

// The variables read outside Define. internal/userconfig reads the
// profile directory before any flag is parsed, and internal/credentials
// the refresh token; each takes its name from here, so config imports
// neither, and the bundle gate imports no keyring.
const (
	EnvConfigDir    = EnvPrefix + "CONFIG_DIR"
	EnvRefreshToken = EnvPrefix + "REFRESH_TOKEN"
)

// envOnly is every variable read outside Define, for Vars.
var envOnly = []string{EnvConfigDir, EnvRefreshToken}

// Lookup is the variable name as env gives it, without the spaces around
// it. Spaces alone are "", unset as an empty variable is, so a form field
// cleared to a space means the default rather than a value the server
// refuses. Define reads every setting through it, and so does each
// reader of a variable outside Define.
func Lookup(env func(string) string, name string) string {
	return strings.TrimSpace(env(name))
}

// removedSettings are the removed settings that made the server ask the
// person, each with what to do instead. Off, the old default, is still accepted. On, or a value
// that is not a boolean, is refused at start: ignored, it would quietly
// allow what its owner set it to stop.
var removedSettings = []struct{ key, instead string }{
	{"REQUIRE_PROMPT", "leave " + EnvPrefix + "ENABLE_DESTRUCTIVE off for a client that runs with nobody watching"},
}

// Define registers one flag per setting on fs. Each flag defaults to the
// matching GDOCS_* variable (read through env) so that a flag passed on
// the command line wins over the environment, and the environment wins
// over the built-in default.
//
// Each variable it reads is recorded for Vars, which the staleness and
// bundle gates read, so neither can fall behind a setting added here.
func Define(fs *flag.FlagSet, env func(string) string) *Settings {
	s := &Settings{removed: map[string]string{}}
	for _, r := range removedSettings {
		s.removed[r.key] = Lookup(env, EnvPrefix+r.key)
		s.vars = append(s.vars, Var{Name: EnvPrefix + r.key, Removed: true})
	}
	bind := func(p *string, name, key, fallback, usage string, isSwitch bool) {
		v := Lookup(env, EnvPrefix+key)
		if v == "" {
			v = fallback
		}
		fs.StringVar(p, name, v, usage+" [env "+EnvPrefix+key+"]")
		s.vars = append(s.vars, Var{Name: EnvPrefix + key, Default: fallback, Switch: isSwitch})
	}
	def := func(p *string, name, key, fallback, usage string) { bind(p, name, key, fallback, usage, false) }
	// A switch is off unless set.
	sw := func(p *string, name, key, usage string) { bind(p, name, key, "false", usage, true) }
	def(&s.Profile, "profile", "PROFILE", "default", "named configuration profile")
	def(&s.LogLevel, "log-level", "LOG_LEVEL", string(LogInfo), "log level: debug, info, warn, error")
	def(&s.LogFormat, "log-format", "LOG_FORMAT", string(LogText), "log format: text, json")
	def(&s.Preview, "preview", "PREVIEW", "", "deprecated and ignored: comments and suggestions are generally available")
	def(&s.DefaultWriteMode, "default-write-mode", "DEFAULT_WRITE_MODE", "", "default write mode: "+WriteModeRule())
	sw(&s.ReadOnly, "read-only", "READ_ONLY", "register only read tools and request read-only scopes")
	sw(&s.EnableDestructive, "enable-destructive", "ENABLE_DESTRUCTIVE", "register destructive tools (delete comment, delete tab)")
	def(&s.ExportDir, "export-dir", "EXPORT_DIR", "", "directory binary exports may be written to (unset disables them)")
	def(&s.HTTPTimeout, "http-timeout", "HTTP_TIMEOUT", "60s", "per-request timeout for Google API calls")
	def(&s.ClientSecretPath, "client-secret", "CLIENT_SECRET", "", "path to the OAuth Desktop client JSON (overrides the stored profile setting)")
	return s
}

// Vars is every variable the server reads, sorted by name: Define's,
// the removed ones included, and the ones read outside it. Define's are
// recorded rather than listed, so they cannot fall behind. The bundle
// and staleness gates derive what they hold from these.
func Vars() []Var {
	vars := defined()
	for _, name := range envOnly {
		vars = append(vars, Var{Name: name})
	}
	slices.SortFunc(vars, func(a, b Var) int { return strings.Compare(a.Name, b.Name) })
	return vars
}

// defined is every variable Define reads, in the order it reads them.
func defined() []Var {
	fs := flag.NewFlagSet("vars", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return Define(fs, func(string) string { return "" }).vars
}

// EnvVars is the names of Vars, sorted. A name listed twice is a
// variable read under two rules, which a test refuses, so it is not
// compacted.
func EnvVars() []string {
	vars := Vars()
	names := make([]string, 0, len(vars))
	for _, v := range vars {
		names = append(names, v.Name)
	}
	return names
}

// Load is Define, Parse and Build for a caller with no flags of its own.
func Load(args []string, env func(string) string) (Config, error) {
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	s := Define(fs, env)
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	return s.Build()
}

// ProfileRule is profilePattern in words, which Build's refusal and the
// bundle's profile field both give.
const ProfileRule = "up to 64 characters from a to z, 0 to 9, - and _, starting with a letter or digit"

// WriteModeRule is WriteModes in words, "suggest, direct or comment",
// which Build's refusal and the bundle's default write mode field both
// give.
func WriteModeRule() string {
	modes := WriteModes()
	words := make([]string, len(modes))
	for i, m := range modes {
		words[i] = string(m)
	}
	return strings.Join(words[:len(words)-1], ", ") + " or " + words[len(words)-1]
}

// asciiLower lowers A to Z alone, so a character Unicode folds into a
// to z, such as the Kelvin sign, is refused rather than renamed.
func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if 'A' <= r && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, s)
}

var (
	profilePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	logLevels      = map[LogLevel]bool{LogDebug: true, LogInfo: true, LogWarn: true, LogError: true}
	logFormats     = map[LogFormat]bool{LogText: true, LogJSON: true}
	writeModes     = map[WriteMode]bool{WriteSuggest: true, WriteDirect: true, WriteComment: true}
)

// ErrInvalid wraps every validation failure.
var ErrInvalid = errors.New("config: invalid")

// Build validates the settings and returns a Config.
func (s *Settings) Build() (Config, error) {
	var c Config
	var errs []error

	c.Profile = asciiLower(strings.TrimSpace(s.Profile))
	if !profilePattern.MatchString(c.Profile) {
		errs = append(errs, fmt.Errorf("%w: profile %q must be %s", ErrInvalid, s.Profile, ProfileRule))
	}

	c.LogLevel = LogLevel(strings.ToLower(strings.TrimSpace(s.LogLevel)))
	if !logLevels[c.LogLevel] {
		errs = append(errs, fmt.Errorf("%w: log level %q (want debug, info, warn, error)", ErrInvalid, s.LogLevel))
	}
	c.LogFormat = LogFormat(strings.ToLower(strings.TrimSpace(s.LogFormat)))
	if !logFormats[c.LogFormat] {
		errs = append(errs, fmt.Errorf("%w: log format %q (want text, json)", ErrInvalid, s.LogFormat))
	}

	// Accepted so an existing configuration still starts; Google made
	// the features it gated generally available on 2026-09-30.
	if strings.TrimSpace(s.Preview) != "" {
		c.Warnings = append(c.Warnings, EnvPrefix+"PREVIEW (--preview) is deprecated and ignored: comments and suggestions are generally available")
	}

	var err error
	if c.ReadOnly, err = parseBool("read-only", s.ReadOnly); err != nil {
		errs = append(errs, err)
	}
	if c.EnableDestructive, err = parseBool("enable-destructive", s.EnableDestructive); err != nil {
		errs = append(errs, err)
	}
	for _, r := range removedSettings {
		if on, err := parseBool(r.key, s.removed[r.key]); err == nil && !on {
			continue
		}
		errs = append(errs, fmt.Errorf("%w: %s%s was removed: the server no longer asks the person, and the client's own "+
			"approval decides each call. Unset it, and %s", ErrInvalid, EnvPrefix, r.key, r.instead))
	}

	mode := WriteMode(strings.ToLower(strings.TrimSpace(s.DefaultWriteMode)))
	switch {
	case mode == "":
		c.DefaultWriteMode = WriteSuggest
	case !writeModes[mode]:
		errs = append(errs, fmt.Errorf("%w: default write mode %q (want %s)", ErrInvalid, s.DefaultWriteMode, WriteModeRule()))
	default:
		c.DefaultWriteMode = mode
	}

	if dir := strings.TrimSpace(s.ExportDir); dir != "" {
		// Existence is part of the rule, not a detail. A relative path was
		// always refused here; an absolute one that does not exist was not,
		// so a typo was accepted at startup and surfaced later as a file
		// operation failing for reasons nobody could see from `status`.
		var info os.FileInfo
		var statErr error
		switch {
		case !filepath.IsAbs(dir):
			errs = append(errs, fmt.Errorf("%w: export dir %q must be an absolute path", ErrInvalid, dir))
		default:
			if info, statErr = os.Stat(dir); statErr != nil {
				errs = append(errs, fmt.Errorf("%w: export dir %q cannot be read: %w", ErrInvalid, dir, statErr))
			} else if !info.IsDir() {
				errs = append(errs, fmt.Errorf("%w: export dir %q is not a directory", ErrInvalid, dir))
			} else {
				c.ExportDir = filepath.Clean(dir)
			}
		}
	}

	if c.HTTPTimeout, err = time.ParseDuration(strings.TrimSpace(s.HTTPTimeout)); err != nil {
		errs = append(errs, fmt.Errorf("%w: http timeout %q: %w", ErrInvalid, s.HTTPTimeout, err))
	} else if c.HTTPTimeout <= 0 || c.HTTPTimeout > 10*time.Minute {
		errs = append(errs, fmt.Errorf("%w: http timeout %s must be between 1s and 10m", ErrInvalid, c.HTTPTimeout))
	}

	c.ClientSecretPath = strings.TrimSpace(s.ClientSecretPath)

	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}
	return c, nil
}

func parseBool(name, v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return false, nil
	case "1", "true", "yes", "on":
		return true, nil
	}
	return false, fmt.Errorf("%w: %s %q (want true or false)", ErrInvalid, name, v)
}

// NewLogger builds the process logger. It writes to w, which must be
// stderr in the server path: stdout carries only JSON-RPC frames.
func NewLogger(c Config, w io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{Level: c.LogLevel.Slog()}
	if c.LogFormat == LogJSON {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}
