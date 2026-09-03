package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mmedum/google-docs-mcp/internal/gapi"
	"github.com/mmedum/google-docs-mcp/internal/plan"
)

// TabRequest manages tabs. Tab operations are always direct edits: the
// API refuses them in suggestion mode.
type TabRequest struct {
	Document       string
	Action         string // add, rename, move, delete
	Tab            string // id, title or number of the tab to change
	Title          string
	Position       int    // 1-based position among siblings (add, move)
	Parent         string // parent tab for nesting (add, move); "none" for top level
	Emoji          string
	Content        string // add: initial markdown content
	ExpectRevision string
}

// TabResult reports the tab change.
type TabResult struct {
	Action     string   `json:"action"`
	TabID      string   `json:"tab_id,omitempty"`
	Title      string   `json:"title,omitempty"`
	RevisionID string   `json:"revision_id"`
	Warnings   []string `json:"warnings,omitempty"`
	Text       string   `json:"-"`
}

// ManageTabs adds, renames, moves or deletes a tab.
func (s *Service) ManageTabs(ctx context.Context, req TabRequest) (*TabResult, error) {
	if err := s.requireWritable(); err != nil {
		return nil, err
	}
	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action == "delete" {
		if err := s.requireDestructive(); err != nil {
			return nil, err
		}
	}
	f, err := s.FetchFresh(ctx, req.Document)
	if err != nil {
		return nil, err
	}
	if req.ExpectRevision != "" && req.ExpectRevision != f.Doc.RevisionID {
		return nil, Errorf("conflict", "the document is at revision %s, not %s; re-read before changing tabs", f.Doc.RevisionID, req.ExpectRevision)
	}
	if len(f.Doc.Tabs) > 0 && f.Doc.Tabs[0].ID == "" {
		return nil, Errorf("unsupported", "this document has no tab ids; tabs cannot be managed through the API")
	}
	res := &TabResult{Action: action, RevisionID: f.Doc.RevisionID}
	var request json.RawMessage
	switch action {
	case "add":
		request, err = s.addTabRequest(f, req, res)
	case "rename", "move":
		request, err = s.updateTabRequest(f, req, action, res)
	case "delete":
		request, err = deleteTabRequest(f, req, res)
	default:
		err = Errorf("invalid", "action %q; use add, rename, move or delete", req.Action)
	}
	if err != nil {
		return nil, err
	}
	out, err := s.api.BatchUpdate(ctx, f.Doc.ID, &gapi.BatchUpdateRequest{Requests: []json.RawMessage{request}, WriteControl: &gapi.WriteControl{RequiredRevisionID: f.Doc.RevisionID}})
	if err != nil {
		return nil, wrapWriteError(err)
	}
	s.Invalidate(f.Doc.ID)
	if out.WriteControl != nil && out.WriteControl.RequiredRevisionID != "" {
		res.RevisionID = out.WriteControl.RequiredRevisionID
	}
	if action == "add" {
		res.TabID = addedTabID(out.Raw)
		s.fillNewTab(ctx, req, res)
	}
	res.Text = tabText(res)
	return res, nil
}

func (s *Service) addTabRequest(f *Fetched, req TabRequest, res *TabResult) (json.RawMessage, error) {
	props, err := s.tabProps(f, req, true)
	if err != nil {
		return nil, err
	}
	if props.Title == "" {
		return nil, Errorf("invalid", "add needs a title")
	}
	res.Title = props.Title
	return plan.AddDocumentTab(props), nil
}

func (s *Service) updateTabRequest(f *Fetched, req TabRequest, action string, res *TabResult) (json.RawMessage, error) {
	tab, err := tabOf(f.Doc, req.Tab)
	if err != nil {
		return nil, err
	}
	res.TabID, res.Title = tab.ID, tab.Title
	props, err := s.tabProps(f, req, false)
	if err != nil {
		return nil, err
	}
	if action == "rename" {
		if props.Title == "" {
			return nil, Errorf("invalid", "rename needs a title")
		}
		props = plan.TabProperties{Title: props.Title, Emoji: props.Emoji}
		res.Title = props.Title
		return plan.UpdateDocumentTabProperties(tab.ID, props), nil
	}
	props.Title = ""
	if props.Index == nil && props.ParentID == "" && props.Emoji == "" {
		return nil, Errorf("invalid", "move needs position or parent")
	}
	if props.ParentID == tab.ID {
		return nil, Errorf("invalid", "a tab cannot be its own parent")
	}
	return plan.UpdateDocumentTabProperties(tab.ID, props), nil
}

func deleteTabRequest(f *Fetched, req TabRequest, res *TabResult) (json.RawMessage, error) {
	tab, err := tabOf(f.Doc, req.Tab)
	if err != nil {
		return nil, err
	}
	if len(f.Doc.Tabs) == 1 {
		return nil, Errorf("invalid", "a document keeps at least one tab; delete the content instead")
	}
	res.TabID, res.Title = tab.ID, tab.Title
	for _, t := range f.Doc.Tabs {
		if t.ParentID == tab.ID {
			res.Warnings = append(res.Warnings, "child tabs were deleted with it")
			break
		}
	}
	return plan.DeleteTab(tab.ID), nil
}

// fillNewTab writes the initial content of an added tab in a second edit.
func (s *Service) fillNewTab(ctx context.Context, req TabRequest, res *TabResult) {
	content := strings.TrimSpace(req.Content)
	if content == "" || res.TabID == "" {
		return
	}
	edit, err := s.Edit(ctx, EditRequest{Document: req.Document, Mode: string(plan.ModeDirect), Ops: []EditOp{{Kind: plan.OpAppend, Content: req.Content, Location: &Location{At: "end", Of: &Target{Tab: res.TabID}}}}})
	if err != nil {
		res.Warnings = append(res.Warnings, "the tab was added empty; writing its content failed: "+err.Error())
		return
	}
	res.RevisionID = edit.RevisionID
	res.Warnings = append(res.Warnings, edit.Warnings...)
}

// tabProps builds the properties an add or move sends, resolving the
// parent reference and converting the position to a zero-based index.
func (s *Service) tabProps(f *Fetched, req TabRequest, adding bool) (plan.TabProperties, error) {
	props := plan.TabProperties{Title: strings.TrimSpace(req.Title), Emoji: strings.TrimSpace(req.Emoji)}
	if req.Position > 0 {
		i := req.Position - 1
		props.Index = &i
	} else if req.Position < 0 {
		return props, Errorf("invalid", "position counts from 1")
	}
	switch parent := strings.TrimSpace(req.Parent); {
	case parent == "":
	case strings.EqualFold(parent, "none"):
		if adding {
			break
		}
		return props, Errorf("unsupported", "moving a tab to the top level is not expressible through the API; move it under another tab or leave it")
	default:
		pt, err := tabOf(f.Doc, parent)
		if err != nil {
			return props, err
		}
		props.ParentID = pt.ID
	}
	return props, nil
}

func addedTabID(raw json.RawMessage) string {
	for _, r := range decodeReplies(raw).Replies {
		var v struct {
			TabProperties struct {
				TabID string `json:"tabId"`
			} `json:"tabProperties"`
		}
		if data, ok := r["addDocumentTab"]; ok && json.Unmarshal(data, &v) == nil && v.TabProperties.TabID != "" {
			return v.TabProperties.TabID
		}
	}
	return ""
}

func tabText(res *TabResult) string {
	var sb strings.Builder
	switch res.Action {
	case "add":
		fmt.Fprintf(&sb, "added tab %q", res.Title)
		if res.TabID != "" {
			fmt.Fprintf(&sb, " (id %s)", res.TabID)
		}
	case "rename":
		fmt.Fprintf(&sb, "renamed tab %s to %q", res.TabID, res.Title)
	case "move":
		fmt.Fprintf(&sb, "moved tab %s (%q)", res.TabID, res.Title)
	case "delete":
		fmt.Fprintf(&sb, "deleted tab %s (%q)", res.TabID, res.Title)
	}
	fmt.Fprintf(&sb, "; revision %s", res.RevisionID)
	for _, w := range res.Warnings {
		sb.WriteString("\nwarning: " + w)
	}
	return sb.String()
}
