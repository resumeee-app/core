package domain

import "context"

type contextKey string

// UserIDContextKey is the context key for authenticated tenant user IDs.
const UserIDContextKey contextKey = "resumeee.user_id"

// IsAdminContextKey is the context key for administrative privilege indicators.
const IsAdminContextKey contextKey = "resumeee.is_admin"

// WithUserID binds an authenticated tenant user ID into the context.
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, UserIDContextKey, userID)
}

// WithIsAdmin binds an administrative privilege indicator into the context.
func WithIsAdmin(ctx context.Context, isAdmin bool) context.Context {
	return context.WithValue(ctx, IsAdminContextKey, isAdmin)
}

// IsAdminFromContext extracts whether the context has administrative privileges.
func IsAdminFromContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	val, ok := ctx.Value(IsAdminContextKey).(bool)
	return ok && val
}

// UserIDFromContext extracts the authenticated tenant user ID from the context.
// Returns ErrUnauthorized if the context lacks an authenticated user.
func UserIDFromContext(ctx context.Context) (string, error) {
	if ctx == nil {
		return "", ErrUnauthorized
	}
	val, ok := ctx.Value(UserIDContextKey).(string)
	if !ok || val == "" {
		return "", ErrUnauthorized
	}
	return val, nil
}
