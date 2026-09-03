package tools

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-docs-mcp/internal/render"
	"github.com/mmedum/google-docs-mcp/internal/service"
)

// resourceScheme is the URI scheme of this server's resources.
const resourceScheme = "gdocs://"

// registerResources adds the resource templates: a document, its outline
// and one tab, each rendered as markdown. Resources are for clients that
// attach a document whole; the read tools scope, budget and show handles.
func registerResources(s *mcp.Server, d Deps) {
	s.AddResourceTemplate(&mcp.ResourceTemplate{
		Name:        "document",
		Title:       "Google Doc as markdown",
		URITemplate: resourceScheme + "{document}",
		MIMEType:    "text/markdown",
		Description: "The whole document as markdown: every tab's body in order, headings, lists, tables and links, " +
			"without handles or pending suggestions, cut at " + fmt.Sprint(MaxMaxChars) + " characters. " +
			"The document is its id. For a scoped read with handles use read_document.",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		id, _, err := resourceParts(req.Params.URI)
		if err != nil {
			return nil, err
		}
		res, err := d.Service.ReadWhole(ctx, id, MaxMaxChars)
		if err != nil {
			return nil, resourceError(req.Params.URI, err)
		}
		header := fmt.Sprintf("<!-- %q · revision %s · %d tab(s)", res.Title, res.RevisionID, res.Tabs)
		if res.Truncated {
			header += " · truncated"
		}
		return markdownResource(req.Params.URI, header+" -->\n"+res.Text), nil
	})

	s.AddResourceTemplate(&mcp.ResourceTemplate{
		Name:        "outline",
		Title:       "Google Doc outline",
		URITemplate: resourceScheme + "{document}/outline",
		MIMEType:    "text/markdown",
		Description: "The heading tree of every tab with stable heading ids, block handles and section sizes; " +
			"the same text get_outline returns.",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		id, _, err := resourceParts(req.Params.URI)
		if err != nil {
			return nil, err
		}
		res, err := d.Service.Outline(ctx, id, "")
		if err != nil {
			return nil, resourceError(req.Params.URI, err)
		}
		return markdownResource(req.Params.URI, res.Text), nil
	})

	s.AddResourceTemplate(&mcp.ResourceTemplate{
		Name:        "tab",
		Title:       "One tab of a Google Doc as markdown",
		URITemplate: resourceScheme + "{document}/tabs/{tab}",
		MIMEType:    "text/markdown",
		Description: "One tab's body as markdown; tab is its id, title or number. Cut at " + fmt.Sprint(MaxMaxChars) + " characters.",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		id, rest, err := resourceParts(req.Params.URI)
		if err != nil {
			return nil, err
		}
		tab := strings.TrimPrefix(rest, "tabs/")
		res, err := d.Service.Read(ctx, service.ReadRequest{
			Document: id,
			Scope:    service.ReadScope{Tab: tab},
			Format:   service.FormatMarkdown,
			Options:  render.Options{MaxChars: MaxMaxChars},
		})
		if err != nil {
			return nil, resourceError(req.Params.URI, err)
		}
		return markdownResource(req.Params.URI, res.Text), nil
	})
}

// resourceParts splits a gdocs:// URI into the document id and the path
// after it, both percent-decoded. The template already matched, so a
// malformed URI is a not-found rather than an invalid-params error.
func resourceParts(uri string) (id, rest string, err error) {
	if !strings.HasPrefix(uri, resourceScheme) {
		return "", "", mcp.ResourceNotFoundError(uri)
	}
	id, rest, _ = strings.Cut(strings.TrimPrefix(uri, resourceScheme), "/")
	if id, err = url.PathUnescape(id); err != nil || id == "" {
		return "", "", mcp.ResourceNotFoundError(uri)
	}
	if rest, err = url.PathUnescape(rest); err != nil {
		return "", "", mcp.ResourceNotFoundError(uri)
	}
	return id, rest, nil
}

// resourceError maps a service failure onto the protocol: a document
// that does not exist is the spec's resource-not-found error, anything
// else carries the LLM-facing "[class] message".
func resourceError(uri string, err error) error {
	var se *service.Error
	if errors.As(err, &se) && (se.Class == "not_found" || se.Class == "invalid") {
		return mcp.ResourceNotFoundError(uri)
	}
	return fail(err)
}

func markdownResource(uri, text string) *mcp.ReadResourceResult {
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "text/markdown", Text: text}}}
}
