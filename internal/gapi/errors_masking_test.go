package gapi

import (
	"strings"
	"testing"
)

// TestAPermissionDenialDoesNotRepeatTheAccount: Google names the account
// in the message of a 403, and that message is repeated verbatim into an
// error string. The MCP stdio transport says clients may capture and
// forward a server's stderr, and the protocol's logging section says log
// messages must not carry personal identifying information.
func TestAPermissionDenialDoesNotRepeatTheAccount(t *testing.T) {
	body := []byte(`{"error":{"code":403,"status":"PERMISSION_DENIED",` +
		`"message":"The user someone.private@example.com does not have permission."}}`)
	e := parseAPIError(403, "GET", "/v1/documents/x", body)
	if strings.Contains(e.Error(), "someone.private@example.com") {
		t.Errorf("the address was repeated verbatim: %s", e.Error())
	}
	if !strings.Contains(e.Error(), "…@example.com") {
		t.Errorf("the domain should survive so the account is still identifiable: %s", e.Error())
	}
	// A body that is not an error envelope is kept verbatim as the
	// message, which is the worse of the two paths.
	raw := parseAPIError(500, "GET", "/x", []byte("upstream refused someone.private@example.com"))
	if strings.Contains(raw.Error(), "someone.private@example.com") {
		t.Errorf("an unparsed body was kept verbatim: %s", raw.Error())
	}
	// A message with no address in it is untouched.
	plain := parseAPIError(404, "GET", "/x", []byte(`{"error":{"message":"Document not found."}}`))
	if plain.Message != "Document not found." {
		t.Errorf("message = %q, want it unchanged", plain.Message)
	}
}
