package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/resumeee-app/resumeee-core"
)

// Presets enforces visibility, defaulting, and built-in immutability rules for tailoring presets.
type Presets struct {
	store *Store
}

// ListPresets returns all presets accessible to userID (built-in/system and user-owned)
// ordered built-ins first, then alphabetically by name and ID.
func (p *Presets) ListPresets(ctx context.Context, userID string) ([]core.Preset, error) {
	var list []core.Preset
	err := p.store.backend.Read(ctx, func(tx ReadTx) error {
		raw, listErr := tx.ListPresets(ctx, userID)
		if listErr != nil {
			return listErr
		}
		list = make([]core.Preset, 0, len(raw))
		for _, item := range raw {
			if item.UserID == "" || item.UserID == core.SystemUserID || item.UserID == userID {
				item.Language = item.EffectiveLanguage()
				list = append(list, cloneValue(item))
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(list, func(i, j int) bool {
		if list[i].IsBuiltIn() != list[j].IsBuiltIn() {
			return list[i].IsBuiltIn()
		}
		if list[i].Name != list[j].Name {
			return list[i].Name < list[j].Name
		}
		return list[i].ID < list[j].ID
	})
	return list, nil
}

// Preset retrieves a preset by id when accessible to userID (built-in or owned).
func (p *Presets) Preset(ctx context.Context, userID, id string) (*core.Preset, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: preset id cannot be empty", core.ErrValidation)
	}
	var out *core.Preset
	err := p.store.backend.Read(ctx, func(tx ReadTx) error {
		pr, getErr := tx.GetPreset(ctx, id)
		if getErr != nil {
			return getErr
		}
		if !presetReadableBy(pr, userID) {
			return core.ErrForbidden
		}
		cp := cloneValue(pr)
		cp.Language = cp.EffectiveLanguage()
		out = cp
		return nil
	})
	return out, err
}

// CreatePreset validates and creates a new tailoring preset.
func (p *Presets) CreatePreset(ctx context.Context, preset *core.Preset) error {
	if preset == nil {
		return fmt.Errorf("%w: preset cannot be nil", core.ErrValidation)
	}
	if strings.TrimSpace(preset.Name) == "" {
		return fmt.Errorf("%w: preset name is required", core.ErrValidation)
	}
	lang, langErr := core.ValidateLanguage(preset.Language)
	if langErr != nil {
		return fmt.Errorf("%w: %w", core.ErrValidation, langErr)
	}

	return p.store.backend.Write(ctx, func(tx Tx) error {
		toSave := cloneValue(preset)
		if toSave.ID == "" {
			toSave.ID = core.GenerateID("preset")
		}
		if existing, err := tx.GetPreset(ctx, toSave.ID); err == nil && existing != nil {
			return core.ErrConflict
		} else if err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}

		now := p.store.now()
		toSave.Language = lang
		if toSave.CreatedAt.IsZero() {
			toSave.CreatedAt = now
		}
		toSave.UpdatedAt = now
		if err := tx.PutPreset(ctx, toSave); err != nil {
			return err
		}
		*preset = *cloneValue(toSave)
		return nil
	})
}

// UpdatePreset updates a user-owned preset while blocking built-in mutations with ErrStrategyImmutable.
func (p *Presets) UpdatePreset(ctx context.Context, preset *core.Preset) error {
	if preset == nil || strings.TrimSpace(preset.ID) == "" {
		return fmt.Errorf("%w: preset id is required", core.ErrValidation)
	}
	if strings.TrimSpace(preset.Name) == "" {
		return fmt.Errorf("%w: preset name is required", core.ErrValidation)
	}
	lang, langErr := core.ValidateLanguage(preset.Language)
	if langErr != nil {
		return fmt.Errorf("%w: %w", core.ErrValidation, langErr)
	}

	return p.store.backend.Write(ctx, func(tx Tx) error {
		existing, err := tx.GetPreset(ctx, preset.ID)
		if err != nil {
			return err
		}
		if existing.IsBuiltIn() {
			return core.ErrStrategyImmutable
		}
		if existing.UserID != preset.UserID {
			return core.ErrForbidden
		}

		toSave := cloneValue(preset)
		toSave.Language = lang
		toSave.CreatedAt = existing.CreatedAt
		toSave.UpdatedAt = p.store.now()
		if err := tx.PutPreset(ctx, toSave); err != nil {
			return err
		}
		*preset = *cloneValue(toSave)
		return nil
	})
}

// DeletePreset deletes a user-owned preset while blocking built-in deletions with ErrStrategyImmutable.
func (p *Presets) DeletePreset(ctx context.Context, userID, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: preset id is required", core.ErrValidation)
	}

	return p.store.backend.Write(ctx, func(tx Tx) error {
		existing, err := tx.GetPreset(ctx, id)
		if err != nil {
			return err
		}
		if existing.IsBuiltIn() {
			return core.ErrStrategyImmutable
		}
		if existing.UserID != userID {
			return core.ErrForbidden
		}
		return tx.DeletePreset(ctx, id)
	})
}

// presetReadableBy reports whether pr may be read by userID.
func presetReadableBy(pr *core.Preset, userID string) bool {
	if pr.IsBuiltIn() || pr.UserID == "" || pr.UserID == core.SystemUserID {
		return true
	}
	return userID != "" && pr.UserID == userID
}
