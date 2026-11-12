package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/resumeee-app/resumeee-core/domain"
)

// TokenVerifierProvider adapts any domain.TokenVerifier into a full auth.Provider.
type TokenVerifierProvider struct {
	name     string
	verifier domain.TokenVerifier
}

// NewTokenVerifierProvider wraps an existing domain.TokenVerifier as an auth.Provider.
func NewTokenVerifierProvider(name string, verifier domain.TokenVerifier) *TokenVerifierProvider {
	trimmed := strings.ToLower(strings.TrimSpace(name))
	if trimmed == "" {
		trimmed = "custom"
	}
	return &TokenVerifierProvider{
		name:     trimmed,
		verifier: verifier,
	}
}

// Name returns the configured provider identifier.
func (p *TokenVerifierProvider) Name() string {
	if p == nil || p.name == "" {
		return "custom"
	}
	return p.name
}

// VerifyIDToken delegates to the underlying domain.TokenVerifier.
func (p *TokenVerifierProvider) VerifyIDToken(ctx context.Context, idToken string) (*domain.TokenInfo, error) {
	if p == nil || p.verifier == nil {
		return nil, domain.ErrUnauthorized
	}
	return p.verifier.VerifyIDToken(ctx, idToken)
}

// AuthenticateRequest extracts the Bearer token and delegates to VerifyIDToken.
func (p *TokenVerifierProvider) AuthenticateRequest(r *http.Request) (*domain.TokenInfo, error) {
	if p == nil || p.verifier == nil {
		return nil, ErrProviderNotConfigured
	}
	token, err := ExtractBearerToken(r)
	if err != nil {
		return nil, err
	}
	return p.VerifyIDToken(r.Context(), token)
}
