package core

import (
	"context"
	"strings"
)

// TokenInfo contains identity attributes verified by a TokenVerifier.
type TokenInfo struct {
	UID   string `json:"uid"`
	Email string `json:"email"`
	Admin bool   `json:"admin,omitempty"`
	Role  string `json:"role,omitempty"`
}

// IsAdmin returns true if the TokenInfo carries administrative privileges via the Admin boolean flag
// or an administrative UserRole.
func (t *TokenInfo) IsAdmin() bool {
	if t == nil {
		return false
	}
	return t.Admin || strings.EqualFold(t.Role, string(UserRoleAdmin))
}

// TokenVerifier validates bearer identity tokens from an external authentication provider.
type TokenVerifier interface {
	VerifyIDToken(ctx context.Context, idToken string) (*TokenInfo, error)
}

// AuthProvider abstracts identity credential validation and provider naming.
type AuthProvider interface {
	TokenVerifier
	Name() string
}
