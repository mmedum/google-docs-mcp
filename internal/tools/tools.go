// Package tools registers the MCP tools. Handlers validate input, call
// the service, and shape the result; every rule worth testing lives in
// the service.
package tools

import (
	"errors"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-docs-mcp/v2/internal/config"
	"github.com/mmedum/google-docs-mcp/v2/internal/service"
)

// Deps are what the tools need.
type Deps struct {
	Service *service.Service
	Config  config.Config
	Logger  *slog.Logger

	// asking is how the tools that ask put their question to the person;
	// Register makes it.
	asking *asking
}

// FullSurface is the configuration under which every tool registers.
//
// It lives beside Register rather than in the command that dumps the
// schemas, because it has to name every gate Register reads and the
// command has no way to know when a new one appears.
//
// --dump-schemas uses it so that the schema diff compares the whole
// registrable surface on both sides. Without it the dump carried a
// default build, delete_comment and delete_tab were in neither surface,
// and a removed field or a new required one on either was invisible with
// every check green. A deployer who turned the flag on is a client
// written against that surface.
//
// TestFullSurfaceRegistersEverything holds the claim rather than this
// comment doing it: it enumerates the gate flags and requires no
// combination to register a tool this one does not.
func FullSurface(cfg config.Config) config.Config {
	cfg.ReadOnly = false
	cfg.EnableDestructive = true
	return cfg
}

// Register adds every tool the configuration allows.
func Register(s *mcp.Server, d Deps) {
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	d.asking = newAsking(d.Logger)
	s.AddReceivingMiddleware(askFailures(d.asking), interactionHint(d.asking))
	registerRead(s, d)
	registerMoreRead(s, d)
	registerCommentsRead(s, d)
	registerHistory(s, d)
	registerResources(s, d)
	if !d.Config.ReadOnly {
		registerWrite(s, d)
		registerCommentsWrite(s, d)
		registerTable(s, d)
		registerLayout(s, d)
		registerObjects(s, d)
		registerTabs(s, d)
	}
}

// text wraps a string as the tool's unstructured content.
func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// fail returns an error whose text is the LLM-facing "[class] message".
// The SDK turns a returned error into a result with isError: true.
func fail(err error) error {
	var se *service.Error
	if errors.As(err, &se) {
		return errors.New(se.Error())
	}
	return errors.New("[unexpected] " + err.Error())
}

// confirmTarget refuses a destructive call whose confirmation does not
// repeat the target. Retyping the id is a different act from setting a
// boolean, which a model can supply as easily as omit — and the refusal
// lives here rather than in the schema on purpose: a required field
// would be a breaking schema change, while a server-side refusal is the
// half a client cannot skip. §12 makes the same point about annotations.
func confirmTarget(what, target, confirm string) error {
	switch {
	case confirm == "":
		return service.Errorf("invalid", "this deletes the %s and cannot be undone through this server; "+
			"repeat the %s in confirm_%s to go ahead, after asking the person", what, what, what)
	case confirm != target:
		return service.Errorf("invalid", "confirm_%s is %q but the %s is %q; they must match, so that the "+
			"deletion names what it deletes twice", what, confirm, what, target)
	}
	return nil
}

var (
	readOnly = &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(false)}
	// writeSafe marks tools that change the document but never delete
	// beyond what the person asked and the guard allows.
	writeSafe = &mcp.ToolAnnotations{DestructiveHint: new(false), OpenWorldHint: new(false)}
	// destructive marks gated tools; the meta asks a client that cannot
	// elicit to involve the person (interactionHint drops it otherwise).
	destructive     = &mcp.ToolAnnotations{DestructiveHint: new(true), OpenWorldHint: new(false)}
	destructiveMeta = mcp.Meta{interactionKey: true}
	// localWrite marks a tool that reads Google and writes only a local
	// file, in the one directory the person configured.
	localWrite = &mcp.ToolAnnotations{DestructiveHint: new(false), OpenWorldHint: new(false)}
)
