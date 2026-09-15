package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/mmedum/google-docs-mcp/internal/redact"
	"github.com/mmedum/google-docs-mcp/internal/version"
)

// statusSchemaVersion is the version of the JSON object `status --json`
// prints. A caller may branch on it; it changes only when a field is
// removed or its meaning changes, never when one is added.
const statusSchemaVersion = 1

// statusReport is everything `status` knows, collected once and then
// rendered either as the lines a person reads or as the object a script
// parses.
//
// One collector, two renderers, because the alternative drifts. A script
// that wants to know whether this server is authorised otherwise has to
// read the text, and the text is written for a person: the field that
// answers the question is a label with a value beside it, and a label is
// free to be reworded in any release. This repository has already done
// that to somebody — `refresh token:` became `token store:` under a
// release, and a check written against the old label started reading
// "not authorised" for an account that was fine.
//
// Nothing here contacts Google. It reports what is configured and where
// the token is, which is the question a launcher asks before starting
// the server; `doctor` is the one that asks Google whether it works.
type statusReport struct {
	SchemaVersion int    `json:"schema_version"`
	Binary        string `json:"binary"`
	Version       string `json:"version"`
	Profile       string `json:"profile"`
	ConfigDir     string `json:"config_dir"`
	// Account is masked to the domain, and masked HERE rather than on
	// the way out. The text path redacts inside outf, which the JSON
	// encoder does not go through: a struct written straight to the
	// stream would carry the address in full. Masking at the collector
	// is the only place both renderers share.
	Account     *string           `json:"account"`
	Credentials statusCredentials `json:"credentials"`
	Scopes      statusScopes      `json:"scopes"`
	Settings    statusSettings    `json:"settings"`
}

// statusCredentials is the half a caller checks before starting the
// server.
type statusCredentials struct {
	// Resolved is the one field worth branching on: true means a refresh
	// token was found, false means every tool will answer [auth] until
	// `login` succeeds. It is always present, so an absent or
	// unparseable object is distinguishable from an unauthorised one —
	// which a grep for a label in the text output cannot do.
	Resolved bool `json:"resolved"`
	// TokenStore is where the token came from — "keyring", "file" or
	// "env" — and null when there is none.
	TokenStore *string `json:"token_store"`
	// Reason says why nothing resolved, and is null when something did.
	Reason              *string `json:"reason"`
	ClientSecretPath    string  `json:"client_secret_path"`
	ClientSecretPresent bool    `json:"client_secret_present"`
}

// statusScopes is what the last login was granted. Documents asks for a
// different pair under GDOCS_READ_ONLY, so a granted set that no longer
// covers the configured one is the commonest reason a working setup
// starts refusing.
type statusScopes struct {
	Granted []string `json:"granted"`
}

// statusSettings is the configuration the text output already lists.
// Durations are Go duration strings so a caller compares them rather
// than parsing prose.
type statusSettings struct {
	Preview          bool     `json:"preview"`
	WriteModes       []string `json:"write_modes"`
	DefaultWriteMode string   `json:"default_write_mode"`
	ReadOnly         bool     `json:"read_only"`
	Destructive      bool     `json:"destructive"`
	ExportDir        *string  `json:"export_dir"`
	HTTPTimeout      string   `json:"http_timeout"`
}

// newStatusReport collects the state without contacting Google.
func newStatusReport(p *profile) statusReport {
	cfg := p.cfg
	modes := make([]string, 0, 3)
	for _, m := range cfg.AvailableWriteModes() {
		modes = append(modes, string(m))
	}
	r := statusReport{
		SchemaVersion: statusSchemaVersion,
		Binary:        "google-docs-mcp",
		Version:       version.String(),
		Profile:       cfg.Profile,
		ConfigDir:     p.dir,
		Account:       orNil(redact.Account(p.user.AccountEmail)),
		Credentials: statusCredentials{
			ClientSecretPath: p.clientSecretPath,
		},
		Scopes: statusScopes{Granted: orEmpty(p.user.Scopes)},
		Settings: statusSettings{
			Preview:          cfg.Preview,
			WriteModes:       modes,
			DefaultWriteMode: string(cfg.DefaultWriteMode),
			ReadOnly:         cfg.ReadOnly,
			Destructive:      cfg.EnableDestructive,
			ExportDir:        orNil(cfg.ExportDir),
			HTTPTimeout:      cfg.HTTPTimeout.String(),
		},
	}
	if _, err := os.Stat(p.clientSecretPath); err == nil {
		r.Credentials.ClientSecretPresent = true
	}
	if _, src, err := p.store.Resolve(); err == nil {
		r.Credentials.Resolved = true
		r.Credentials.TokenStore = orNil(string(src))
	} else {
		// Through the redactor for the same reason Account is: this
		// error is formatted elsewhere and an address can arrive inside
		// one nothing here wrote, and the JSON path does not pass
		// through outf.
		r.Credentials.Reason = orNil(redact.Accounts(err.Error()))
	}
	return r
}

// writeText writes the human-readable form: the same lines, in the same
// order, that `status` has always printed.
func (r statusReport) writeText(w io.Writer) {
	outf(w, "%s\n", version.Info())
	outf(w, "profile:        %s\n", r.Profile)
	outf(w, "config dir:     %s\n", r.ConfigDir)
	outf(w, "account:        %s\n", orUnknown(deref(r.Account)))
	exists := "missing"
	if r.Credentials.ClientSecretPresent {
		exists = "present"
	}
	outf(w, "client secret:  %s (%s)\n", r.Credentials.ClientSecretPath, exists)
	if r.Credentials.Resolved {
		outf(w, "token store:    %s\n", deref(r.Credentials.TokenStore))
	} else {
		outf(w, "token store:    none (%s)\n", deref(r.Credentials.Reason))
	}
	if len(r.Scopes.Granted) > 0 {
		outf(w, "scopes:         %s\n", strings.Join(r.Scopes.Granted, " "))
	}
	outf(w, "preview:        %t\n", r.Settings.Preview)
	outf(w, "write modes:    %s (default %s)\n",
		strings.Join(r.Settings.WriteModes, "/"), r.Settings.DefaultWriteMode)
	outf(w, "read-only:      %t\n", r.Settings.ReadOnly)
	outf(w, "destructive:    %t\n", r.Settings.Destructive)
	outf(w, "export dir:     %s\n", orUnknown(deref(r.Settings.ExportDir)))
	outf(w, "http timeout:   %s\n", r.Settings.HTTPTimeout)
}

// writeJSON writes the object, indented and newline-terminated, so the
// whole of stdout is one JSON value.
func (r statusReport) writeJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	// Paths are not HTML, and an escaped ampersand in one is a path a
	// caller cannot compare against its own.
	enc.SetEscapeHTML(false)
	return enc.Encode(r)
}

// orNil turns an unset string into the JSON null that says so. An empty
// string would be a value, and a caller cannot tell a value it does not
// recognise from one that is not there.
func orNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// orEmpty keeps a list a list. A nil slice marshals as null, and a
// caller counting it has to guard for that before it can count.
func orEmpty(ss []string) []string {
	if ss == nil {
		return []string{}
	}
	return ss
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
