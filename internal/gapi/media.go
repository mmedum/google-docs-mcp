package gapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
)

// A Markdown file that Docs opens in place is a Drive blob: the Docs API
// reads it and refuses every write, and Drive serves and replaces its
// bytes (spike D, architecture §7.5). These are the three calls that
// need.

// DownloadFile returns a file's content, for a file with binary content;
// a Google Doc has none and is exported instead.
func (c *Client) DownloadFile(ctx context.Context, id string) ([]byte, error) {
	u := c.drive + "/files/" + url.PathEscape(id) + "?alt=media&supportsAllDrives=true"
	return c.doAccept(ctx, kindRead, http.MethodGet, u, nil, "*/*")
}

// DownloadRevision returns one revision's content of a file with binary
// content. Drive's guide says only revisions kept forever download; the
// owner downloaded every unpinned revision live (spike D), and a refusal
// comes back as Google's reason.
func (c *Client) DownloadRevision(ctx context.Context, fileID, revisionID string) ([]byte, error) {
	u := c.drive + "/files/" + url.PathEscape(fileID) + "/revisions/" + url.PathEscape(revisionID) + "?alt=media"
	return c.doAccept(ctx, kindRead, http.MethodGet, u, nil, "*/*")
}

// UploadFields is what UploadFile and CreateFile ask Drive to return
// about the file they wrote.
const UploadFields = "id,name,mimeType,headRevisionId,md5Checksum,size,modifiedTime,webViewLink"

// NewFile is the metadata of a file CreateFile makes.
type NewFile struct {
	Name string `json:"name"`
	// MimeType is the file's type. A Google Workspace type with content
	// of another type asks Drive to convert it on import: a Google Doc
	// from text/markdown.
	MimeType      string            `json:"mimeType"`
	AppProperties map[string]string `json:"appProperties,omitempty"`
}

// CreateFile makes a file in the caller's My Drive from data sent as
// mediaType: one multipart upload, the metadata first and the content
// second, as Drive's upload guide describes. Not repeated on a failure
// whose outcome is unknown, which would make a second file.
func (c *Client) CreateFile(ctx context.Context, meta NewFile, mediaType string, data []byte) (*File, error) {
	m, err := json.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("%w: encode file metadata: %w", ErrUnexpected, err)
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for _, part := range []struct {
		contentType string
		data        []byte
	}{{acceptJSON + "; charset=UTF-8", m}, {mediaType, data}} {
		h := textproto.MIMEHeader{}
		h.Set("Content-Type", part.contentType)
		pw, err := w.CreatePart(h)
		if err != nil {
			return nil, fmt.Errorf("%w: build upload: %w", ErrUnexpected, err)
		}
		if _, err := pw.Write(part.data); err != nil {
			return nil, fmt.Errorf("%w: build upload: %w", ErrUnexpected, err)
		}
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("%w: build upload: %w", ErrUnexpected, err)
	}
	return c.upload(ctx, http.MethodPost, "/files", "multipart", body.Bytes(), "multipart/related; boundary="+w.Boundary())
}

// UploadFile replaces a file's content with data, sent as mimeType.
// Drive takes the media part's type as the file's own: a Markdown file
// uploaded as text/plain becomes text/plain, which the Docs API then
// refuses, so the caller passes the type the file has. Drive offers no
// precondition (it ignores If-Match and serves no ETag), so a caller
// checks the head revision itself just before. Every call is a new
// revision, identical bytes included.
//
// A failure that leaves the outcome unknown is ErrAmbiguous: the upload
// may have landed.
func (c *Client) UploadFile(ctx context.Context, id string, data []byte, mimeType string) (*File, error) {
	return c.upload(ctx, http.MethodPatch, "/files/"+url.PathEscape(id), "media", data, mimeType)
}

// upload sends one upload to Drive's media endpoint and decodes the
// file it reports. Not repeated on a failure whose outcome is unknown.
func (c *Client) upload(ctx context.Context, method, path, uploadType string, body []byte, contentType string) (*File, error) {
	v := url.Values{}
	v.Set("uploadType", uploadType)
	v.Set("supportsAllDrives", "true")
	v.Set("fields", UploadFields)
	out, err := c.send(ctx, kindDriveWrite, method, c.uploadBase()+path+"?"+v.Encode(), body, acceptJSON, contentType)
	if err != nil {
		return nil, wrapAmbiguousWrite(err)
	}
	var f File
	if err := json.Unmarshal(out, &f); err != nil {
		return nil, fmt.Errorf("%w: decode uploaded file: %w", ErrUnexpected, err)
	}
	return &f, nil
}

// SetAppProperties sets this client's properties on a file, leaving
// any it does not name.
func (c *Client) SetAppProperties(ctx context.Context, id string, props map[string]string) error {
	body, err := json.Marshal(map[string]any{"appProperties": props})
	if err != nil {
		return fmt.Errorf("%w: encode properties: %w", ErrUnexpected, err)
	}
	u := c.drive + "/files/" + url.PathEscape(id) + "?fields=id&supportsAllDrives=true"
	_, err = c.do(ctx, kindDriveWrite, http.MethodPatch, u, body)
	return wrapAmbiguousWrite(err)
}

// uploadBase is Drive's media endpoint, which sits beside the API on
// the same host: /upload/drive/v3 rather than /drive/v3.
func (c *Client) uploadBase() string {
	return strings.Replace(c.drive, "/drive/v3", "/upload/drive/v3", 1)
}
