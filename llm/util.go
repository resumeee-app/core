package llm

import (
	"fmt"
	"io"
	"net/http"
)

// maxLLMResponseBodyBytes limits the bytes read from upstream LLM APIs to 16 MB to prevent memory exhaustion.
const maxLLMResponseBodyBytes = 16 << 20

// readBoundedBody reads an HTTP response body with an upper size bound of 16 MB.
func readBoundedBody(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(io.LimitReader(r, maxLLMResponseBodyBytes))
}

func resolveHTTPClient(c *http.Client) *http.Client {
	if c != nil {
		return c
	}
	return defaultLLMHTTPClient
}

type boundedReadCloser struct {
	reader io.Reader
	closer io.Closer
}

func (b *boundedReadCloser) Read(p []byte) (int, error) {
	return b.reader.Read(p)
}

func (b *boundedReadCloser) Close() error {
	if b.closer != nil {
		return b.closer.Close()
	}
	return nil
}

type boundedResponseTransport struct {
	base http.RoundTripper
}

func (t *boundedResponseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rt := t.base
	if rt == nil {
		rt = http.DefaultTransport
	}
	resp, err := rt.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil {
		return resp, err
	}
	resp.Body = &boundedReadCloser{
		reader: io.LimitReader(resp.Body, maxLLMResponseBodyBytes),
		closer: resp.Body,
	}
	return resp, nil
}

// wrapBoundedHTTPClient returns a shallow clone of client whose Transport enforces
// maxLLMResponseBodyBytes on upstream response bodies (used for SDK-managed HTTP transports).
func wrapBoundedHTTPClient(client *http.Client) *http.Client {
	resolved := resolveHTTPClient(client)
	cloned := *resolved
	cloned.Transport = &boundedResponseTransport{base: resolved.Transport}
	return &cloned
}

// checkStatusError converts upstream HTTP error status codes into canonical domain LLM errors.
func checkStatusError(provider string, statusCode int, body []byte) error {
	if statusCode == http.StatusOK {
		return nil
	}
	if statusCode == http.StatusBadRequest {
		return fmt.Errorf("%w: status 400: %s", ErrBadRequest, string(body))
	}
	if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
		return fmt.Errorf("%w: status %d: %s", ErrUnauthorized, statusCode, string(body))
	}
	if statusCode == http.StatusTooManyRequests {
		return fmt.Errorf("%w: status 429: %s", ErrRateLimited, string(body))
	}
	if statusCode >= 500 {
		return fmt.Errorf("%w: status %d: %s", ErrUpstream, statusCode, string(body))
	}
	return fmt.Errorf("%s api returned status %d: %s", provider, statusCode, string(body))
}

// stripCodeFences removes surrounding markdown formatting (e.g. ```json ... ```) or conversational preambles from raw LLM output.
func stripCodeFences(s string) string {
	return ExtractJSON(s)
}
