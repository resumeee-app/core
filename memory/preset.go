package memory

import (
	"context"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

// presetVisibleToUser reports whether p is owned by userID or is a shared built-in preset.
func presetVisibleToUser(p *core.Preset, userID string) bool {
	return p.UserID == userID || p.IsBuiltIn()
}

// ListPresets returns user-owned and shared built-in presets.
func (s *Store) ListPresets(_ context.Context, userID string) ([]core.Preset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var list []core.Preset
	for _, pr := range s.presets {
		if presetVisibleToUser(pr, userID) {
			list = append(list, *clone(pr))
		}
	}
	return list, nil
}

// Preset fetches a user-owned or built-in preset by ID.
func (s *Store) Preset(_ context.Context, userID, id string) (*core.Preset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	pr, ok := s.presets[id]
	if !ok || !presetVisibleToUser(pr, userID) {
		return nil, core.ErrNotFound
	}
	return clone(pr), nil
}

// CreatePreset persists a preset, defaulting blank Language values to core.DefaultLanguage.
func (s *Store) CreatePreset(_ context.Context, preset *core.Preset) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if preset == nil {
		return core.ErrValidation
	}
	if preset.Language == "" {
		preset.Language = core.DefaultLanguage
	}
	if _, exists := s.presets[preset.ID]; exists {
		return core.ErrConflict
	}
	now := time.Now().UTC()
	if preset.CreatedAt.IsZero() {
		preset.CreatedAt = now
	}
	preset.UpdatedAt = now
	s.presets[preset.ID] = clone(preset)
	return nil
}

// UpdatePreset updates a user-owned preset while rejecting mutations to built-in strategies.
func (s *Store) UpdatePreset(_ context.Context, preset *core.Preset) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if preset == nil {
		return core.ErrValidation
	}
	if preset.IsBuiltIn() {
		return core.ErrStrategyImmutable
	}
	existing, exists := s.presets[preset.ID]
	if !exists || existing.UserID != preset.UserID {
		return core.ErrNotFound
	}
	if existing.IsBuiltIn() {
		return core.ErrStrategyImmutable
	}
	if preset.Language == "" {
		preset.Language = core.DefaultLanguage
	}
	preset.UpdatedAt = time.Now().UTC()
	s.presets[preset.ID] = clone(preset)
	return nil
}

// DeletePreset deletes a user-owned preset while rejecting deletions of built-in strategies.
func (s *Store) DeletePreset(_ context.Context, userID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if (core.Preset{ID: id, UserID: userID}).IsBuiltIn() {
		return core.ErrStrategyImmutable
	}
	existing, exists := s.presets[id]
	if !exists || existing.UserID != userID {
		return core.ErrNotFound
	}
	if existing.IsBuiltIn() {
		return core.ErrStrategyImmutable
	}
	delete(s.presets, id)
	return nil
}
