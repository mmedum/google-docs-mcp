package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// smoke drives the built binary over stdio without credentials, twice.
//
// Two runs, because one cannot prove both things. The first writes every
// message, waits, then closes stdin, and asserts what came back: the
// server answers, lists its tools, refuses an uncredentialed call with
// the `[auth]` class rather than crashing, and offers its resource
// templates. The second closes stdin the instant the last byte is
// written and asserts only the exit code — that is how a client actually
// goes away, and it is a different path, because the server is still
// working when the pipe ends. The SDK reports that as JSON-RPC -32004
// with the EOF only in the message text, so `errors.Is(err, io.EOF)`
// never matches it and the process used to exit 1 on an ordinary
// shutdown, which every host logs as a crash.
//
// A run that sleeps before closing stdin cannot see that bug: with
// nothing in flight the exit really is 0. That is why the second run
// exists and why it must not wait.
func smoke(w io.Writer, args []string) error {
	bin, err := filepath.Abs(binOr(args))
	if err != nil {
		return err
	}
	cfg, err := os.MkdirTemp("", "gates-smoke-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(cfg) }()
	env := append(os.Environ(),
		"GDOCS_CONFIG_DIR="+filepath.Join(cfg, "cfg"),
		"GDOCS_LOG_LEVEL=error",
	)

	const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}`
	conversation := strings.Join([]string{
		initialize,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":4,"method":"resources/templates/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_document","arguments":{"document":"1SyntheticFixtureDocumentIdXXXXXXXXXXXXXXXXXX"}}}`,
	}, "\n") + "\n"

	out, _, err := drive(bin, env, conversation, time.Second)
	if err != nil {
		return fmt.Errorf("the conversation run failed: %w", err)
	}
	for _, want := range []struct{ needle, why string }{
		{`"id":1`, "no initialize response"},
		{`"name":"read_document"`, "read_document missing from tools/list"},
		{`"uriTemplate":"gdocs://{document}"`, "resource templates missing"},
	} {
		if !strings.Contains(out, want.needle) {
			return fmt.Errorf("%s:\n%s", want.why, clip(out))
		}
	}
	call := lineWith(out, `"id":3`)
	if !strings.Contains(call, `"isError":true`) {
		return fmt.Errorf("a tool call without credentials should be a tool error:\n%s", clip(out))
	}
	if !strings.Contains(call, "[auth]") {
		return fmt.Errorf("the tool error should carry the [auth] class:\n%s", clip(call))
	}

	// The abrupt close. No pause: the point is that the server is still
	// working when the pipe ends.
	if _, stderr, err := drive(bin, env, initialize+"\n", 0); err != nil {
		return fmt.Errorf("an abrupt client disconnect should exit 0: %w\n%s", err, clip(stderr))
	}

	_, err = fmt.Fprintln(w, "stdio smoke ok")
	return err
}

// drive runs the binary with the given input, closing stdin after pause,
// and fails when it exits non-zero.
func drive(bin string, env []string, input string, pause time.Duration) (stdout, stderr string, err error) {
	cmd := exec.Command(bin)
	cmd.Env = env
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", "", err
	}
	if err := cmd.Start(); err != nil {
		return "", "", err
	}
	if _, err := io.WriteString(stdin, input); err != nil {
		return "", "", err
	}
	if pause > 0 {
		time.Sleep(pause)
	}
	if err := stdin.Close(); err != nil {
		return "", "", err
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err = <-done:
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		return out.String(), errBuf.String(), errors.New("the server did not exit after stdin closed")
	}
	return out.String(), errBuf.String(), err
}

func lineWith(text, needle string) string {
	for _, l := range strings.Split(text, "\n") {
		if strings.Contains(l, needle) {
			return l
		}
	}
	return ""
}

// clip keeps a failure message readable. The smoke test drives a server
// with no credentials against a synthetic id, so nothing here can carry a
// real document — but it is bounded anyway, because an unbounded dump of
// server output into a CI log is how that stops being true.
func clip(s string) string {
	const max = 800
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
