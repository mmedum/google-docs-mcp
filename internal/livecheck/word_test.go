//go:build live

package livecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-docs-mcp/v3/internal/auth"
	"github.com/mmedum/google-docs-mcp/v3/internal/credentials"
	"github.com/mmedum/google-docs-mcp/v3/internal/gapi"
	"github.com/mmedum/google-docs-mcp/v3/internal/gdocs"
	"github.com/mmedum/google-docs-mcp/v3/internal/userconfig"
)

// driveClient is a client of the signed-in account, for what no tool
// does: uploading a Word file to work on.
func driveClient(t *testing.T) *gapi.Client {
	t.Helper()
	uc, err := userconfig.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	oc, err := auth.LoadClientSecret(uc.ClientSecretPath, auth.Scopes(false))
	if err != nil {
		t.Fatal(err)
	}
	tokenFile, _ := userconfig.TokenFilePath("default")
	store := &credentials.Store{Profile: "default", Keyring: credentials.OSKeyring(), FilePath: tokenFile}
	rt, _, err := store.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	return gapi.New(auth.TokenSource(context.Background(), oc, rt, 60*time.Second), gapi.Options{Timeout: 60 * time.Second, UserAgent: "google-docs-mcp/livecheck"})
}

// TestLiveWord writes a Word file through the server in every mode and
// reads back what Docs makes of it: the text, a list item at its level,
// a tracked change as a suggestion, Word comments as comments.
func TestLiveWord(t *testing.T) {
	d := start(t)
	data, err := os.ReadFile("../ooxml/testdata/brief.docx")
	if err != nil {
		t.Fatal(err)
	}
	f, err := driveClient(t).CreateFile(context.Background(), gapi.NewFile{Name: "google-docs-mcp live word (safe to delete).docx", MimeType: gapi.WordMimeType}, gapi.WordMimeType, data)
	if err != nil {
		t.Fatal(shown(fmt.Sprintf("upload: %v", err), 300))
	}
	doc := f.ID

	if info := d.ok("word: get_document", "get_document", map[string]any{"document": doc}); !strings.Contains(info, "a Word file (.docx)") {
		t.Error(shown("get_document does not say it is a Word file:\n"+info, 900))
	}
	read := d.ok("word: read with handles", "read_document", map[string]any{"document": doc, "with_handles": true})
	second := first(regexp.MustCompile(`(?m)^\[(p\d+)\][ \t]+-[ \t]+Second point$`), read)
	if second == "" {
		t.Fatal(shown("no handle for Second point:\n"+read, 900))
	}
	edit := func(label, mode string, op map[string]any) {
		d.ok("word: "+label, "edit_document", map[string]any{"document": doc, "mode": mode, "ops": []any{op}})
	}
	edit("a sentence changed", "direct", map[string]any{"op": "replace", "target": map[string]any{"text": "Closing line."}, "content": "Closing words."})
	edit("an item nested under another", "direct", map[string]any{"op": "insert", "content": "- Deeper", "list_level": 1,
		"location": map[string]any{"at": "after", "of": map[string]any{"handle": second}}})
	edit("a word suggested", "suggest", map[string]any{"op": "replace", "target": map[string]any{"text": "Intro"}, "content": "Opening"})
	edit("a change proposed", "comment", map[string]any{"op": "replace", "target": map[string]any{"text": "Two"}, "content": "Three"})
	d.ok("word: a comment", "add_comment", map[string]any{"document": doc, "target": map[string]any{"text": "Brief"}, "content": "Live test: a Word comment."})

	raw := d.ok("word: read it back", "read_document", map[string]any{"document": doc, "format": "raw", "max_chars": 100000})
	var elems []*gdocs.StructuralElement
	if err := json.Unmarshal([]byte(raw), &elems); err != nil {
		t.Fatal(shown(fmt.Sprintf("the raw read is not the elements: %v\n%s", err, raw), 600))
	}
	var problems []string
	texts, lists := map[string]bool{}, map[string]*gdocs.Bullet{}
	inserted, deleted := "", ""
	for _, el := range elems {
		p := el.Paragraph
		if p == nil {
			continue
		}
		var b strings.Builder
		for _, e := range p.Elements {
			if r := e.TextRun; r != nil {
				b.WriteString(r.Content)
				if len(r.SuggestedInsertionIDs) > 0 {
					inserted += r.Content
				}
				if len(r.SuggestedDeletionIDs) > 0 {
					deleted += r.Content
				}
			}
		}
		text := strings.TrimSuffix(b.String(), "\n")
		texts[text] = true
		lists[text] = p.Bullet
	}
	if !texts["Closing words."] {
		problems = append(problems, "the sentence was not changed")
	}
	if dp, sp := lists["Deeper"], lists["Second point"]; dp == nil || sp == nil || dp.NestingLevel != 1 || dp.ListID != sp.ListID {
		problems = append(problems, "the nested item is not at level 1 of the list")
	}
	if inserted != "Opening" || deleted != "Intro" {
		problems = append(problems, fmt.Sprintf("the suggestion inserts %q and deletes %q, want Opening and Intro", inserted, deleted))
	}
	listed := d.ok("word: list the comments", "list_comments", map[string]any{"document": doc})
	for _, want := range []string{"Live test: a Word comment.", "Proposed change to"} {
		if !strings.Contains(listed, want) {
			problems = append(problems, "no comment "+want)
		}
	}
	if len(problems) > 0 {
		t.Error(shown(strings.Join(problems, "\n")+"\n"+raw, 3000))
	}
}
