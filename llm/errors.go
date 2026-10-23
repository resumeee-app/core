package llm

import "errors"

// Sentinel errors returned by LLM adapters and registry.
var (
	// ErrUnauthorized indicates the LLM provider rejected the API key or authentication token (HTTP 401/403).
	ErrUnauthorized = errors.New("llm provider unauthorized: invalid API key")

	// ErrRateLimited indicates the provider returned a 429 Too Many Requests response.
	ErrRateLimited = errors.New("llm provider rate limit exceeded")

	// ErrUpstream indicates a provider-side server error (HTTP 5xx).
	ErrUpstream = errors.New("llm provider upstream error")

	// ErrInvalidResponse indicates the LLM response was truncated, malformed, or failed schema unmarshaling.
	ErrInvalidResponse = errors.New("llm provider returned invalid response")

	// ErrProviderNotFound indicates the requested LLM provider is not registered or configured.
	ErrProviderNotFound = errors.New("llm provider not found")

	// ErrNoProviders indicates no LLM providers have been registered or configured.
	ErrNoProviders = errors.New("no llm providers configured")
)
