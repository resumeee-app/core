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

// checkStatusError converts upstream HTTP error status codes into canonical domain LLM errors.
func checkStatusError(provider string, statusCode int, body []byte) error {
	if statusCode == http.StatusOK {
		return nil
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
