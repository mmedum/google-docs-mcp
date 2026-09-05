//go:build live

package livecheck

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// liveResources reads the three resource templates, including the
// unencoded-space URI that must not resolve.
func liveResources(t *testing.T, d *driver, doc string) {
	tmpl, err := d.cs.ListResourceTemplates(d.ctx, nil)
	if err != nil {
		t.Fatalf("list resource templates: %v", err)
	}
	var uris []string
	for _, r := range tmpl.ResourceTemplates {
		uris = append(uris, r.URITemplate)
	}
	t.Logf("=== resource templates ===\n%v", uris)
	if len(uris) != 3 {
		t.Errorf("want three templates, got %v", uris)
	}

	read := func(label, uri string, wantErr bool) {
		t.Helper()
		res, err := d.cs.ReadResource(d.ctx, &mcp.ReadResourceParams{URI: uri})
		if wantErr {
			if err == nil {
				t.Errorf("%s should not resolve", label)
			} else {
				t.Logf("=== %s (not found, as it should be) ===", label)
			}
			return
		}
		if err != nil {
			t.Errorf("%s: %v", label, err)
			return
		}
		var b strings.Builder
		for _, c := range res.Contents {
			b.WriteString(c.Text)
		}
		t.Logf("=== %s ===\n%s", label, shown(b.String(), 500))
	}
	read("resource: whole document", "gdocs://"+doc, false)
	read("resource: outline", "gdocs://"+doc+"/outline", false)
	read("resource: one tab", "gdocs://"+doc+"/tabs/"+url.PathEscape("Appendix A"), false)
	read("resource: a tab name with an unencoded space", "gdocs://"+doc+"/tabs/Appendix A", true)
	read("resource: unknown document", "gdocs://1NoSuchDocumentIdXXXXXXXXXXXXXXXXXXXXXXXXXXX/outline", true)
}

// liveRegistration asks servers started with other configurations what
// they register. Read-only mode and the destructive gate are claims about
// what exists at all, so the check is the listing, not the flag.
func liveRegistration(t *testing.T, d *driver) {
	writeTools := []string{"create_document", "edit_document", "format_document", "edit_table", "insert_object",
		"layout_document", "manage_tabs", "add_comment", "reply_comment", "review_suggestion"}
	gated := []string{"delete_comment", "delete_tab"}

	tools := func(env ...string) []string {
		t.Helper()
		bin, _ := exec.LookPath("../../google-docs-mcp")
		if bin == "" {
			bin = "../../google-docs-mcp"
		}
		cmd := exec.Command(bin)
		cmd.Env = append(append(os.Environ(), "GDOCS_LOG_LEVEL=warn"), env...)
		ctx := context.Background()
		cs, err := mcp.NewClient(&mcp.Implementation{Name: "config", Version: "0"}, nil).
			Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
		if err != nil {
			t.Fatalf("connect with %v: %v", env, err)
		}
		defer func() { _ = cs.Close() }()
		list, err := cs.ListTools(ctx, nil)
		if err != nil {
			t.Fatalf("list tools with %v: %v", env, err)
		}
		var names []string
		for _, tool := range list.Tools {
			names = append(names, tool.Name)
		}
		slices.Sort(names)
		return names
	}

	off := tools("GDOCS_ENABLE_DESTRUCTIVE=false")
	on := tools("GDOCS_ENABLE_DESTRUCTIVE=true")
	readOnly := tools("GDOCS_READ_ONLY=true", "GDOCS_ENABLE_DESTRUCTIVE=true")
	t.Logf("=== tools by configuration ===\ndestructive off: %d, on: %d, read-only: %d", len(off), len(on), len(readOnly))

	for _, name := range gated {
		if slices.Contains(off, name) {
			t.Errorf("destructive off still registers %s", name)
		}
		if !slices.Contains(on, name) {
			t.Errorf("destructive on is missing %s", name)
		}
	}
	for _, name := range append(slices.Clone(writeTools), gated...) {
		if slices.Contains(readOnly, name) {
			t.Errorf("read-only registers the write tool %s", name)
		}
	}
	if len(readOnly) >= len(off) {
		t.Errorf("read-only should register a strict subset: %d vs %d", len(readOnly), len(off))
	}
}
