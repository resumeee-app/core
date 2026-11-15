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

// Profiles manages Tier-1 candidate profile roots, MainRevision bumps, and consolidated snapshots.
type Profiles struct {
	store *Store
}

// Profile retrieves the candidate profile root for userID.
func (p *Profiles) Profile(ctx context.Context, userID string) (*core.Profile, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, fmt.Errorf("%w: user id cannot be empty", core.ErrValidation)
	}
	var out *core.Profile
	err := p.store.backend.Read(ctx, func(tx ReadTx) error {
		prof, getErr := tx.Profile(ctx, userID)
		if getErr != nil {
			return getErr
		}
		out = cloneValue(prof)
		return nil
	})
	return out, err
}

// UpsertProfile creates or updates a candidate profile root while maintaining monotonic MainRevision.
func (p *Profiles) UpsertProfile(ctx context.Context, profile *core.Profile) error {
	if profile == nil || strings.TrimSpace(profile.UserID) == "" {
		return fmt.Errorf("%w: profile user id is required", core.ErrValidation)
	}

	return p.store.backend.Write(ctx, func(tx Tx) error {
		existing, err := tx.Profile(ctx, profile.UserID)
		if err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}

		now := p.store.now()
		toSave := cloneValue(profile)
		if toSave.ID == "" {
			if existing != nil && existing.ID != "" {
				toSave.ID = existing.ID
			} else {
				toSave.ID = "profile-" + toSave.UserID
			}
		}
		if existing != nil {
			toSave.MainRevision = existing.MainRevision + 1
			if !existing.CreatedAt.IsZero() {
				toSave.CreatedAt = existing.CreatedAt
			}
		} else if toSave.MainRevision == 0 {
			toSave.MainRevision = 1
		}
		if toSave.CreatedAt.IsZero() {
			toSave.CreatedAt = now
		}
		toSave.UpdatedAt = now
		if err := tx.PutProfile(ctx, toSave); err != nil {
			return err
		}
		*profile = *cloneValue(toSave)
		return nil
	})
}

// IncrementMainRevision atomically advances userID's MainRevision and returns the updated revision.
func (p *Profiles) IncrementMainRevision(ctx context.Context, userID string) (int64, error) {
	if strings.TrimSpace(userID) == "" {
		return 0, fmt.Errorf("%w: user id cannot be empty", core.ErrValidation)
	}

	var nextRev int64
	err := p.store.backend.Write(ctx, func(tx Tx) error {
		prof, getErr := tx.Profile(ctx, userID)
		if getErr != nil {
			return getErr
		}
		prof.MainRevision++
		prof.UpdatedAt = p.store.now()
		if err := tx.PutProfile(ctx, prof); err != nil {
			return err
		}
		nextRev = prof.MainRevision
		return nil
	})
	return nextRev, err
}

// Snapshot assembles a complete, read-consistent candidate MainProfileSnapshot with consolidated skills.
func (p *Profiles) Snapshot(ctx context.Context, userID string) (*core.MainProfileSnapshot, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, fmt.Errorf("%w: user id cannot be empty", core.ErrValidation)
	}

	var snap *core.MainProfileSnapshot
	err := p.store.backend.Read(ctx, func(tx ReadTx) error {
		prof, err := tx.Profile(ctx, userID)
		if err != nil {
			return err
		}
		skills, err := tx.ListSkills(ctx, userID, false)
		if err != nil {
			return err
		}
		exps, err := tx.ListExperiences(ctx, userID)
		if err != nil {
			return err
		}
		projs, err := tx.ListProjects(ctx, userID)
		if err != nil {
			return err
		}
		edus, err := tx.ListEducations(ctx, userID)
		if err != nil {
			return err
		}
		certs, err := tx.ListCertifications(ctx, userID)
		if err != nil {
			return err
		}
		facts, err := tx.ListFacts(ctx, userID)
		if err != nil {
			return err
		}

		snap = buildSnapshot(prof, skills, exps, projs, edus, certs, facts, p.store.now())
		return nil
	})
	return snap, err
}

// ReplaceCareerGraph atomically replaces the tenant's entire Tier-1 career graph and increments MainRevision.
func (p *Profiles) ReplaceCareerGraph(ctx context.Context, userID string, snapshot *core.MainProfileSnapshot) error {
	if strings.TrimSpace(userID) == "" || snapshot == nil {
		return fmt.Errorf("%w: user id and snapshot are required", core.ErrValidation)
	}

	return p.store.backend.Write(ctx, func(tx Tx) error {
		// Execute every transaction read before issuing any write (Firestore & SQLite safe).
		existingProf, err := tx.Profile(ctx, userID)
		if err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}
		accessibleSkills, err := tx.ListSkills(ctx, userID, true)
		if err != nil {
			return err
		}
		oldExps, err := tx.ListExperiences(ctx, userID)
		if err != nil {
			return err
		}
		oldProjs, err := tx.ListProjects(ctx, userID)
		if err != nil {
			return err
		}
		oldEdus, err := tx.ListEducations(ctx, userID)
		if err != nil {
			return err
		}
		oldCerts, err := tx.ListCertifications(ctx, userID)
		if err != nil {
			return err
		}
		oldFacts, err := tx.ListFacts(ctx, userID)
		if err != nil {
			return err
		}

		now := p.store.now()
		reg := newSkillRegistry(userID, accessibleSkills, now)

		nextProf, rev := prepareReplacedProfile(userID, existingProf, snapshot.Profile, now)
		snapshot.MainRevision = rev

		resolvedSkills := reg.registerMany(snapshot.Skills)
		nextExps, expFacts := prepareReplacedExperiences(userID, snapshot.Experiences, reg, now)
		nextProjs, projFacts := prepareReplacedProjects(userID, snapshot.Projects, reg, now)
		nextEdus, eduFacts := prepareReplacedEducations(userID, snapshot.Education, reg, now)
		nextCerts := prepareReplacedCertifications(userID, snapshot.Certifications, now)

		extraFacts, valErr := validateAndPrepareSnapshotFacts(userID, snapshot.Facts, nextExps, nextProjs, nextEdus, expFacts, projFacts, eduFacts, reg, now)
		if valErr != nil {
			return valErr
		}

		allFacts := make([]core.Fact, 0, len(expFacts)+len(projFacts)+len(eduFacts)+len(extraFacts))
		allFacts = append(allFacts, expFacts...)
		allFacts = append(allFacts, projFacts...)
		allFacts = append(allFacts, eduFacts...)
		allFacts = append(allFacts, extraFacts...)

		// Delete existing user-owned records first, never touching global skills.
		if err := deleteTenantCareerRecords(ctx, tx, userID, accessibleSkills, oldExps, oldProjs, oldEdus, oldCerts, oldFacts); err != nil {
			return err
		}
		if err := tx.PutProfile(ctx, nextProf); err != nil {
			return err
		}

		toSaveSkills := mergeSavedSkills(userID, resolvedSkills, reg.created)
		return writeReplacedCareerRecords(ctx, tx, toSaveSkills, nextExps, nextProjs, nextEdus, nextCerts, allFacts)
	})
}

// buildSnapshot hydrates parent entities with child facts and consolidates all skills.
func buildSnapshot(
	prof *core.Profile,
	skills []core.Skill,
	exps []core.WorkExperience,
	projs []core.Project,
	edus []core.Education,
	certs []core.Certification,
	facts []core.Fact,
	now time.Time,
) *core.MainProfileSnapshot {
	factsByOwner := make(map[string][]core.Fact)
	for _, f := range facts {
		if f.OwnerID == "" || f.OwnerType == "" {
			continue
		}
		fCopy := cloneValue(f)
		key := string(fCopy.OwnerType) + ":" + fCopy.OwnerID
		factsByOwner[key] = append(factsByOwner[key], fCopy)
	}

	outExps := make([]core.WorkExperience, 0, len(exps))
	for _, exp := range exps {
		c := cloneValue(exp)
		c.Facts = sortFactsCopy(factsByOwner[string(core.OwnerTypeWorkExperience)+":"+c.ID])
		outExps = append(outExps, c)
	}
	sort.Slice(outExps, func(i, j int) bool { return outExps[i].ID < outExps[j].ID })

	outProjs := make([]core.Project, 0, len(projs))
	for _, proj := range projs {
		c := cloneValue(proj)
		c.Facts = sortFactsCopy(factsByOwner[string(core.OwnerTypeProject)+":"+c.ID])
		outProjs = append(outProjs, c)
	}
	sort.Slice(outProjs, func(i, j int) bool { return outProjs[i].ID < outProjs[j].ID })

	outEdus := make([]core.Education, 0, len(edus))
	for _, edu := range edus {
		c := cloneValue(edu)
		c.Facts = sortFactsCopy(factsByOwner[string(core.OwnerTypeEducation)+":"+c.ID])
		outEdus = append(outEdus, c)
	}
	sort.Slice(outEdus, func(i, j int) bool { return outEdus[i].ID < outEdus[j].ID })

	outCerts := cloneValue(certs)
	sort.Slice(outCerts, func(i, j int) bool { return outCerts[i].ID < outCerts[j].ID })

	outSkills := cloneValue(skills)
	sort.Slice(outSkills, func(i, j int) bool { return outSkills[i].ID < outSkills[j].ID })

	rawSnap := &core.MainProfileSnapshot{
		MainRevision:   prof.MainRevision,
		Profile:        *cloneValue(prof),
		Experiences:    outExps,
		Projects:       outProjs,
		Education:      outEdus,
		Certifications: outCerts,
		Skills:         outSkills,
		Facts:          sortFactsCopy(facts),
		ExportedAt:     now,
	}
	return cloneValue(rawSnap.ConsolidateSkills())
}

// sortFactsCopy returns a deterministically sorted copy of facts.
func sortFactsCopy(facts []core.Fact) []core.Fact {
	if len(facts) == 0 {
		return nil
	}
	out := cloneValue(facts)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// prepareReplacedProfile computes the new Profile row and monotonic MainRevision for ReplaceCareerGraph.
func prepareReplacedProfile(userID string, existing *core.Profile, incoming core.Profile, now time.Time) (*core.Profile, int64) {
	rev := int64(1)
	if existing != nil {
		rev = existing.MainRevision + 1
	}
	next := cloneValue(incoming)
	next.UserID = userID
	next.MainRevision = rev
	if next.ID == "" {
		if existing != nil && existing.ID != "" {
			next.ID = existing.ID
		} else {
			next.ID = "profile-" + userID
		}
	}
	if existing != nil && next.Email == "" {
		next.Email = existing.Email
	}
	if existing != nil && !existing.CreatedAt.IsZero() && next.CreatedAt.IsZero() {
		next.CreatedAt = existing.CreatedAt
	}
	if next.CreatedAt.IsZero() {
		next.CreatedAt = now
	}
	next.UpdatedAt = now
	return &next, rev
}

// prepareReplacedExperiences normalizes experiences and extracts child facts for persistence.
func prepareReplacedExperiences(userID string, exps []core.WorkExperience, reg *skillRegistry, now time.Time) ([]core.WorkExperience, []core.Fact) {
	out := make([]core.WorkExperience, 0, len(exps))
	var facts []core.Fact
	for _, exp := range exps {
		c := cloneValue(exp)
		c.UserID = userID
		ensureEntityIDs(&c.ID, &c.ShortID, "exp", now, &c.CreatedAt, &c.UpdatedAt)
		c.Skills = reg.registerMany(c.Skills)
		facts = append(facts, prepareReplacedFacts(userID, core.OwnerTypeWorkExperience, c.ID, c.Facts, reg, now)...)
		c.Facts = nil
		out = append(out, c)
	}
	return out, facts
}

// prepareReplacedProjects normalizes projects and extracts child facts for persistence.
func prepareReplacedProjects(userID string, projs []core.Project, reg *skillRegistry, now time.Time) ([]core.Project, []core.Fact) {
	out := make([]core.Project, 0, len(projs))
	var facts []core.Fact
	for _, proj := range projs {
		c := cloneValue(proj)
		c.UserID = userID
		ensureEntityIDs(&c.ID, &c.ShortID, "proj", now, &c.CreatedAt, &c.UpdatedAt)
		c.Skills = reg.registerMany(c.Skills)
		facts = append(facts, prepareReplacedFacts(userID, core.OwnerTypeProject, c.ID, c.Facts, reg, now)...)
		c.Facts = nil
		out = append(out, c)
	}
	return out, facts
}

// prepareReplacedEducations normalizes education entries and extracts child facts for persistence.
func prepareReplacedEducations(userID string, edus []core.Education, reg *skillRegistry, now time.Time) ([]core.Education, []core.Fact) {
	out := make([]core.Education, 0, len(edus))
	var facts []core.Fact
	for _, edu := range edus {
		c := cloneValue(edu)
		c.UserID = userID
		ensureEntityIDs(&c.ID, &c.ShortID, "edu", now, &c.CreatedAt, &c.UpdatedAt)
		facts = append(facts, prepareReplacedFacts(userID, core.OwnerTypeEducation, c.ID, c.Facts, reg, now)...)
		c.Facts = nil
		out = append(out, c)
	}
	return out, facts
}

// prepareReplacedCertifications normalizes certification records for persistence.
func prepareReplacedCertifications(userID string, certs []core.Certification, now time.Time) []core.Certification {
	out := make([]core.Certification, 0, len(certs))
	for _, cert := range certs {
		c := cloneValue(cert)
		c.UserID = userID
		ensureEntityIDs(&c.ID, &c.ShortID, "cert", now, &c.CreatedAt, &c.UpdatedAt)
		out = append(out, c)
	}
	return out
}

// prepareReplacedFacts normalizes fact records bound to an optional parent entity.
func prepareReplacedFacts(userID string, ownerType core.OwnerType, ownerID string, facts []core.Fact, reg *skillRegistry, now time.Time) []core.Fact {
	out := make([]core.Fact, 0, len(facts))
	for _, f := range facts {
		c := cloneValue(f)
		c.UserID = userID
		if ownerID != "" {
			c.OwnerType = ownerType
			c.OwnerID = ownerID
		}
		ensureEntityIDs(&c.ID, &c.ShortID, "fact", now, &c.CreatedAt, &c.UpdatedAt)
		c.Skills = reg.registerMany(c.Skills)
		out = append(out, c)
	}
	return out
}

// mergeSavedSkills deduplicates explicit tenant skills and auto-registered tenant skills by ID.
func mergeSavedSkills(userID string, resolved []core.Skill, created []*core.Skill) []core.Skill {
	seen := make(map[string]struct{})
	var out []core.Skill
	for _, sk := range resolved {
		if sk.IsGlobal || sk.UserID != userID {
			continue
		}
		if _, ok := seen[sk.ID]; !ok {
			seen[sk.ID] = struct{}{}
			out = append(out, sk)
		}
	}
	for _, sk := range created {
		if sk.IsGlobal || sk.UserID != userID {
			continue
		}
		if _, ok := seen[sk.ID]; !ok {
			seen[sk.ID] = struct{}{}
			out = append(out, *sk)
		}
	}
	return out
}

// deleteTenantCareerRecords deletes existing user-owned career entities prior to replacement writes.
func deleteTenantCareerRecords(
	ctx context.Context,
	tx Tx,
	userID string,
	skills []core.Skill,
	exps []core.WorkExperience,
	projs []core.Project,
	edus []core.Education,
	certs []core.Certification,
	facts []core.Fact,
) error {
	for _, f := range facts {
		if f.UserID == userID {
			if err := tx.DeleteFact(ctx, f.ID); err != nil && !errors.Is(err, core.ErrNotFound) {
				return err
			}
		}
	}
	for _, sk := range skills {
		if !sk.IsGlobal && sk.UserID == userID {
			if err := tx.DeleteSkill(ctx, sk.ID); err != nil && !errors.Is(err, core.ErrNotFound) {
				return err
			}
		}
	}
	for _, exp := range exps {
		if err := tx.DeleteExperience(ctx, exp.ID); err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}
	}
	for _, proj := range projs {
		if err := tx.DeleteProject(ctx, proj.ID); err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}
	}
	for _, edu := range edus {
		if err := tx.DeleteEducation(ctx, edu.ID); err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}
	}
	for _, cert := range certs {
		if err := tx.DeleteCertification(ctx, cert.ID); err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}
	}
	return nil
}

// writeReplacedCareerRecords persists newly prepared Tier-1 career graph records inside tx.
func writeReplacedCareerRecords(
	ctx context.Context,
	tx Tx,
	skills []core.Skill,
	exps []core.WorkExperience,
	projs []core.Project,
	edus []core.Education,
	certs []core.Certification,
	facts []core.Fact,
) error {
	for i := range skills {
		if err := tx.PutSkill(ctx, &skills[i]); err != nil {
			return err
		}
	}
	for i := range exps {
		if err := tx.PutExperience(ctx, &exps[i]); err != nil {
			return err
		}
	}
	for i := range projs {
		if err := tx.PutProject(ctx, &projs[i]); err != nil {
			return err
		}
	}
	for i := range edus {
		if err := tx.PutEducation(ctx, &edus[i]); err != nil {
			return err
		}
	}
	for i := range certs {
		if err := tx.PutCertification(ctx, &certs[i]); err != nil {
			return err
		}
	}
	for i := range facts {
		if err := tx.PutFact(ctx, &facts[i]); err != nil {
			return err
		}
	}
	return nil
}

// validateAndPrepareSnapshotFacts checks snapshot-level fact owner references and deduplicates inline parent facts.
func validateAndPrepareSnapshotFacts(
	userID string,
	snapshotFacts []core.Fact,
	exps []core.WorkExperience,
	projs []core.Project,
	edus []core.Education,
	expFacts []core.Fact,
	projFacts []core.Fact,
	eduFacts []core.Fact,
	reg *skillRegistry,
	now time.Time,
) ([]core.Fact, error) {
	validOwners := make(map[string]bool, len(exps)+len(projs)+len(edus))
	for _, e := range exps {
		validOwners[string(core.OwnerTypeWorkExperience)+":"+e.ID] = true
	}
	for _, p := range projs {
		validOwners[string(core.OwnerTypeProject)+":"+p.ID] = true
	}
	for _, edu := range edus {
		validOwners[string(core.OwnerTypeEducation)+":"+edu.ID] = true
	}

	seenIDs := make(map[string]bool, len(expFacts)+len(projFacts)+len(eduFacts))
	for _, group := range [][]core.Fact{expFacts, projFacts, eduFacts} {
		for _, f := range group {
			if f.ID != "" {
				seenIDs[f.ID] = true
			}
		}
	}

	var extra []core.Fact
	for _, f := range snapshotFacts {
		if f.ID != "" && seenIDs[f.ID] {
			continue
		}
		if f.OwnerType != "" || f.OwnerID != "" {
			if !validOwners[string(f.OwnerType)+":"+f.OwnerID] {
				return nil, fmt.Errorf("%w: fact %q references missing %s owner %q", core.ErrValidation, f.ID, f.OwnerType, f.OwnerID)
			}
		}
		extra = append(extra, prepareReplacedFacts(userID, "", "", []core.Fact{f}, reg, now)...)
	}
	return extra, nil
}
