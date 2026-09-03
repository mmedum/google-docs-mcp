// Package gapi is a thin raw REST client for the Google Docs and Drive
// APIs. Requests are built by hand so Developer Preview fields that the
// generated client lacks can be sent; responses decode into the
// generated types where they exist. No MCP imports live here.
package gapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/time/rate"
)

// Default endpoints.
const (
	DefaultDocsBaseURL  = "https://docs.googleapis.com"
	DefaultDriveBaseURL = "https://www.googleapis.com/drive/v3"
)

// RetryPolicy bounds retries for transient failures.
type RetryPolicy struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

// DefaultRetry is exponential backoff with jitter, capped at 30 seconds.
func DefaultRetry() RetryPolicy {
	return RetryPolicy{MaxAttempts: 5, BaseDelay: 500 * time.Millisecond, MaxDelay: 30 * time.Second}
}

// Options configure a Client. Zero values are production defaults.
type Options struct {
	// BaseTransport sits under the OAuth transport. nil uses http.DefaultTransport.
	BaseTransport http.RoundTripper
	DocsBaseURL   string
	DriveBaseURL  string
	Logger        *slog.Logger
	// Timeout applies per attempt.
	Timeout time.Duration
	Retry   RetryPolicy
	// Limiters stay under the per-user quotas (300 reads and 60 writes per minute).
	ReadLimiter  *rate.Limiter
	WriteLimiter *rate.Limiter
	UserAgent    string
	// Sleep is replaced in tests.
	Sleep func(context.Context, time.Duration) error
}

// Client talks to Google with one user's credentials.
type Client struct {
	httpc    *http.Client
	docs     string
	drive    string
	log      *slog.Logger
	timeout  time.Duration
	retry    RetryPolicy
	readLim  *rate.Limiter
	writeLim *rate.Limiter
	// Drive writes (comments) have a far higher quota than Docs batches.
	driveWriteLim *rate.Limiter
	ua            string
	sleep         func(context.Context, time.Duration) error
}

// New builds a client whose requests carry tokens from ts.
func New(ts oauth2.TokenSource, o Options) *Client {
	base := o.BaseTransport
	if base == nil {
		base = http.DefaultTransport
	}
	c := &Client{
		httpc:    &http.Client{Transport: &oauth2.Transport{Source: ts, Base: base}},
		docs:     strings.TrimRight(o.DocsBaseURL, "/"),
		drive:    strings.TrimRight(o.DriveBaseURL, "/"),
		log:      o.Logger,
		timeout:  o.Timeout,
		retry:    o.Retry,
		readLim:  o.ReadLimiter,
		writeLim: o.WriteLimiter,
		ua:       o.UserAgent,
		sleep:    o.Sleep,
	}
	if c.docs == "" {
		c.docs = DefaultDocsBaseURL
	}
	if c.drive == "" {
		c.drive = DefaultDriveBaseURL
	}
	if c.log == nil {
		c.log = slog.New(slog.DiscardHandler)
	}
	if c.timeout <= 0 {
		c.timeout = 60 * time.Second
	}
	if c.retry.MaxAttempts <= 0 {
		c.retry = DefaultRetry()
	}
	if c.readLim == nil {
		c.readLim = rate.NewLimiter(rate.Limit(4), 20)
	}
	if c.writeLim == nil {
		c.writeLim = rate.NewLimiter(rate.Limit(0.8), 5)
	}
	c.driveWriteLim = rate.NewLimiter(rate.Limit(5), 10)
	if c.ua == "" {
		c.ua = "google-docs-mcp"
	}
	if c.sleep == nil {
		c.sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
	return c
}

type reqKind int

const (
	kindRead reqKind = iota
	kindWrite
	kindDriveWrite
)

// do performs one logical request with rate limiting and retries and
// returns the response body.
func (c *Client) do(ctx context.Context, k reqKind, method, rawURL string, body []byte) ([]byte, error) {
	return c.doAccept(ctx, k, method, rawURL, body, "application/json")
}

// doAccept is do with an explicit Accept header (exports return bytes).
func (c *Client) doAccept(ctx context.Context, k reqKind, method, rawURL string, body []byte, accept string) ([]byte, error) {
	lim := c.readLim
	switch k {
	case kindWrite:
		lim = c.writeLim
	case kindDriveWrite:
		lim = c.driveWriteLim
	}
	if err := lim.Wait(ctx); err != nil {
		return nil, err
	}
	path := redactPath(rawURL)
	var lastErr error
	for attempt := 1; attempt <= c.retry.MaxAttempts; attempt++ {
		res, err := c.once(ctx, method, rawURL, body, accept)
		if err == nil {
			c.log.DebugContext(ctx, "google api", "method", method, "path", path, "status", res.status, "ms", res.elapsed.Milliseconds(), "attempt", attempt)
			return res.body, nil
		}
		lastErr = err
		retry, after := retryable(k, err)
		c.log.DebugContext(ctx, "google api error", "method", method, "path", path, "attempt", attempt, "retry", retry && attempt < c.retry.MaxAttempts, "err", err)
		if !retry || attempt == c.retry.MaxAttempts {
			break
		}
		if err := c.sleep(ctx, c.backoff(attempt, after)); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

type attemptResult struct {
	status  int
	body    []byte
	elapsed time.Duration
}

// transientError carries a Retry-After hint alongside an APIError.
type transientError struct {
	err   error
	after time.Duration
}

func (t *transientError) Error() string { return t.err.Error() }
func (t *transientError) Unwrap() error { return t.err }

func (c *Client) once(ctx context.Context, method, rawURL string, body []byte, accept string) (*attemptResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", c.ua)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	start := time.Now()
	resp, err := c.httpc.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || (errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil) {
			return nil, err
		}
		return nil, wrapTransportError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: read body: %w", ErrNetwork, err)
	}
	res := &attemptResult{status: resp.StatusCode, body: data, elapsed: time.Since(start)}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return res, nil
	}
	apiErr := parseAPIError(resp.StatusCode, method, redactPath(rawURL), data)
	if resp.StatusCode == 429 || resp.StatusCode >= 500 {
		return nil, &transientError{err: apiErr, after: parseRetryAfter(resp.Header.Get("Retry-After"))}
	}
	return nil, apiErr
}

// retryable decides whether an attempt may be repeated. Reads retry on
// any transient failure. Writes retry only when Google answered 429 or
// 503, which proves nothing was applied; a network failure on a write is
// reported as ambiguous instead.
func retryable(k reqKind, err error) (bool, time.Duration) {
	var te *transientError
	if errors.As(err, &te) {
		var ae *APIError
		if k == kindWrite && errors.As(te.err, &ae) && ae.Status != 429 && ae.Status != 503 {
			return false, 0
		}
		return true, te.after
	}
	if errors.Is(err, ErrNetwork) {
		return k == kindRead, 0
	}
	return false, 0
}

func (c *Client) backoff(attempt int, after time.Duration) time.Duration {
	if after > 0 {
		return min(after, c.retry.MaxDelay)
	}
	d := c.retry.BaseDelay << (attempt - 1)
	if d > c.retry.MaxDelay || d <= 0 {
		d = c.retry.MaxDelay
	}
	// Full jitter.
	return time.Duration(rand.Int64N(int64(d)) + int64(d)/4) //nolint:gosec // jitter, not security
}

func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

var idInPath = regexp.MustCompile(`/(documents|files)/([^/?]+)`)

// ShortID shortens a document or file id for logs.
func ShortID(id string) string {
	if len(id) > 6 {
		return id[:6] + "…"
	}
	return id
}

// redactPath shortens document and file ids in URLs before they reach logs.
func redactPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "?"
	}
	return idInPath.ReplaceAllStringFunc(u.Path, func(m string) string {
		parts := idInPath.FindStringSubmatch(m)
		return "/" + parts[1] + "/" + ShortID(parts[2])
	})
}

// wrapAmbiguousWrite marks a write whose outcome is unknown.
func wrapAmbiguousWrite(err error) error {
	if errors.Is(err, ErrNetwork) {
		return fmt.Errorf("%w: %w", ErrAmbiguous, err)
	}
	return err
}
