package gapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/mmedum/google-docs-mcp/internal/gdocs"
)

// View modes for documents.get.
const (
	SuggestionsInline          = "SUGGESTIONS_INLINE"
	SuggestionsPreviewAccepted = "PREVIEW_SUGGESTIONS_ACCEPTED"
	CommentsIncluded           = "COMMENTS_VIEW_MODE_INCLUDED" // Developer Preview
)

// GetOptions select what documents.get returns. Tabs content is always
// requested because every other choice loses information.
type GetOptions struct {
	SuggestionsViewMode string
	CommentsViewMode    string
}

// PreviewFields are documents.get fields that only exist in Developer
// Preview and are absent from the generated types. They are kept raw
// until the schema is confirmed against a live enrolled project.
type PreviewFields struct {
	Comments    json.RawMessage `json:"comments,omitempty"`
	Suggestions json.RawMessage `json:"suggestions,omitempty"`
}

// DocumentResult is a decoded documents.get response plus the raw bytes.
type DocumentResult struct {
	Document *gdocs.Document
	Raw      json.RawMessage
	Preview  PreviewFields
}

// GetDocument fetches a document with tabs content.
func (c *Client) GetDocument(ctx context.Context, id string, o GetOptions) (*DocumentResult, error) {
	q := url.Values{}
	q.Set("includeTabsContent", "true")
	if o.SuggestionsViewMode != "" {
		q.Set("suggestionsViewMode", o.SuggestionsViewMode)
	}
	if o.CommentsViewMode != "" {
		q.Set("commentsViewMode", o.CommentsViewMode)
	}
	u := c.docs + "/v1/documents/" + url.PathEscape(id) + "?" + q.Encode()
	body, err := c.do(ctx, kindRead, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	var d gdocs.Document
	if err := json.Unmarshal(body, &d); err != nil {
		return nil, fmt.Errorf("%w: decode document: %w", ErrUnexpected, err)
	}
	var p PreviewFields
	_ = json.Unmarshal(body, &p)
	return &DocumentResult{Document: &d, Raw: body, Preview: p}, nil
}

// WriteControl guards a batchUpdate against concurrent edits and, in
// Developer Preview, selects suggestion mode.
type WriteControl struct {
	RequiredRevisionID string `json:"requiredRevisionId,omitempty"`
	TargetRevisionID   string `json:"targetRevisionId,omitempty"`
	WriteMode          string `json:"writeMode,omitempty"` // "SUGGEST" (preview)
}

// BatchUpdateRequest is the body of documents.batchUpdate. Requests are
// pre-marshalled so GA and preview request types share one path.
type BatchUpdateRequest struct {
	Requests     []json.RawMessage `json:"requests"`
	WriteControl *WriteControl     `json:"writeControl,omitempty"`
}

// BatchUpdateResponse is the decoded reply.
type BatchUpdateResponse struct {
	DocumentID   string            `json:"documentId"`
	Replies      []json.RawMessage `json:"replies"`
	WriteControl *WriteControl     `json:"writeControl"`
	Raw          json.RawMessage   `json:"-"`
}

// BatchUpdate applies requests atomically. A network failure after the
// request was sent is reported as ErrAmbiguous, never retried blindly.
func (c *Client) BatchUpdate(ctx context.Context, id string, req *BatchUpdateRequest) (*BatchUpdateResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("%w: encode batchUpdate: %w", ErrInvalid, err)
	}
	u := c.docs + "/v1/documents/" + url.PathEscape(id) + ":batchUpdate"
	data, err := c.do(ctx, kindWrite, http.MethodPost, u, body)
	if err != nil {
		return nil, wrapAmbiguousWrite(err)
	}
	var out BatchUpdateResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("%w: decode batchUpdate reply: %w", ErrUnexpected, err)
	}
	out.Raw = data
	return &out, nil
}

// CreateDocument creates an empty document with the title and returns it.
func (c *Client) CreateDocument(ctx context.Context, title string) (*gdocs.Document, error) {
	body, err := json.Marshal(map[string]string{"title": title})
	if err != nil {
		return nil, err
	}
	data, err := c.do(ctx, kindWrite, http.MethodPost, c.docs+"/v1/documents", body)
	if err != nil {
		return nil, wrapAmbiguousWrite(err)
	}
	var d gdocs.Document
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("%w: decode created document: %w", ErrUnexpected, err)
	}
	return &d, nil
}
