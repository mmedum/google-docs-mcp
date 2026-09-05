//go:build evals

// Package evals scores whether a model can actually do the job through
// these tools. Each task seeds one scratch document, runs Claude Code
// headless with only this server available, then checks the document
// through the server again and inspects the tool-call trace.
//
//	make build && go test -tags=evals ./internal/evals -v -timeout 40m
//	go test -tags=evals ./internal/evals -v -run TestEvals/replace-suggest
//
// Never part of CI: it needs a login, a working `claude` CLI, and it
// spends real API usage (roughly 10-20 cents a task; EVAL_MODEL=sonnet is
// cheaper). Traces and report.md land in $LIVE_OUT/evals. The scratch
// documents are left behind; delete them afterwards.
package evals

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const mcpName = "gdocs"

const seedDoc = `# Eval document

This document was created by google-docs-mcp evals. It is safe to delete.

## Background

Revenue grew a lot in Q3. The team shipped **three** features.

- First point
- Second point
- Nested point

## Data

Numbers go here.

## Next steps

1. Review the numbers
2. Send the summary

Closing line with a [link](https://example.com).`

// writeTools are the tools that change a document; several checks ask
// what the model reached for rather than only what it produced.
var writeTools = map[string]bool{
	"edit_document": true, "format_document": true, "edit_table": true, "insert_object": true,
	"manage_tabs": true, "delete_tab": true, "add_comment": true, "reply_comment": true,
	"delete_comment": true, "review_suggestion": true, "create_document": true,
}

// server is a client of our own binary, used to seed a document and to
// score it afterwards — never by the model under test.
type server struct {
	t   *testing.T
	cs  *mcp.ClientSession
	ctx context.Context
}

func binPath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("../../google-docs-mcp")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("build the binary first (make build): %v", err)
	}
	return p
}

func connect(t *testing.T) *server {
	t.Helper()
	cmd := exec.Command(binPath(t))
	cmd.Env = append(os.Environ(), "GDOCS_LOG_LEVEL=warn", "GDOCS_PREVIEW="+previewFlag())
	ctx := context.Background()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "evals", Version: "0"}, nil).
		Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return &server{t: t, cs: cs, ctx: ctx}
}

func previewFlag() string {
	if v := os.Getenv("GDOCS_PREVIEW"); v != "" {
		return v
	}
	return "true"
}

// must runs a tool for seeding or scoring. A failure here is the
// harness's problem, not the model's, so it stops the task.
func (s *server) must(name string, args map[string]any) (string, map[string]any) {
	s.t.Helper()
	res, err := s.cs.CallTool(s.ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		s.t.Fatalf("%s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	if res.IsError {
		s.t.Fatalf("harness step %s failed: %s", name, b.String())
	}
	sc, _ := res.StructuredContent.(map[string]any)
	return b.String(), sc
}

func (s *server) seed(name string) string {
	s.t.Helper()
	_, sc := s.must("create_document", map[string]any{
		"title": "google-docs-mcp eval " + name + " (safe to delete)", "content": seedDoc})
	doc, _ := sc["id"].(string)
	if doc == "" {
		s.t.Fatal("create_document returned no id")
	}
	return doc
}

func (s *server) read(doc string, args map[string]any) string {
	s.t.Helper()
	full := map[string]any{"document": doc, "with_handles": true}
	for k, v := range args {
		full[k] = v
	}
	text, _ := s.must("read_document", full)
	return text
}

// scraped pulls one number out of a tool's text. The wording is the
// model's contract, not ours, so a miss is a harness failure.
func (s *server) scraped(re *regexp.Regexp, text, what string) int {
	s.t.Helper()
	m := re.FindStringSubmatch(text)
	if m == nil {
		s.t.Fatalf("cannot read %s from the tool text; the wording changed:\n%s", what, clip(text, 400))
	}
	var n int
	fmt.Sscanf(m[1], "%d", &n)
	return n
}

var (
	pendingRE   = regexp.MustCompile(`(?m)^(\d+) pending suggestion`)
	footnoteRE  = regexp.MustCompile(`(\d+) footnotes`)
	tabTitleRE  = regexp.MustCompile(`(?m)^- tab \d+ "(.*?)"`)
	threadRE    = regexp.MustCompile(`\[(\S+?)\]`)
	newDocIDRE  = regexp.MustCompile(`/document/d/([A-Za-z0-9_-]{20,})`)
	bulletThird = regexp.MustCompile(`\n\[p\d+\] - Third point`)
	closingRE   = regexp.MustCompile(`\[(p\d+)\] Closing line`)
	footnoteRef = regexp.MustCompile(`Q3\.?\[\^1\]|\[\^1\].*Q3`)
	threeRE     = regexp.MustCompile(`\b(3|three)\b`)
)

func (s *server) pendingSuggestions(doc string) int {
	text, _ := s.must("list_suggestions", map[string]any{"document": doc})
	return s.scraped(pendingRE, text, "the suggestion count")
}

type docInfo struct {
	footnotes int
	tabs      []string
}

func (s *server) info(doc string) docInfo {
	text, _ := s.must("get_document", map[string]any{"document": doc})
	var tabs []string
	for _, m := range tabTitleRE.FindAllStringSubmatch(text, -1) {
		tabs = append(tabs, m[1])
	}
	if len(tabs) == 0 {
		s.t.Fatalf("cannot read the tab list from get_document; the wording changed:\n%s", clip(text, 400))
	}
	return docInfo{footnotes: s.scraped(footnoteRE, text, "the footnote count"), tabs: tabs}
}

type thread struct {
	id       string
	handle   string
	resolved bool
	content  string
	replies  []string
}

func (s *server) threads(doc string) []thread {
	text, _ := s.must("list_comments", map[string]any{"document": doc})
	var out []thread
	for _, line := range strings.Split(text, "\n")[1:] {
		switch {
		case strings.HasPrefix(line, "- "):
			head, content, _ := strings.Cut(line[2:], ": ")
			th := thread{id: strings.Fields(head)[0], resolved: strings.Contains(head, "[resolved]"), content: content}
			if m := threadRE.FindStringSubmatch(head); m != nil &&
				!strings.HasPrefix(m[1], "resolved") && !strings.HasPrefix(m[1], "deleted") {
				th.handle = m[1]
			}
			out = append(out, th)
		case strings.HasPrefix(line, "    ↳ ") && len(out) > 0:
			_, reply, _ := strings.Cut(line[6:], ": ")
			out[len(out)-1].replies = append(out[len(out)-1].replies, reply)
		}
	}
	return out
}

func clip(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
