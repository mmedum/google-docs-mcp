package main

import (
	"io"
	"strings"
	"testing"
)

const releasedDump = `{"tools": [{
	"name": "add_comment",
	"inputSchema": {"required": ["document"], "properties": {"document": {}, "quote": {}}},
	"outputSchema": {"properties": {"id": {}, "anchored": {}}}
}]}`

func TestSchemaDiffBreaksOnALostOutputField(t *testing.T) {
	built := `{"tools": [{
		"name": "add_comment",
		"inputSchema": {"required": ["document"], "properties": {"document": {}, "quote": {}}},
		"outputSchema": {"properties": {"id": {}}}
	}]}`
	var out strings.Builder
	if !diff(&out, mustParse(t, releasedDump), mustParse(t, built)) {
		t.Fatal("dropping an output field passed the diff")
	}
	if want := "add_comment: output field removed anchored"; !strings.Contains(out.String(), want) {
		t.Errorf("the report does not name the lost field; want %q in:\n%s", want, out.String())
	}
}

func TestSchemaDiffAllowsANewOutputField(t *testing.T) {
	built := `{"tools": [{
		"name": "add_comment",
		"inputSchema": {"required": ["document"], "properties": {"document": {}, "quote": {}}},
		"outputSchema": {"properties": {"id": {}, "anchored": {}, "handle": {}}}
	}]}`
	if diff(io.Discard, mustParse(t, releasedDump), mustParse(t, built)) {
		t.Error("adding an output field failed the diff")
	}
}

func mustParse(t *testing.T, s string) *schemaDump {
	t.Helper()
	d, err := parseSchemas([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return d
}
