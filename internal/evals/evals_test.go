//go:build evals

package evals

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// result is one task's outcome, kept for the report.
type result struct {
	Task     string   `json:"task"`
	Doc      string   `json:"document"`
	Passed   int      `json:"passed"`
	Total    int      `json:"total"`
	Checks   []string `json:"checks"`
	Trace    *trace   `json:"trace"`
	Failures []string `json:"failures,omitempty"`
}

var (
	mu      sync.Mutex
	results []result
)

func outDir(t *testing.T) string {
	t.Helper()
	base := os.Getenv("LIVE_OUT")
	if base == "" {
		base = filepath.Join(os.TempDir(), "google-docs-mcp-live")
	}
	dir := filepath.Join(base, "evals")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestEvals runs each task as a subtest, so -run filters tasks and a
// failure names the check that failed rather than a count.
func TestEvals(t *testing.T) {
	for _, tk := range tasks {
		t.Run(tk.name, func(t *testing.T) {
			s := connect(t)
			var doc, prompt string
			if tk.noDoc {
				prompt = tk.prompt
			} else {
				doc = s.seed(tk.name)
				t.Logf("document: https://docs.google.com/document/d/%s/edit", doc)
				if tk.setup != nil {
					tk.setup(s, doc)
				}
				if tk.promptFn != nil {
					prompt = tk.promptFn(s, doc)
				} else {
					prompt = fmt.Sprintf(tk.prompt, doc)
				}
			}

			tr := runClaude(t, prompt, tk.env)
			checks := tk.check(s, doc, tr)

			res := result{Task: tk.name, Doc: doc, Total: len(checks), Trace: tr}
			for _, c := range checks {
				mark := "fail"
				if c.ok {
					mark = "pass"
					res.Passed++
				} else {
					res.Failures = append(res.Failures, c.name+": "+c.detail)
				}
				res.Checks = append(res.Checks, mark+" "+c.name)
			}
			mu.Lock()
			results = append(results, res)
			mu.Unlock()

			raw, err := json.MarshalIndent(res, "", "  ")
			if err == nil {
				_ = os.WriteFile(filepath.Join(outDir(t), tk.name+".json"), raw, 0o644)
			}
			t.Logf("%d/%d checks, %d calls, %d turns, $%.2f, %.0fs: %s",
				res.Passed, res.Total, len(tr.Calls), tr.Turns, tr.Cost, tr.Seconds, strings.Join(tr.toolNames(), " → "))
			for _, f := range res.Failures {
				t.Errorf("%s", f)
			}
		})
	}
	writeReport(t)
}

// writeReport rebuilds report.md from whatever ran, so a filtered run
// still leaves a readable artefact.
func writeReport(t *testing.T) {
	t.Helper()
	if len(results) == 0 {
		return
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Task < results[j].Task })
	var b strings.Builder
	passed, cost := 0, 0.0
	for _, r := range results {
		if r.Passed == r.Total {
			passed++
		}
		cost += r.Trace.Cost
	}
	model := os.Getenv("EVAL_MODEL")
	if model == "" {
		model = "default"
	}
	fmt.Fprintf(&b, "# Agent eval report\n\nmodel: %s; %d/%d tasks passed; total $%.2f\n\n", model, passed, len(results), cost)
	b.WriteString("| task | result | checks | calls | turns | cost | s |\n|---|---|---|---|---|---|---|\n")
	for _, r := range results {
		verdict := "pass"
		if r.Passed != r.Total {
			verdict = "FAIL"
		}
		fmt.Fprintf(&b, "| %s | %s | %d/%d | %d | %d | %.2f | %.1f |\n",
			r.Task, verdict, r.Passed, r.Total, len(r.Trace.Calls), r.Trace.Turns, r.Trace.Cost, r.Trace.Seconds)
	}
	b.WriteString("\n## Tool use across tasks\n\n")
	for _, line := range measures() {
		b.WriteString("- " + line + "\n")
	}
	b.WriteString("\n## Tool call sequences\n\n")
	for _, r := range results {
		fmt.Fprintf(&b, "- %s: %s\n", r.Task, strings.Join(r.Trace.toolNames(), " → "))
	}
	path := filepath.Join(outDir(t), "report.md")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Errorf("write report: %v", err)
		return
	}
	t.Logf("report: %s", path)
}

// measures are the cross-task numbers that say how the tools were used,
// not whether the task passed.
func measures() []string {
	m := map[string]int{}
	order := []string{
		"read_document calls", "reads with format text", "reads that turned handles off",
		"reads whole body (no scope)", "reads with max_chars set",
		"targets by text", "targets by handle", "targets by heading", "targets by cell",
		"dry runs", "tool errors", "get_document first", "tool searches (deferred tool lookups)",
	}
	for _, k := range order {
		m[k] = 0
	}
	for _, r := range results {
		// The client loads deferred tools with its own ToolSearch calls,
		// which are not the model reaching for this server; skip them or
		// the metric measures the client and reads 0 for every task.
		var server []toolCall
		for _, c := range r.Trace.Calls {
			if c.tool() != "ToolSearch" {
				server = append(server, c)
			}
		}
		if len(server) > 0 && server[0].tool() == "get_document" {
			m["get_document first"]++
		}
		for i, c := range r.Trace.Calls {
			if c.tool() == "ToolSearch" {
				m["tool searches (deferred tool lookups)"]++
				continue
			}
			if i < len(r.Trace.Results) && r.Trace.Results[i].Error {
				m["tool errors"]++
			}
			if b, _ := c.Input["dry_run"].(bool); b {
				m["dry runs"]++
			}
			if c.tool() == "read_document" {
				m["read_document calls"]++
				if f, _ := c.Input["format"].(string); strings.EqualFold(f, "text") {
					m["reads with format text"]++
				}
				if h, ok := c.Input["with_handles"].(bool); ok && !h {
					m["reads that turned handles off"]++
				}
				if _, ok := c.Input["max_chars"]; ok {
					m["reads with max_chars set"]++
				}
				scoped := false
				for _, k := range []string{"heading", "heading_id", "from_handle", "segment", "tab", "revision"} {
					if v, ok := c.Input[k]; ok && v != "" {
						scoped = true
					}
				}
				if !scoped {
					m["reads whole body (no scope)"]++
				}
			}
			countTargets(c.Input, m)
		}
	}
	out := make([]string, 0, len(order))
	for _, k := range order {
		out = append(out, fmt.Sprintf("%s: %d", k, m[k]))
	}
	return out
}

// countTargets walks an op list and records how the model addressed
// content, which is the design question the evals exist to answer.
func countTargets(input map[string]any, m map[string]int) {
	ops, _ := input["ops"].([]any)
	for _, raw := range ops {
		op, _ := raw.(map[string]any)
		for _, key := range []string{"target", "location"} {
			t, _ := op[key].(map[string]any)
			if of, ok := t["of"].(map[string]any); ok {
				t = of
			}
			for field, metric := range map[string]string{
				"text": "targets by text", "handle": "targets by handle",
				"heading": "targets by heading", "cell": "targets by cell",
			} {
				if v, ok := t[field]; ok && v != "" {
					m[metric]++
				}
			}
		}
	}
	if _, ok := input["cells"]; ok {
		m["targets by cell"]++
	}
}
