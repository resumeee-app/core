package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/resumeee-app/resumeee-core"
)

// Templates enforces template syntax validation, visibility, and built-in immutability rules.
type Templates struct {
	store *Store
}

// templateQueryOwners returns the distinct owner user IDs eligible when listing templates for userID.
func templateQueryOwners(userID string) []string {
	owners := []string{core.SystemUserID}
	if userID != core.SystemUserID {
		owners = append(owners, userID)
	}
	return owners
}

// ListTemplates returns built-in and user-owned template sets ordered built-ins first, then by name and ID.
func (t *Templates) ListTemplates(ctx context.Context, userID string) ([]core.TemplateSet, error) {
	var list []core.TemplateSet
	err := t.store.backend.Read(ctx, func(tx ReadTx) error {
		raw, listErr := tx.ListTemplates(ctx, templateQueryOwners(userID), []string{builtInIDPrefix}, true)
		if listErr != nil {
			return listErr
		}
		list = make([]core.TemplateSet, 0, len(raw))
		for _, item := range raw {
			if isTemplateVisible(&item, userID) {
				list = append(list, cloneValue(item))
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(list, func(i, j int) bool {
		if list[i].IsBuiltIn != list[j].IsBuiltIn {
			return list[i].IsBuiltIn
		}
		if list[i].Name != list[j].Name {
			return list[i].Name < list[j].Name
		}
		return list[i].ID < list[j].ID
	})
	return list, nil
}

// Template retrieves a template set by id when visible to userID.
func (t *Templates) Template(ctx context.Context, userID, id string) (*core.TemplateSet, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: template id cannot be empty", core.ErrValidation)
	}
	var out *core.TemplateSet
	err := t.store.backend.Read(ctx, func(tx ReadTx) error {
		tmpl, getErr := tx.Template(ctx, id)
		if getErr != nil {
			return getErr
		}
		if !isTemplateVisible(tmpl, userID) {
			return core.ErrForbidden
		}
		out = cloneValue(tmpl)
		return nil
	})
	return out, err
}

// CreateTemplate validates syntax and stores a new template set.
func (t *Templates) CreateTemplate(ctx context.Context, tmpl *core.TemplateSet) error {
	if tmpl == nil {
		return fmt.Errorf("%w: template cannot be nil", core.ErrValidation)
	}
	if strings.TrimSpace(tmpl.Name) == "" {
		return fmt.Errorf("%w: template name is required", core.ErrValidation)
	}
	if err := (core.DefaultTemplateValidator{}).Validate(tmpl); err != nil {
		return err
	}

	return t.store.backend.Write(ctx, func(tx Tx) error {
		toSave := cloneValue(tmpl)
		if toSave.ID == "" {
			toSave.ID = core.GenerateID("tmpl")
		}
		if existing, err := tx.Template(ctx, toSave.ID); err == nil && existing != nil {
			return core.ErrConflict
		} else if err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}

		now := t.store.now()
		if toSave.CreatedAt.IsZero() {
			toSave.CreatedAt = now
		}
		toSave.UpdatedAt = now
		if err := tx.PutTemplate(ctx, toSave); err != nil {
			return err
		}
		*tmpl = *cloneValue(toSave)
		return nil
	})
}

// UpdateTemplate validates syntax and updates a user-owned template set, rejecting built-ins with ErrStrategyImmutable.
func (t *Templates) UpdateTemplate(ctx context.Context, tmpl *core.TemplateSet) error {
	if tmpl == nil || strings.TrimSpace(tmpl.ID) == "" {
		return fmt.Errorf("%w: template id is required", core.ErrValidation)
	}
	if strings.TrimSpace(tmpl.Name) == "" {
		return fmt.Errorf("%w: template name is required", core.ErrValidation)
	}
	if err := (core.DefaultTemplateValidator{}).Validate(tmpl); err != nil {
		return err
	}

	return t.store.backend.Write(ctx, func(tx Tx) error {
		existing, err := tx.Template(ctx, tmpl.ID)
		if err != nil {
			return err
		}
		if isBuiltInTemplate(existing) {
			return core.ErrStrategyImmutable
		}
		if existing.UserID != tmpl.UserID {
			return core.ErrForbidden
		}

		toSave := cloneValue(tmpl)
		toSave.CreatedAt = existing.CreatedAt
		toSave.UpdatedAt = t.store.now()
		if err := tx.PutTemplate(ctx, toSave); err != nil {
			return err
		}
		*tmpl = *cloneValue(toSave)
		return nil
	})
}

// DeleteTemplate removes a user-owned template set while protecting built-ins with ErrStrategyImmutable.
func (t *Templates) DeleteTemplate(ctx context.Context, userID, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: template id is required", core.ErrValidation)
	}

	return t.store.backend.Write(ctx, func(tx Tx) error {
		existing, err := tx.Template(ctx, id)
		if err != nil {
			return err
		}
		if isBuiltInTemplate(existing) {
			return core.ErrStrategyImmutable
		}
		if existing.UserID != userID {
			return core.ErrForbidden
		}
		return tx.DeleteTemplate(ctx, id)
	})
}

// isBuiltInTemplate reports whether a template set is a protected system built-in.
func isBuiltInTemplate(tmpl *core.TemplateSet) bool {
	return tmpl != nil && (tmpl.IsBuiltIn || strings.HasPrefix(tmpl.ID, "builtin-") || tmpl.UserID == core.SystemUserID)
}

// isTemplateVisible reports whether userID may read tmpl.
func isTemplateVisible(tmpl *core.TemplateSet, userID string) bool {
	return tmpl != nil && (tmpl.UserID == userID || isBuiltInTemplate(tmpl))
}
