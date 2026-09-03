package service

import (
	"context"
	"strings"

	"github.com/mmedum/google-docs-mcp/internal/doc"
	"github.com/mmedum/google-docs-mcp/internal/plan"
)

// CreateRequest makes a new document.
type CreateRequest struct {
	Title         string
	Content       string
	ContentFormat string
}

// CreateResult describes the new document.
type CreateResult struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	URL        string   `json:"url"`
	RevisionID string   `json:"revision_id"`
	Warnings   []string `json:"warnings,omitempty"`
}

// Create makes a document and, when content is given, writes it directly
// (a new document has nothing to suggest against).
func (s *Service) Create(ctx context.Context, req CreateRequest) (*CreateResult, error) {
	if err := s.requireWritable(); err != nil {
		return nil, err
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return nil, Errorf("invalid", "title is empty")
	}
	d, err := s.api.CreateDocument(ctx, title)
	if err != nil {
		return nil, wrapWriteError(err)
	}
	res := &CreateResult{ID: d.DocumentID, Title: d.Title, URL: doc.DocumentURL(d.DocumentID), RevisionID: d.RevisionID}
	if strings.TrimSpace(req.Content) == "" {
		return res, nil
	}
	// The create reply is the whole (empty) document, so the edit plans
	// against it without another read.
	f, err := s.adopt(d)
	if err != nil {
		return nil, err
	}
	edit, _, err := s.editFetched(ctx, f, EditRequest{Document: d.DocumentID, Mode: string(plan.ModeDirect), Ops: []EditOp{{Kind: plan.OpAppend, Content: req.Content, ContentFormat: req.ContentFormat}}})
	if err != nil {
		res.Warnings = append(res.Warnings, "the document was created empty; writing the content failed: "+err.Error())
		return res, nil //nolint:nilerr // the document exists; the caller gets it with a warning
	}
	res.RevisionID = edit.RevisionID
	res.Warnings = append(res.Warnings, edit.Warnings...)
	return res, nil
}
