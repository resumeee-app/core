package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/resumeee-app/resumeee-core"
)

// Users enforces domain validation and privilege invariants for user accounts.
type Users struct {
	store *Store
}

// User retrieves a registered user account by id.
func (u *Users) User(ctx context.Context, id string) (*core.User, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: user id cannot be empty", core.ErrValidation)
	}
	var out *core.User
	err := u.store.backend.Read(ctx, func(tx ReadTx) error {
		usr, getErr := tx.User(ctx, id)
		if getErr != nil {
			return getErr
		}
		out = cloneValue(usr)
		return nil
	})
	return out, err
}

// UpsertUser creates or updates a user account while preserving admin privileges and original CreatedAt.
func (u *Users) UpsertUser(ctx context.Context, user *core.User) error {
	if user == nil || strings.TrimSpace(user.ID) == "" {
		return fmt.Errorf("%w: user id is required", core.ErrValidation)
	}

	return u.store.backend.Write(ctx, func(tx Tx) error {
		existing, err := tx.User(ctx, user.ID)
		if err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}

		now := u.store.now()
		toSave := cloneValue(user)
		toSave.UpdatedAt = now
		if existing != nil {
			if existing.CreatedAt.IsZero() {
				toSave.CreatedAt = now
			} else {
				toSave.CreatedAt = existing.CreatedAt
			}
			// Preserve existing admin privileges across subsequent login upserts.
			if existing.Role == core.UserRoleAdmin {
				toSave.Role = core.UserRoleAdmin
			}
		} else {
			if toSave.CreatedAt.IsZero() {
				toSave.CreatedAt = now
			}
			if toSave.Role == "" {
				toSave.Role = core.UserRoleUser
			}
		}
		if err := tx.PutUser(ctx, toSave); err != nil {
			return err
		}
		*user = *cloneValue(toSave)
		return nil
	})
}
