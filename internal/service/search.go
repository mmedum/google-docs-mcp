package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mmedum/google-docs-mcp/internal/doc"
	"github.com/mmedum/google-docs-mcp/internal/gapi"
)

// SearchRequest looks for documents in Drive.
type SearchRequest struct {
	Query         string // full-text
	Title         string // name contains
	ModifiedAfter string // RFC 3339 or YYYY-MM-DD
	Owner         string // email
	Limit         int
	PageToken     string
}

// SearchHit is one document.
type SearchHit struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	Modified   string `json:"modified,omitempty"`
	ModifiedBy string `json:"modified_by,omitempty"`
	Owner      string `json:"owner,omitempty"`
}

// SearchResult is a page of hits.
type SearchResult struct {
	Hits          []SearchHit `json:"hits"`
	NextPageToken string      `json:"next_page_token,omitempty"`
	DriveQuery    string      `json:"drive_query"`
	Text          string      `json:"-"`
}

// Search finds Google Docs by title or content, newest first.
func (s *Service) Search(ctx context.Context, req SearchRequest) (*SearchResult, error) {
	parts := []string{"mimeType = " + gapi.QuoteDriveValue(gapi.DocumentMimeType), "trashed = false"}
	if t := strings.TrimSpace(req.Title); t != "" {
		parts = append(parts, "name contains "+gapi.QuoteDriveValue(t))
	}
	if q := strings.TrimSpace(req.Query); q != "" {
		parts = append(parts, "fullText contains "+gapi.QuoteDriveValue(q))
	}
	if m := strings.TrimSpace(req.ModifiedAfter); m != "" {
		ts, err := ParseTime(m)
		if err != nil {
			return nil, Errorf("invalid", "modified_after %q: use YYYY-MM-DD or RFC 3339", m)
		}
		parts = append(parts, "modifiedTime > "+gapi.QuoteDriveValue(ts))
	}
	if o := strings.TrimSpace(req.Owner); o != "" {
		parts = append(parts, gapi.QuoteDriveValue(o)+" in owners")
	}
	q := strings.Join(parts, " and ")
	list, err := s.api.SearchFiles(ctx, q, req.Limit, req.PageToken)
	if err != nil {
		return nil, wrapAPI(err, "search")
	}
	res := &SearchResult{DriveQuery: q, NextPageToken: list.NextPageToken, Hits: []SearchHit{}}
	for _, f := range list.Files {
		h := SearchHit{ID: f.ID, Title: f.Name, URL: f.WebViewLink, Modified: f.ModifiedTime, ModifiedBy: userLabel(f.LastModifyingUser)}
		if len(f.Owners) > 0 {
			h.Owner = userLabel(f.Owners[0])
		}
		if h.URL == "" {
			h.URL = doc.DocumentURL(f.ID)
		}
		res.Hits = append(res.Hits, h)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d document(s)", len(res.Hits))
	if res.NextPageToken != "" {
		sb.WriteString(" (more available; pass page_token)")
	}
	sb.WriteString("\n")
	for _, h := range res.Hits {
		fmt.Fprintf(&sb, "- %s — %s", h.Title, h.URL)
		if h.Modified != "" {
			fmt.Fprintf(&sb, " (modified %s", h.Modified)
			if h.ModifiedBy != "" {
				fmt.Fprintf(&sb, " by %s", h.ModifiedBy)
			}
			sb.WriteString(")")
		}
		sb.WriteString("\n")
	}
	res.Text = strings.TrimRight(sb.String(), "\n")
	return res, nil
}

// ParseTime accepts YYYY-MM-DD or RFC 3339 and returns RFC 3339 in UTC.
func ParseTime(s string) (string, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC().Format(time.RFC3339), nil
		}
	}
	return "", fmt.Errorf("bad time")
}
