package main

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

// TestClientWentAway covers how every session ends. The SDK reports a
// closed connection as JSON-RPC -32004 with the EOF only as message
// text, so errors.Is(err, io.EOF) does not match it; before this was
// handled the process exited non-zero whenever a client closed stdin
// promptly, and a host reports that as a crash.
func TestClientWentAway(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"server closing, the shape the SDK actually returns", &jsonrpc.Error{Code: -32004, Message: "server is closing: EOF"}, true},
		{"client closing", &jsonrpc.Error{Code: -32003, Message: "client is closing"}, true},
		{"wrapped in context, as Run returns it", errWrap(&jsonrpc.Error{Code: -32004, Message: "server is closing: EOF"}), true},
		{"plain EOF", io.EOF, true},
		{"cancelled context", context.Canceled, true},
		{"a real protocol failure is not a disconnect", &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: "internal error"}, false},
		{"any other error", errors.New("transport exploded"), false},
		// Matching the message text instead of the code would call this a
		// clean shutdown. Silly, but it is the difference the code match
		// buys, so the test records it rather than the fix alone.
		{"an error that merely reads like the SDK's", errors.New("the server is closing time at the pub"), false},
	} {
		if got := clientWentAway(tc.err); got != tc.want {
			t.Errorf("%s: got %t, want %t", tc.name, got, tc.want)
		}
	}
}

func errWrap(err error) error { return errors.Join(errors.New("serving"), err) }

// `doctor` output is what the bug form asks people to paste into a
// public issue, so what these two drop is the whole point of them.
func TestMaskAddress(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ann.petersen@acme-corp.example", "…@acme-corp.example"},
		{"a@b.example", "…@b.example"},
		// Not an address: left alone rather than mangled, so a strange
		// value stays legible to whoever is debugging it.
		{"", ""},
		{"not-an-address", "not-an-address"},
		{"@nolocal.example", "@nolocal.example"},
		{"nodomain@", "nodomain@"},
	}
	for _, c := range cases {
		if got := maskAddress(c.in); got != c.want {
			t.Errorf("maskAddress(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMaskClientID(t *testing.T) {
	cases := []struct{ in, want string }{
		{
			// The name the Cloud console gives the file it hands you,
			// which is how the id gets printed without anyone choosing to.
			"/home/u/.config/google-docs-mcp/client_secret_123456789012-abc123def456ghi789.apps.googleusercontent.com.json",
			"/home/u/.config/google-docs-mcp/client_secret_<id>.apps.googleusercontent.com.json",
		},
		{
			// A second download, which the browser renames. The old
			// pattern required the pristine suffix and printed the id in
			// full for this one.
			"/home/u/Downloads/client_secret_123456789012-abc123.apps.googleusercontent.com (1).json",
			"/home/u/Downloads/client_secret_<id>.apps.googleusercontent.com (1).json",
		},
		{
			// A path the person chose keeps every character: the directory
			// and the file name are what make this line worth printing.
			"/home/u/secrets/gdocs-client.json",
			"/home/u/secrets/gdocs-client.json",
		},
	}
	for _, c := range cases {
		if got := maskClientID(c.in); got != c.want {
			t.Errorf("maskClientID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The finding this test exists for: masking the one Printf that formats
// the path left the id in every error some other package had already
// formatted it into — including "read client secret <path>", the
// commonest failure there is, printed three lines under the masked line.
func TestDoctorOutputHidesWhatItKnows(t *testing.T) {
	const (
		path = "/home/u/.config/gdocs/client_secret_123456789012-abc123def456.apps.googleusercontent.com.json"
		ref  = "https://docs.google.com/document/d/1SyntheticDocumentIdXXXXXXXXXXXX/edit"
	)
	hide := redactor(ref)

	lines := []string{
		"auth: read client secret " + path + ": open " + path + ": no such file or directory",
		"OAuth client JSON not found at " + path,
		`"` + ref + `" is not a Google Docs document id or URL`,
	}
	for _, line := range lines {
		got := hide(line)
		if strings.Contains(got, "123456789012-abc123def456") {
			t.Errorf("client id survived: %q", got)
		}
		if strings.Contains(got, "1SyntheticDocumentIdXXXXXXXXXXXX") {
			t.Errorf("document reference survived: %q", got)
		}
	}

	// An error naming only the base name is the case a known-value
	// replacement of the full path would have missed, which is why the
	// id is caught by shape instead.
	if got := hide("auth: open client_secret_123456789012-abc123def456.apps.googleusercontent.com.json: denied"); strings.Contains(got, "123456789012-abc123def456") {
		t.Errorf("client id survived in a base-name-only error: %q", got)
	}

	// The directory still reaches the reader: it is what makes the line
	// worth printing when someone's setup is wrong.
	if !strings.Contains(hide(lines[1]), "/home/u/.config/gdocs/") {
		t.Error("the directory should survive; it is the diagnostic part")
	}

	// An address can arrive in an error nothing here formatted: Google
	// echoes its `message` field verbatim in a 403, and it names the
	// account.
	if got := hide("googleapi: Error 403: ann.petersen@acme-corp.example does not have permission"); strings.Contains(got, "ann.petersen") {
		t.Errorf("address survived an API error: %q", got)
	}

	// Nothing to hide is not an error.
	if got := redactor("")("plain text"); got != "plain text" {
		t.Errorf("empty redactor changed %q", got)
	}
}

// Everything this command prints goes through one of four functions, and
// this is what keeps it that way.
//
// The rule exists because the alternative was tried and failed three
// times: `status`, then the startup log, then `login` each printed a
// path or an address, each was fixed on its own, and each fix taught the
// next site nothing. The last one was six lines from the one before it.
// A print added tomorrow inherits the redaction instead of needing an
// author who remembers both rules.
func TestNothingPrintsOutsideTheBoundary(t *testing.T) {
	const file = "main.go"
	// usage writes static help to a caller-supplied writer; the other
	// three are the boundary itself.
	boundary := map[string]bool{"usage": true, "fail": true, "warnStderr": true, "outf": true}

	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}

	funcs, found := 0, 0
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		funcs++
		if boundary[fn.Name.Name] {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "fmt" || !strings.HasPrefix(sel.Sel.Name, "Print") && !strings.HasPrefix(sel.Sel.Name, "Fprint") {
				return true
			}
			found++
			t.Errorf("%s writes with fmt.%s at %s — use outf or fail, so the redaction is not "+
				"something each new print has to remember", fn.Name.Name, sel.Sel.Name, fset.Position(call.Pos()))
			return true
		})
	}

	// Zero functions walked and zero stray prints are the same output.
	if funcs < 15 {
		t.Fatalf("only %d functions walked in %s; the check is not reading it", funcs, file)
	}
	t.Logf("%d functions walked, %d stray prints", funcs, found)
}
