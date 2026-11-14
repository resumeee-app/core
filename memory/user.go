package memory

import (
	"context"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

// User retrieves a user record by ID or returns core.ErrNotFound.
func (s *Store) User(_ context.Context, id string) (*core.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	u, ok := s.users[id]
	if !ok {
		return nil, core.ErrNotFound
	}
	return clone(u), nil
}

// UpsertUser creates or updates an authenticated user record while preserving elevated admin privileges.
func (s *Store) UpsertUser(_ context.Context, user *core.User) error {
	if user == nil || user.ID == "" {
		return core.ErrValidation
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	existing, ok := s.users[user.ID]
	if !ok {
		u := clone(user)
		if u.CreatedAt.IsZero() {
			u.CreatedAt = now
		}
		u.UpdatedAt = now
		s.users[user.ID] = u
		return nil
	}

	existing.Email = user.Email
	existing.FullName = user.FullName
	// Preserve existing admin role unless explicitly promoted.
	if existing.Role == core.UserRoleAdmin && user.Role != core.UserRoleAdmin {
		existing.Role = core.UserRoleAdmin
	} else if user.Role != "" {
		existing.Role = user.Role
	}
	if user.Tier != "" {
		existing.Tier = user.Tier
	}
	existing.IsActive = user.IsActive
	existing.UpdatedAt = now
	s.users[user.ID] = existing
	return nil
}
