// Package tools registers the MCP tools. Handlers validate input, call
// the service, and shape the result; every rule worth testing lives in
// the service.
package tools

import (
	"errors"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-docs-mcp/internal/config"
	"github.com/mmedum/google-docs-mcp/internal/service"
)

// Deps are what the tools need.
type Deps struct {
	Service *service.Service
	Config  config.Config
	Logger  *slog.Logger
}

// Register adds every tool the configuration allows.
func Register(s *mcp.Server, d Deps) {
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
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
	// destructive marks gated tools; the meta asks the client to involve
	// the person.
	destructive     = &mcp.ToolAnnotations{DestructiveHint: new(true), OpenWorldHint: new(false)}
	destructiveMeta = mcp.Meta{"anthropic/requiresUserInteraction": true}
)
