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
	if !d.Config.ReadOnly {
		registerWrite(s, d)
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

func boolp(b bool) *bool { return &b }

var readOnly = &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: boolp(false)}
