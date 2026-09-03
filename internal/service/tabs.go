package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
	Parent         string // parent tab for nesting (add, move)
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

// tabBuilder turns a request into the one batchUpdate request an action needs.
type tabBuilder func(f *Fetched, req TabRequest, res *TabResult) (json.RawMessage, error)

// ManageTabs adds, renames or moves a tab. Deleting is DeleteTab.
func (s *Service) ManageTabs(ctx context.Context, req TabRequest) (*TabResult, error) {
	action := strings.ToLower(strings.TrimSpace(req.Action))
	var build tabBuilder
	switch action {
	case "add":
		build = addTabRequest
	case "rename":
		build = renameTabRequest
	case "move":
		build = moveTabRequest
	case "delete":
		return nil, Errorf("invalid", "deleting a tab is the delete_tab tool, registered only with GDOCS_ENABLE_DESTRUCTIVE=true")
	default:
		return nil, Errorf("invalid", "action %q; use add, rename or move", req.Action)
	}
	res, err := s.tabChange(ctx, req, action, build)
	if err != nil {
		return nil, err
	}
	if action == "add" {
		s.fillNewTab(ctx, req, res)
		res.Text = tabText(res)
	}
	return res, nil
}

// DeleteTab deletes a tab with its content and child tabs.
func (s *Service) DeleteTab(ctx context.Context, req TabRequest) (*TabResult, error) {
	if err := s.requireDestructive(); err != nil {
		return nil, err
	}
	return s.tabChange(ctx, req, "delete", deleteTabRequest)
}

// tabChange runs one tab request against a fresh fetch, guarded by the
// revision it was planned against.
func (s *Service) tabChange(ctx context.Context, req TabRequest, action string, build tabBuilder) (*TabResult, error) {
	if err := s.requireWritable(); err != nil {
		return nil, err
	}
	f, err := s.FetchFresh(ctx, req.Document)
	if err != nil {
		return nil, err
	}
	if err := checkRevision(req.ExpectRevision, f.Doc.RevisionID, "changing tabs"); err != nil {
		return nil, err
	}
	if len(f.Doc.Tabs) > 0 && f.Doc.Tabs[0].ID == "" {
		return nil, Errorf("unsupported", "this document has no tab ids; tabs cannot be managed through the API")
	}
	res := &TabResult{Action: action, RevisionID: f.Doc.RevisionID}
	request, err := build(f, req, res)
	if err != nil {
		return nil, err
	}
	env, revision, err := s.batchUpdate(ctx, f, []json.RawMessage{request}, "")
	if err != nil {
		return nil, err
	}
	res.RevisionID = revision
	if action == "add" {
		res.TabID = env.addedTabID()
	}
	res.Text = tabText(res)
	return res, nil
}

func addTabRequest(f *Fetched, req TabRequest, res *TabResult) (json.RawMessage, error) {
	props := plan.TabProperties{Title: strings.TrimSpace(req.Title), Emoji: strings.TrimSpace(req.Emoji)}
	if props.Title == "" {
		return nil, Errorf("invalid", "add needs a title")
	}
	if err := placeTab(f, req, &props, true); err != nil {
		return nil, err
	}
	res.Title = props.Title
	return plan.AddDocumentTab(props), nil
}

func renameTabRequest(f *Fetched, req TabRequest, res *TabResult) (json.RawMessage, error) {
	tab, err := tabOf(f.Doc, req.Tab)
	if err != nil {
		return nil, err
	}
	if req.Position != 0 || strings.TrimSpace(req.Parent) != "" {
		return nil, Errorf("invalid", "rename changes the title or emoji; use move for position or parent")
	}
	props := plan.TabProperties{Title: strings.TrimSpace(req.Title), Emoji: strings.TrimSpace(req.Emoji)}
	if props.Title == "" && props.Emoji == "" {
		return nil, Errorf("invalid", "rename needs a title or an emoji")
	}
	res.TabID, res.Title = tab.ID, tab.Title
	if props.Title != "" {
		res.Title = props.Title
	}
	return plan.UpdateDocumentTabProperties(tab.ID, props), nil
}

func moveTabRequest(f *Fetched, req TabRequest, res *TabResult) (json.RawMessage, error) {
	tab, err := tabOf(f.Doc, req.Tab)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Title) != "" || strings.TrimSpace(req.Emoji) != "" {
		return nil, Errorf("invalid", "move changes position or parent; use rename for the title or emoji")
	}
	var props plan.TabProperties
	if err := placeTab(f, req, &props, false); err != nil {
		return nil, err
	}
	if props.Index == nil && props.ParentID == "" {
		return nil, Errorf("invalid", "move needs position or parent")
	}
	if props.ParentID == tab.ID {
		return nil, Errorf("invalid", "a tab cannot be its own parent")
	}
	res.TabID, res.Title = tab.ID, tab.Title
	return plan.UpdateDocumentTabProperties(tab.ID, props), nil
}

// placeTab fills the position and parent of an add or move. "none" as
// the parent means the top level, which is the default when adding and
// not expressible when moving.
func placeTab(f *Fetched, req TabRequest, props *plan.TabProperties, adding bool) error {
	if req.Position < 0 {
		return Errorf("invalid", "position counts from 1")
	}
	if req.Position > 0 {
		i := req.Position - 1
		props.Index = &i
	}
	switch parent := strings.TrimSpace(req.Parent); {
	case parent == "":
	case strings.EqualFold(parent, "none"):
		if !adding {
			return Errorf("unsupported", "moving a tab to the top level is not expressible through the API; move it under another tab or leave it")
		}
	default:
		pt, err := tabOf(f.Doc, parent)
		if err != nil {
			return err
		}
		props.ParentID = pt.ID
	}
	return nil
}

func deleteTabRequest(f *Fetched, req TabRequest, res *TabResult) (json.RawMessage, error) {
	tab, err := tabOf(f.Doc, req.Tab)
	if err != nil {
		return nil, err
	}
	roots := 0
	for _, t := range f.Doc.Tabs {
		if t.ParentID == "" {
			roots++
		}
	}
	if tab.ParentID == "" && roots == 1 {
		return nil, Errorf("invalid", "a document keeps at least one top-level tab; delete the content instead")
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
