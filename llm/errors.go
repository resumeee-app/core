package llm

import (
	"fmt"
	"strings"

	"github.com/resumeee-app/resumeee-core"
)

// taggedError associates an LLM sentinel message with core classification sentinels without altering Error().
type taggedError struct {
	msg  string
	tags []error
}

// Error returns the canonical error description for taggedError.
func (e *taggedError) Error() string {
	return e.msg
}

// Unwrap returns underlying classification sentinels for errors.Is evaluation.
func (e *taggedError) Unwrap() []error {
	return e.tags
}

// Sentinel errors returned by LLM adapters and registry.
var (
	// ErrBadRequest indicates the LLM provider rejected the request due to invalid parameters or schema (HTTP 400).
	ErrBadRequest error = &taggedError{
		msg:  "llm provider bad request: invalid prompt or schema",
		tags: []error{core.ErrPermanentFailure},
	}

	// ErrUnauthorized indicates the LLM provider rejected the API key or authentication token (HTTP 401/403).
	ErrUnauthorized error = &taggedError{
		msg:  "llm provider unauthorized: invalid API key",
		tags: []error{core.ErrUnauthorized, core.ErrPermanentFailure},
	}

	// ErrUpstreamQuota indicates upstream provider billing, credit balance, or account quota exhaustion.
	ErrUpstreamQuota error = &taggedError{
		msg:  "llm provider billing or account quota exhausted",
		tags: []error{core.ErrUpstreamQuota, core.ErrPermanentFailure},
	}

	// ErrRateLimited indicates the provider returned a transient 429 Too Many Requests response.
	ErrRateLimited error = &taggedError{
		msg:  "llm provider rate limit exceeded",
		tags: []error{core.ErrTransientFailure},
	}

	// ErrUpstream indicates a provider-side server error (HTTP 5xx).
	ErrUpstream error = &taggedError{
		msg:  "llm provider upstream error",
		tags: []error{core.ErrTransientFailure},
	}

	// ErrInvalidResponse indicates the LLM response was truncated, malformed, or failed schema unmarshaling.
	ErrInvalidResponse error = &taggedError{
		msg:  "llm provider returned invalid response",
		tags: []error{core.ErrPermanentFailure},
	}

	// ErrProviderNotFound indicates the requested LLM provider is not registered or configured.
	ErrProviderNotFound error = &taggedError{
		msg:  "llm provider not found",
		tags: []error{core.ErrPermanentFailure},
	}

	// ErrNoProviders indicates no LLM providers have been registered or configured.
	ErrNoProviders error = &taggedError{
		msg:  "no llm providers configured",
		tags: []error{core.ErrPermanentFailure},
	}
)

// upstreamQuotaIndicators lists provider error substrings identifying non-retryable billing/account exhaustion.
var upstreamQuotaIndicators = []string{
	"insufficient_quota",
	"exceeded your current quota",
	"billing_not_active",
	"billing account",
	"check billing",
	"credit balance is too low",
	"payment required",
	"account_deactivated",
	"billing_hard_limit_reached",
}

// isUpstreamQuotaMessage reports whether an upstream error payload describes account/billing exhaustion.
func isUpstreamQuotaMessage(body string) bool {
	lower := strings.ToLower(body)
	for _, marker := range upstreamQuotaIndicators {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// MapHTTPStatusError maps a non-200 HTTP status code and provider payload into canonical llm sentinels.
func MapHTTPStatusError(provider string, statusCode int, body string) error {
	trimmed := strings.TrimSpace(body)
	if statusCode != 200 && isUpstreamQuotaMessage(trimmed) {
		return fmt.Errorf("%w: status %d: %s", ErrUpstreamQuota, statusCode, body)
	}
	switch {
	case statusCode == 200:
		return nil
	case statusCode == 402:
		return fmt.Errorf("%w: status 402: %s", ErrUpstreamQuota, body)
	case statusCode == 400 || statusCode == 404 || statusCode == 422:
		return fmt.Errorf("%w: status %d: %s", ErrBadRequest, statusCode, body)
	case statusCode == 401 || statusCode == 403:
		return fmt.Errorf("%w: status %d: %s", ErrUnauthorized, statusCode, body)
	case statusCode == 429:
		return fmt.Errorf("%w: status 429: %s", ErrRateLimited, body)
	case statusCode >= 500:
		return fmt.Errorf("%w: status %d: %s", ErrUpstream, statusCode, body)
	default:
		if trimmed == "" {
			trimmed = fmt.Sprintf("%s api returned status %d", provider, statusCode)
			return fmt.Errorf("%s", trimmed)
		}
		return fmt.Errorf("%s api returned status %d: %s", provider, statusCode, body)
	}
}
