// Package provenance implements anti-hallucination provenance validation ("No Lies" invariant),
// verifying that all tailored achievements cite verified Main Profile facts and skills.
//
// It performs strict citation checks against main profile snapshots to ensure that
// generative AI tailoring runs never invent work experiences, bullet points, skills,
// or metrics not backed by candidate-verified career records.
package provenance

import (
	"fmt"
	"strings"

	"github.com/resumeee-app/core"
	"github.com/resumeee-app/core/branch"
	"github.com/resumeee-app/core/worker"
)

// Compile-time assertions verifying Validator satisfies branch and worker provenance interfaces.
var (
	_ branch.ProvenanceValidator = (*Validator)(nil)
	_ worker.ProvenanceValidator = (*Validator)(nil)
)

// Validator audits tailored resume content against a candidate's main profile snapshot.
type Validator struct{}

// New creates a new Validator instance.
func New() *Validator {
	return &Validator{}
}

// Validate is a package-level convenience function delegating to Validator.
func Validate(content *core.TailoredContent, snapshot *core.MainProfileSnapshot, branch *core.Branch) (*core.ValidationResult, error) {
	return New().Validate(content, snapshot, branch)
}

// ValidateStrict is a package-level convenience function delegating to Validator.
func ValidateStrict(content *core.TailoredContent, snapshot *core.MainProfileSnapshot, branch *core.Branch) error {
	return New().ValidateStrict(content, snapshot, branch)
}

// factMetadata records the canonical owner keys and owner type for a verified fact.
type factMetadata struct {
	OwnerID      string
	OwnerShortID string
	OwnerType    core.OwnerType
}

// snapshotIndex holds dual-key (canonical UUID and compact short ID) lookup maps
// built from a MainProfileSnapshot for provenance auditing.
type snapshotIndex struct {
	skills         map[string]core.Skill
	experiences    map[string]core.WorkExperience
	projects       map[string]core.Project
	education      map[string]core.Education
	certifications map[string]core.Certification
	facts          map[string]factMetadata
}

// newSnapshotIndex indexes all verified skills, entities, and facts from snapshot by canonical and short IDs.
func newSnapshotIndex(snapshot *core.MainProfileSnapshot) *snapshotIndex {
	allSkills := snapshot.AllSkills()
	idx := &snapshotIndex{
		skills:         make(map[string]core.Skill, len(allSkills)*2),
		experiences:    make(map[string]core.WorkExperience, len(snapshot.Experiences)*2),
		projects:       make(map[string]core.Project, len(snapshot.Projects)*2),
		education:      make(map[string]core.Education, len(snapshot.Education)*2),
		certifications: make(map[string]core.Certification, len(snapshot.Certifications)*2),
		facts:          make(map[string]factMetadata),
	}

	registerSkills(idx.skills, allSkills)
	idx.registerExperiences(snapshot.Experiences)
	idx.registerProjects(snapshot.Projects)
	idx.registerEducation(snapshot.Education)
	idx.registerCertifications(snapshot.Certifications)
	idx.registerStandaloneFacts(snapshot.Facts)
	return idx
}

// registerSkills indexes skills under canonical ID, short ID, slug, and lowercase name.
func registerSkills(validSkills map[string]core.Skill, skills []core.Skill) {
	for _, s := range skills {
		validSkills[s.ID] = s
		if short := s.EffectiveShortID(); short != "" {
			validSkills[short] = s
		}
		if s.Slug != "" {
			validSkills[s.Slug] = s
		}
		if s.Name != "" {
			validSkills[strings.ToLower(s.Name)] = s
		}
	}
}

// registerFacts indexes a slice of facts under both canonical ID and short ID with owner metadata.
func registerFacts(factRegistry map[string]factMetadata, facts []core.Fact, ownerID, ownerShortID string, defaultOwnerType core.OwnerType) {
	for _, f := range facts {
		ownerType := f.OwnerType
		if ownerType == "" {
			ownerType = defaultOwnerType
		}
		meta := factMetadata{OwnerID: ownerID, OwnerShortID: ownerShortID, OwnerType: ownerType}
		factRegistry[f.ID] = meta
		if short := f.EffectiveShortID(); short != "" {
			factRegistry[short] = meta
		}
	}
}

// registerExperiences indexes verified work experiences and their embedded facts.
func (i *snapshotIndex) registerExperiences(experiences []core.WorkExperience) {
	for _, exp := range experiences {
		i.experiences[exp.ID] = exp
		if short := exp.EffectiveShortID(); short != "" {
			i.experiences[short] = exp
		}
		registerFacts(i.facts, exp.Facts, exp.ID, exp.EffectiveShortID(), core.OwnerTypeWorkExperience)
	}
}

// registerProjects indexes verified projects and their embedded facts.
func (i *snapshotIndex) registerProjects(projects []core.Project) {
	for _, proj := range projects {
		i.projects[proj.ID] = proj
		if short := proj.EffectiveShortID(); short != "" {
			i.projects[short] = proj
		}
		registerFacts(i.facts, proj.Facts, proj.ID, proj.EffectiveShortID(), core.OwnerTypeProject)
	}
}

// registerEducation indexes verified education entries and their embedded facts.
func (i *snapshotIndex) registerEducation(education []core.Education) {
	for _, edu := range education {
		i.education[edu.ID] = edu
		if short := edu.EffectiveShortID(); short != "" {
			i.education[short] = edu
		}
		registerFacts(i.facts, edu.Facts, edu.ID, edu.EffectiveShortID(), core.OwnerTypeEducation)
	}
}

// registerCertifications indexes verified certifications by canonical and short IDs.
func (i *snapshotIndex) registerCertifications(certs []core.Certification) {
	for _, cert := range certs {
		i.certifications[cert.ID] = cert
		if short := cert.EffectiveShortID(); short != "" {
			i.certifications[short] = cert
		}
	}
}

// registerStandaloneFacts indexes standalone facts by resolving owner short IDs where possible.
func (i *snapshotIndex) registerStandaloneFacts(facts []core.Fact) {
	for _, f := range facts {
		ownerID := f.OwnerID
		ownerShortID := f.OwnerID
		if origExp, exists := i.experiences[f.OwnerID]; exists {
			ownerID = origExp.ID
			ownerShortID = origExp.EffectiveShortID()
		} else if origProj, exists := i.projects[f.OwnerID]; exists {
			ownerID = origProj.ID
			ownerShortID = origProj.EffectiveShortID()
		} else if origEdu, exists := i.education[f.OwnerID]; exists {
			ownerID = origEdu.ID
			ownerShortID = origEdu.EffectiveShortID()
		}
		meta := factMetadata{OwnerID: ownerID, OwnerShortID: ownerShortID, OwnerType: f.OwnerType}
		i.facts[f.ID] = meta
		if short := f.EffectiveShortID(); short != "" {
			i.facts[short] = meta
		}
	}
}

// markExcludedIDs adds canonical and short IDs of an entity to the excluded lookup set so
// exclusion matches regardless of which identifier form an LLM cites.
func markExcludedIDs(excluded map[string]bool, canonicalID, shortID string) {
	excluded[canonicalID] = true
	if shortID != "" {
		excluded[shortID] = true
	}
}

// buildExcludedSet expands branch ExcludedEntityIDs across canonical and short entity keys.
func buildExcludedSet(idx *snapshotIndex, branch *core.Branch) map[string]bool {
	excludedSet := make(map[string]bool)
	if branch == nil {
		return excludedSet
	}
	for _, id := range branch.ExcludedEntityIDs {
		excludedSet[id] = true
		if orig, ok := idx.experiences[id]; ok {
			markExcludedIDs(excludedSet, orig.ID, orig.EffectiveShortID())
		}
		if orig, ok := idx.projects[id]; ok {
			markExcludedIDs(excludedSet, orig.ID, orig.EffectiveShortID())
		}
		if orig, ok := idx.education[id]; ok {
			markExcludedIDs(excludedSet, orig.ID, orig.EffectiveShortID())
		}
		if orig, ok := idx.certifications[id]; ok {
			markExcludedIDs(excludedSet, orig.ID, orig.EffectiveShortID())
		}
		if orig, ok := idx.skills[id]; ok {
			markExcludedIDs(excludedSet, orig.ID, orig.EffectiveShortID())
		}
	}
	return excludedSet
}

// recordRetainedEntity records all identifier variants of a retained entity for pinning checks.
func recordRetainedEntity(retained map[string]bool, citedID, canonicalID, shortID string) {
	retained[citedID] = true
	retained[canonicalID] = true
	if shortID != "" {
		retained[shortID] = true
	}
}

// clonePtr returns a shallow value copy of p when non-nil to isolate sanitized output from snapshot mutations.
func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	cp := *p
	return &cp
}

// isExcluded reports whether any identifier form of an entity is in excludedSet.
func isExcluded(excludedSet map[string]bool, citedID, canonicalID, shortID string) bool {
	return excludedSet[citedID] || excludedSet[canonicalID] || (shortID != "" && excludedSet[shortID])
}

// Validate audits and sanitizes TailoredContent against MainProfileSnapshot and Branch rules.
// It verifies fact and skill citations using dual-key resolution (matching both canonical UUIDs and short IDs),
// strips ungrounded or cross-entity spoofed claims, filters excluded entities, and checks for pinned omissions.
func (v *Validator) Validate(content *core.TailoredContent, snapshot *core.MainProfileSnapshot, branch *core.Branch) (*core.ValidationResult, error) {
	if content == nil {
		return nil, fmt.Errorf("%w: content cannot be nil", core.ErrValidation)
	}
	if snapshot == nil {
		return nil, fmt.Errorf("%w: main profile snapshot cannot be nil", core.ErrValidation)
	}

	idx := newSnapshotIndex(snapshot)
	excludedSet := buildExcludedSet(idx, branch)
	retained := make(map[string]bool)
	var warnings []string

	sanitized := &core.TailoredContent{
		Summary:        content.Summary,
		SkillGroups:    sanitizeSkillGroups(content.SkillGroups, idx.skills, retained, &warnings),
		Experiences:    sanitizeExperiences(content.Experiences, idx, excludedSet, retained, &warnings),
		Projects:       sanitizeProjects(content.Projects, idx, excludedSet, retained, &warnings),
		Education:      sanitizeEducation(content.Education, idx, excludedSet, retained, &warnings),
		Certifications: sanitizeCertifications(content.Certifications, idx, excludedSet, retained, &warnings),
	}

	warnings = appendPinnedWarnings(warnings, branch, retained)

	return &core.ValidationResult{
		SanitizedContent: sanitized,
		Warnings:         warnings,
	}, nil
}

// ValidateStrict runs Validate and returns core.ErrProvenance if any warnings are emitted.
func (v *Validator) ValidateStrict(content *core.TailoredContent, snapshot *core.MainProfileSnapshot, branch *core.Branch) error {
	res, err := v.Validate(content, snapshot, branch)
	if err != nil {
		return err
	}
	if len(res.Warnings) > 0 {
		return core.ErrProvenance
	}
	return nil
}

// sanitizeSkillGroups validates cited skills in each group and records retained skill IDs.
func sanitizeSkillGroups(groups []core.TailoredSkillGroup, validSkills map[string]core.Skill, retained map[string]bool, warnings *[]string) []core.TailoredSkillGroup {
	if groups == nil {
		return nil
	}
	sanitized := make([]core.TailoredSkillGroup, 0, len(groups))
	for _, group := range groups {
		sg, groupWarnings, ok := sanitizeSkillGroup(group, validSkills)
		*warnings = append(*warnings, groupWarnings...)
		if !ok {
			continue
		}
		sanitized = append(sanitized, sg)
		for _, sid := range sg.SourceSkillIDs {
			retained[sid] = true
			if s, exists := validSkills[sid]; exists {
				recordRetainedEntity(retained, sid, s.ID, s.EffectiveShortID())
			}
		}
	}
	return sanitized
}

// verifyGroundedEntity checks whether a tailored entity cites a valid, non-excluded canonical entity,
// appending the corresponding provenance warning when the entry must be pruned.
func verifyGroundedEntity(
	kind string,
	sourceID string,
	tailoredLabel string,
	found bool,
	origID string,
	origShortID string,
	origLabel string,
	excludedSet map[string]bool,
	warnings *[]string,
) bool {
	if excludedSet[sourceID] {
		label := tailoredLabel
		if label == "" && found {
			label = origLabel
		}
		*warnings = append(*warnings, fmt.Sprintf("[EXCLUDED_ENTITY] pruned excluded %s '%s' (%s)", kind, sourceID, label))
		return false
	}
	if !found {
		*warnings = append(*warnings, fmt.Sprintf("[UNGROUNDED_ENTITY] pruned ungrounded %s '%s'", kind, sourceID))
		return false
	}
	if isExcluded(excludedSet, sourceID, origID, origShortID) {
		*warnings = append(*warnings, fmt.Sprintf("[EXCLUDED_ENTITY] pruned excluded %s '%s' (%s)", kind, sourceID, origLabel))
		return false
	}
	return true
}

// sanitizeExperiences verifies experience identity, reconciles canonical metadata, and audits bullets.
func sanitizeExperiences(exps []core.TailoredExperience, idx *snapshotIndex, excludedSet, retained map[string]bool, warnings *[]string) []core.TailoredExperience {
	if exps == nil {
		return nil
	}
	sanitized := make([]core.TailoredExperience, 0, len(exps))
	for _, exp := range exps {
		origExp, ok := idx.experiences[exp.SourceEntityID]
		var origID, origShortID, origLabel string
		if ok {
			origID, origShortID, origLabel = origExp.ID, origExp.EffectiveShortID(), origExp.Company
		}
		if !verifyGroundedEntity("experience", exp.SourceEntityID, exp.Company, ok, origID, origShortID, origLabel, excludedSet, warnings) {
			continue
		}

		duties := origExp.DutiesSummary
		if exp.DutiesSummary != "" {
			duties = exp.DutiesSummary
		}

		sanitized = append(sanitized, core.TailoredExperience{
			SourceEntityID:    origExp.ID,
			Company:           origExp.Company,
			RoleTitle:         origExp.RoleTitle,
			EmploymentType:    origExp.EmploymentType,
			StartDate:         origExp.StartDate,
			EndDate:           clonePtr(origExp.EndDate),
			IsCurrent:         origExp.IsCurrent,
			Location:          origExp.Location,
			CompanyLocation:   origExp.CompanyLocation,
			CompanyContact:    clonePtr(origExp.CompanyContact),
			SupervisorName:    origExp.SupervisorName,
			SupervisorContact: origExp.SupervisorContact,
			HoursPerWeek:      clonePtr(origExp.HoursPerWeek),
			DutiesSummary:     duties,
			Bullets:           sanitizeBullets("experience", exp.SourceEntityID, exp.Bullets, idx.facts, idx.skills, warnings),
			Metadata:          core.CloneMetadata(origExp.Metadata),
		})
		recordRetainedEntity(retained, exp.SourceEntityID, origExp.ID, origExp.EffectiveShortID())
	}
	return sanitized
}

// sanitizeProjects verifies project identity, reconciles canonical metadata, and audits bullets.
func sanitizeProjects(projs []core.TailoredProject, idx *snapshotIndex, excludedSet, retained map[string]bool, warnings *[]string) []core.TailoredProject {
	if projs == nil {
		return nil
	}
	sanitized := make([]core.TailoredProject, 0, len(projs))
	for _, proj := range projs {
		origProj, ok := idx.projects[proj.SourceEntityID]
		var origID, origShortID, origLabel string
		if ok {
			origID, origShortID, origLabel = origProj.ID, origProj.EffectiveShortID(), origProj.Name
		}
		if !verifyGroundedEntity("project", proj.SourceEntityID, proj.Name, ok, origID, origShortID, origLabel, excludedSet, warnings) {
			continue
		}

		summary := origProj.Summary
		if proj.Summary != "" {
			summary = proj.Summary
		}

		sanitized = append(sanitized, core.TailoredProject{
			SourceEntityID: origProj.ID,
			Name:           origProj.Name,
			Role:           origProj.Role,
			URL:            origProj.URL,
			StartDate:      clonePtr(origProj.StartDate),
			EndDate:        clonePtr(origProj.EndDate),
			Summary:        summary,
			Bullets:        sanitizeBullets("project", proj.SourceEntityID, proj.Bullets, idx.facts, idx.skills, warnings),
			Metadata:       core.CloneMetadata(origProj.Metadata),
		})
		recordRetainedEntity(retained, proj.SourceEntityID, origProj.ID, origProj.EffectiveShortID())
	}
	return sanitized
}

// sanitizeEducation verifies education identity, reconciles canonical metadata, and audits bullets.
func sanitizeEducation(edus []core.TailoredEducation, idx *snapshotIndex, excludedSet, retained map[string]bool, warnings *[]string) []core.TailoredEducation {
	if edus == nil {
		return nil
	}
	sanitized := make([]core.TailoredEducation, 0, len(edus))
	for _, edu := range edus {
		origEdu, ok := idx.education[edu.SourceEntityID]
		var origID, origShortID, origLabel string
		if ok {
			origID, origShortID, origLabel = origEdu.ID, origEdu.EffectiveShortID(), origEdu.Institution
		}
		if !verifyGroundedEntity("education", edu.SourceEntityID, edu.Institution, ok, origID, origShortID, origLabel, excludedSet, warnings) {
			continue
		}

		sanitized = append(sanitized, core.TailoredEducation{
			SourceEntityID: origEdu.ID,
			Institution:    origEdu.Institution,
			Degree:         origEdu.Degree,
			Field:          origEdu.Field,
			StartDate:      clonePtr(origEdu.StartDate),
			EndDate:        clonePtr(origEdu.EndDate),
			Location:       origEdu.Location,
			Score:          origEdu.Score,
			Bullets:        sanitizeBullets("education", edu.SourceEntityID, edu.Bullets, idx.facts, idx.skills, warnings),
			Metadata:       core.CloneMetadata(origEdu.Metadata),
		})
		recordRetainedEntity(retained, edu.SourceEntityID, origEdu.ID, origEdu.EffectiveShortID())
	}
	return sanitized
}

// sanitizeCertifications verifies certification identity and reconciles canonical metadata.
func sanitizeCertifications(certs []core.TailoredCert, idx *snapshotIndex, excludedSet, retained map[string]bool, warnings *[]string) []core.TailoredCert {
	if certs == nil {
		return nil
	}
	sanitized := make([]core.TailoredCert, 0, len(certs))
	for _, cert := range certs {
		origCert, ok := idx.certifications[cert.SourceEntityID]
		var origID, origShortID, origLabel string
		if ok {
			origID, origShortID, origLabel = origCert.ID, origCert.EffectiveShortID(), origCert.Name
		}
		if !verifyGroundedEntity("certification", cert.SourceEntityID, cert.Name, ok, origID, origShortID, origLabel, excludedSet, warnings) {
			continue
		}

		summary := origCert.Summary
		if cert.Summary != "" {
			summary = cert.Summary
		}

		sanitized = append(sanitized, core.TailoredCert{
			SourceEntityID: origCert.ID,
			Name:           origCert.Name,
			Issuer:         origCert.Issuer,
			Date:           clonePtr(origCert.Date),
			URL:            origCert.URL,
			Summary:        summary,
			Metadata:       core.CloneMetadata(origCert.Metadata),
		})
		recordRetainedEntity(retained, cert.SourceEntityID, origCert.ID, origCert.EffectiveShortID())
	}
	return sanitized
}

// appendPinnedWarnings emits warnings when branch-pinned entities are missing from sanitized output.
func appendPinnedWarnings(warnings []string, branch *core.Branch, retained map[string]bool) []string {
	if branch == nil || len(branch.PinnedEntityIDs) == 0 {
		return warnings
	}
	for _, pinnedID := range branch.PinnedEntityIDs {
		if !retained[pinnedID] {
			warnings = append(warnings, fmt.Sprintf("[OMITTED_PINNED_ENTITY] pinned entity '%s' was omitted from tailored content", pinnedID))
		}
	}
	return warnings
}

// sanitizeSkillGroup verifies skill IDs against valid snapshot skills and removes ungrounded entries.
func sanitizeSkillGroup(group core.TailoredSkillGroup, validSkills map[string]core.Skill) (core.TailoredSkillGroup, []string, bool) {
	var warnings []string
	if len(group.SourceSkillIDs) == 0 {
		if len(group.Skills) > 0 {
			warnings = append(warnings, fmt.Sprintf("[UNGROUNDED_SKILL] pruned ungrounded skills from category '%s': missing source skill citations", group.Category))
		}
		return core.TailoredSkillGroup{}, warnings, false
	}

	alignedNames := len(group.Skills) == len(group.SourceSkillIDs)
	var validIDs []string
	var validSkillNames []string

	for i, sid := range group.SourceSkillIDs {
		if _, ok := validSkills[sid]; ok {
			validIDs = append(validIDs, sid)
			if alignedNames {
				validSkillNames = append(validSkillNames, group.Skills[i])
			}
		} else {
			warnings = append(warnings, fmt.Sprintf("[UNGROUNDED_SKILL] pruned ungrounded skill '%s' from category '%s'", sid, group.Category))
		}
	}

	if len(validIDs) == 0 {
		return core.TailoredSkillGroup{}, warnings, false
	}
	if !alignedNames {
		validSkillNames = reconcileMismatchedSkillNames(group.Skills, validIDs, validSkills)
	}

	return core.TailoredSkillGroup{
		Category:       group.Category,
		Skills:         validSkillNames,
		SourceSkillIDs: validIDs,
	}, warnings, true
}

// reconcileMismatchedSkillNames filters display skill names against grounded validIDs, falling back to canonical names.
func reconcileMismatchedSkillNames(rawNames, validIDs []string, validSkills map[string]core.Skill) []string {
	validNameSet := make(map[string]bool, len(validIDs))
	for _, sid := range validIDs {
		validNameSet[strings.ToLower(validSkills[sid].Name)] = true
	}

	var matched []string
	for _, sName := range rawNames {
		if validNameSet[strings.ToLower(sName)] {
			matched = append(matched, sName)
		}
	}
	if len(matched) > 0 {
		return matched
	}

	fallback := make([]string, 0, len(validIDs))
	for _, sid := range validIDs {
		fallback = append(fallback, validSkills[sid].Name)
	}
	return fallback
}

// validateBulletFacts ensures all source facts cited by a bullet exist and belong to the enclosing entity.
func validateBulletFacts(entityType, entityID string, factIDs []string, factRegistry map[string]factMetadata, warnings *[]string) bool {
	if len(factIDs) == 0 {
		*warnings = append(*warnings, fmt.Sprintf("[UNGROUNDED_BULLET] pruned bullet in %s '%s': missing source fact citations", entityType, entityID))
		return false
	}
	for _, factID := range factIDs {
		meta, ok := factRegistry[factID]
		if !ok {
			*warnings = append(*warnings, fmt.Sprintf("[FABRICATED_FACT] pruned bullet in %s '%s': fact '%s' not found in main profile", entityType, entityID, factID))
			return false
		}
		if meta.OwnerID != entityID && meta.OwnerShortID != entityID {
			*warnings = append(*warnings, fmt.Sprintf("[CROSS_ENTITY_SPOOF] pruned bullet in %s '%s': fact '%s' belongs to owner '%s', not '%s'", entityType, entityID, factID, meta.OwnerID, entityID))
			return false
		}
	}
	return true
}

// filterBulletSkills strips ungrounded skill citations from a bullet while preserving nil vs empty semantics.
func filterBulletSkills(entityType, entityID string, skillIDs []string, validSkills map[string]core.Skill, warnings *[]string) []string {
	if skillIDs == nil {
		return nil
	}
	valid := make([]string, 0, len(skillIDs))
	for _, skillID := range skillIDs {
		if _, ok := validSkills[skillID]; ok {
			valid = append(valid, skillID)
		} else {
			*warnings = append(*warnings, fmt.Sprintf("[UNGROUNDED_BULLET_SKILL] pruned ungrounded skill '%s' cited on bullet in %s '%s'", skillID, entityType, entityID))
		}
	}
	return valid
}

// sanitizeBullets evaluates candidate accomplishment bullets for an experience, project, or education item,
// enforcing cited fact existence, enclosing-owner matching under dual-key resolution, and skill validation.
func sanitizeBullets(entityType, entityID string, bullets []core.TailoredBullet, factRegistry map[string]factMetadata, validSkills map[string]core.Skill, warnings *[]string) []core.TailoredBullet {
	if bullets == nil {
		return nil
	}

	sanitizedBullets := make([]core.TailoredBullet, 0, len(bullets))
	for _, bullet := range bullets {
		if !validateBulletFacts(entityType, entityID, bullet.SourceFactIDs, factRegistry, warnings) {
			continue
		}

		sanitizedFactIDs := make([]string, len(bullet.SourceFactIDs))
		copy(sanitizedFactIDs, bullet.SourceFactIDs)

		sanitizedBullets = append(sanitizedBullets, core.TailoredBullet{
			Text:           bullet.Text,
			SourceFactIDs:  sanitizedFactIDs,
			SourceSkillIDs: filterBulletSkills(entityType, entityID, bullet.SourceSkillIDs, validSkills, warnings),
		})
	}

	return sanitizedBullets
}
