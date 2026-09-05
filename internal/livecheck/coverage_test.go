//go:build live

package livecheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmedum/google-docs-mcp/internal/plan"
)

// exemptOps are op kinds no step names as an "op", with the reason. They
// are op kinds inside the server, but a caller reaches them through
// insert_object's action, so the driver exercises them as calls instead.
var exemptOps = map[plan.OpKind]string{
	plan.OpInsertObject: "insert_object action insert",
	plan.OpReplaceImage: "insert_object action replace",
	plan.OpDeleteObject: "insert_object action delete",
}

// TestCoverage enforces the rule the package comment states, which was a
// comment in the Python driver and stayed one through the port: every
// registered tool is driven by a step, and every op kind appears as an
// "op". It reads this package's own source, so a tool or an op added
// without a step fails here instead of going unnoticed until someone
// reads the driver — which is how style_columns, style_rows and the
// named-range ops once went months without ever running.
func TestCoverage(t *testing.T) {
	src := packageSource(t)
	d := start(t)

	tools, err := d.cs.ListTools(d.ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) == 0 {
		t.Fatal("the server registered no tools")
	}
	var missing []string
	for _, tool := range tools.Tools {
		// The name as a step passes it: d.ok("label", "read_document", …).
		if !strings.Contains(src, `"`+tool.Name+`"`) {
			missing = append(missing, tool.Name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d registered tool(s) that no step calls: %s\n%s",
			len(missing), strings.Join(missing, ", "),
			"add a step, or unregister the tool")
	}

	var uncovered []string
	for _, tool := range []plan.Tool{plan.ToolEdit, plan.ToolFormat, plan.ToolTable, plan.ToolObject, plan.ToolLayout} {
		for _, kind := range plan.KindsOf(tool) {
			if _, ok := exemptOps[kind]; ok {
				continue
			}
			if !strings.Contains(src, `"op": "`+string(kind)+`"`) {
				uncovered = append(uncovered, string(tool)+"/"+string(kind))
			}
		}
	}
	if len(uncovered) > 0 {
		t.Errorf("%d op kind(s) that no step runs: %s", len(uncovered), strings.Join(uncovered, ", "))
	}

	t.Logf("=== coverage ===\n%d tools, %d op kinds driven, %d exempt (%s)",
		len(tools.Tools), countKinds()-len(exemptOps), len(exemptOps), exemptReasons())
}

// packageSource is every Go file in this package, concatenated, with
// whole-line comments dropped: a tool named only in a comment — this
// file's own exemption list, for one — is not a step that calls it.
func packageSource(t *testing.T) string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".go" {
			continue
		}
		data, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for line := range strings.Lines(string(data)) {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			b.WriteString(line)
		}
	}
	return b.String()
}

func countKinds() int {
	n := 0
	for _, tool := range []plan.Tool{plan.ToolEdit, plan.ToolFormat, plan.ToolTable, plan.ToolObject, plan.ToolLayout} {
		n += len(plan.KindsOf(tool))
	}
	return n
}

func exemptReasons() string {
	var out []string
	for kind, why := range exemptOps {
		out = append(out, string(kind)+": "+why)
	}
	return strings.Join(out, "; ")
}
