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

// ReplyFields is what reply calls ask for.
const ReplyFields = "id,content,author(displayName,emailAddress),createdTime,action,deleted"

// CommentFields is what the comment calls ask for.
const CommentFields = "id,content,htmlContent,author(displayName,emailAddress),createdTime,modifiedTime,resolved,deleted,anchor,quotedFileContent,replies(" + ReplyFields + ")"

// commentURL is the Drive URL of a file's comment collection or, with an
// id, of one thread.
func (c *Client) commentURL(fileID, commentID string) string {
	u := c.drive + "/files/" + url.PathEscape(fileID) + "/comments"
	if commentID != "" {
		u += "/" + url.PathEscape(commentID)
	}
	return u
}

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
	data, err := c.do(ctx, kindDriveWrite, http.MethodPost, c.commentURL(fileID, "")+"?fields="+url.QueryEscape(CommentFields), body)
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
	v := url.Values{}
	v.Set("fields", "nextPageToken,comments("+CommentFields+")")
	v.Set("pageSize", "100")
	v.Set("includeDeleted", fmt.Sprint(includeDeleted))
	return listPages[DriveComment](ctx, c, c.commentURL(fileID, ""), v, "comments")
}

// listPages collects every page of a Drive list call whose items sit
// under field, stopping after a generous cap.
func listPages[T any](ctx context.Context, c *Client, base string, v url.Values, field string) ([]*T, error) {
	var all []*T
	for {
		body, err := c.do(ctx, kindRead, http.MethodGet, base+"?"+v.Encode(), nil)
		if err != nil {
			return nil, err
		}
		var page map[string]json.RawMessage
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("%w: decode %s: %w", ErrUnexpected, field, err)
		}
		var items []*T
		if raw, ok := page[field]; ok {
			if err := json.Unmarshal(raw, &items); err != nil {
				return nil, fmt.Errorf("%w: decode %s: %w", ErrUnexpected, field, err)
			}
		}
		all = append(all, items...)
		var token string
		if raw, ok := page["nextPageToken"]; ok {
			_ = json.Unmarshal(raw, &token)
		}
		if token == "" || len(all) > 5000 {
			return all, nil
		}
		v.Set("pageToken", token)
	}
}

// CreateReply posts a reply on a comment thread. action is "" for a plain
// reply, "resolve" to resolve the thread or "reopen" to reopen it.
func (c *Client) CreateReply(ctx context.Context, fileID, commentID, content, action string) (*DriveReply, error) {
	payload := map[string]any{}
	if content != "" {
		payload["content"] = content
	}
	if action != "" {
		payload["action"] = action
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	data, err := c.do(ctx, kindDriveWrite, http.MethodPost, c.commentURL(fileID, commentID)+"/replies?fields="+url.QueryEscape(ReplyFields), body)
	if err != nil {
		return nil, wrapAmbiguousWrite(err)
	}
	var out DriveReply
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("%w: decode reply: %w", ErrUnexpected, err)
	}
	return &out, nil
}

// GetComment returns one comment thread with its replies.
func (c *Client) GetComment(ctx context.Context, fileID, commentID string) (*DriveComment, error) {
	v := url.Values{}
	v.Set("fields", CommentFields)
	v.Set("includeDeleted", "true")
	data, err := c.do(ctx, kindRead, http.MethodGet, c.commentURL(fileID, commentID)+"?"+v.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var out DriveComment
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("%w: decode comment: %w", ErrUnexpected, err)
	}
	return &out, nil
}

// DeleteComment deletes a comment thread. Drive keeps it listable with
// includeDeleted=true.
func (c *Client) DeleteComment(ctx context.Context, fileID, commentID string) error {
	_, err := c.do(ctx, kindDriveWrite, http.MethodDelete, c.commentURL(fileID, commentID), nil)
	return wrapAmbiguousWrite(err)
}

// DeleteReply deletes one reply of a thread.
func (c *Client) DeleteReply(ctx context.Context, fileID, commentID, replyID string) error {
	_, err := c.do(ctx, kindDriveWrite, http.MethodDelete, c.commentURL(fileID, commentID)+"/replies/"+url.PathEscape(replyID), nil)
	return wrapAmbiguousWrite(err)
}

// Revision is a Drive revision (version history entry) of a document.
type Revision struct {
	ID                string `json:"id,omitempty"`
	MimeType          string `json:"mimeType,omitempty"`
	ModifiedTime      string `json:"modifiedTime,omitempty"`
	KeepForever       bool   `json:"keepForever,omitempty"`
	LastModifyingUser *User  `json:"lastModifyingUser,omitempty"`
}

// RevisionFields is what ListRevisions asks for per revision.
const RevisionFields = "id,mimeType,modifiedTime,keepForever,lastModifyingUser(displayName,emailAddress)"

// ListRevisions returns every revision of the file, oldest first as Drive
// orders them.
func (c *Client) ListRevisions(ctx context.Context, fileID string) ([]*Revision, error) {
	v := url.Values{}
	v.Set("fields", "nextPageToken,revisions("+RevisionFields+")")
	v.Set("pageSize", "1000")
	return listPages[Revision](ctx, c, c.drive+"/files/"+url.PathEscape(fileID)+"/revisions", v, "revisions")
}

// operation is the long-running operation files.download returns.
type operation struct {
	Name  string `json:"name"`
	Done  bool   `json:"done"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Response struct {
		DownloadURI string `json:"downloadUri"`
	} `json:"response"`
}

// ExportRevision exports one revision of a document through
// files.download, the only Drive method that takes a revisionId for
// Docs files. It starts a long-running operation, polls it, then fetches
// the content it names. The content URL must stay on Google's hosts.
func (c *Client) ExportRevision(ctx context.Context, fileID, revisionID, mimeType string) ([]byte, error) {
	v := url.Values{}
	v.Set("mimeType", mimeType)
	v.Set("revisionId", revisionID)
	body, err := c.do(ctx, kindRead, http.MethodPost, c.drive+"/files/"+url.PathEscape(fileID)+"/download?"+v.Encode(), []byte("{}"))
	if err != nil {
		return nil, err
	}
	var op operation
	if err := json.Unmarshal(body, &op); err != nil {
		return nil, fmt.Errorf("%w: decode download operation: %w", ErrUnexpected, err)
	}
	for attempt := 1; !op.Done; attempt++ {
		if attempt > 20 {
			return nil, fmt.Errorf("%w: the export of revision %s did not finish in time", ErrServer, revisionID)
		}
		if err := c.sleep(ctx, c.backoff(min(attempt, 4), 0)); err != nil {
			return nil, err
		}
		body, err = c.do(ctx, kindRead, http.MethodGet, c.drive+"/operations/"+url.PathEscape(op.Name), nil)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(body, &op); err != nil {
			return nil, fmt.Errorf("%w: decode operation: %w", ErrUnexpected, err)
		}
	}
	if op.Error != nil {
		return nil, fmt.Errorf("%w: export of revision %s: %s", rpcClass(op.Error.Code), revisionID, op.Error.Message)
	}
	if op.Response.DownloadURI == "" {
		return nil, fmt.Errorf("%w: download operation returned no content URL", ErrUnexpected)
	}
	// The URL is checked against the host allowlist like every request.
	return c.doAccept(ctx, kindRead, http.MethodGet, op.Response.DownloadURI, nil, "*/*")
}

// googleHost reports whether a host is one this server may send
// credentials to: the APIs, Google's content hosts, and docs.google.com,
// where the export URLs that files.download returns for old revisions
// live (observed live 2026-09-03: docs.google.com/feeds/download/...).
func googleHost(host string) bool {
	host = strings.ToLower(host)
	return host == "googleapis.com" || strings.HasSuffix(host, ".googleapis.com") || strings.HasSuffix(host, ".googleusercontent.com") || host == "docs.google.com"
}

// rpcClass maps a google.rpc.Code from an operation error onto the
// sentinel classes.
func rpcClass(code int) error {
	switch code {
	case 3:
		return ErrInvalid
	case 5:
		return ErrNotFound
	case 7:
		return ErrForbidden
	case 8:
		return ErrRateLimited
	case 16:
		return ErrUnauthorized
	}
	return ErrServer
}
