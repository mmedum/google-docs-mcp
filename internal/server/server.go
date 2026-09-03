// Package server wires the MCP SDK to the tools and offers a schema dump
// through an in-memory client session.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-docs-mcp/internal/config"
	"github.com/mmedum/google-docs-mcp/internal/service"
	"github.com/mmedum/google-docs-mcp/internal/tools"
)

// Name is the MCP server name.
const Name = "google-docs-mcp"

// SDKVersion is recorded in schema dumps so a diff caused by an SDK
// upgrade can be told apart from a tool-surface change.
const SDKVersion = "v1.7.0"

const instructions = "Google Docs tools. Start with get_document (metadata, capabilities, default write mode) or " +
	"get_outline (headings with stable heading_id and block handles), then read_document scoped to a section; avoid " +
	"reading long documents whole. Edit with edit_document and format_document: target content by quoting exact text, " +
	"by heading_id, or by handle; never by position. Every write takes mode suggest, direct or comment; use the mode " +
	"the person asked for, and dry_run when unsure. Handles like p12 are valid for the revision_id they came with."

// Deps are what the server needs.
type Deps struct {
	Service *service.Service
	Config  config.Config
	Logger  *slog.Logger
	Version string
}

// New builds the MCP server with every tool registered.
func New(d Deps) *mcp.Server {
	opts := &mcp.ServerOptions{Instructions: instructions}
	if d.Logger != nil && d.Logger.Enabled(context.Background(), slog.LevelDebug) {
		opts.Logger = d.Logger
	}
	s := mcp.NewServer(&mcp.Implementation{Name: Name, Version: d.Version}, opts)
	tools.Register(s, tools.Deps{Service: d.Service, Config: d.Config, Logger: d.Logger})
	return s
}

// DumpSchemas writes the tool list as the wire would carry it: the SDK
// has no public tool enumerator, so an in-memory client asks the server.
func DumpSchemas(ctx context.Context, s *mcp.Server, w io.Writer, version string) error {
	ct, st := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		return fmt.Errorf("connect server: %w", err)
	}
	defer func() { _ = ss.Close() }()
	client := mcp.NewClient(&mcp.Implementation{Name: "schema-dump", Version: version}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		return fmt.Errorf("connect client: %w", err)
	}
	defer func() { _ = cs.Close() }()
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		return fmt.Errorf("list tools: %w", err)
	}
	sort.Slice(res.Tools, func(i, j int) bool { return res.Tools[i].Name < res.Tools[j].Name })
	out := struct {
		Server  string      `json:"server"`
		Version string      `json:"version"`
		SDK     string      `json:"sdk"`
		Tools   []*mcp.Tool `json:"tools"`
	}{Name, version, SDKVersion, res.Tools}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
