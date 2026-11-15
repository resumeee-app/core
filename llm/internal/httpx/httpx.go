// Package httpx provides shared HTTP transport configuration, bounded response reading,
// JSON request dispatch, and status-to-domain error mapping for LLM provider subpackages.
package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/resumeee-app/resumeee-core/llm"
)

// Transport timeout and response body safety bounds shared across HTTP-based LLM clients.
const (
	// DefaultTimeout bounds upstream LLM HTTP requests so stalled connections cannot hang workers.
	DefaultTimeout = 10 * time.Minute

	// MaxResponseBodyBytes caps response body reads at 16 MiB to prevent unbounded memory allocation.
	MaxResponseBodyBytes = 16 << 20
)

// defaultHTTPClient provides a shared, pooled HTTP client configured with DefaultTimeout.
var defaultHTTPClient = &http.Client{
	Timeout: DefaultTimeout,
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          50,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	},
}

// Settings holds normalized transport configuration for an LLM client backend.
type Settings struct {
	BaseURL      string
	HTTPClient   *http.Client
	DefaultModel string
	Timeout      time.Duration
}

// DefaultSettings returns a Settings value pre-populated with defaultBaseURL, defaultModel,
// and the shared pooled HTTP client.
func DefaultSettings(defaultBaseURL, defaultModel string) Settings {
	return Settings{
		BaseURL:      defaultBaseURL,
		HTTPClient:   defaultHTTPClient,
		DefaultModel: defaultModel,
	}
}

// SetBaseURL overrides BaseURL when baseURL is non-empty after whitespace trimming.
func (s *Settings) SetBaseURL(baseURL string) {
	if trimmed := strings.TrimSpace(baseURL); trimmed != "" {
		s.BaseURL = trimmed
	}
}

// SetHTTPClient overrides HTTPClient when client is non-nil.
func (s *Settings) SetHTTPClient(client *http.Client) {
	if client != nil {
		s.HTTPClient = client
	}
}

// SetDefaultModel overrides DefaultModel when model is non-empty after whitespace trimming.
func (s *Settings) SetDefaultModel(model string) {
	if trimmed := strings.TrimSpace(model); trimmed != "" {
		s.DefaultModel = trimmed
	}
}

// SetTimeout sets a custom request timeout when timeout is positive.
func (s *Settings) SetTimeout(timeout time.Duration) {
	if timeout > 0 {
		s.Timeout = timeout
	}
}

// Finalize ensures HTTPClient is non-nil and applies any configured custom Timeout on a shallow
// copy so shared or caller-supplied clients are never mutated in place.
func (s *Settings) Finalize() {
	if s.HTTPClient == nil {
		s.HTTPClient = defaultHTTPClient
	}
	if s.Timeout > 0 {
		cloned := *s.HTTPClient
		cloned.Timeout = s.Timeout
		s.HTTPClient = &cloned
	}
}

// ResolveClient returns c when non-nil, otherwise falling back to the shared pooled HTTP client.
func ResolveClient(c *http.Client) *http.Client {
	if c != nil {
		return c
	}
	return defaultHTTPClient
}

// ReadBoundedBody reads at most MaxResponseBodyBytes from r.
func ReadBoundedBody(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(io.LimitReader(r, MaxResponseBodyBytes))
}

// boundedReadCloser pairs a length-limited io.Reader with the underlying response io.Closer.
type boundedReadCloser struct {
	reader io.Reader
	closer io.Closer
}

// Read delegates reads to the length-limited reader.
func (b *boundedReadCloser) Read(p []byte) (int, error) {
	return b.reader.Read(p)
}

// Close closes the underlying HTTP response body when present.
func (b *boundedReadCloser) Close() error {
	if b.closer != nil {
		return b.closer.Close()
	}
	return nil
}

// boundedResponseTransport wraps an http.RoundTripper to cap every response body at limitBytes.
type boundedResponseTransport struct {
	base       http.RoundTripper
	limitBytes int64
}

// RoundTrip executes the HTTP request and bounds the returned response body stream.
func (t *boundedResponseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rt := t.base
	if rt == nil {
		rt = http.DefaultTransport
	}
	resp, err := rt.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil {
		return resp, err
	}
	limit := t.limitBytes
	if limit <= 0 {
		limit = MaxResponseBodyBytes
	}
	resp.Body = &boundedReadCloser{
		reader: io.LimitReader(resp.Body, limit),
		closer: resp.Body,
	}
	return resp, nil
}

// WrapBoundedClient returns a shallow clone of client whose transport caps response bodies
// at MaxResponseBodyBytes (used by SDK-managed HTTP transports).
func WrapBoundedClient(client *http.Client) *http.Client {
	return WrapBoundedClientWithLimit(client, MaxResponseBodyBytes)
}

// WrapBoundedClientWithLimit returns a shallow clone of client whose transport caps response bodies
// at limitBytes, defaulting to MaxResponseBodyBytes when limitBytes is non-positive.
func WrapBoundedClientWithLimit(client *http.Client, limitBytes int64) *http.Client {
	if limitBytes <= 0 {
		limitBytes = MaxResponseBodyBytes
	}
	resolved := ResolveClient(client)
	cloned := *resolved
	cloned.Transport = &boundedResponseTransport{
		base:       resolved.Transport,
		limitBytes: limitBytes,
	}
	return &cloned
}

// CheckStatusError maps non-200 upstream HTTP status codes to canonical domain llm sentinel errors
// without embedding raw upstream response body payloads into returned error messages.
func CheckStatusError(provider string, statusCode int, body []byte) error {
	return llm.MapHTTPStatusError(provider, statusCode, string(body))
}

// PostJSON marshals payload as JSON, sends an HTTP POST to url with headers, reads a bounded
// response body, and returns any non-200 status mapped through CheckStatusError.
func PostJSON(ctx context.Context, client *http.Client, url string, payload any, headers map[string]string, provider string) ([]byte, int, error) {
	reqBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to marshal %s request: %w", provider, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBytes))
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := ResolveClient(client).Do(httpReq)
	if err != nil {
		return nil, 0, fmt.Errorf("%s http request failed: %w", provider, err)
	}
	if resp == nil {
		return nil, 0, fmt.Errorf("%s http request returned empty response", provider)
	}
	if resp.Body != nil {
		defer func() { _ = resp.Body.Close() }()
	}

	bodyBytes, err := ReadBoundedBody(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to read response body: %w", err)
	}
	if err := CheckStatusError(provider, resp.StatusCode, bodyBytes); err != nil {
		return bodyBytes, resp.StatusCode, err
	}
	return bodyBytes, resp.StatusCode, nil
}
