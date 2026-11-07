package domain

import "context"

type contextKey string

// UserIDContextKey is the context key for authenticated tenant user IDs.
const UserIDContextKey contextKey = "resumeee.user_id"

// WithUserID binds an authenticated tenant user ID into the context.
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, UserIDContextKey, userID)
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
