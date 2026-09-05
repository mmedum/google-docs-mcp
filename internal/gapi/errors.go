package gapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/oauth2"
)

// Sentinel error classes. Every error returned by the client wraps
// exactly one of them so callers can branch with errors.Is.
var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrMissingScope = errors.New("missing scope")
	ErrForbidden    = errors.New("forbidden")
	ErrNotFound     = errors.New("not found")
	ErrRateLimited  = errors.New("rate limited")
	ErrServer       = errors.New("server error")
	ErrInvalid      = errors.New("invalid request")
	ErrConflict     = errors.New("revision conflict")
	ErrNetwork      = errors.New("network error")
	ErrAmbiguous    = errors.New("ambiguous outcome")
	ErrUnexpected   = errors.New("unexpected response")
	// ErrNoCredentials is the token-source error when no login exists.
	ErrNoCredentials = errors.New("no credentials stored; run `google-docs-mcp login`")
)

// NoCredentials is a token source that always fails with ErrNoCredentials
// so a server without a login still starts and answers every call with
// an actionable auth error.
type NoCredentials struct{ Reason error }

// Token implements oauth2.TokenSource.
func (n NoCredentials) Token() (*oauth2.Token, error) {
	if n.Reason != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoCredentials, n.Reason)
	}
	return nil, ErrNoCredentials
}

// APIError is a non-2xx response from Google, decoded from the standard
// error envelope.
type APIError struct {
	Status  int
	RPC     string // e.g. NOT_FOUND, PERMISSION_DENIED
	Reason  string // e.g. ACCESS_TOKEN_SCOPE_INSUFFICIENT
	Message string
	Method  string
	Path    string
}

func (e *APIError) Error() string {
	// The reason is the part that says whether a refusal can be acted on
	// — storageQuotaExceeded and downloadRestrictedForRevision both arrive
	// as a bare 403 otherwise — so it goes in the message, not just in the
	// field the classifier reads.
	status := e.RPC
	if e.Reason != "" && e.Reason != e.RPC {
		if status == "" {
			status = e.Reason
		} else {
			status += " (" + e.Reason + ")"
		}
	}
	return fmt.Sprintf("google api: HTTP %d %s: %s [%s %s]", e.Status, status, e.Message, e.Method, e.Path)
}

// rateLimit403 are the reasons Drive answers with 403 that mean a quota
// rather than a permission: it reports throttling as 403, not 429, and
// prescribes exponential backoff for it. The value says whether backing
// off can help — the per-minute limits refill, the daily project quota
// does not.
//
// Keys are normalised by reasonKey. The Drive guide spells these
// camelCase in the legacy `error.errors[]` envelope, while a
// google.rpc.ErrorInfo detail spells the same condition UPPER_SNAKE, and
// parseAPIError prefers the detail — so a lookup on the literal string
// would miss silently if Drive ever sends both.
//
// https://developers.google.com/workspace/drive/api/guides/handle-errors
var rateLimit403 = map[string]bool{
	reasonKey("rateLimitExceeded"):        true,
	reasonKey("userRateLimitExceeded"):    true,
	reasonKey("sharingRateLimitExceeded"): true,
	reasonKey("dailyLimitExceeded"):       false,
}

// reasonKey folds the two spellings of a reason onto one key.
func reasonKey(reason string) string {
	return strings.ToLower(strings.ReplaceAll(reason, "_", ""))
}

// retryableThrottle reports whether backing off can clear a 403.
func retryableThrottle(e *APIError) bool {
	return e.Status == 403 && rateLimit403[reasonKey(e.Reason)]
}

// throttled reports whether an error is a 403 that means "slow down".
func throttled(e *APIError) bool {
	_, ok := rateLimit403[reasonKey(e.Reason)]
	return e.Status == 403 && ok
}

// Unwrap maps the response onto a sentinel class.
func (e *APIError) Unwrap() error {
	msg := strings.ToLower(e.Message)
	switch {
	case e.Status == 401:
		return ErrUnauthorized
	case e.Status == 403 && (e.Reason == "ACCESS_TOKEN_SCOPE_INSUFFICIENT" || strings.Contains(msg, "insufficient authentication scopes")):
		return ErrMissingScope
	case throttled(e):
		return ErrRateLimited
	case e.Status == 403:
		return ErrForbidden
	case e.Status == 404:
		return ErrNotFound
	case e.Status == 429:
		return ErrRateLimited
	case e.Status >= 500:
		return ErrServer
	case e.Status == 400 && strings.Contains(msg, "revision"):
		return ErrConflict
	case e.Status == 400:
		return ErrInvalid
	}
	return ErrUnexpected
}

// AuthError is a failure to obtain an access token (revoked or expired
// refresh token, bad client secret).
type AuthError struct {
	Code string
	Msg  string
}

func (e *AuthError) Error() string {
	if e.Msg != "" {
		return fmt.Sprintf("google auth: %s: %s", e.Code, e.Msg)
	}
	return "google auth: " + e.Code
}

// Unwrap classifies every auth failure as unauthorized.
func (e *AuthError) Unwrap() error { return ErrUnauthorized }

type googleErrorBody struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
		Errors  []struct {
			Reason  string `json:"reason"`
			Message string `json:"message"`
		} `json:"errors"`
		Details []struct {
			Type   string `json:"@type"`
			Reason string `json:"reason"`
		} `json:"details"`
	} `json:"error"`
}

func parseAPIError(status int, method, path string, body []byte) *APIError {
	e := &APIError{Status: status, Method: method, Path: path}
	var g googleErrorBody
	if err := json.Unmarshal(body, &g); err == nil && g.Error.Message != "" {
		e.Message = g.Error.Message
		e.RPC = g.Error.Status
		for _, d := range g.Error.Details {
			if d.Reason != "" {
				e.Reason = d.Reason
				break
			}
		}
		if e.Reason == "" && len(g.Error.Errors) > 0 {
			e.Reason = g.Error.Errors[0].Reason
		}
	} else {
		e.Message = strings.TrimSpace(string(body))
		if r := []rune(e.Message); len(r) > 300 {
			e.Message = string(r[:300]) + "…"
		}
		if e.Message == "" {
			e.Message = "empty error body"
		}
	}
	return e
}

// wrapTransportError turns errors from the oauth2 transport into typed
// auth errors, and everything else into ErrNetwork.
func wrapTransportError(err error) error {
	if errors.Is(err, ErrNoCredentials) {
		return &AuthError{Code: "no_credentials", Msg: "run `google-docs-mcp login`"}
	}
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		code := re.ErrorCode
		if code == "" {
			code = fmt.Sprintf("HTTP %d", re.Response.StatusCode)
		}
		return &AuthError{Code: code, Msg: re.ErrorDescription}
	}
	if strings.Contains(err.Error(), "oauth2:") {
		return &AuthError{Code: "token", Msg: err.Error()}
	}
	return fmt.Errorf("%w: %w", ErrNetwork, err)
}

// Classes lists every class Class can return, so a test can check this
// half of the vocabulary against the service's list rather than trusting
// two comments to agree.
func Classes() []string {
	return []string{"auth", "forbidden", "not_found", "rate_limited", "server",
		"invalid", "conflict", "network", "ambiguous_outcome", "unexpected"}
}

// Class returns a short lower-case class name for an error, for
// LLM-facing messages. Every value it returns is in Classes, and
// service.Classes is the whole vocabulary this server speaks.
func Class(err error) string {
	switch {
	case errors.Is(err, ErrMissingScope):
		return "forbidden"
	case errors.Is(err, ErrUnauthorized):
		return "auth"
	case errors.Is(err, ErrForbidden):
		return "forbidden"
	case errors.Is(err, ErrNotFound):
		return "not_found"
	case errors.Is(err, ErrRateLimited):
		return "rate_limited"
	case errors.Is(err, ErrServer):
		return "server"
	case errors.Is(err, ErrConflict):
		return "conflict"
	case errors.Is(err, ErrInvalid):
		return "invalid"
	case errors.Is(err, ErrAmbiguous):
		// Not "ambiguous": that word is taken by a target matching
		// several things, which asks the caller to choose. This one says
		// the write may or may not have landed, which asks them to look.
		return "ambiguous_outcome"
	case errors.Is(err, ErrNetwork):
		return "network"
	}
	return "unexpected"
}
