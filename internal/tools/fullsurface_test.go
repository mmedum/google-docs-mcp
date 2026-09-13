package tools

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-docs-mcp/internal/config"
)

// registeredUnder is the set of tool names a configuration registers.
func registeredUnder(t *testing.T, cfg config.Config) map[string]bool {
	t.Helper()
	ctx := context.Background()
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	Register(s, Deps{Config: cfg})

	ct, st := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, tool := range res.Tools {
		out[tool.Name] = true
	}
	return out
}

// TestFullSurfaceRegistersEverything holds the claim FullSurface's
// comment makes. --dump-schemas emits that surface, and the schema diff
// compares two dumps — so a tool missing from it is a tool whose removal
// or new required field the gate cannot report, quietly, with every
// check green.
func TestFullSurfaceRegistersEverything(t *testing.T) {
	full := registeredUnder(t, FullSurface(config.Config{}))
	if len(full) == 0 {
		t.Fatal("the full surface registers nothing, so this test is reading nothing")
	}
	for _, readOnly := range []bool{false, true} {
		for _, destructive := range []bool{false, true} {
			cfg := config.Config{ReadOnly: readOnly, EnableDestructive: destructive}
			for name := range registeredUnder(t, cfg) {
				if !full[name] {
					t.Errorf("%s registers with read_only=%v destructive=%v and is missing from the "+
						"full surface, so the schema dump would not carry it", name, readOnly, destructive)
				}
			}
		}
	}
}
