// Command google-docs-mcp is a Model Context Protocol server for Google
// Docs. It speaks MCP over stdio to Claude Code, Claude Desktop and any
// other MCP client.
//
// Subcommands:
//
//	google-docs-mcp login    authorize a Google account (opens a browser)
//	google-docs-mcp logout   revoke and forget the stored token
//	google-docs-mcp status   show the active profile and where the token lives
//	google-docs-mcp doctor   run live checks against Google
//	google-docs-mcp          run the MCP server (default)
//	google-docs-mcp --version | --dump-schemas
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"

	"github.com/mmedum/google-docs-mcp/internal/auth"
	"github.com/mmedum/google-docs-mcp/internal/config"
	"github.com/mmedum/google-docs-mcp/internal/credentials"
	"github.com/mmedum/google-docs-mcp/internal/gapi"
	"github.com/mmedum/google-docs-mcp/internal/redact"
	"github.com/mmedum/google-docs-mcp/internal/server"
	"github.com/mmedum/google-docs-mcp/internal/service"
	"github.com/mmedum/google-docs-mcp/internal/userconfig"
	"github.com/mmedum/google-docs-mcp/internal/version"
)

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "login":
			os.Exit(cmdLogin(os.Args[2:]))
		case "logout":
			os.Exit(cmdLogout(os.Args[2:]))
		case "status":
			os.Exit(cmdStatus(os.Args[2:]))
		case "doctor":
			os.Exit(cmdDoctor(os.Args[2:]))
		case "help", "-h", "--help":
			usage(os.Stdout)
			return
		}
	}
	os.Exit(runServer(os.Args[1:]))
}

func usage(w io.Writer) {
	_, _ = fmt.Fprint(w, `google-docs-mcp — MCP server for Google Docs

Usage:
  google-docs-mcp                 run the MCP server over stdio
  google-docs-mcp login           authorize a Google account
  google-docs-mcp logout          revoke and delete the stored token
  google-docs-mcp status          show profile, token location, settings
  google-docs-mcp doctor [DOC]    live checks; DOC is an id or URL to read
  google-docs-mcp --version
  google-docs-mcp --dump-schemas  print tool schemas as JSON

Settings come from GDOCS_* environment variables; every subcommand also
accepts the matching flags (run one with -h).
`)
}

func fail(format string, args ...any) int {
	fmt.Fprintln(os.Stderr, "google-docs-mcp: "+redactText(fmt.Sprintf(format, args...)))
	return 1
}

// profile bundles the per-profile state the commands share.
type profile struct {
	cfg              config.Config
	dir              string
	clientSecretPath string
	user             userconfig.Config
	hasConfig        bool // a config file existed for the profile
	store            *credentials.Store
}

func loadConfig(fs *flag.FlagSet, args []string) (config.Config, error) {
	settings := config.Define(fs, os.Getenv)
	if err := fs.Parse(args); err != nil {
		return config.Config{}, err
	}
	return settings.Build()
}

func openProfile(cfg config.Config, warn func(string)) (*profile, error) {
	dir, err := userconfig.ProfileDir(cfg.Profile)
	if err != nil {
		return nil, err
	}
	p := &profile{cfg: cfg, dir: dir}
	p.user, err = userconfig.Load(cfg.Profile)
	if err != nil && !errors.Is(err, userconfig.ErrNotFound) {
		return nil, err
	}
	p.hasConfig = err == nil
	switch {
	case cfg.ClientSecretPath != "":
		p.clientSecretPath = cfg.ClientSecretPath
	case p.user.ClientSecretPath != "":
		p.clientSecretPath = p.user.ClientSecretPath
	default:
		p.clientSecretPath, err = userconfig.DefaultClientSecretPath(cfg.Profile)
		if err != nil {
			return nil, err
		}
	}
	tokenFile, err := userconfig.TokenFilePath(cfg.Profile)
	if err != nil {
		return nil, err
	}
	p.store = &credentials.Store{Profile: cfg.Profile, Keyring: credentials.OSKeyring(), FilePath: tokenFile, Warn: warn}
	return p, nil
}

// tokenSource builds the refresh-token-backed source, or reports why not.
func (p *profile) tokenSource(ctx context.Context) (oauth2.TokenSource, credentials.Source, error) {
	oc, err := auth.LoadClientSecret(p.clientSecretPath, auth.Scopes(p.cfg.ReadOnly))
	if err != nil {
		return nil, "", err
	}
	tok, src, err := p.store.Resolve()
	if err != nil {
		return nil, "", err
	}
	return auth.TokenSource(ctx, oc, tok, p.cfg.HTTPTimeout), src, nil
}

func runServer(args []string) int {
	fs := flag.NewFlagSet("google-docs-mcp", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var showVersion, dumpSchemas bool
	fs.BoolVar(&showVersion, "version", false, "print version and exit")
	fs.BoolVar(&dumpSchemas, "dump-schemas", false, "print tool schemas as JSON and exit")
	cfg, err := loadConfig(fs, args)
	if showVersion {
		outf("%s\n", version.Info())
		return 0
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return fail("%v", err)
	}
	logger := config.NewLogger(cfg, os.Stderr)
	slog.SetDefault(logger)

	if dumpSchemas {
		srv := server.New(server.Deps{Config: cfg, Logger: logger, Version: version.String()})
		if err := server.DumpSchemas(context.Background(), srv, os.Stdout, version.String()); err != nil {
			return fail("dump schemas: %v", err)
		}
		return 0
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	p, err := openProfile(cfg, func(msg string) { logger.Warn(msg) })
	if err != nil {
		return fail("%v", err)
	}
	ts, src, err := p.tokenSource(ctx)
	if err != nil {
		// Masked: this err comes from LoadClientSecret, which formats the
		// client secret path into it, and the bug form invites people to
		// paste the debug log. The same hole the `doctor` fix closed,
		// six lines above the fix.
		logger.Warn("no usable credentials; every tool will return an [auth] error until `google-docs-mcp login` succeeds", "err", redactText(err.Error()))
		ts = gapi.NoCredentials{Reason: err}
	} else {
		// Masked for the same reason as `status`: docs/security.md tells
		// people a debug log is safe to attach to a bug report, and this
		// line runs at Info on every start.
		logger.Info("credentials resolved", "profile", cfg.Profile, "source", string(src), "account", redactText(p.user.AccountEmail))
		// Warm the token off the startup path; the token source is safe
		// for concurrent use and the first tool call reuses the result.
		go func() {
			if _, err := ts.Token(); err != nil {
				logger.Warn("credential check failed; tools will return [auth] errors until `google-docs-mcp login` succeeds", "err", err)
			} else {
				logger.Info("credential check ok")
			}
		}()
	}

	api := gapi.New(ts, gapi.Options{Logger: logger, Timeout: cfg.HTTPTimeout, UserAgent: "google-docs-mcp/" + version.String()})
	svc := service.New(api, service.Options{
		Preview: cfg.Preview, ReadOnly: cfg.ReadOnly, Destructive: cfg.EnableDestructive,
		DefaultWriteMode: cfg.DefaultWriteMode, ExportDir: cfg.ExportDir, Logger: logger,
	})
	srv := server.New(server.Deps{Service: svc, Config: cfg, Logger: logger, Version: version.String()})
	logger.Info("serving MCP over stdio", "version", version.String(), "preview", cfg.Preview, "read_only", cfg.ReadOnly, "default_write_mode", cfg.DefaultWriteMode)
	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil && !clientWentAway(err) {
		return fail("server: %v", err)
	}
	logger.Info("client disconnected; exiting")
	return 0
}

// clientWentAway reports whether the server stopped because the client
// closed the connection, which is how every session ends. The SDK wraps
// the reason as JSON-RPC error -32004 ("server is closing") with the EOF
// only as text, so errors.Is(err, io.EOF) does not match it and the
// process would exit non-zero on an ordinary shutdown — which a host
// then reports as a crash.
func clientWentAway(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
		return true
	}
	var wire *jsonrpc.Error
	if errors.As(err, &wire) {
		return wire.Code == closingCode || wire.Code == clientClosingCode
	}
	return false
}

// The SDK's closing codes. They live in an internal package, but
// jsonrpc.Error is the same type, so the codes can be compared directly
// rather than sniffed out of the message text.
const (
	closingCode       = -32004 // server is closing
	clientClosingCode = -32003 // client is closing
)

// openCommand parses a subcommand's flags, loads the configuration and
// opens the profile. A non-nil exit code means the caller should return it.
func openCommand(name string, args []string, define func(*flag.FlagSet)) (*profile, *flag.FlagSet, *int) {
	fs := flag.NewFlagSet("google-docs-mcp "+name, flag.ContinueOnError)
	if define != nil {
		define(fs)
	}
	cfg, err := loadConfig(fs, args)
	if err != nil {
		code := 1
		if errors.Is(err, flag.ErrHelp) {
			code = 0
		} else {
			fail("%v", err)
		}
		return nil, fs, &code
	}
	p, err := openProfile(cfg, warnStderr)
	if err != nil {
		code := fail("%v", err)
		return nil, fs, &code
	}
	return p, fs, nil
}

func warnStderr(msg string) { fmt.Fprintln(os.Stderr, "warning: "+redactText(msg)) }

func cmdLogin(args []string) int {
	var noBrowser bool
	var timeout time.Duration
	p, _, code := openCommand("login", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&noBrowser, "no-browser", false, "print the URL instead of opening a browser")
		fs.DurationVar(&timeout, "timeout", 5*time.Minute, "how long to wait for the browser")
	})
	if code != nil {
		return *code
	}
	cfg := p.cfg
	if _, err := os.Stat(p.clientSecretPath); err != nil {
		return fail("OAuth client JSON not found at %s\nCreate a Desktop-app OAuth client in the Google Cloud console, download its JSON, and either place it there or pass --client-secret PATH (GDOCS_CLIENT_SECRET).", p.clientSecretPath)
	}
	scopes := auth.Scopes(cfg.ReadOnly)
	oc, err := auth.LoadClientSecret(p.clientSecretPath, scopes)
	if err != nil {
		// `login` is where this actually fails — bad JSON, the wrong
		// client type, permissions — so it is the likelier route to a
		// pasted client id than the `doctor` path that was fixed first.
		return fail("%v", err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	opts := auth.LoginOptions{Out: os.Stdout, Timeout: timeout, HTTPTimeout: cfg.HTTPTimeout}
	if noBrowser {
		opts.OpenBrowser = func(string) error { return errors.New("--no-browser") }
	}
	tok, err := auth.Login(ctx, oc, opts)
	if err != nil {
		return fail("login failed: %v", err)
	}
	src, err := p.store.Save(tok.RefreshToken)
	if err != nil {
		return fail("store token: %v", err)
	}
	email := ""
	if info, err := auth.Inspect(ctx, nil, tok.AccessToken); err == nil {
		email = info.Email
		if missing := auth.HasScopes(info.Scopes, scopes); len(missing) > 0 {
			warnStderr("Google granted fewer scopes than requested; missing: " + strings.Join(missing, ", ") + ". Re-run login and approve every checkbox.")
		}
	}
	if email == "" {
		api := gapi.New(oauth2.StaticTokenSource(tok), gapi.Options{Timeout: 30 * time.Second})
		if u, err := api.About(ctx); err == nil {
			email = u.EmailAddress
		}
	}
	p.user.ClientSecretPath = p.clientSecretPath
	p.user.AccountEmail = email
	p.user.TokenStore = string(src)
	p.user.Scopes = scopes
	if err := userconfig.Save(cfg.Profile, p.user); err != nil {
		return fail("save profile: %v", err)
	}
	outf("Logged in as %s (profile %q, token stored in %s).\n", orUnknown(email), cfg.Profile, src)
	return 0
}

func cmdLogout(args []string) int {
	p, _, code := openCommand("logout", args, nil)
	if code != nil {
		return *code
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Only the stored token is revoked and removed; an environment
	// override is outside this command's reach.
	if tok, _, err := p.store.ResolveStored(); err == nil {
		if err := auth.Revoke(ctx, nil, tok); err != nil {
			warnStderr(fmt.Sprintf("could not revoke the token at Google (%v); it is still deleted locally", err))
		}
	}
	if os.Getenv(credentials.EnvVar) != "" {
		warnStderr(credentials.EnvVar + " is set; logout cannot remove or revoke it")
	}
	if err := p.store.Delete(); err != nil {
		return fail("%v", err)
	}
	if p.hasConfig {
		p.user.AccountEmail, p.user.TokenStore, p.user.Scopes = "", "", nil
		if err := userconfig.Save(p.cfg.Profile, p.user); err != nil {
			return fail("save profile: %v", err)
		}
	}
	outf("Logged out of profile %q.\n", p.cfg.Profile)
	return 0
}

func cmdStatus(args []string) int {
	p, _, code := openCommand("status", args, nil)
	if code != nil {
		return *code
	}
	printStatus(p)
	return 0
}

func printStatus(p *profile) {
	cfg := p.cfg
	outf("%s\n", version.Info())
	outf("profile:         %s (%s)\n", cfg.Profile, p.dir)
	exists := "missing"
	if _, err := os.Stat(p.clientSecretPath); err == nil {
		exists = "present"
	}
	outf("client secret:   %s (%s)\n", p.clientSecretPath, exists)
	if _, src, err := p.store.Resolve(); err == nil {
		outf("refresh token:   stored in %s\n", src)
	} else {
		outf("refresh token:   none (%v)\n", err)
	}
	outf("account:         %s\n", orUnknown(p.user.AccountEmail))
	if len(p.user.Scopes) > 0 {
		outf("scopes at login: %s\n", strings.Join(p.user.Scopes, " "))
	}
	outf("preview:         %t\n", cfg.Preview)
	outf("write modes:     %s (default %s)\n", joinModes(cfg.AvailableWriteModes()), cfg.DefaultWriteMode)
	outf("read-only:       %t\n", cfg.ReadOnly)
	outf("destructive:     %t\n", cfg.EnableDestructive)
	outf("export dir:      %s\n", orUnknown(cfg.ExportDir))
	outf("http timeout:    %s\n", cfg.HTTPTimeout)
}

func cmdDoctor(args []string) int {
	p, fs, code := openCommand("doctor", args, nil)
	if code != nil {
		return *code
	}
	cfg := p.cfg
	printStatus(p)
	outf("\n")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	failed := 0
	// Errors are redacted too, not just the lines this command formats
	// itself. Masking the one Printf that prints the path was not
	// enough: the path reached the output again inside an error another
	// package had formatted — `auth: read client secret <path>`, on the
	// commonest failure there is, three lines under the masked line.
	ref := ""
	if fs.NArg() > 0 {
		ref = fs.Arg(0)
	}
	hide := redactor(ref)
	check := func(name string, err error, detail string) {
		if err != nil {
			failed++
			outf("✘ %s: %v\n", name, hide(err.Error()))
			return
		}
		outf("✔ %s%s\n", name, detail)
	}

	ts, _, err := p.tokenSource(ctx)
	check("credentials found", err, "")
	if err != nil {
		return 1
	}
	tok, err := ts.Token()
	check("refresh token exchange", err, "")
	if err != nil {
		return 1
	}
	wanted := auth.Scopes(cfg.ReadOnly)
	if info, err := auth.Inspect(ctx, nil, tok.AccessToken); err != nil {
		check("token inspection", err, "")
	} else if missing := auth.HasScopes(info.Scopes, wanted); len(missing) > 0 {
		check("granted scopes", fmt.Errorf("missing %s; re-run login", strings.Join(missing, ", ")), "")
	} else {
		check("granted scopes", nil, fmt.Sprintf(" (%d scopes, token valid %s)", len(info.Scopes), info.ExpiresIn.Round(time.Second)))
	}

	api := gapi.New(ts, gapi.Options{Timeout: cfg.HTTPTimeout, UserAgent: "google-docs-mcp/" + version.String()})
	// The address is masked but not dropped. `status` prints the account
	// from the profile file, written at the last login; this line is the
	// only one that says whose token is actually being used, which
	// differs whenever GDOCS_REFRESH_TOKEN overrides the stored one or
	// the profile is stale. Dropping it answered "am I signed in as the
	// right account?" with the unchecked half.
	if u, err := api.About(ctx); err != nil {
		check("Drive API (about.get)", err, "")
	} else {
		check("Drive API (about.get)", nil, " as "+u.EmailAddress)
	}

	if ref != "" {
		svc := service.New(api, service.Options{Logger: slog.New(slog.DiscardHandler)})
		f, err := svc.Fetch(ctx, ref)
		if err != nil {
			check("Docs API (documents.get)", err, "")
		} else {
			// Counts, not values. The title is content from a real
			// document and the revision id is an identifier, and this
			// output is what the bug form asks people to paste. Not
			// formatted at all rather than formatted and masked, so
			// there is no value path for a later edit to widen.
			check("Docs API (documents.get)", nil, fmt.Sprintf(" %d tab(s), revision present", len(f.Doc.Tabs)))
			_, perr := api.GetDocument(ctx, f.Doc.ID, gapi.GetOptions{SuggestionsViewMode: gapi.SuggestionsInline, CommentsViewMode: gapi.CommentsIncluded})
			switch {
			case perr == nil:
				check("Developer Preview (commentsViewMode)", nil, " accepted; GDOCS_PREVIEW=true will work")
			case cfg.Preview:
				check("Developer Preview (commentsViewMode)", perr, "")
			default:
				outf("• Developer Preview not available for this project yet (%s); suggestion mode and anchored comments stay off\n", gapi.Class(perr))
			}
		}
	} else {
		outf("• pass a document id or URL to also test documents.get and the Developer Preview\n")
	}
	if failed > 0 {
		outf("\n%d check(s) failed\n", failed)
		return 1
	}
	outf("\nall checks passed\n")
	return 0
}

// redactor returns the function `doctor` passes its error lines
// through. The two halves are caught differently on purpose: a client id
// has a shape, so maskClientID finds it anywhere — including in an error
// that named only the file's base name, which a known-value replacement
// of the full path would have missed — while a document reference the
// caller typed has no shape at all, so the only way to catch it is that
// we are holding the value.
// redactText is the one place this command removes what must not be
// printed. Every stream it writes goes through it.
//
// It exists because masking at each call site does not hold: three
// separate discoveries of one rule — the status line, then the startup
// log, then `login` — each got a different subset of it, and the newest
// insight (an address can arrive inside an error nothing here formatted,
// which is how a 403 names the account) reached only the newest site. A
// print added tomorrow is safe by default now; before, it was safe only
// if its author remembered both rules.
func redactText(s string) string {
	s = redact.Address.ReplaceAllStringFunc(s, maskAddress)
	return maskClientID(s)
}

// redactor adds what only one command knows: the document reference the
// caller typed, which has no shape and so cannot be caught by a pattern.
func redactor(ref string) func(string) string {
	return func(s string) string {
		if ref != "" {
			s = strings.ReplaceAll(s, ref, "<ref>")
		}
		return redactText(s)
	}
}

// outf and failf are the two streams. Nothing in this file writes to
// stdout or stderr except through them.
func outf(format string, args ...any) {
	fmt.Print(redactText(fmt.Sprintf(format, args...)))
}

// maskAddress keeps the domain and drops the local part, because these
// two halves answer different questions. `doctor` output is what the bug
// form asks people to paste into a public issue, so the address itself —
// the part that identifies a person, and that can be correlated or
// spammed — must not survive. But the reason anyone reads this line is
// "am I signed in as the right account?", and for someone with a work
// and a personal Google account the domain is precisely what answers it.
// Keeping the domain does mean a company domain appears in pasted
// output; that is the deliberate half of the trade, not an oversight.
func maskAddress(addr string) string {
	local, domain, ok := strings.Cut(addr, "@")
	if !ok || local == "" || domain == "" {
		return addr
	}
	return "…@" + domain
}

// maskClientID removes a Google OAuth client id from a path. Nobody
// chose to print a client id: the Cloud console names the file it hands
// you after the client, so printing where the client secret lives prints
// the id with it. The directory and the file's shape are what make the
// line useful when someone's setup is wrong, and both survive.
func maskClientID(path string) string {
	return clientIDInName.ReplaceAllString(path, "client_secret_<id>")
}

// Only the id span is replaced, because the file does not always keep
// the name the console gave it: a second download becomes
// "… (1).json" and people rename them. Requiring the full
// ".apps.googleusercontent.com.json" suffix left every variant printing
// the id in full.
var clientIDInName = regexp.MustCompile(`client_secret_[0-9]+-[A-Za-z0-9_-]+`)

func orUnknown(s string) string {
	if s == "" {
		return "(unset)"
	}
	return s
}

func joinModes(ms []config.WriteMode) string {
	parts := make([]string, len(ms))
	for i, m := range ms {
		parts[i] = string(m)
	}
	return strings.Join(parts, "/")
}
