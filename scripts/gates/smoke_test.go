package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestMain doubles as a stand-in server for the test below: with
// GATES_DYING_SERVER set, the test binary writes a panic to stderr and
// exits without reading a byte of its stdin, which is what a server
// whose init fails does.
func TestMain(m *testing.M) {
	if os.Getenv("GATES_DYING_SERVER") != "" {
		fmt.Fprintln(os.Stderr, "panic: open /nonexistent/credentials: not a directory")
		os.Exit(2)
	}
	os.Exit(m.Run())
}

// The review that found this said drive returned from a failed write
// without reaping the child and without its stderr, so a server that died
// during init reported a bare broken pipe with the panic that explained
// it discarded. The fix went in untested, which a sibling admitting the
// same gap in its own port is what made obvious.
//
// The input is deliberately larger than a pipe buffer: a small write
// lands in the buffer and succeeds even against a dead child, which would
// exercise the Wait path rather than the write path this is about.
func TestDriveReturnsWhyTheServerDied(t *testing.T) {
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	input := strings.Repeat(`{"jsonrpc":"2.0","method":"initialize"}`+"\n", 100000)
	start := time.Now()
	out, stderr, err := drive(bin, append(os.Environ(), "GATES_DYING_SERVER=1"), input, 0)
	if err == nil {
		t.Fatal("a server that exits during init should be an error")
	}
	if !strings.Contains(stderr, "panic: open") {
		t.Errorf("the child's stderr is not in the result: %q (err %v, stdout %q)", stderr, err, out)
	}
	if d := time.Since(start); d > 15*time.Second {
		t.Errorf("drive took %s, so it waited out the 20s timeout rather than the child", d)
	}
}
