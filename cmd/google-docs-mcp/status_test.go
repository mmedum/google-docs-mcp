package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-docs-mcp/internal/config"
	"github.com/mmedum/google-docs-mcp/internal/credentials"
	"github.com/mmedum/google-docs-mcp/internal/userconfig"
)

// The JSON path does not go through outf, which is where the text path
// redacts. A struct written straight to the encoder would carry the
// address in full, so the masking has to happen at the collector — and
// this is the test that says so. It drives newStatusReport, not
// writeJSON: asserting on a hand-built struct would only prove that a
// string put into a field comes out of it.
func TestTheCollectorMasksTheAccount(t *testing.T) {
	dir := t.TempDir()
	p := &profile{
		cfg:              config.Config{Profile: "test", HTTPTimeout: time.Minute},
		dir:              dir,
		clientSecretPath: filepath.Join(dir, "client_secret.json"),
		user:             userconfig.Config{AccountEmail: "someone@example.com"},
		store: &credentials.Store{
			Profile:  "no-such-profile-for-a-test",
			FilePath: filepath.Join(dir, "token.json"),
		},
	}
	r := newStatusReport(p)
	if r.Account == nil {
		t.Fatal("the account is absent; it was set on the profile")
	}
	if strings.Contains(*r.Account, "someone") {
		t.Errorf("the local part survived collection: %q", *r.Account)
	}
	if !strings.HasSuffix(*r.Account, "@example.com") {
		t.Errorf("the domain was lost, which is the half a diagnosis needs: %q", *r.Account)
	}

	var buf bytes.Buffer
	if err := r.writeJSON(&buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "someone@") {
		t.Errorf("the local part reached the JSON:\n%s", buf.String())
	}
}

// With no token anywhere, the collector must say so rather than leave
// the field at its zero value and let a caller guess.
func TestTheCollectorReportsAnUnresolvedCredential(t *testing.T) {
	dir := t.TempDir()
	p := &profile{
		cfg:              config.Config{Profile: "test", HTTPTimeout: time.Minute},
		dir:              dir,
		clientSecretPath: filepath.Join(dir, "client_secret.json"),
		store: &credentials.Store{
			Profile:  "no-such-profile-for-a-test",
			FilePath: filepath.Join(dir, "token.json"),
		},
	}
	r := newStatusReport(p)
	if r.Credentials.Resolved {
		t.Error("resolved is true with no token anywhere")
	}
	if r.Credentials.Reason == nil || *r.Credentials.Reason == "" {
		t.Error("nothing resolved and no reason given")
	}
	if r.Credentials.ClientSecretPresent {
		t.Error("a client secret was reported present in an empty directory")
	}
}

// A caller's whole reason for reading this is to find out whether it can
// start the server. Both answers have to be representable, and the
// unauthorised one has to say why.
func TestTheRefusalStateIsRepresentable(t *testing.T) {
	reason := "credentials: no refresh token found; run `google-docs-mcp login`"
	r := statusReport{
		SchemaVersion: statusSchemaVersion,
		Credentials:   statusCredentials{Resolved: false, Reason: &reason},
		Scopes:        statusScopes{Granted: orEmpty(nil)},
	}
	var buf bytes.Buffer
	if err := r.writeJSON(&buf); err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatalf("the output is not JSON: %v\n%s", err, buf.String())
	}
	creds, _ := back["credentials"].(map[string]any)
	if creds["resolved"] != false {
		t.Errorf("resolved = %v, want false", creds["resolved"])
	}
	if creds["token_store"] != nil {
		t.Errorf("token_store = %v, want null when nothing resolved", creds["token_store"])
	}
	if s, _ := creds["reason"].(string); !strings.Contains(s, "login") {
		t.Errorf("reason does not name the fix: %q", s)
	}
	// An absent object and an unauthorised one must be distinguishable,
	// which a grep for a label in the text output cannot do.
	if _, ok := creds["resolved"]; !ok {
		t.Error("resolved is absent; a caller cannot tell that from unauthorised")
	}
}

// A nil slice marshals as null, and a caller counting scopes has to
// guard for that before it can count.
func TestEmptyListsStayLists(t *testing.T) {
	var buf bytes.Buffer
	if err := (statusReport{Scopes: statusScopes{Granted: orEmpty(nil)}}).writeJSON(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"granted": []`) {
		t.Errorf("an empty scope list is not an empty array:\n%s", buf.String())
	}
}

// The whole of stdout has to be one JSON value, or a caller cannot parse
// it without stripping something first.
func TestTheJSONIsExactlyOneValue(t *testing.T) {
	var buf bytes.Buffer
	if err := (statusReport{SchemaVersion: statusSchemaVersion}).writeJSON(&buf); err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(&buf)
	var first any
	if err := dec.Decode(&first); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if dec.More() {
		t.Error("stdout carries more than one JSON value")
	}
}

// Paths are not HTML, and an escaped ampersand is a path a caller cannot
// compare against its own.
func TestPathsAreNotHTMLEscaped(t *testing.T) {
	var buf bytes.Buffer
	r := statusReport{Credentials: statusCredentials{ClientSecretPath: "/home/a&b/client_secret.json"}}
	if err := r.writeJSON(&buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), `\u0026`) {
		t.Errorf("the path was HTML-escaped:\n%s", buf.String())
	}
}

// One collector, two renderers: the text must keep saying what it said,
// because that is what makes the JSON worth adding rather than a second
// thing to keep in step.
func TestTextStillNamesWhatItAlwaysDid(t *testing.T) {
	store := "keyring"
	r := statusReport{
		Profile:     "default",
		ConfigDir:   "/tmp/cfg",
		Account:     orNil("…@example.com"),
		Credentials: statusCredentials{Resolved: true, TokenStore: &store, ClientSecretPath: "/tmp/cs.json"},
		Scopes:      statusScopes{Granted: []string{"a", "b"}},
		Settings:    statusSettings{WriteModes: []string{"direct"}, DefaultWriteMode: "direct", HTTPTimeout: "1m0s"},
	}
	var buf bytes.Buffer
	r.writeText(&buf)
	for _, want := range []string{
		"profile:", "config dir:", "account:", "client secret:", "token store:",
		"scopes:", "preview:", "write modes:", "read-only:", "destructive:",
		"export dir:", "http timeout:",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("the text output lost %q:\n%s", want, buf.String())
		}
	}
}
