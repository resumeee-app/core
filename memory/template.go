package memory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

// isImmutableTemplate reports whether tmpl is a system or built-in template set.
func isImmutableTemplate(tmpl *core.TemplateSet) bool {
	return tmpl.IsBuiltIn || strings.HasPrefix(tmpl.ID, "builtin-") || tmpl.UserID == core.SystemUserID
}

// templateVisibleToUser reports whether tmpl is owned by userID or is a shared built-in template.
func templateVisibleToUser(tmpl *core.TemplateSet, userID string) bool {
	return tmpl.UserID == userID || tmpl.IsBuiltIn || tmpl.UserID == core.SystemUserID
}

// ListTemplates returns user-owned and built-in template sets.
func (s *Store) ListTemplates(_ context.Context, userID string) ([]core.TemplateSet, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var list []core.TemplateSet
	for _, tmpl := range s.templates {
		if templateVisibleToUser(tmpl, userID) {
			list = append(list, *clone(tmpl))
		}
	}
	return list, nil
}

// Template fetches a visible template set matching ID.
func (s *Store) Template(_ context.Context, userID, id string) (*core.TemplateSet, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tmpl, ok := s.templates[id]
	if !ok || !templateVisibleToUser(tmpl, userID) {
		return nil, core.ErrNotFound
	}
	return clone(tmpl), nil
}

// CreateTemplate persists a new non-nil template set when its ID is not already present.
func (s *Store) CreateTemplate(_ context.Context, tmpl *core.TemplateSet) error {
	if tmpl == nil {
		return fmt.Errorf("%w: template set cannot be nil", core.ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.templates[tmpl.ID]; exists {
		return core.ErrConflict
	}
	now := time.Now().UTC()
	if tmpl.CreatedAt.IsZero() {
		tmpl.CreatedAt = now
	}
	tmpl.UpdatedAt = now
	s.templates[tmpl.ID] = clone(tmpl)
	return nil
}

// UpdateTemplate replaces a user-owned template set while protecting built-in templates.
func (s *Store) UpdateTemplate(_ context.Context, tmpl *core.TemplateSet) error {
	if tmpl == nil {
		return fmt.Errorf("%w: template set cannot be nil", core.ErrValidation)
	}
	if isImmutableTemplate(tmpl) {
		return core.ErrStrategyImmutable
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, exists := s.templates[tmpl.ID]
	if !exists {
		return core.ErrNotFound
	}
	if isImmutableTemplate(existing) {
		return core.ErrStrategyImmutable
	}
	if existing.UserID != tmpl.UserID {
		return core.ErrForbidden
	}
	tmpl.UpdatedAt = time.Now().UTC()
	s.templates[tmpl.ID] = clone(tmpl)
	return nil
}

// DeleteTemplate removes a user-owned template set while protecting built-in templates.
func (s *Store) DeleteTemplate(_ context.Context, userID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, exists := s.templates[id]
	if !exists {
		return core.ErrNotFound
	}
	if isImmutableTemplate(existing) {
		return core.ErrStrategyImmutable
	}
	if existing.UserID != userID {
		return core.ErrForbidden
	}
	delete(s.templates, id)
	return nil
}
