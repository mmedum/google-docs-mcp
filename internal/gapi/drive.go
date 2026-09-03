package gapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// User is a Drive user reference.
type User struct {
	DisplayName  string `json:"displayName,omitempty"`
	EmailAddress string `json:"emailAddress,omitempty"`
}

// About is the about.get response subset we read.
type About struct {
	User *User `json:"user,omitempty"`
}

// FileCapabilities are the caller's rights on a file.
type FileCapabilities struct {
	CanEdit    bool `json:"canEdit,omitempty"`
	CanComment bool `json:"canComment,omitempty"`
	CanShare   bool `json:"canShare,omitempty"`
}

// File is the files.get response subset we read.
type File struct {
	ID                string            `json:"id,omitempty"`
	Name              string            `json:"name,omitempty"`
	MimeType          string            `json:"mimeType,omitempty"`
	CreatedTime       string            `json:"createdTime,omitempty"`
	ModifiedTime      string            `json:"modifiedTime,omitempty"`
	Owners            []*User           `json:"owners,omitempty"`
	LastModifyingUser *User             `json:"lastModifyingUser,omitempty"`
	WebViewLink       string            `json:"webViewLink,omitempty"`
	Version           string            `json:"version,omitempty"`
	Trashed           bool              `json:"trashed,omitempty"`
	Capabilities      *FileCapabilities `json:"capabilities,omitempty"`
}

// About returns the signed-in user, the cheapest authenticated call.
func (c *Client) About(ctx context.Context) (*User, error) {
	body, err := c.do(ctx, kindRead, http.MethodGet, c.drive+"/about?fields=user", nil)
	if err != nil {
		return nil, err
	}
	var a About
	if err := json.Unmarshal(body, &a); err != nil {
		return nil, fmt.Errorf("%w: decode about: %w", ErrUnexpected, err)
	}
	if a.User == nil {
		return nil, fmt.Errorf("%w: about without user", ErrUnexpected)
	}
	return a.User, nil
}

// FileFields is what GetFile asks Drive for.
const FileFields = "id,name,mimeType,createdTime,modifiedTime,owners(displayName,emailAddress),lastModifyingUser(displayName,emailAddress),webViewLink,version,trashed,capabilities(canEdit,canComment,canShare)"

// GetFile returns Drive metadata for a document.
func (c *Client) GetFile(ctx context.Context, id string) (*File, error) {
	q := url.Values{}
	q.Set("fields", FileFields)
	q.Set("supportsAllDrives", "true")
	u := c.drive + "/files/" + url.PathEscape(id) + "?" + q.Encode()
	body, err := c.do(ctx, kindRead, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	var f File
	if err := json.Unmarshal(body, &f); err != nil {
		return nil, fmt.Errorf("%w: decode file: %w", ErrUnexpected, err)
	}
	return &f, nil
}

// DocumentMimeType is the Drive MIME type of a Google Doc.
const DocumentMimeType = "application/vnd.google-apps.document"

// ExportMimeTypes maps short format names to Drive export MIME types.
var ExportMimeTypes = map[string]string{
	"pdf":  "application/pdf",
	"docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"odt":  "application/vnd.oasis.opendocument.text",
	"rtf":  "application/rtf",
	"txt":  "text/plain",
	"html": "text/html",
	"md":   "text/markdown",
	"epub": "application/epub+zip",
}

// FileList is a page of files.list results.
type FileList struct {
	Files         []*File `json:"files"`
	NextPageToken string  `json:"nextPageToken,omitempty"`
}

// SearchFields is what SearchFiles asks for per file.
const SearchFields = "nextPageToken,files(id,name,mimeType,modifiedTime,createdTime,owners(displayName,emailAddress),lastModifyingUser(displayName,emailAddress),webViewLink)"

// SearchFiles runs a Drive query (the caller builds and escapes q) across
// My Drive and shared drives, newest first.
func (c *Client) SearchFiles(ctx context.Context, q string, limit int, pageToken string) (*FileList, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	v := url.Values{}
	v.Set("q", q)
	v.Set("pageSize", fmt.Sprint(limit))
	v.Set("orderBy", "modifiedTime desc")
	v.Set("fields", SearchFields)
	v.Set("supportsAllDrives", "true")
	v.Set("includeItemsFromAllDrives", "true")
	v.Set("corpora", "allDrives")
	if pageToken != "" {
		v.Set("pageToken", pageToken)
	}
	body, err := c.do(ctx, kindRead, http.MethodGet, c.drive+"/files?"+v.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var out FileList
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("%w: decode file list: %w", ErrUnexpected, err)
	}
	return &out, nil
}

// QuoteDriveValue escapes a string for use inside single quotes in a
// Drive query.
func QuoteDriveValue(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return "'" + s + "'"
}

// Export downloads the document converted to the MIME type. Google caps
// exports at 10 MB.
func (c *Client) Export(ctx context.Context, id, mimeType string) ([]byte, error) {
	v := url.Values{}
	v.Set("mimeType", mimeType)
	u := c.drive + "/files/" + url.PathEscape(id) + "/export?" + v.Encode()
	return c.doAccept(ctx, kindRead, http.MethodGet, u, nil, "*/*")
}

// DriveComment is a Drive API comment thread on a file.
type DriveComment struct {
	ID                string        `json:"id,omitempty"`
	Content           string        `json:"content,omitempty"`
	HTMLContent       string        `json:"htmlContent,omitempty"`
	Author            *User         `json:"author,omitempty"`
	CreatedTime       string        `json:"createdTime,omitempty"`
	ModifiedTime      string        `json:"modifiedTime,omitempty"`
	Resolved          bool          `json:"resolved,omitempty"`
	Deleted           bool          `json:"deleted,omitempty"`
	Anchor            string        `json:"anchor,omitempty"`
	QuotedFileContent *QuotedText   `json:"quotedFileContent,omitempty"`
	Replies           []*DriveReply `json:"replies,omitempty"`
}

// QuotedText is the text a comment refers to.
type QuotedText struct {
	MimeType string `json:"mimeType,omitempty"`
	Value    string `json:"value,omitempty"`
}

// DriveReply is one reply in a thread.
type DriveReply struct {
	ID          string `json:"id,omitempty"`
	Content     string `json:"content,omitempty"`
	Author      *User  `json:"author,omitempty"`
	CreatedTime string `json:"createdTime,omitempty"`
	Action      string `json:"action,omitempty"`
	Deleted     bool   `json:"deleted,omitempty"`
}

// CommentFields is what the comment calls ask for.
const CommentFields = "id,content,htmlContent,author(displayName,emailAddress),createdTime,modifiedTime,resolved,deleted,anchor,quotedFileContent,replies(id,content,author(displayName,emailAddress),createdTime,action,deleted)"

// CreateComment adds a Drive comment quoting text. The Docs UI shows it
// unanchored (with the quote) because Drive anchors are opaque to Docs.
func (c *Client) CreateComment(ctx context.Context, fileID, content, quote string) (*DriveComment, error) {
	payload := map[string]any{"content": content}
	if quote != "" {
		payload["quotedFileContent"] = map[string]string{"mimeType": "text/plain", "value": quote}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	u := c.drive + "/files/" + url.PathEscape(fileID) + "/comments?fields=" + url.QueryEscape(CommentFields)
	data, err := c.do(ctx, kindWrite, http.MethodPost, u, body)
	if err != nil {
		return nil, wrapAmbiguousWrite(err)
	}
	var out DriveComment
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("%w: decode comment: %w", ErrUnexpected, err)
	}
	return &out, nil
}

// ListComments returns every comment thread on the file.
func (c *Client) ListComments(ctx context.Context, fileID string, includeDeleted bool) ([]*DriveComment, error) {
	var all []*DriveComment
	token := ""
	for {
		v := url.Values{}
		v.Set("fields", "nextPageToken,comments("+CommentFields+")")
		v.Set("pageSize", "100")
		v.Set("includeDeleted", fmt.Sprint(includeDeleted))
		if token != "" {
			v.Set("pageToken", token)
		}
		body, err := c.do(ctx, kindRead, http.MethodGet, c.drive+"/files/"+url.PathEscape(fileID)+"/comments?"+v.Encode(), nil)
		if err != nil {
			return nil, err
		}
		var page struct {
			Comments      []*DriveComment `json:"comments"`
			NextPageToken string          `json:"nextPageToken"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("%w: decode comments: %w", ErrUnexpected, err)
		}
		all = append(all, page.Comments...)
		if page.NextPageToken == "" || len(all) > 5000 {
			return all, nil
		}
		token = page.NextPageToken
	}
}
