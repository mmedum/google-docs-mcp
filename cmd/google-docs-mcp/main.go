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
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"

	"github.com/mmedum/google-docs-mcp/internal/auth"
	"github.com/mmedum/google-docs-mcp/internal/config"
	"github.com/mmedum/google-docs-mcp/internal/credentials"
	"github.com/mmedum/google-docs-mcp/internal/gapi"
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
	fmt.Fprintf(os.Stderr, "google-docs-mcp: "+format+"\n", args...)
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
	return auth.TokenSource(ctx, oc, tok), src, nil
}

func runServer(args []string) int {
	fs := flag.NewFlagSet("google-docs-mcp", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var showVersion, dumpSchemas bool
	fs.BoolVar(&showVersion, "version", false, "print version and exit")
	fs.BoolVar(&dumpSchemas, "dump-schemas", false, "print tool schemas as JSON and exit")
	cfg, err := loadConfig(fs, args)
	if showVersion {
		fmt.Println(version.Info())
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
		srv := server.New(server.Deps{Config: cfg, Logger: logger, Version: version.Version})
		if err := server.DumpSchemas(context.Background(), srv, os.Stdout, version.Version); err != nil {
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
		logger.Warn("no usable credentials; every tool will return an [auth] error until `google-docs-mcp login` succeeds", "err", err)
		ts = gapi.NoCredentials{Reason: err}
	} else {
		logger.Info("credentials resolved", "profile", cfg.Profile, "source", string(src), "account", p.user.AccountEmail)
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

	api := gapi.New(ts, gapi.Options{Logger: logger, Timeout: cfg.HTTPTimeout, UserAgent: "google-docs-mcp/" + version.Version})
	svc := service.New(api, service.Options{
		Preview: cfg.Preview, ReadOnly: cfg.ReadOnly, Destructive: cfg.EnableDestructive,
		DefaultWriteMode: cfg.DefaultWriteMode, ExportDir: cfg.ExportDir, Logger: logger,
	})
	srv := server.New(server.Deps{Service: svc, Config: cfg, Logger: logger, Version: version.Version})
	logger.Info("serving MCP over stdio", "version", version.Version, "preview", cfg.Preview, "read_only", cfg.ReadOnly, "default_write_mode", cfg.DefaultWriteMode)
	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
		return fail("server: %v", err)
	}
	logger.Info("client disconnected; exiting")
	return 0
}

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

func warnStderr(msg string) { fmt.Fprintln(os.Stderr, "warning: "+msg) }

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
		return fail("%v", err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	opts := auth.LoginOptions{Out: os.Stdout, Timeout: timeout}
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
	fmt.Printf("Logged in as %s (profile %q, token stored in %s).\n", orUnknown(email), cfg.Profile, src)
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
	fmt.Printf("Logged out of profile %q.\n", p.cfg.Profile)
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
	fmt.Println(version.Info())
	fmt.Printf("profile:         %s (%s)\n", cfg.Profile, p.dir)
	exists := "missing"
	if _, err := os.Stat(p.clientSecretPath); err == nil {
		exists = "present"
	}
	fmt.Printf("client secret:   %s (%s)\n", p.clientSecretPath, exists)
	if _, src, err := p.store.Resolve(); err == nil {
		fmt.Printf("refresh token:   stored in %s\n", src)
	} else {
		fmt.Printf("refresh token:   none (%v)\n", err)
	}
	fmt.Printf("account:         %s\n", orUnknown(p.user.AccountEmail))
	if len(p.user.Scopes) > 0 {
		fmt.Printf("scopes at login: %s\n", strings.Join(p.user.Scopes, " "))
	}
	fmt.Printf("preview:         %t\n", cfg.Preview)
	fmt.Printf("write modes:     %s (default %s)\n", joinModes(cfg.AvailableWriteModes()), cfg.DefaultWriteMode)
	fmt.Printf("read-only:       %t\n", cfg.ReadOnly)
	fmt.Printf("destructive:     %t\n", cfg.EnableDestructive)
	fmt.Printf("export dir:      %s\n", orUnknown(cfg.ExportDir))
	fmt.Printf("http timeout:    %s\n", cfg.HTTPTimeout)
}

func cmdDoctor(args []string) int {
	p, fs, code := openCommand("doctor", args, nil)
	if code != nil {
		return *code
	}
	cfg := p.cfg
	printStatus(p)
	fmt.Println()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	failed := 0
	check := func(name string, err error, detail string) {
		if err != nil {
			failed++
			fmt.Printf("✘ %s: %v\n", name, err)
			return
		}
		fmt.Printf("✔ %s%s\n", name, detail)
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

	api := gapi.New(ts, gapi.Options{Timeout: cfg.HTTPTimeout, UserAgent: "google-docs-mcp/" + version.Version})
	if u, err := api.About(ctx); err != nil {
		check("Drive API (about.get)", err, "")
	} else {
		check("Drive API (about.get)", nil, " as "+u.EmailAddress)
	}

	if fs.NArg() > 0 {
		ref := fs.Arg(0)
		svc := service.New(api, service.Options{Logger: slog.New(slog.DiscardHandler)})
		f, err := svc.Fetch(ctx, ref)
		if err != nil {
			check("Docs API (documents.get)", err, "")
		} else {
			check("Docs API (documents.get)", nil, fmt.Sprintf(" %q, %d tab(s), revision %s", f.Doc.Title, len(f.Doc.Tabs), f.Doc.RevisionID))
			_, perr := api.GetDocument(ctx, f.Doc.ID, gapi.GetOptions{SuggestionsViewMode: gapi.SuggestionsInline, CommentsViewMode: gapi.CommentsIncluded})
			switch {
			case perr == nil:
				check("Developer Preview (commentsViewMode)", nil, " accepted; GDOCS_PREVIEW=true will work")
			case cfg.Preview:
				check("Developer Preview (commentsViewMode)", perr, "")
			default:
				fmt.Printf("• Developer Preview not available for this project yet (%s); suggestion mode and anchored comments stay off\n", gapi.Class(perr))
			}
		}
	} else {
		fmt.Println("• pass a document id or URL to also test documents.get and the Developer Preview")
	}
	if failed > 0 {
		fmt.Printf("\n%d check(s) failed\n", failed)
		return 1
	}
	fmt.Println("\nall checks passed")
	return 0
}

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
