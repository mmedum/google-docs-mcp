package doc

import (
	"errors"
	"regexp"
	"strings"
)

var (
	bareID   = regexp.MustCompile(`^[A-Za-z0-9_-]{20,}$`)
	urlID    = regexp.MustCompile(`/d/([A-Za-z0-9_-]{20,})`)
	queryID  = regexp.MustCompile(`[?&]id=([A-Za-z0-9_-]{20,})`)
	errBadID = errors.New("not a Google Docs id or URL")
)

// ParseID accepts a document id or any docs.google.com URL and returns
// the id.
func ParseID(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errBadID
	}
	if bareID.MatchString(s) {
		return s, nil
	}
	if m := urlID.FindStringSubmatch(s); m != nil {
		return m[1], nil
	}
	if m := queryID.FindStringSubmatch(s); m != nil {
		return m[1], nil
	}
	return "", errBadID
}

// DocumentURL is the canonical edit URL for an id.
func DocumentURL(id string) string {
	return "https://docs.google.com/document/d/" + id + "/edit"
}
