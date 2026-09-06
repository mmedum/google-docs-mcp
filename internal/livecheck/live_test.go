//go:build live

// Package livecheck drives every tool and every op against the
// signed-in account, through the server binary over stdio, exactly as a
// client would. It is a manual check, never part of CI: it writes to
// real documents and costs real API calls.
//
//	make build && go test -tags=live ./internal/livecheck -v -timeout 20m
//
// Deletion steps run only with GDOCS_ENABLE_DESTRUCTIVE=true. Suggestion
// mode and anchored comments need Developer Preview (GDOCS_PREVIEW=true,
// the default here). The scratch document is left behind on purpose and
// its URL is printed at the end; delete it when you are done.
//
// Coverage rule when a tool or op is added: every tool in --dump-schemas
// must appear in a d.call, and every op kind in plan.Kinds in an "op".
package livecheck

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// driver is a connected client plus the transcript helpers.
type driver struct {
	t           *testing.T
	cs          *mcp.ClientSession
	ctx         context.Context
	destructive bool
	preview     bool
}

// start builds nothing and assumes `make build`: the point is to drive
// the binary a person would run.
func start(t *testing.T) *driver {
	t.Helper()
	bin, err := filepath.Abs("../../google-docs-mcp")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("build the binary first (make build): %v", err)
	}
	env := append(os.Environ(), "GDOCS_LOG_LEVEL=warn")
	if os.Getenv("GDOCS_PREVIEW") == "" {
		env = append(env, "GDOCS_PREVIEW=true")
	}
	cmd := exec.Command(bin)
	cmd.Env = env
	cmd.Stderr = os.Stderr
	ctx := context.Background()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "livecheck", Version: "0"}, nil).
		Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	d := &driver{t: t, cs: cs, ctx: ctx,
		destructive: truthy(os.Getenv("GDOCS_ENABLE_DESTRUCTIVE")),
		preview:     os.Getenv("GDOCS_PREVIEW") != "false",
	}
	return d
}

func truthy(s string) bool {
	switch strings.ToLower(s) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// call runs one tool and logs a scrubbed transcript line. It returns the
// text unredacted, so a step can parse the ids and URLs it needs out of
// it — pass it through shown before logging it anywhere — along with the
// structured result that write tools also carry, and whether the tool
// refused, which is often the point of the step.
func (d *driver) call(label, name string, args map[string]any) (string, map[string]any, bool) {
	d.t.Helper()
	res, err := d.cs.CallTool(d.ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		d.t.Fatalf("%s: protocol error: %v", label, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	text := b.String()
	d.t.Logf("=== %s (isError=%t) ===\n%s", label, res.IsError, shown(text, 900))
	sc, _ := res.StructuredContent.(map[string]any)
	return text, sc, res.IsError
}

// ok fails the test when a step that should work did not.
func (d *driver) ok(label, name string, args map[string]any) string {
	d.t.Helper()
	text, _, isErr := d.call(label, name, args)
	if isErr {
		d.t.Errorf("%s should have worked: %s", label, shown(text, 300))
	}
	return text
}

// okStruct is ok when the step's ids are needed afterwards.
func (d *driver) okStruct(label, name string, args map[string]any) (string, map[string]any) {
	d.t.Helper()
	text, sc, isErr := d.call(label, name, args)
	if isErr {
		d.t.Errorf("%s should have worked: %s", label, shown(text, 300))
	}
	return text, sc
}

// strings pulls a list of ids out of a structured result.
func strs(sc map[string]any, key string) []string {
	raw, _ := sc[key].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// str pulls one string out of a structured result.
func str(sc map[string]any, key string) string {
	s, _ := sc[key].(string)
	return s
}

// refused fails the test when a step that should be refused was not, and
// checks the refusal says why.
func (d *driver) refused(label, name string, args map[string]any, want string) string {
	d.t.Helper()
	text, _, isErr := d.call(label, name, args)
	switch {
	case !isErr:
		d.t.Errorf("%s should have been refused, got: %s", label, shown(text, 300))
	case want != "" && !strings.Contains(text, want):
		d.t.Errorf("%s refused for the wrong reason, wanted %q: %s", label, want, shown(text, 300))
	}
	return text
}

// first returns the first submatch of re in s, or "".
func first(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}
