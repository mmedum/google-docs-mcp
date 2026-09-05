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
	"time"

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
	"the person asked for, and dry_run when unsure. Handles like p12 are valid for the revision_id they came with. " +
	"Resources gdocs://<id>, gdocs://<id>/outline and gdocs://<id>/tabs/<tab> hold the same content as markdown for " +
	"clients that attach a document whole; they carry no handles."

// Deps are what the server needs.
type Deps struct {
	Service *service.Service
	Config  config.Config
	Logger  *slog.Logger
	Version string
}

// New builds the MCP server with every tool registered.
func New(d Deps) *mcp.Server {
	// The SDK logs transport errors and one handler warning, never a
	// JSON-RPC frame (checked in v1.7.0, §18). Attach it at debug, where
	// its session chatter belongs; at info it would put two lines per
	// session into every client's log file for nothing.
	opts := &mcp.ServerOptions{Instructions: instructions}
	if d.Logger != nil && d.Logger.Enabled(context.Background(), slog.LevelDebug) {
		opts.Logger = d.Logger
	}
	s := mcp.NewServer(&mcp.Implementation{Name: Name, Version: d.Version}, opts)
	if d.Logger != nil {
		s.AddReceivingMiddleware(logCalls(d.Logger))
	}
	tools.Register(s, tools.Deps{Service: d.Service, Config: d.Config, Logger: d.Logger})
	return s
}

// logCalls records that a call happened and how it went, and nothing
// about what it carried. Until now the server logged nothing per call,
// so a bug report could say only what the person saw. The fields are the
// method, the tool name, the outcome and the duration: params and
// results are the person's document — ids, titles, the text itself — and
// a log they are asked to paste into a bug report should be safe to
// paste by construction rather than by their vigilance.
func logCalls(logger *slog.Logger) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if !logger.Enabled(ctx, slog.LevelDebug) {
				return next(ctx, method, req)
			}
			start := time.Now()
			res, err := next(ctx, method, req)
			attrs := []any{"method", method, "ms", time.Since(start).Milliseconds()}
			// The tool name is the one part of a request that is ours,
			// not the person's: it comes from this server's schema.
			if ct, ok := req.(*mcp.CallToolRequest); ok && ct.Params != nil {
				attrs = append(attrs, "tool", ct.Params.Name)
			}
			if err != nil {
				attrs = append(attrs, "failed", true)
			} else if ctr, ok := res.(*mcp.CallToolResult); ok && ctr != nil {
				attrs = append(attrs, "tool_error", ctr.IsError)
			}
			logger.Debug("mcp call", attrs...)
			return res, err
		}
	}
}

// DumpSchemas writes the tool list and the resource templates as the wire
// would carry them: the SDK has no public enumerator, so an in-memory
// client asks the server.
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
	tmpls, err := cs.ListResourceTemplates(ctx, nil)
	if err != nil {
		return fmt.Errorf("list resource templates: %w", err)
	}
	sort.Slice(tmpls.ResourceTemplates, func(i, j int) bool {
		return tmpls.ResourceTemplates[i].URITemplate < tmpls.ResourceTemplates[j].URITemplate
	})
	out := struct {
		Server            string                  `json:"server"`
		Version           string                  `json:"version"`
		SDK               string                  `json:"sdk"`
		Tools             []*mcp.Tool             `json:"tools"`
		ResourceTemplates []*mcp.ResourceTemplate `json:"resourceTemplates"`
	}{Name, version, SDKVersion, res.Tools, tmpls.ResourceTemplates}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
