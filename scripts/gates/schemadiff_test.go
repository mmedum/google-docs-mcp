package main

import (
	"os"
	"strings"
	"testing"
)

// TestTheDumpAsksForTheWholeSurface holds the rule a comment cannot.
//
// delete_comment and delete_tab register only under
// GDOCS_ENABLE_DESTRUCTIVE, so a default dump does not contain them and
// this gate could not see them change — a removed field or a new
// required one on either was invisible, on both sides, quietly. Both
// sides go through this one function, so setting it here is what makes
// the two comparable; the test is that it is set at all.
func TestTheDumpAsksForTheWholeSurface(t *testing.T) {
	src, err := os.ReadFile("schemadiff.go")
	if err != nil {
		t.Fatal(err)
	}
	fn := string(src)
	i := strings.Index(fn, "func dumpSchemas(")
	if i < 0 {
		t.Fatal("dumpSchemas has been renamed; this check is not reading it")
	}
	body := fn[i:]
	if j := strings.Index(body, "\n}\n"); j >= 0 {
		body = body[:j]
	}
	if !strings.Contains(body, "GDOCS_ENABLE_DESTRUCTIVE=true") {
		t.Error("the schema dump does not ask for the destructive tools, so the gate cannot see them " +
			"change; a tool behind a flag can lose a field or gain a required one like any other")
	}
	if !strings.Contains(body, "cmd.Env") {
		t.Error("dumpSchemas builds no environment, so whatever it sets cannot reach the binary")
	}
}
