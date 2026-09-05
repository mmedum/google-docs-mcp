package main

import (
	"context"
	"errors"
	"io"
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
	} {
		if got := clientWentAway(tc.err); got != tc.want {
			t.Errorf("%s: got %t, want %t", tc.name, got, tc.want)
		}
	}
}

func errWrap(err error) error { return errors.Join(errors.New("serving"), err) }
