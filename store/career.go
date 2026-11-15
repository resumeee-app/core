package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

// Career manages Tier-1 career entities, skill resolution, ShortID lookups, and MainRevision bumps.
type Career struct {
	store *Store
}

// ListSkills returns all non-global skills owned by userID.
func (c *Career) ListSkills(ctx context.Context, userID string) ([]core.Skill, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, fmt.Errorf("%w: user id cannot be empty", core.ErrValidation)
	}
	var list []core.Skill
	err := c.store.backend.Read(ctx, func(tx ReadTx) error {
		raw, err := tx.ListSkills(ctx, userID, false)
		if err != nil {
			return err
		}
		for _, sk := range raw {
			if sk.UserID == userID && !sk.IsGlobal {
				list = append(list, cloneValue(sk))
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list, nil
}

// ListGlobalSkills returns all curated global skills.
func (c *Career) ListGlobalSkills(ctx context.Context) ([]core.Skill, error) {
	var list []core.Skill
	err := c.store.backend.Read(ctx, func(tx ReadTx) error {
		raw, err := tx.ListSkills(ctx, core.SystemUserID, true)
		if err != nil {
			return err
		}
		for _, sk := range raw {
			if sk.IsGlobal || sk.UserID == "" || sk.UserID == core.SystemUserID {
				list = append(list, cloneValue(sk))
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list, nil
}

// Skill fetches a global or user-owned skill matching ID or ShortID.
func (c *Career) Skill(ctx context.Context, userID, id string) (*core.Skill, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: skill id cannot be empty", core.ErrValidation)
	}
	var out *core.Skill
	err := c.store.backend.Read(ctx, func(tx ReadTx) error {
		sk, err := tx.Skill(ctx, id)
		if err == nil && sk != nil {
			if sk.IsGlobal || sk.UserID == core.SystemUserID || sk.UserID == userID {
				out = cloneValue(sk)
				return nil
			}
			return core.ErrForbidden
		}
		if err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}
		all, listErr := tx.ListSkills(ctx, userID, true)
		if listErr != nil {
			return listErr
		}
		for _, candidate := range all {
			if (candidate.IsGlobal || candidate.UserID == core.SystemUserID || candidate.UserID == userID) &&
				matchesEntityID(candidate.ID, candidate.ShortID, id) {
				cp := cloneValue(candidate)
				out = &cp
				return nil
			}
		}
		return core.ErrNotFound
	})
	return out, err
}

// CreateSkill validates ownership and creates a skill, bumping MainRevision for tenant skills.
func (c *Career) CreateSkill(ctx context.Context, skill *core.Skill) error {
	if skill == nil {
		return fmt.Errorf("%w: skill cannot be nil", core.ErrValidation)
	}
	if strings.TrimSpace(skill.Name) == "" {
		return fmt.Errorf("%w: skill name is required", core.ErrValidation)
	}
	if skill.IsGlobal && skill.UserID != "" && skill.UserID != core.SystemUserID {
		return fmt.Errorf("%w: only system may create global skills", core.ErrForbidden)
	}

	return c.store.backend.Write(ctx, func(tx Tx) error {
		toSave := cloneValue(skill)
		now := c.store.now()
		ensureEntityIDs(&toSave.ID, &toSave.ShortID, "skill", now, &toSave.CreatedAt, &toSave.UpdatedAt)
		if toSave.Slug == "" {
			toSave.Slug = core.Slugify(toSave.Name)
		}

		if existing, err := tx.Skill(ctx, toSave.ID); err == nil && existing != nil {
			return core.ErrConflict
		} else if err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}

		var prof *core.Profile
		if !toSave.IsGlobal && toSave.UserID != "" && toSave.UserID != core.SystemUserID {
			var pErr error
			prof, pErr = tx.Profile(ctx, toSave.UserID)
			if pErr != nil && !errors.Is(pErr, core.ErrNotFound) {
				return pErr
			}
		}

		if err := tx.PutSkill(ctx, toSave); err != nil {
			return err
		}
		if !toSave.IsGlobal && toSave.UserID != "" && toSave.UserID != core.SystemUserID {
			if err := bumpProfileRevisionTx(ctx, tx, toSave.UserID, prof, now); err != nil {
				return err
			}
		}
		*skill = *cloneValue(toSave)
		return nil
	})
}

// UpdateSkill replaces a tenant-owned skill while rejecting global mutations (ErrGlobalSkillImmutable)
// and foreign tenant mutations (ErrForbidden).
func (c *Career) UpdateSkill(ctx context.Context, skill *core.Skill) error {
	if skill == nil || strings.TrimSpace(skill.ID) == "" {
		return fmt.Errorf("%w: skill id is required", core.ErrValidation)
	}

	return c.store.backend.Write(ctx, func(tx Tx) error {
		existing, err := tx.Skill(ctx, skill.ID)
		if err != nil {
			return err
		}
		if existing.IsGlobal || existing.UserID == core.SystemUserID {
			return core.ErrGlobalSkillImmutable
		}
		if existing.UserID != skill.UserID {
			return core.ErrForbidden
		}

		prof, pErr := tx.Profile(ctx, skill.UserID)
		if pErr != nil && !errors.Is(pErr, core.ErrNotFound) {
			return pErr
		}

		now := c.store.now()
		toSave := cloneValue(skill)
		if toSave.Slug == "" {
			toSave.Slug = core.Slugify(toSave.Name)
		}
		if toSave.ShortID == "" {
			toSave.ShortID = existing.ShortID
		}
		if toSave.ShortID == "" {
			toSave.ShortID = core.GenerateShortID("skill")
		}
		toSave.CreatedAt = existing.CreatedAt
		toSave.UpdatedAt = now
		if err := tx.PutSkill(ctx, toSave); err != nil {
			return err
		}
		if err := bumpProfileRevisionTx(ctx, tx, skill.UserID, prof, now); err != nil {
			return err
		}
		*skill = *cloneValue(toSave)
		return nil
	})
}

// DeleteSkill removes a user-owned skill while guarding global and foreign skills.
func (c *Career) DeleteSkill(ctx context.Context, userID, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: skill id cannot be empty", core.ErrValidation)
	}

	return c.store.backend.Write(ctx, func(tx Tx) error {
		existing, err := tx.Skill(ctx, id)
		if err != nil {
			return err
		}
		if existing.IsGlobal || existing.UserID == core.SystemUserID {
			return core.ErrGlobalSkillImmutable
		}
		if existing.UserID != userID {
			return core.ErrForbidden
		}

		prof, pErr := tx.Profile(ctx, userID)
		if pErr != nil && !errors.Is(pErr, core.ErrNotFound) {
			return pErr
		}
		if err := tx.DeleteSkill(ctx, id); err != nil {
			return err
		}
		return bumpProfileRevisionTx(ctx, tx, userID, prof, c.store.now())
	})
}

// ListExperiences returns all work experiences owned by userID with preloaded child facts.
func (c *Career) ListExperiences(ctx context.Context, userID string) ([]core.WorkExperience, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, fmt.Errorf("%w: user id cannot be empty", core.ErrValidation)
	}
	var list []core.WorkExperience
	err := c.store.backend.Read(ctx, func(tx ReadTx) error {
		exps, err := tx.ListExperiences(ctx, userID)
		if err != nil {
			return err
		}
		facts, err := tx.ListFacts(ctx, userID)
		if err != nil {
			return err
		}
		factsByOwner := groupFactsByOwner(facts, core.OwnerTypeWorkExperience)
		list = make([]core.WorkExperience, 0, len(exps))
		for _, exp := range exps {
			cp := cloneValue(exp)
			cp.Facts = sortFactsCopy(factsByOwner[cp.ID])
			list = append(list, cp)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list, nil
}

// Experience fetches a user-owned work experience by ID or ShortID with preloaded child facts.
func (c *Career) Experience(ctx context.Context, userID, id string) (*core.WorkExperience, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: experience id cannot be empty", core.ErrValidation)
	}
	var out *core.WorkExperience
	err := c.store.backend.Read(ctx, func(tx ReadTx) error {
		exp, err := findOwnedExperience(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		facts, fErr := tx.ListFactsByOwner(ctx, userID, core.OwnerTypeWorkExperience, exp.ID)
		if fErr != nil {
			return fErr
		}
		exp.Facts = sortFactsCopy(facts)
		out = exp
		return nil
	})
	return out, err
}

// CreateExperience persists a new work experience, resolves skills, stores inline facts, and bumps MainRevision.
func (c *Career) CreateExperience(ctx context.Context, exp *core.WorkExperience) error {
	if exp == nil || strings.TrimSpace(exp.UserID) == "" {
		return fmt.Errorf("%w: experience user id is required", core.ErrValidation)
	}

	return c.store.backend.Write(ctx, func(tx Tx) error {
		now := c.store.now()
		toSave := cloneValue(exp)
		ensureEntityIDs(&toSave.ID, &toSave.ShortID, "exp", now, &toSave.CreatedAt, &toSave.UpdatedAt)

		if existing, err := tx.Experience(ctx, toSave.ID); err == nil && existing != nil {
			return core.ErrConflict
		} else if err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}
		prof, err := tx.Profile(ctx, toSave.UserID)
		if err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}
		skills, err := tx.ListSkills(ctx, toSave.UserID, true)
		if err != nil {
			return err
		}

		reg := newSkillRegistry(toSave.UserID, skills, now)
		toSave.Skills = reg.registerMany(toSave.Skills)
		facts := prepareReplacedFacts(toSave.UserID, core.OwnerTypeWorkExperience, toSave.ID, toSave.Facts, reg, now)

		stored := cloneValue(toSave)
		stored.Facts = nil
		if err := reg.flush(ctx, tx); err != nil {
			return err
		}
		if err := tx.PutExperience(ctx, stored); err != nil {
			return err
		}
		for i := range facts {
			if err := tx.PutFact(ctx, &facts[i]); err != nil {
				return err
			}
		}
		if err := bumpProfileRevisionTx(ctx, tx, toSave.UserID, prof, now); err != nil {
			return err
		}
		toSave.Facts = sortFactsCopy(facts)
		*exp = *cloneValue(toSave)
		return nil
	})
}

// UpdateExperience replaces a user-owned work experience, syncs skills and inline facts, and bumps MainRevision.
func (c *Career) UpdateExperience(ctx context.Context, exp *core.WorkExperience) error {
	if exp == nil || strings.TrimSpace(exp.ID) == "" || strings.TrimSpace(exp.UserID) == "" {
		return fmt.Errorf("%w: experience id and user id are required", core.ErrValidation)
	}

	return c.store.backend.Write(ctx, func(tx Tx) error {
		existing, err := tx.Experience(ctx, exp.ID)
		if err != nil {
			return err
		}
		if existing.UserID != exp.UserID {
			return core.ErrNotFound
		}
		prof, err := tx.Profile(ctx, exp.UserID)
		if err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}
		skills, err := tx.ListSkills(ctx, exp.UserID, true)
		if err != nil {
			return err
		}
		var oldFacts []core.Fact
		if exp.Facts != nil {
			oldFacts, err = tx.ListFactsByOwner(ctx, exp.UserID, core.OwnerTypeWorkExperience, exp.ID)
			if err != nil {
				return err
			}
		}

		now := c.store.now()
		reg := newSkillRegistry(exp.UserID, skills, now)
		toSave := cloneValue(exp)
		if toSave.ShortID == "" {
			toSave.ShortID = existing.ShortID
		}
		ensureEntityIDs(&toSave.ID, &toSave.ShortID, "exp", now, &toSave.CreatedAt, &toSave.UpdatedAt)
		toSave.CreatedAt = existing.CreatedAt
		toSave.UpdatedAt = now
		toSave.Skills = reg.registerMany(toSave.Skills)

		var nextFacts []core.Fact
		if exp.Facts != nil {
			nextFacts = prepareReplacedFacts(exp.UserID, core.OwnerTypeWorkExperience, exp.ID, toSave.Facts, reg, now)
		}

		stored := cloneValue(toSave)
		stored.Facts = nil
		if err := reg.flush(ctx, tx); err != nil {
			return err
		}
		if err := replaceFactsTx(ctx, tx, oldFacts, nextFacts, exp.Facts != nil); err != nil {
			return err
		}
		if err := tx.PutExperience(ctx, stored); err != nil {
			return err
		}
		if err := bumpProfileRevisionTx(ctx, tx, exp.UserID, prof, now); err != nil {
			return err
		}
		if exp.Facts != nil {
			toSave.Facts = sortFactsCopy(nextFacts)
		}
		*exp = *cloneValue(toSave)
		return nil
	})
}

// DeleteExperience removes a user-owned work experience and its child facts and bumps MainRevision.
func (c *Career) DeleteExperience(ctx context.Context, userID, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: experience id cannot be empty", core.ErrValidation)
	}

	return c.store.backend.Write(ctx, func(tx Tx) error {
		existing, err := tx.Experience(ctx, id)
		if err != nil {
			return err
		}
		if existing.UserID != userID {
			return core.ErrNotFound
		}
		prof, err := tx.Profile(ctx, userID)
		if err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}
		facts, err := tx.ListFactsByOwner(ctx, userID, core.OwnerTypeWorkExperience, id)
		if err != nil {
			return err
		}

		if err := deleteFactsTx(ctx, tx, facts); err != nil {
			return err
		}
		if err := tx.DeleteExperience(ctx, id); err != nil {
			return err
		}
		return bumpProfileRevisionTx(ctx, tx, userID, prof, c.store.now())
	})
}

// ListProjects returns all projects owned by userID with preloaded child facts.
func (c *Career) ListProjects(ctx context.Context, userID string) ([]core.Project, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, fmt.Errorf("%w: user id cannot be empty", core.ErrValidation)
	}
	var list []core.Project
	err := c.store.backend.Read(ctx, func(tx ReadTx) error {
		projs, err := tx.ListProjects(ctx, userID)
		if err != nil {
			return err
		}
		facts, err := tx.ListFacts(ctx, userID)
		if err != nil {
			return err
		}
		factsByOwner := groupFactsByOwner(facts, core.OwnerTypeProject)
		list = make([]core.Project, 0, len(projs))
		for _, proj := range projs {
			cp := cloneValue(proj)
			cp.Facts = sortFactsCopy(factsByOwner[cp.ID])
			list = append(list, cp)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list, nil
}

// Project fetches a user-owned project by ID or ShortID with preloaded child facts.
func (c *Career) Project(ctx context.Context, userID, id string) (*core.Project, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: project id cannot be empty", core.ErrValidation)
	}
	var out *core.Project
	err := c.store.backend.Read(ctx, func(tx ReadTx) error {
		proj, err := findOwnedProject(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		facts, fErr := tx.ListFactsByOwner(ctx, userID, core.OwnerTypeProject, proj.ID)
		if fErr != nil {
			return fErr
		}
		proj.Facts = sortFactsCopy(facts)
		out = proj
		return nil
	})
	return out, err
}

// CreateProject persists a new project, resolves skills, stores inline facts, and bumps MainRevision.
func (c *Career) CreateProject(ctx context.Context, proj *core.Project) error {
	if proj == nil || strings.TrimSpace(proj.UserID) == "" {
		return fmt.Errorf("%w: project user id is required", core.ErrValidation)
	}

	return c.store.backend.Write(ctx, func(tx Tx) error {
		now := c.store.now()
		toSave := cloneValue(proj)
		ensureEntityIDs(&toSave.ID, &toSave.ShortID, "proj", now, &toSave.CreatedAt, &toSave.UpdatedAt)

		if existing, err := tx.Project(ctx, toSave.ID); err == nil && existing != nil {
			return core.ErrConflict
		} else if err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}
		prof, err := tx.Profile(ctx, toSave.UserID)
		if err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}
		skills, err := tx.ListSkills(ctx, toSave.UserID, true)
		if err != nil {
			return err
		}

		reg := newSkillRegistry(toSave.UserID, skills, now)
		toSave.Skills = reg.registerMany(toSave.Skills)
		facts := prepareReplacedFacts(toSave.UserID, core.OwnerTypeProject, toSave.ID, toSave.Facts, reg, now)

		stored := cloneValue(toSave)
		stored.Facts = nil
		if err := reg.flush(ctx, tx); err != nil {
			return err
		}
		if err := tx.PutProject(ctx, stored); err != nil {
			return err
		}
		for i := range facts {
			if err := tx.PutFact(ctx, &facts[i]); err != nil {
				return err
			}
		}
		if err := bumpProfileRevisionTx(ctx, tx, toSave.UserID, prof, now); err != nil {
			return err
		}
		toSave.Facts = sortFactsCopy(facts)
		*proj = *cloneValue(toSave)
		return nil
	})
}

// UpdateProject replaces a user-owned project, syncs skills and inline facts, and bumps MainRevision.
func (c *Career) UpdateProject(ctx context.Context, proj *core.Project) error {
	if proj == nil || strings.TrimSpace(proj.ID) == "" || strings.TrimSpace(proj.UserID) == "" {
		return fmt.Errorf("%w: project id and user id are required", core.ErrValidation)
	}

	return c.store.backend.Write(ctx, func(tx Tx) error {
		existing, err := tx.Project(ctx, proj.ID)
		if err != nil {
			return err
		}
		if existing.UserID != proj.UserID {
			return core.ErrNotFound
		}
		prof, err := tx.Profile(ctx, proj.UserID)
		if err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}
		skills, err := tx.ListSkills(ctx, proj.UserID, true)
		if err != nil {
			return err
		}
		var oldFacts []core.Fact
		if proj.Facts != nil {
			oldFacts, err = tx.ListFactsByOwner(ctx, proj.UserID, core.OwnerTypeProject, proj.ID)
			if err != nil {
				return err
			}
		}

		now := c.store.now()
		reg := newSkillRegistry(proj.UserID, skills, now)
		toSave := cloneValue(proj)
		if toSave.ShortID == "" {
			toSave.ShortID = existing.ShortID
		}
		ensureEntityIDs(&toSave.ID, &toSave.ShortID, "proj", now, &toSave.CreatedAt, &toSave.UpdatedAt)
		toSave.CreatedAt = existing.CreatedAt
		toSave.UpdatedAt = now
		toSave.Skills = reg.registerMany(toSave.Skills)

		var nextFacts []core.Fact
		if proj.Facts != nil {
			nextFacts = prepareReplacedFacts(proj.UserID, core.OwnerTypeProject, proj.ID, toSave.Facts, reg, now)
		}

		stored := cloneValue(toSave)
		stored.Facts = nil
		if err := reg.flush(ctx, tx); err != nil {
			return err
		}
		if err := replaceFactsTx(ctx, tx, oldFacts, nextFacts, proj.Facts != nil); err != nil {
			return err
		}
		if err := tx.PutProject(ctx, stored); err != nil {
			return err
		}
		if err := bumpProfileRevisionTx(ctx, tx, proj.UserID, prof, now); err != nil {
			return err
		}
		if proj.Facts != nil {
			toSave.Facts = sortFactsCopy(nextFacts)
		}
		*proj = *cloneValue(toSave)
		return nil
	})
}

// DeleteProject removes a user-owned project and its child facts and bumps MainRevision.
func (c *Career) DeleteProject(ctx context.Context, userID, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: project id cannot be empty", core.ErrValidation)
	}

	return c.store.backend.Write(ctx, func(tx Tx) error {
		existing, err := tx.Project(ctx, id)
		if err != nil {
			return err
		}
		if existing.UserID != userID {
			return core.ErrNotFound
		}
		prof, err := tx.Profile(ctx, userID)
		if err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}
		facts, err := tx.ListFactsByOwner(ctx, userID, core.OwnerTypeProject, id)
		if err != nil {
			return err
		}

		if err := deleteFactsTx(ctx, tx, facts); err != nil {
			return err
		}
		if err := tx.DeleteProject(ctx, id); err != nil {
			return err
		}
		return bumpProfileRevisionTx(ctx, tx, userID, prof, c.store.now())
	})
}

// ListEducation returns all education records owned by userID with preloaded child facts.
func (c *Career) ListEducation(ctx context.Context, userID string) ([]core.Education, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, fmt.Errorf("%w: user id cannot be empty", core.ErrValidation)
	}
	var list []core.Education
	err := c.store.backend.Read(ctx, func(tx ReadTx) error {
		edus, err := tx.ListEducations(ctx, userID)
		if err != nil {
			return err
		}
		facts, err := tx.ListFacts(ctx, userID)
		if err != nil {
			return err
		}
		factsByOwner := groupFactsByOwner(facts, core.OwnerTypeEducation)
		list = make([]core.Education, 0, len(edus))
		for _, edu := range edus {
			cp := cloneValue(edu)
			cp.Facts = sortFactsCopy(factsByOwner[cp.ID])
			list = append(list, cp)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list, nil
}

// Education fetches a user-owned education entry by ID or ShortID with preloaded child facts.
func (c *Career) Education(ctx context.Context, userID, id string) (*core.Education, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: education id cannot be empty", core.ErrValidation)
	}
	var out *core.Education
	err := c.store.backend.Read(ctx, func(tx ReadTx) error {
		edu, err := findOwnedEducation(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		facts, fErr := tx.ListFactsByOwner(ctx, userID, core.OwnerTypeEducation, edu.ID)
		if fErr != nil {
			return fErr
		}
		edu.Facts = sortFactsCopy(facts)
		out = edu
		return nil
	})
	return out, err
}

// CreateEducation persists a new education record, stores inline facts, and bumps MainRevision.
func (c *Career) CreateEducation(ctx context.Context, edu *core.Education) error {
	if edu == nil || strings.TrimSpace(edu.UserID) == "" {
		return fmt.Errorf("%w: education user id is required", core.ErrValidation)
	}

	return c.store.backend.Write(ctx, func(tx Tx) error {
		now := c.store.now()
		toSave := cloneValue(edu)
		ensureEntityIDs(&toSave.ID, &toSave.ShortID, "edu", now, &toSave.CreatedAt, &toSave.UpdatedAt)

		if existing, err := tx.Education(ctx, toSave.ID); err == nil && existing != nil {
			return core.ErrConflict
		} else if err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}
		prof, err := tx.Profile(ctx, toSave.UserID)
		if err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}
		skills, err := tx.ListSkills(ctx, toSave.UserID, true)
		if err != nil {
			return err
		}

		reg := newSkillRegistry(toSave.UserID, skills, now)
		facts := prepareReplacedFacts(toSave.UserID, core.OwnerTypeEducation, toSave.ID, toSave.Facts, reg, now)

		stored := cloneValue(toSave)
		stored.Facts = nil
		if err := reg.flush(ctx, tx); err != nil {
			return err
		}
		if err := tx.PutEducation(ctx, stored); err != nil {
			return err
		}
		for i := range facts {
			if err := tx.PutFact(ctx, &facts[i]); err != nil {
				return err
			}
		}
		if err := bumpProfileRevisionTx(ctx, tx, toSave.UserID, prof, now); err != nil {
			return err
		}
		toSave.Facts = sortFactsCopy(facts)
		*edu = *cloneValue(toSave)
		return nil
	})
}

// UpdateEducation replaces a user-owned education record and inline facts and bumps MainRevision.
func (c *Career) UpdateEducation(ctx context.Context, edu *core.Education) error {
	if edu == nil || strings.TrimSpace(edu.ID) == "" || strings.TrimSpace(edu.UserID) == "" {
		return fmt.Errorf("%w: education id and user id are required", core.ErrValidation)
	}

	return c.store.backend.Write(ctx, func(tx Tx) error {
		existing, err := tx.Education(ctx, edu.ID)
		if err != nil {
			return err
		}
		if existing.UserID != edu.UserID {
			return core.ErrNotFound
		}
		prof, err := tx.Profile(ctx, edu.UserID)
		if err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}
		var skills []core.Skill
		var oldFacts []core.Fact
		if edu.Facts != nil {
			skills, err = tx.ListSkills(ctx, edu.UserID, true)
			if err != nil {
				return err
			}
			oldFacts, err = tx.ListFactsByOwner(ctx, edu.UserID, core.OwnerTypeEducation, edu.ID)
			if err != nil {
				return err
			}
		}

		now := c.store.now()
		toSave := cloneValue(edu)
		if toSave.ShortID == "" {
			toSave.ShortID = existing.ShortID
		}
		ensureEntityIDs(&toSave.ID, &toSave.ShortID, "edu", now, &toSave.CreatedAt, &toSave.UpdatedAt)
		toSave.CreatedAt = existing.CreatedAt
		toSave.UpdatedAt = now

		reg := newSkillRegistry(edu.UserID, skills, now)
		var nextFacts []core.Fact
		if edu.Facts != nil {
			nextFacts = prepareReplacedFacts(edu.UserID, core.OwnerTypeEducation, edu.ID, toSave.Facts, reg, now)
		}

		stored := cloneValue(toSave)
		stored.Facts = nil
		if err := reg.flush(ctx, tx); err != nil {
			return err
		}
		if err := replaceFactsTx(ctx, tx, oldFacts, nextFacts, edu.Facts != nil); err != nil {
			return err
		}
		if err := tx.PutEducation(ctx, stored); err != nil {
			return err
		}
		if err := bumpProfileRevisionTx(ctx, tx, edu.UserID, prof, now); err != nil {
			return err
		}
		if edu.Facts != nil {
			toSave.Facts = sortFactsCopy(nextFacts)
		}
		*edu = *cloneValue(toSave)
		return nil
	})
}

// DeleteEducation removes a user-owned education record and its child facts and bumps MainRevision.
func (c *Career) DeleteEducation(ctx context.Context, userID, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: education id cannot be empty", core.ErrValidation)
	}

	return c.store.backend.Write(ctx, func(tx Tx) error {
		existing, err := tx.Education(ctx, id)
		if err != nil {
			return err
		}
		if existing.UserID != userID {
			return core.ErrNotFound
		}
		prof, err := tx.Profile(ctx, userID)
		if err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}
		facts, err := tx.ListFactsByOwner(ctx, userID, core.OwnerTypeEducation, id)
		if err != nil {
			return err
		}

		if err := deleteFactsTx(ctx, tx, facts); err != nil {
			return err
		}
		if err := tx.DeleteEducation(ctx, id); err != nil {
			return err
		}
		return bumpProfileRevisionTx(ctx, tx, userID, prof, c.store.now())
	})
}

// ListCertifications returns all certifications owned by userID.
func (c *Career) ListCertifications(ctx context.Context, userID string) ([]core.Certification, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, fmt.Errorf("%w: user id cannot be empty", core.ErrValidation)
	}
	var list []core.Certification
	err := c.store.backend.Read(ctx, func(tx ReadTx) error {
		raw, err := tx.ListCertifications(ctx, userID)
		if err != nil {
			return err
		}
		list = cloneValue(raw)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list, nil
}

// Certification fetches a user-owned certification matching ID or ShortID.
func (c *Career) Certification(ctx context.Context, userID, id string) (*core.Certification, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: certification id cannot be empty", core.ErrValidation)
	}
	var out *core.Certification
	err := c.store.backend.Read(ctx, func(tx ReadTx) error {
		cert, err := tx.Certification(ctx, id)
		if err == nil && cert != nil && cert.UserID == userID {
			out = cloneValue(cert)
			return nil
		}
		if err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}
		all, listErr := tx.ListCertifications(ctx, userID)
		if listErr != nil {
			return listErr
		}
		for _, candidate := range all {
			if candidate.UserID == userID && matchesEntityID(candidate.ID, candidate.ShortID, id) {
				cp := cloneValue(candidate)
				out = &cp
				return nil
			}
		}
		return core.ErrNotFound
	})
	return out, err
}

// CreateCertification persists a new certification and bumps the owner's MainRevision.
func (c *Career) CreateCertification(ctx context.Context, cert *core.Certification) error {
	if cert == nil || strings.TrimSpace(cert.UserID) == "" {
		return fmt.Errorf("%w: certification user id is required", core.ErrValidation)
	}

	return c.store.backend.Write(ctx, func(tx Tx) error {
		now := c.store.now()
		toSave := cloneValue(cert)
		ensureEntityIDs(&toSave.ID, &toSave.ShortID, "cert", now, &toSave.CreatedAt, &toSave.UpdatedAt)

		if existing, err := tx.Certification(ctx, toSave.ID); err == nil && existing != nil {
			return core.ErrConflict
		} else if err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}
		prof, err := tx.Profile(ctx, toSave.UserID)
		if err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}

		if err := tx.PutCertification(ctx, toSave); err != nil {
			return err
		}
		if err := bumpProfileRevisionTx(ctx, tx, toSave.UserID, prof, now); err != nil {
			return err
		}
		*cert = *cloneValue(toSave)
		return nil
	})
}

// UpdateCertification replaces a user-owned certification and bumps MainRevision.
func (c *Career) UpdateCertification(ctx context.Context, cert *core.Certification) error {
	if cert == nil || strings.TrimSpace(cert.ID) == "" || strings.TrimSpace(cert.UserID) == "" {
		return fmt.Errorf("%w: certification id and user id are required", core.ErrValidation)
	}

	return c.store.backend.Write(ctx, func(tx Tx) error {
		existing, err := tx.Certification(ctx, cert.ID)
		if err != nil {
			return err
		}
		if existing.UserID != cert.UserID {
			return core.ErrNotFound
		}
		prof, err := tx.Profile(ctx, cert.UserID)
		if err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}

		now := c.store.now()
		toSave := cloneValue(cert)
		if toSave.ShortID == "" {
			toSave.ShortID = existing.ShortID
		}
		ensureEntityIDs(&toSave.ID, &toSave.ShortID, "cert", now, &toSave.CreatedAt, &toSave.UpdatedAt)
		toSave.CreatedAt = existing.CreatedAt
		toSave.UpdatedAt = now
		if err := tx.PutCertification(ctx, toSave); err != nil {
			return err
		}
		if err := bumpProfileRevisionTx(ctx, tx, cert.UserID, prof, now); err != nil {
			return err
		}
		*cert = *cloneValue(toSave)
		return nil
	})
}

// DeleteCertification deletes a user-owned certification and bumps MainRevision.
func (c *Career) DeleteCertification(ctx context.Context, userID, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: certification id cannot be empty", core.ErrValidation)
	}

	return c.store.backend.Write(ctx, func(tx Tx) error {
		existing, err := tx.Certification(ctx, id)
		if err != nil {
			return err
		}
		if existing.UserID != userID {
			return core.ErrNotFound
		}
		prof, err := tx.Profile(ctx, userID)
		if err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}

		if err := tx.DeleteCertification(ctx, id); err != nil {
			return err
		}
		return bumpProfileRevisionTx(ctx, tx, userID, prof, c.store.now())
	})
}

// ListFactsByOwner returns all facts belonging to ownerType and ownerID for userID.
func (c *Career) ListFactsByOwner(ctx context.Context, userID, ownerType, ownerID string) ([]core.Fact, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, fmt.Errorf("%w: user id cannot be empty", core.ErrValidation)
	}
	var list []core.Fact
	err := c.store.backend.Read(ctx, func(tx ReadTx) error {
		raw, err := tx.ListFactsByOwner(ctx, userID, core.OwnerType(ownerType), ownerID)
		if err != nil {
			return err
		}
		list = sortFactsCopy(raw)
		return nil
	})
	return list, err
}

// Fact fetches a user-owned fact matching ID or ShortID.
func (c *Career) Fact(ctx context.Context, userID, id string) (*core.Fact, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: fact id cannot be empty", core.ErrValidation)
	}
	var out *core.Fact
	err := c.store.backend.Read(ctx, func(tx ReadTx) error {
		f, err := tx.Fact(ctx, id)
		if err == nil && f != nil && f.UserID == userID {
			out = cloneValue(f)
			return nil
		}
		if err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}
		all, listErr := tx.ListFacts(ctx, userID)
		if listErr != nil {
			return listErr
		}
		for _, candidate := range all {
			if candidate.UserID == userID && matchesEntityID(candidate.ID, candidate.ShortID, id) {
				cp := cloneValue(candidate)
				out = &cp
				return nil
			}
		}
		return core.ErrNotFound
	})
	return out, err
}

// CreateFact validates parent ownership when OwnerID is set, resolves skills, persists the fact, and bumps MainRevision.
func (c *Career) CreateFact(ctx context.Context, fact *core.Fact) error {
	if fact == nil || strings.TrimSpace(fact.UserID) == "" {
		return fmt.Errorf("%w: fact user id is required", core.ErrValidation)
	}

	return c.store.backend.Write(ctx, func(tx Tx) error {
		now := c.store.now()
		toSave := cloneValue(fact)
		ensureEntityIDs(&toSave.ID, &toSave.ShortID, "fact", now, &toSave.CreatedAt, &toSave.UpdatedAt)

		if existing, err := tx.Fact(ctx, toSave.ID); err == nil && existing != nil {
			return core.ErrConflict
		} else if err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}
		if err := verifyFactOwnerTx(ctx, tx, toSave.UserID, toSave.OwnerType, toSave.OwnerID); err != nil {
			return err
		}
		prof, err := tx.Profile(ctx, toSave.UserID)
		if err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}
		skills, err := tx.ListSkills(ctx, toSave.UserID, true)
		if err != nil {
			return err
		}

		reg := newSkillRegistry(toSave.UserID, skills, now)
		toSave.Skills = reg.registerMany(toSave.Skills)
		if err := reg.flush(ctx, tx); err != nil {
			return err
		}
		if err := tx.PutFact(ctx, toSave); err != nil {
			return err
		}
		if err := bumpProfileRevisionTx(ctx, tx, toSave.UserID, prof, now); err != nil {
			return err
		}
		*fact = *cloneValue(toSave)
		return nil
	})
}

// UpdateFact replaces a user-owned fact, resolves skills, and bumps MainRevision.
func (c *Career) UpdateFact(ctx context.Context, fact *core.Fact) error {
	if fact == nil || strings.TrimSpace(fact.ID) == "" || strings.TrimSpace(fact.UserID) == "" {
		return fmt.Errorf("%w: fact id and user id are required", core.ErrValidation)
	}

	return c.store.backend.Write(ctx, func(tx Tx) error {
		existing, err := tx.Fact(ctx, fact.ID)
		if err != nil {
			return err
		}
		if existing.UserID != fact.UserID {
			return core.ErrNotFound
		}
		if err := verifyFactOwnerTx(ctx, tx, fact.UserID, fact.OwnerType, fact.OwnerID); err != nil {
			return err
		}
		prof, err := tx.Profile(ctx, fact.UserID)
		if err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}
		skills, err := tx.ListSkills(ctx, fact.UserID, true)
		if err != nil {
			return err
		}

		now := c.store.now()
		reg := newSkillRegistry(fact.UserID, skills, now)
		toSave := cloneValue(fact)
		if toSave.ShortID == "" {
			toSave.ShortID = existing.ShortID
		}
		ensureEntityIDs(&toSave.ID, &toSave.ShortID, "fact", now, &toSave.CreatedAt, &toSave.UpdatedAt)
		toSave.CreatedAt = existing.CreatedAt
		toSave.UpdatedAt = now
		toSave.Skills = reg.registerMany(toSave.Skills)

		if err := reg.flush(ctx, tx); err != nil {
			return err
		}
		if err := tx.PutFact(ctx, toSave); err != nil {
			return err
		}
		if err := bumpProfileRevisionTx(ctx, tx, fact.UserID, prof, now); err != nil {
			return err
		}
		*fact = *cloneValue(toSave)
		return nil
	})
}

// DeleteFact removes a user-owned fact and bumps MainRevision.
func (c *Career) DeleteFact(ctx context.Context, userID, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: fact id cannot be empty", core.ErrValidation)
	}

	return c.store.backend.Write(ctx, func(tx Tx) error {
		existing, err := tx.Fact(ctx, id)
		if err != nil {
			return err
		}
		if existing.UserID != userID {
			return core.ErrNotFound
		}
		prof, err := tx.Profile(ctx, userID)
		if err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}
		if err := tx.DeleteFact(ctx, id); err != nil {
			return err
		}
		return bumpProfileRevisionTx(ctx, tx, userID, prof, c.store.now())
	})
}

// skillRegistry resolves and registers skills within a single transaction without issuing reads mid-write.
type skillRegistry struct {
	userID  string
	now     time.Time
	byID    map[string]core.Skill
	bySlug  map[string]core.Skill
	created []*core.Skill
}

// newSkillRegistry indexes existing global and tenant skills for fast deduplicated lookup.
func newSkillRegistry(userID string, existing []core.Skill, now time.Time) *skillRegistry {
	reg := &skillRegistry{
		userID: userID,
		now:    now,
		byID:   make(map[string]core.Skill, len(existing)),
		bySlug: make(map[string]core.Skill, len(existing)),
	}
	for _, sk := range existing {
		reg.index(sk)
	}
	return reg
}

// index records sk in byID and bySlug lookup maps, preferring global definitions on slug collisions.
func (r *skillRegistry) index(sk core.Skill) {
	if sk.ID != "" {
		r.byID[sk.ID] = sk
	}
	slug := sk.Slug
	if slug == "" && sk.Name != "" {
		slug = core.Slugify(sk.Name)
	}
	if slug != "" {
		if prev, exists := r.bySlug[slug]; !exists || (!prev.IsGlobal && sk.IsGlobal) {
			r.bySlug[slug] = sk
		}
	}
}

// registerMany resolves each skill reference against accessible global/tenant skills,
// synthesizing missing tenant skills on demand and deduplicating the resulting slice.
func (r *skillRegistry) registerMany(skills []core.Skill) []core.Skill {
	if len(skills) == 0 {
		return nil
	}
	resolved := make([]core.Skill, 0, len(skills))
	for _, input := range skills {
		sk, ok := r.resolveOne(input)
		if ok {
			resolved = append(resolved, sk)
		}
	}
	return core.DeduplicateSkills(resolved)
}

// resolveOne resolves or creates a single canonical skill entry in memory.
func (r *skillRegistry) resolveOne(input core.Skill) (core.Skill, bool) {
	if input.ID != "" {
		if existing, ok := r.byID[input.ID]; ok {
			return existing, true
		}
	}
	name := strings.TrimSpace(input.Name)
	slug := input.Slug
	if slug == "" && name != "" {
		slug = core.Slugify(name)
	}
	if slug != "" {
		if existing, ok := r.bySlug[slug]; ok {
			return existing, true
		}
	}
	if name == "" && input.ID == "" {
		return core.Skill{}, false
	}

	next := cloneValue(input)
	next.UserID = r.userID
	next.IsGlobal = false
	next.Name = name
	if next.Name == "" {
		next.Name = input.ID
	}
	if next.Slug == "" {
		next.Slug = core.Slugify(next.Name)
	}
	ensureEntityIDs(&next.ID, &next.ShortID, "skill", r.now, &next.CreatedAt, &next.UpdatedAt)
	r.index(next)
	r.created = append(r.created, &next)
	return next, true
}

// flush persists all newly synthesized tenant skills inside tx.
func (r *skillRegistry) flush(ctx context.Context, tx Tx) error {
	for _, sk := range r.created {
		if err := tx.PutSkill(ctx, sk); err != nil {
			return err
		}
	}
	return nil
}

// verifyFactOwnerTx confirms that an explicit fact owner exists and belongs to userID.
func verifyFactOwnerTx(ctx context.Context, tx ReadTx, userID string, ownerType core.OwnerType, ownerID string) error {
	if ownerID == "" {
		return nil
	}
	switch ownerType {
	case core.OwnerTypeWorkExperience:
		exp, err := tx.Experience(ctx, ownerID)
		if err != nil {
			return err
		}
		if exp.UserID != userID {
			return core.ErrNotFound
		}
	case core.OwnerTypeProject:
		proj, err := tx.Project(ctx, ownerID)
		if err != nil {
			return err
		}
		if proj.UserID != userID {
			return core.ErrNotFound
		}
	case core.OwnerTypeEducation:
		edu, err := tx.Education(ctx, ownerID)
		if err != nil {
			return err
		}
		if edu.UserID != userID {
			return core.ErrNotFound
		}
	default:
		return fmt.Errorf("%w: unsupported fact owner type %q", core.ErrValidation, ownerType)
	}
	return nil
}

// ensureEntityIDs populates missing ID, ShortID, CreatedAt, and UpdatedAt fields on career entities.
func ensureEntityIDs(id, shortID *string, prefix string, now time.Time, createdAt, updatedAt *time.Time) {
	if *id == "" {
		*id = core.GenerateID(prefix)
	}
	if *shortID == "" {
		*shortID = core.GenerateShortID(prefix)
	}
	if createdAt.IsZero() {
		*createdAt = now
	}
	*updatedAt = now
}

// bumpProfileRevisionTx increments existing userID's MainRevision or creates a baseline profile at revision 1.
func bumpProfileRevisionTx(ctx context.Context, tx Tx, userID string, existing *core.Profile, now time.Time) error {
	if strings.TrimSpace(userID) == "" {
		return nil
	}
	if existing == nil {
		return tx.PutProfile(ctx, &core.Profile{
			ID:           "profile-" + userID,
			UserID:       userID,
			FullName:     userID,
			MainRevision: 1,
			CreatedAt:    now,
			UpdatedAt:    now,
		})
	}
	updated := cloneValue(existing)
	updated.MainRevision++
	updated.UpdatedAt = now
	return tx.PutProfile(ctx, updated)
}

// matchesEntityID checks whether query matches id or non-empty shortID.
func matchesEntityID(id, shortID, query string) bool {
	return id == query || (shortID != "" && shortID == query) || (id != "" && core.ShortenID(id) == query)
}

// groupFactsByOwner groups facts belonging to ownerType keyed by OwnerID.
func groupFactsByOwner(facts []core.Fact, ownerType core.OwnerType) map[string][]core.Fact {
	grouped := make(map[string][]core.Fact)
	for _, f := range facts {
		if f.OwnerType == ownerType && f.OwnerID != "" {
			grouped[f.OwnerID] = append(grouped[f.OwnerID], cloneValue(f))
		}
	}
	return grouped
}

// findOwnedExperience resolves a work experience owned by userID by ID or ShortID.
func findOwnedExperience(ctx context.Context, tx ReadTx, userID, id string) (*core.WorkExperience, error) {
	if exp, err := tx.Experience(ctx, id); err == nil && exp != nil && exp.UserID == userID {
		return cloneValue(exp), nil
	} else if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, err
	}
	all, err := tx.ListExperiences(ctx, userID)
	if err != nil {
		return nil, err
	}
	for _, candidate := range all {
		if candidate.UserID == userID && matchesEntityID(candidate.ID, candidate.ShortID, id) {
			cp := cloneValue(candidate)
			return &cp, nil
		}
	}
	return nil, core.ErrNotFound
}

// findOwnedProject resolves a project owned by userID by ID or ShortID.
func findOwnedProject(ctx context.Context, tx ReadTx, userID, id string) (*core.Project, error) {
	if proj, err := tx.Project(ctx, id); err == nil && proj != nil && proj.UserID == userID {
		return cloneValue(proj), nil
	} else if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, err
	}
	all, err := tx.ListProjects(ctx, userID)
	if err != nil {
		return nil, err
	}
	for _, candidate := range all {
		if candidate.UserID == userID && matchesEntityID(candidate.ID, candidate.ShortID, id) {
			cp := cloneValue(candidate)
			return &cp, nil
		}
	}
	return nil, core.ErrNotFound
}

// findOwnedEducation resolves an education record owned by userID by ID or ShortID.
func findOwnedEducation(ctx context.Context, tx ReadTx, userID, id string) (*core.Education, error) {
	if edu, err := tx.Education(ctx, id); err == nil && edu != nil && edu.UserID == userID {
		return cloneValue(edu), nil
	} else if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, err
	}
	all, err := tx.ListEducations(ctx, userID)
	if err != nil {
		return nil, err
	}
	for _, candidate := range all {
		if candidate.UserID == userID && matchesEntityID(candidate.ID, candidate.ShortID, id) {
			cp := cloneValue(candidate)
			return &cp, nil
		}
	}
	return nil, core.ErrNotFound
}

// replaceFactsTx deletes existing child facts and inserts nextFacts when replace is true.
func replaceFactsTx(ctx context.Context, tx Tx, oldFacts, nextFacts []core.Fact, replace bool) error {
	if !replace {
		return nil
	}
	if err := deleteFactsTx(ctx, tx, oldFacts); err != nil {
		return err
	}
	for i := range nextFacts {
		if err := tx.PutFact(ctx, &nextFacts[i]); err != nil {
			return err
		}
	}
	return nil
}

// deleteFactsTx deletes every fact record in facts.
func deleteFactsTx(ctx context.Context, tx Tx, facts []core.Fact) error {
	for _, f := range facts {
		if err := tx.DeleteFact(ctx, f.ID); err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}
	}
	return nil
}
