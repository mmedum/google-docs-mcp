//go:build evals

package evals

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// trace is what the model did: the tool calls it made, what came back,
// and what it finally said.
type trace struct {
	Calls   []toolCall   `json:"calls"`
	Results []toolResult `json:"results"`
	Final   string       `json:"final"`
	Cost    float64      `json:"cost"`
	Turns   int          `json:"turns"`
	Seconds float64      `json:"seconds"`
	Exit    int          `json:"exit"`
	Stderr  string       `json:"stderr,omitempty"`
}

type toolCall struct {
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

type toolResult struct {
	Error bool   `json:"error"`
	Text  string `json:"text"`
}

// tool is the bare tool name, without the client's mcp__server__ prefix.
func (c toolCall) tool() string { return strings.TrimPrefix(c.Name, "mcp__"+mcpName+"__") }

func (tr *trace) callsTo(name string) []toolCall {
	var out []toolCall
	for _, c := range tr.Calls {
		if c.tool() == name {
			out = append(out, c)
		}
	}
	return out
}

func (tr *trace) writes() []toolCall {
	var out []toolCall
	for _, c := range tr.Calls {
		if writeTools[c.tool()] {
			out = append(out, c)
		}
	}
	return out
}

func (tr *trace) toolNames() []string {
	var out []string
	for _, c := range tr.Calls {
		out = append(out, c.tool())
	}
	return out
}

// runClaude drives Claude Code headless with only this server's tools,
// in a workdir of its own so the client picks up no project config.
func runClaude(t *testing.T, prompt string) *trace {
	t.Helper()
	dir := t.TempDir()
	cfg := map[string]any{"mcpServers": map[string]any{mcpName: map[string]any{
		"command": binPath(t),
		"env":     map[string]string{"GDOCS_PREVIEW": previewFlag(), "GDOCS_LOG_LEVEL": "warn"},
	}}}
	cfgPath := filepath.Join(dir, "mcp.json")
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	args := []string{"-p", prompt, "--output-format", "stream-json", "--verbose",
		"--mcp-config", cfgPath, "--strict-mcp-config",
		"--allowedTools", "mcp__" + mcpName + "__*",
		"--max-budget-usd", budget(), "--no-session-persistence"}
	if m := os.Getenv("EVAL_MODEL"); m != "" {
		args = append(args, "--model", m)
	}
	cmd := exec.Command("claude", args...)
	cmd.Dir = dir
	// A nested run inherits the parent session's CLAUDE_* variables and
	// then refuses to start; strip them.
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "CLAUDE") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "GDOCS_PREVIEW="+previewFlag())

	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	started := time.Now()
	runErr := cmd.Run()
	tr := &trace{Seconds: time.Since(started).Round(time.Millisecond / 10).Seconds()}
	if ee, ok := runErr.(*exec.ExitError); ok {
		tr.Exit = ee.ExitCode()
	} else if runErr != nil {
		t.Fatalf("claude: %v (is the CLI on PATH?)", runErr)
	}
	tr.Stderr = lastChars(stderr.String(), 2000)
	parseStream(stdout.String(), tr)
	return tr
}

func budget() string {
	if b := os.Getenv("EVAL_BUDGET_USD"); b != "" {
		return b
	}
	return "1.50"
}

// parseStream turns the stream-json transcript into the trace. Tool
// results arrive later than their calls and are matched by id.
func parseStream(out string, tr *trace) {
	byID := map[string]int{}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var ev struct {
			Type    string  `json:"type"`
			Result  string  `json:"result"`
			Cost    float64 `json:"total_cost_usd"`
			Turns   int     `json:"num_turns"`
			Message struct {
				Content []struct {
					Type      string          `json:"type"`
					ID        string          `json:"id"`
					Name      string          `json:"name"`
					Input     map[string]any  `json:"input"`
					ToolUseID string          `json:"tool_use_id"`
					IsError   bool            `json:"is_error"`
					Content   json.RawMessage `json:"content"`
				} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		for _, c := range ev.Message.Content {
			switch {
			case ev.Type == "assistant" && c.Type == "tool_use":
				byID[c.ID] = len(tr.Calls)
				tr.Calls = append(tr.Calls, toolCall{Name: c.Name, Input: c.Input})
				tr.Results = append(tr.Results, toolResult{})
			case ev.Type == "user" && c.Type == "tool_result":
				if i, ok := byID[c.ToolUseID]; ok {
					tr.Results[i] = toolResult{Error: c.IsError, Text: clipRaw(resultText(c.Content), 4000)}
				}
			}
		}
		if ev.Type == "result" {
			tr.Final, tr.Cost, tr.Turns = ev.Result, ev.Cost, ev.Turns
		}
	}
}

// resultText accepts both shapes a tool result takes: a bare string, or
// a list of content blocks.
func resultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var b strings.Builder
		for _, x := range blocks {
			b.WriteString(x.Text)
		}
		return b.String()
	}
	return ""
}

func clipRaw(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func lastChars(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
