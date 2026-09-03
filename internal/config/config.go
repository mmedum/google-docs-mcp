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
	"path/filepath"
	"regexp"
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

// Write modes. Suggest needs Developer Preview; Comment works everywhere.
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
	Preview           bool
	DefaultWriteMode  WriteMode
	ReadOnly          bool
	EnableDestructive bool
	ExportDir         string
	HTTPTimeout       time.Duration
	ClientSecretPath  string
}

// AvailableWriteModes lists the modes this configuration can honour.
func (c Config) AvailableWriteModes() []WriteMode {
	if c.Preview {
		return []WriteMode{WriteSuggest, WriteDirect, WriteComment}
	}
	return []WriteMode{WriteDirect, WriteComment}
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
}

// Define registers one flag per setting on fs. Each flag defaults to the
// matching GDOCS_* variable (read through env) so that a flag passed on
// the command line wins over the environment, and the environment wins
// over the built-in default.
func Define(fs *flag.FlagSet, env func(string) string) *Settings {
	s := &Settings{}
	def := func(p *string, name, key, fallback, usage string) {
		v := env(EnvPrefix + key)
		if v == "" {
			v = fallback
		}
		fs.StringVar(p, name, v, usage+" [env "+EnvPrefix+key+"]")
	}
	def(&s.Profile, "profile", "PROFILE", "default", "named configuration profile")
	def(&s.LogLevel, "log-level", "LOG_LEVEL", string(LogInfo), "log level: debug, info, warn, error")
	def(&s.LogFormat, "log-format", "LOG_FORMAT", string(LogText), "log format: text, json")
	def(&s.Preview, "preview", "PREVIEW", "false", "enable Developer Preview features (suggestion mode, anchored comments)")
	def(&s.DefaultWriteMode, "default-write-mode", "DEFAULT_WRITE_MODE", "", "default write mode: suggest, direct, comment")
	def(&s.ReadOnly, "read-only", "READ_ONLY", "false", "register only read tools and request read-only scopes")
	def(&s.EnableDestructive, "enable-destructive", "ENABLE_DESTRUCTIVE", "false", "register destructive tools (delete comment, delete tab)")
	def(&s.ExportDir, "export-dir", "EXPORT_DIR", "", "directory binary exports may be written to (unset disables them)")
	def(&s.HTTPTimeout, "http-timeout", "HTTP_TIMEOUT", "60s", "per-request timeout for Google API calls")
	def(&s.ClientSecretPath, "client-secret", "CLIENT_SECRET", "", "path to the OAuth Desktop client JSON (overrides the stored profile setting)")
	return s
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

	c.Profile = strings.ToLower(strings.TrimSpace(s.Profile))
	if !profilePattern.MatchString(c.Profile) {
		errs = append(errs, fmt.Errorf("%w: profile %q must match %s", ErrInvalid, s.Profile, profilePattern))
	}

	c.LogLevel = LogLevel(strings.ToLower(strings.TrimSpace(s.LogLevel)))
	if !logLevels[c.LogLevel] {
		errs = append(errs, fmt.Errorf("%w: log level %q (want debug, info, warn, error)", ErrInvalid, s.LogLevel))
	}
	c.LogFormat = LogFormat(strings.ToLower(strings.TrimSpace(s.LogFormat)))
	if !logFormats[c.LogFormat] {
		errs = append(errs, fmt.Errorf("%w: log format %q (want text, json)", ErrInvalid, s.LogFormat))
	}

	var err error
	if c.Preview, err = parseBool("preview", s.Preview); err != nil {
		errs = append(errs, err)
	}
	if c.ReadOnly, err = parseBool("read-only", s.ReadOnly); err != nil {
		errs = append(errs, err)
	}
	if c.EnableDestructive, err = parseBool("enable-destructive", s.EnableDestructive); err != nil {
		errs = append(errs, err)
	}

	mode := WriteMode(strings.ToLower(strings.TrimSpace(s.DefaultWriteMode)))
	switch {
	case mode == "":
		if c.Preview {
			c.DefaultWriteMode = WriteSuggest
		} else {
			c.DefaultWriteMode = WriteDirect
		}
	case !writeModes[mode]:
		errs = append(errs, fmt.Errorf("%w: default write mode %q (want suggest, direct, comment)", ErrInvalid, s.DefaultWriteMode))
	case mode == WriteSuggest && !c.Preview:
		errs = append(errs, fmt.Errorf("%w: default write mode suggest needs Developer Preview (set %sPREVIEW=true) or choose direct or comment", ErrInvalid, EnvPrefix))
	default:
		c.DefaultWriteMode = mode
	}

	if dir := strings.TrimSpace(s.ExportDir); dir != "" {
		if !filepath.IsAbs(dir) {
			errs = append(errs, fmt.Errorf("%w: export dir %q must be an absolute path", ErrInvalid, dir))
		}
		c.ExportDir = filepath.Clean(dir)
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
