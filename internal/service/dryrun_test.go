package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmedum/google-docs-mcp/v4/internal/config"
	"github.com/mmedum/google-docs-mcp/v4/internal/gapi"
)

// sent lists every write the fake recorded, by kind, so a dry run that
// leaks any of them names it.
func sent(api *fakeAPI) []string {
	var out []string
	for kind, n := range map[string]int{"batchUpdate": len(api.batches), "createDocument": len(api.created),
		"createReply": len(api.posted), "update": len(api.edited), "delete": len(api.deleted),
		"upload": len(api.uploads), "createFile": len(api.createdMeta)} {
		if n > 0 {
			out = append(out, kind)
		}
	}
	if api.exported != "" {
		out = append(out, "export "+api.exported)
	}
	return out
}

func TestDryRunSendsNothing(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		svc  func(t *testing.T) (*Service, *fakeAPI)
		run  func(svc *Service) (dryRun bool, text string, err error)
	}{
		{"manage_tabs add", newWritable, func(svc *Service) (bool, string, error) {
			res, err := svc.ManageTabs(ctx, TabRequest{Document: fixtureID, Action: "add", Title: "Appendix", Content: "Hello tab", DryRun: true})
			return tabDry(res, err)
		}},
		{"manage_tabs rename", newWritable, func(svc *Service) (bool, string, error) {
			res, err := svc.ManageTabs(ctx, TabRequest{Document: fixtureID, Action: "rename", Tab: "Notes", Title: "Notes 2", DryRun: true})
			return tabDry(res, err)
		}},
		{"manage_tabs move", newWritable, func(svc *Service) (bool, string, error) {
			res, err := svc.ManageTabs(ctx, TabRequest{Document: fixtureID, Action: "move", Tab: "Notes", Position: 1, DryRun: true})
			return tabDry(res, err)
		}},
		{"reply_comment reply", commentService, func(svc *Service) (bool, string, error) {
			return replyDry(svc.Reply(ctx, ReplyRequest{Document: fixtureID, CommentID: "c1", Content: "Agreed.", DryRun: true}))
		}},
		{"reply_comment resolve", commentService, func(svc *Service) (bool, string, error) {
			return replyDry(svc.Reply(ctx, ReplyRequest{Document: fixtureID, CommentID: "c1", Action: "resolve", DryRun: true}))
		}},
		{"reply_comment reopen", commentService, func(svc *Service) (bool, string, error) {
			return replyDry(svc.Reply(ctx, ReplyRequest{Document: fixtureID, CommentID: "c2", Action: "reopen", DryRun: true}))
		}},
		{"reply_comment edit", commentService, func(svc *Service) (bool, string, error) {
			return replyDry(svc.Reply(ctx, ReplyRequest{Document: fixtureID, CommentID: "c2", ReplyID: "r1", Action: "edit", Content: "Fixed.", DryRun: true}))
		}},
		{"reply_comment suggestion", func(t *testing.T) (*Service, *fakeAPI) {
			svc, api := writable(t, true)
			api.raw = withSuggestionThread(t, api.raw)
			return svc, api
		}, func(svc *Service) (bool, string, error) {
			return replyDry(svc.Reply(ctx, ReplyRequest{Document: fixtureID, SuggestionID: "s1", Content: "It reads better.", DryRun: true}))
		}},
		{"reply_comment markdown file", func(t *testing.T) (*Service, *fakeAPI) { return mdService(t, config.WriteDirect) }, func(svc *Service) (bool, string, error) {
			return replyDry(svc.Reply(ctx, ReplyRequest{Document: fixtureID, CommentID: "AAAeditor", Content: "Agreed.", DryRun: true}))
		}},
		{"create_document document", newWritable, func(svc *Service) (bool, string, error) {
			return createDry(svc.Create(ctx, CreateRequest{Title: "Plan", Content: "# Plan\n\nA line.", DryRun: true}))
		}},
		{"create_document markdown file", newWritable, func(svc *Service) (bool, string, error) {
			return createDry(svc.Create(ctx, CreateRequest{Title: "Plan", Kind: KindMarkdownFile, Content: "# Plan\n", DryRun: true}))
		}},
		{"create_document working copy", func(t *testing.T) (*Service, *fakeAPI) { return mdService(t, config.WriteDirect) }, func(svc *Service) (bool, string, error) {
			return createDry(svc.Create(ctx, CreateRequest{FromFile: fixtureID, DryRun: true}))
		}},
		{"export_document binary", exportService, func(svc *Service) (bool, string, error) {
			res, err := svc.Export(ctx, ExportRequest{Document: fixtureID, Format: "pdf", DryRun: true})
			if err != nil {
				return false, "", err
			}
			if entries, _ := os.ReadDir(svc.opts.ExportDir); len(entries) != 0 || res.Path == "" {
				return false, res.Text, nil
			}
			return res.DryRun, res.Text, nil
		}},
		{"export_document inline", exportService, func(svc *Service) (bool, string, error) {
			res, err := svc.Export(ctx, ExportRequest{Document: fixtureID, Format: "md", DryRun: true})
			if err != nil {
				return false, "", err
			}
			return res.DryRun, res.Text, nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, api := tc.svc(t)
			dry, text, err := tc.run(svc)
			if err != nil {
				t.Fatalf("dry run: %v", err)
			}
			if got := sent(api); len(got) != 0 {
				t.Errorf("dry run sent %v, want nothing", got)
			}
			if !dry || !strings.HasPrefix(text, "dry run: ") {
				t.Errorf("result dry_run = %v, text %q, want a dry run that says so", dry, text)
			}
		})
	}
}

// A dry run refuses what the call would refuse, since it runs the same
// checks.
func TestDryRunRefusesWhatTheCallRefuses(t *testing.T) {
	ctx := context.Background()
	svc, _ := commentService(t)
	if _, err := svc.Reply(ctx, ReplyRequest{Document: fixtureID, CommentID: "c2", Action: "resolve", DryRun: true}); classOf(err) != "invalid" {
		t.Errorf("dry run resolving a resolved thread: %v, want [invalid]", err)
	}
	if _, err := svc.Reply(ctx, ReplyRequest{Document: fixtureID, CommentID: "c2", ReplyID: "r9", Action: "edit", Content: "x", DryRun: true}); classOf(err) != "not_found" {
		t.Errorf("dry run editing a missing reply: %v, want [not_found]", err)
	}
	if _, err := svc.Create(ctx, CreateRequest{Title: "x", Kind: KindMarkdownFile, Content: "<!-- gdoc_comment_begin: c1 -->", DryRun: true}); classOf(err) != "invalid" {
		t.Errorf("dry run creating a Markdown file with comment markup: %v, want [invalid]", err)
	}
	if _, err := svc.Create(ctx, CreateRequest{Title: "x", Content: "one<br>two <b>bold</b>", DryRun: true}); classOf(err) != "unsupported" {
		t.Errorf("dry run creating a document with content Docs cannot take: %v, want [unsupported]", err)
	}
	svc, _ = newWritable(t)
	if _, err := svc.Export(ctx, ExportRequest{Document: fixtureID, Format: "pdf", DryRun: true}); classOf(err) != "unavailable" {
		t.Errorf("dry run of a binary export with no export dir: %v, want [unavailable]", err)
	}
	svc, _ = wordService(t)
	for _, dry := range []bool{true, false} {
		if _, err := svc.Export(ctx, ExportRequest{Document: fixtureID, Format: "pdf", DryRun: dry}); classOf(err) != "unsupported" {
			t.Errorf("export of a Word file (dry run %v): %v, want [unsupported]", dry, err)
		}
	}
}

// A dry-run export fails only where the export would: on a missing
// document or an export directory it cannot look in, not on a metadata
// error the export works around.
func TestExportDryRunFailsWhereTheExportWould(t *testing.T) {
	ctx := context.Background()
	svc, api := exportService(t)
	api.fileErr = &gapi.APIError{Status: 500, Message: "backend error"}
	res, err := svc.Export(ctx, ExportRequest{Document: fixtureID, Format: "pdf", DryRun: true})
	if err != nil || filepath.Base(res.Path) != "document-1Synth.pdf" {
		t.Errorf("dry run with a failing metadata read: %+v %v, want the path document-1Synth.pdf", res, err)
	}
	api.fileErr = &gapi.APIError{Status: 404, Message: "missing"}
	if _, err := svc.Export(ctx, ExportRequest{Document: fixtureID, Format: "pdf", DryRun: true}); classOf(err) != "not_found" {
		t.Errorf("dry run of a missing document: %v, want [not_found]", err)
	}
	api.fileErr = nil
	notADir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notADir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	svc.opts.ExportDir = notADir
	if _, err := svc.Export(ctx, ExportRequest{Document: fixtureID, Format: "pdf", DryRun: true}); classOf(err) != "unexpected" {
		t.Errorf("dry run into an export dir that is a file: %v, want [unexpected]", err)
	}
}

func newWritable(t *testing.T) (*Service, *fakeAPI) { return writable(t, false) }

func commentService(t *testing.T) (*Service, *fakeAPI) {
	svc, api := writable(t, false)
	api.comments = []*gapi.DriveComment{{ID: "c1", Content: "Please check"},
		{ID: "c2", Content: "Done?", Resolved: true, Replies: []*gapi.DriveReply{{ID: "r1", Content: "Yes"}}}}
	return svc, api
}

func exportService(t *testing.T) (*Service, *fakeAPI) {
	svc, api := writable(t, false)
	svc.opts.ExportDir = t.TempDir()
	return svc, api
}

func tabDry(res *TabResult, err error) (bool, string, error) {
	if err != nil {
		return false, "", err
	}
	return res.DryRun, res.Text, nil
}

func replyDry(res *ReplyResult, err error) (bool, string, error) {
	if err != nil {
		return false, "", err
	}
	return res.DryRun, res.Text, nil
}

func createDry(res *CreateResult, err error) (bool, string, error) {
	if err != nil {
		return false, "", err
	}
	return res.DryRun, res.Text, nil
}

func TestExportDryRunNamesTheFreePath(t *testing.T) {
	svc, _ := exportService(t)
	dir := svc.opts.ExportDir
	if err := os.WriteFile(filepath.Join(dir, "Quarterly Report-1Synth.pdf"), []byte("earlier"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := svc.Export(context.Background(), ExportRequest{Document: fixtureID, Format: "pdf", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "Quarterly Report-1Synth-2.pdf"); res.Path != want {
		t.Errorf("dry-run path = %q, want %q, the name an export would take", res.Path, want)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("export dir holds %d files after a dry run, want the 1 written before it", len(entries))
	}
}
