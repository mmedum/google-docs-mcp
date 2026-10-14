package gapi

import (
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestUploadFileSendsTheBytesAsTheGivenType(t *testing.T) {
	const content = "# Title\n\nBody.\n"
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		got, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPatch || r.URL.Path != "/upload/drive/v3/files/abc" || q.Get("uploadType") != "media" ||
			q.Get("supportsAllDrives") != "true" || r.Header.Get("Content-Type") != "text/markdown" || string(got) != content {
			t.Errorf("upload: %s %s?%s as %q with %q", r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Content-Type"), got)
		}
		_, _ = w.Write([]byte(`{"id":"abc","mimeType":"text/markdown","headRevisionId":"r2","md5Checksum":"m2","size":"15"}`))
	}))
	f, err := c.UploadFile(context.Background(), "abc", []byte(content), "text/markdown")
	if err != nil || f.HeadRevisionID != "r2" || f.MD5Checksum != "m2" || f.Size != "15" || f.MimeType != "text/markdown" {
		t.Fatalf("UploadFile = %+v, %v", f, err)
	}
}

func TestUploadFileIsNotRepeatedWhenTheOutcomeIsUnknown(t *testing.T) {
	var calls int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		googleError(w, http.StatusServiceUnavailable, "backend error", "UNAVAILABLE", "")
	}))
	_, err := c.UploadFile(context.Background(), "abc", []byte("x"), "text/markdown")
	if !errors.Is(err, ErrAmbiguous) || calls != 1 {
		t.Fatalf("UploadFile after a 503 = %v after %d call(s), want ErrAmbiguous after 1", err, calls)
	}
}

func TestDownloadFileReturnsTheBytes(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/drive/v3/files/abc" || q.Get("alt") != "media" || q.Has("prettyPrint") {
			t.Errorf("download: %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte("# not JSON"))
	}))
	data, err := c.DownloadFile(context.Background(), "abc")
	if err != nil || string(data) != "# not JSON" {
		t.Fatalf("DownloadFile = %q, %v", data, err)
	}
}

func TestDownloadRevisionReturnsTheBytes(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/drive/v3/files/abc/revisions/r1" || r.URL.Query().Get("alt") != "media" {
			t.Errorf("revision download: %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte("# old"))
	}))
	data, err := c.DownloadRevision(context.Background(), "abc", "r1")
	if err != nil || string(data) != "# old" {
		t.Fatalf("DownloadRevision = %q, %v", data, err)
	}
}

func TestCreateFileSendsMetadataThenContent(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mt != "multipart/related" || r.URL.Path != "/upload/drive/v3/files" || r.URL.Query().Get("uploadType") != "multipart" {
			t.Errorf("create: %s %s?%s as %q", r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Content-Type"))
			return
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		var parts []string
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			data, _ := io.ReadAll(p)
			parts = append(parts, p.Header.Get("Content-Type")+" "+string(data))
		}
		want := []string{`application/json; charset=UTF-8 {"name":"notes.md","mimeType":"text/markdown"}`, "text/markdown # Notes\n"}
		if len(parts) != 2 || parts[0] != want[0] || parts[1] != want[1] {
			t.Errorf("parts = %q, want %q", parts, want)
		}
		_, _ = w.Write([]byte(`{"id":"new1","name":"notes.md","mimeType":"text/markdown","headRevisionId":"r1"}`))
	}))
	f, err := c.CreateFile(context.Background(), NewFile{Name: "notes.md", MimeType: "text/markdown"}, "text/markdown", []byte("# Notes\n"))
	if err != nil || f.ID != "new1" || f.HeadRevisionID != "r1" {
		t.Fatalf("CreateFile = %+v, %v", f, err)
	}
}

func TestCreateFileImportsWithProperties(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		p, _ := mr.NextPart()
		meta, _ := io.ReadAll(p)
		p, _ = mr.NextPart()
		if want := `{"name":"Plan","mimeType":"application/vnd.google-apps.document","appProperties":{"md_source":"abc"}}`; string(meta) != want || p.Header.Get("Content-Type") != "text/markdown" {
			t.Errorf("metadata %s, media as %q; want %s as text/markdown", meta, p.Header.Get("Content-Type"), want)
		}
		_, _ = w.Write([]byte(`{"id":"doc1"}`))
	}))
	f, err := c.CreateFile(context.Background(), NewFile{Name: "Plan", MimeType: DocumentMimeType, AppProperties: map[string]string{"md_source": "abc"}}, "text/markdown", []byte("# Plan\n"))
	if err != nil || f.ID != "doc1" {
		t.Fatalf("CreateFile = %+v, %v", f, err)
	}
}

func TestSetAppProperties(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPatch || r.URL.Path != "/drive/v3/files/doc1" || string(got) != `{"appProperties":{"md_base":"r2"}}` {
			t.Errorf("set: %s %s %s", r.Method, r.URL.Path, got)
		}
		_, _ = w.Write([]byte(`{"id":"doc1"}`))
	}))
	if err := c.SetAppProperties(context.Background(), "doc1", map[string]string{"md_base": "r2"}); err != nil {
		t.Fatal(err)
	}
}
