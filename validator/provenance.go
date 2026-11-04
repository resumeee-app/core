// Package validator implements anti-hallucination provenance validation ("No Lies" invariant),
// verifying that all tailored achievements cite verified Master Profile facts and skills.
package validator

import (
	"fmt"
	"strings"

	"resumeee/internal/domain"
)

// ValidationResult contains the sanitized tailored content and structured warnings.
type ValidationResult struct {
	SanitizedContent *domain.TailoredContent
	Warnings         []string
}

// ProvenanceValidator audits and sanitizes TailoredContent against MasterProfileSnapshot and Branch rules.
type ProvenanceValidator interface {
	Validate(content *domain.TailoredContent, snapshot *domain.MasterProfileSnapshot, branch *domain.Branch) (*ValidationResult, error)
	ValidateStrict(content *domain.TailoredContent, snapshot *domain.MasterProfileSnapshot, branch *domain.Branch) error
}

// Validator implements the ProvenanceValidator interface.
type Validator struct{}

// New creates a new instance of Validator.
func New() *Validator {
	return &Validator{}
}

// Validate is a package-level convenience function delegating to Validator.
func Validate(content *domain.TailoredContent, snapshot *domain.MasterProfileSnapshot, branch *domain.Branch) (*ValidationResult, error) {
	return New().Validate(content, snapshot, branch)
}

// ValidateStrict is a package-level convenience function delegating to Validator.
func ValidateStrict(content *domain.TailoredContent, snapshot *domain.MasterProfileSnapshot, branch *domain.Branch) error {
	return New().ValidateStrict(content, snapshot, branch)
}

type factMetadata struct {
	OwnerID      string
	OwnerShortID string
	OwnerType    domain.OwnerType
}

func registerSkills(validSkills map[string]domain.Skill, skills []domain.Skill) {
	for _, s := range skills {
		validSkills[s.ID] = s
		if short := s.EffectiveShortID(); short != "" {
			validSkills[short] = s
		}
	}
}

func registerFacts(factRegistry map[string]factMetadata, validSkills map[string]domain.Skill, facts []domain.Fact, ownerID, ownerShortID string, defaultOwnerType domain.OwnerType) {
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
		registerSkills(validSkills, f.Skills)
	}
}

// Validate audits and sanitizes TailoredContent against MasterProfileSnapshot and Branch rules.
// It verifies fact and skill citations using dual-key resolution (matching both canonical UUIDs and short IDs),
// strips ungrounded or cross-entity spoofed claims, filters excluded entities, and checks for pinned omissions.
//
// Parameters:
//   - content: The candidate TailoredContent produced by the LLM.
//   - snapshot: The authoritative MasterProfileSnapshot against which citations are verified.
//   - branch: Branch configuration containing PinnedEntityIDs and ExcludedEntityIDs (optional).
//
// Returns:
//   - ValidationResult containing SanitizedContent, list of Warnings, and IsValid boolean flag.
//   - Error if content or snapshot is nil.
func (v *Validator) Validate(content *domain.TailoredContent, snapshot *domain.MasterProfileSnapshot, branch *domain.Branch) (*ValidationResult, error) {
	if content == nil {
		return nil, fmt.Errorf("%w: content cannot be nil", domain.ErrValidation)
	}
	if snapshot == nil {
		return nil, fmt.Errorf("%w: master profile snapshot cannot be nil", domain.ErrValidation)
	}

	validSkills := make(map[string]domain.Skill, len(snapshot.Skills)*2)
	registerSkills(validSkills, snapshot.Skills)

	validExperiences := make(map[string]domain.WorkExperience, len(snapshot.Experiences)*2)
	validProjects := make(map[string]domain.Project, len(snapshot.Projects)*2)
	validEducation := make(map[string]domain.Education, len(snapshot.Education)*2)
	validCertifications := make(map[string]domain.Certification, len(snapshot.Certifications)*2)
	factRegistry := make(map[string]factMetadata)

	for _, exp := range snapshot.Experiences {
		validExperiences[exp.ID] = exp
		if exp.EffectiveShortID() != "" {
			validExperiences[exp.EffectiveShortID()] = exp
		}
		registerSkills(validSkills, exp.Skills)
		registerFacts(factRegistry, validSkills, exp.Facts, exp.ID, exp.EffectiveShortID(), domain.OwnerTypeWorkExperience)
	}

	for _, proj := range snapshot.Projects {
		validProjects[proj.ID] = proj
		if proj.EffectiveShortID() != "" {
			validProjects[proj.EffectiveShortID()] = proj
		}
		registerSkills(validSkills, proj.Skills)
		registerFacts(factRegistry, validSkills, proj.Facts, proj.ID, proj.EffectiveShortID(), domain.OwnerTypeProject)
	}

	for _, edu := range snapshot.Education {
		validEducation[edu.ID] = edu
		if edu.EffectiveShortID() != "" {
			validEducation[edu.EffectiveShortID()] = edu
		}
		registerFacts(factRegistry, validSkills, edu.Facts, edu.ID, edu.EffectiveShortID(), domain.OwnerTypeEducation)
	}

	for _, cert := range snapshot.Certifications {
		validCertifications[cert.ID] = cert
		if cert.EffectiveShortID() != "" {
			validCertifications[cert.EffectiveShortID()] = cert
		}
	}

	for _, f := range snapshot.Facts {
		ownerID := f.OwnerID
		ownerShortID := f.OwnerID
		if origExp, exists := validExperiences[f.OwnerID]; exists {
			ownerID = origExp.ID
			ownerShortID = origExp.EffectiveShortID()
		} else if origProj, exists := validProjects[f.OwnerID]; exists {
			ownerID = origProj.ID
			ownerShortID = origProj.EffectiveShortID()
		} else if origEdu, exists := validEducation[f.OwnerID]; exists {
			ownerID = origEdu.ID
			ownerShortID = origEdu.EffectiveShortID()
		}
		meta := factMetadata{OwnerID: ownerID, OwnerShortID: ownerShortID, OwnerType: f.OwnerType}
		factRegistry[f.ID] = meta
		if f.EffectiveShortID() != "" {
			factRegistry[f.EffectiveShortID()] = meta
		}
		registerSkills(validSkills, f.Skills)
	}

	excludedSet := make(map[string]bool)
	if branch != nil {
		for _, id := range branch.ExcludedEntityIDs {
			excludedSet[id] = true
			if orig, exists := validExperiences[id]; exists {
				excludedSet[orig.ID] = true
				if orig.EffectiveShortID() != "" {
					excludedSet[orig.EffectiveShortID()] = true
				}
			}
			if orig, exists := validProjects[id]; exists {
				excludedSet[orig.ID] = true
				if orig.EffectiveShortID() != "" {
					excludedSet[orig.EffectiveShortID()] = true
				}
			}
			if orig, exists := validEducation[id]; exists {
				excludedSet[orig.ID] = true
				if orig.EffectiveShortID() != "" {
					excludedSet[orig.EffectiveShortID()] = true
				}
			}
			if orig, exists := validCertifications[id]; exists {
				excludedSet[orig.ID] = true
				if orig.EffectiveShortID() != "" {
					excludedSet[orig.EffectiveShortID()] = true
				}
			}
			if orig, exists := validSkills[id]; exists {
				excludedSet[orig.ID] = true
				if orig.EffectiveShortID() != "" {
					excludedSet[orig.EffectiveShortID()] = true
				}
			}
		}
	}

	var warnings []string
	retainedEntityIDs := make(map[string]bool)

	sanitized := &domain.TailoredContent{
		Summary: content.Summary,
	}

	// 1. Skill Groups
	if content.SkillGroups != nil {
		sanitizedGroups := make([]domain.TailoredSkillGroup, 0, len(content.SkillGroups))
		for _, group := range content.SkillGroups {
			sg, groupWarnings, ok := sanitizeSkillGroup(group, validSkills)
			warnings = append(warnings, groupWarnings...)
			if ok {
				sanitizedGroups = append(sanitizedGroups, sg)
				for _, sid := range sg.SourceSkillIDs {
					retainedEntityIDs[sid] = true
					if s, exists := validSkills[sid]; exists {
						retainedEntityIDs[s.ID] = true
						if s.EffectiveShortID() != "" {
							retainedEntityIDs[s.EffectiveShortID()] = true
						}
					}
				}
			}
		}
		sanitized.SkillGroups = sanitizedGroups
	}

	// 2. Experiences
	if content.Experiences != nil {
		sanitizedExperiences := make([]domain.TailoredExperience, 0, len(content.Experiences))
		for _, exp := range content.Experiences {
			if excludedSet[exp.SourceEntityID] {
				company := exp.Company
				if company == "" {
					if orig, exists := validExperiences[exp.SourceEntityID]; exists {
						company = orig.Company
					}
				}
				warnings = append(warnings, fmt.Sprintf("[EXCLUDED_ENTITY] pruned excluded experience '%s' (%s)", exp.SourceEntityID, company))
				continue
			}

			origExp, ok := validExperiences[exp.SourceEntityID]
			if !ok {
				warnings = append(warnings, fmt.Sprintf("[UNGROUNDED_ENTITY] pruned ungrounded experience '%s'", exp.SourceEntityID))
				continue
			}

			if excludedSet[origExp.ID] || (origExp.EffectiveShortID() != "" && excludedSet[origExp.EffectiveShortID()]) {
				warnings = append(warnings, fmt.Sprintf("[EXCLUDED_ENTITY] pruned excluded experience '%s' (%s)", exp.SourceEntityID, origExp.Company))
				continue
			}

			sanitizedBullets := sanitizeBullets("experience", exp.SourceEntityID, exp.Bullets, factRegistry, validSkills, &warnings)

			sanitizedExp := domain.TailoredExperience{
				SourceEntityID:    origExp.ID,
				Company:           origExp.Company,
				RoleTitle:         origExp.RoleTitle,
				StartDate:         origExp.StartDate,
				Location:          origExp.Location,
				CompanyLocation:   origExp.CompanyLocation,
				SupervisorName:    origExp.SupervisorName,
				SupervisorContact: origExp.SupervisorContact,
				Bullets:           sanitizedBullets,
			}
			if origExp.EndDate != nil {
				end := *origExp.EndDate
				sanitizedExp.EndDate = &end
			}
			if origExp.CompanyContact != nil {
				cc := *origExp.CompanyContact
				sanitizedExp.CompanyContact = &cc
			}
			if origExp.HoursPerWeek != nil {
				hpw := *origExp.HoursPerWeek
				sanitizedExp.HoursPerWeek = &hpw
			}
			if exp.DutiesSummary != "" {
				sanitizedExp.DutiesSummary = exp.DutiesSummary
			} else {
				sanitizedExp.DutiesSummary = origExp.DutiesSummary
			}

			sanitizedExperiences = append(sanitizedExperiences, sanitizedExp)
			retainedEntityIDs[exp.SourceEntityID] = true
			retainedEntityIDs[origExp.ID] = true
			if origExp.EffectiveShortID() != "" {
				retainedEntityIDs[origExp.EffectiveShortID()] = true
			}
		}
		sanitized.Experiences = sanitizedExperiences
	}

	// 3. Projects
	if content.Projects != nil {
		sanitizedProjects := make([]domain.TailoredProject, 0, len(content.Projects))
		for _, proj := range content.Projects {
			if excludedSet[proj.SourceEntityID] {
				name := proj.Name
				if name == "" {
					if orig, exists := validProjects[proj.SourceEntityID]; exists {
						name = orig.Name
					}
				}
				warnings = append(warnings, fmt.Sprintf("[EXCLUDED_ENTITY] pruned excluded project '%s' (%s)", proj.SourceEntityID, name))
				continue
			}

			origProj, ok := validProjects[proj.SourceEntityID]
			if !ok {
				warnings = append(warnings, fmt.Sprintf("[UNGROUNDED_ENTITY] pruned ungrounded project '%s'", proj.SourceEntityID))
				continue
			}

			if excludedSet[origProj.ID] || (origProj.EffectiveShortID() != "" && excludedSet[origProj.EffectiveShortID()]) {
				warnings = append(warnings, fmt.Sprintf("[EXCLUDED_ENTITY] pruned excluded project '%s' (%s)", proj.SourceEntityID, origProj.Name))
				continue
			}

			sanitizedBullets := sanitizeBullets("project", proj.SourceEntityID, proj.Bullets, factRegistry, validSkills, &warnings)

			sanitizedProj := domain.TailoredProject{
				SourceEntityID: origProj.ID,
				Name:           origProj.Name,
				Role:           origProj.Role,
				URL:            origProj.URL,
				StartDate:      origProj.StartDate,
				Bullets:        sanitizedBullets,
			}
			if origProj.EndDate != nil {
				end := *origProj.EndDate
				sanitizedProj.EndDate = &end
			}
			if proj.Summary != "" {
				sanitizedProj.Summary = proj.Summary
			} else {
				sanitizedProj.Summary = origProj.Summary
			}

			sanitizedProjects = append(sanitizedProjects, sanitizedProj)
			retainedEntityIDs[proj.SourceEntityID] = true
			retainedEntityIDs[origProj.ID] = true
			if origProj.EffectiveShortID() != "" {
				retainedEntityIDs[origProj.EffectiveShortID()] = true
			}
		}
		sanitized.Projects = sanitizedProjects
	}

	// 4. Education
	if content.Education != nil {
		sanitizedEducation := make([]domain.TailoredEducation, 0, len(content.Education))
		for _, edu := range content.Education {
			if excludedSet[edu.SourceEntityID] {
				inst := edu.Institution
				if inst == "" {
					if orig, exists := validEducation[edu.SourceEntityID]; exists {
						inst = orig.Institution
					}
				}
				warnings = append(warnings, fmt.Sprintf("[EXCLUDED_ENTITY] pruned excluded education '%s' (%s)", edu.SourceEntityID, inst))
				continue
			}

			origEdu, ok := validEducation[edu.SourceEntityID]
			if !ok {
				warnings = append(warnings, fmt.Sprintf("[UNGROUNDED_ENTITY] pruned ungrounded education '%s'", edu.SourceEntityID))
				continue
			}

			if excludedSet[origEdu.ID] || (origEdu.EffectiveShortID() != "" && excludedSet[origEdu.EffectiveShortID()]) {
				warnings = append(warnings, fmt.Sprintf("[EXCLUDED_ENTITY] pruned excluded education '%s' (%s)", edu.SourceEntityID, origEdu.Institution))
				continue
			}

			sanitizedBullets := sanitizeBullets("education", edu.SourceEntityID, edu.Bullets, factRegistry, validSkills, &warnings)

			sanitizedEdu := domain.TailoredEducation{
				SourceEntityID: origEdu.ID,
				Institution:    origEdu.Institution,
				Degree:         origEdu.Degree,
				Field:          origEdu.Field,
				StartDate:      origEdu.StartDate,
				Score:          origEdu.Score,
				Bullets:        sanitizedBullets,
			}
			if origEdu.EndDate != nil {
				end := *origEdu.EndDate
				sanitizedEdu.EndDate = &end
			}

			sanitizedEducation = append(sanitizedEducation, sanitizedEdu)
			retainedEntityIDs[edu.SourceEntityID] = true
			retainedEntityIDs[origEdu.ID] = true
			if origEdu.EffectiveShortID() != "" {
				retainedEntityIDs[origEdu.EffectiveShortID()] = true
			}
		}
		sanitized.Education = sanitizedEducation
	}

	// 5. Certifications
	if content.Certifications != nil {
		sanitizedCerts := make([]domain.TailoredCert, 0, len(content.Certifications))
		for _, cert := range content.Certifications {
			if excludedSet[cert.SourceEntityID] {
				name := cert.Name
				if name == "" {
					if orig, exists := validCertifications[cert.SourceEntityID]; exists {
						name = orig.Name
					}
				}
				warnings = append(warnings, fmt.Sprintf("[EXCLUDED_ENTITY] pruned excluded certification '%s' (%s)", cert.SourceEntityID, name))
				continue
			}

			origCert, ok := validCertifications[cert.SourceEntityID]
			if !ok {
				warnings = append(warnings, fmt.Sprintf("[UNGROUNDED_ENTITY] pruned ungrounded certification '%s'", cert.SourceEntityID))
				continue
			}

			if excludedSet[origCert.ID] || (origCert.EffectiveShortID() != "" && excludedSet[origCert.EffectiveShortID()]) {
				warnings = append(warnings, fmt.Sprintf("[EXCLUDED_ENTITY] pruned excluded certification '%s' (%s)", cert.SourceEntityID, origCert.Name))
				continue
			}

			sanitizedCert := domain.TailoredCert{
				SourceEntityID: origCert.ID,
				Name:           origCert.Name,
				Issuer:         origCert.Issuer,
				Date:           origCert.Date,
				URL:            origCert.URL,
			}

			sanitizedCerts = append(sanitizedCerts, sanitizedCert)
			retainedEntityIDs[cert.SourceEntityID] = true
			retainedEntityIDs[origCert.ID] = true
			if origCert.EffectiveShortID() != "" {
				retainedEntityIDs[origCert.EffectiveShortID()] = true
			}
		}
		sanitized.Certifications = sanitizedCerts
	}

	// 6. Branch Pinning Verification
	if branch != nil && len(branch.PinnedEntityIDs) > 0 {
		for _, pinnedID := range branch.PinnedEntityIDs {
			if !retainedEntityIDs[pinnedID] {
				warnings = append(warnings, fmt.Sprintf("[OMITTED_PINNED_ENTITY] pinned entity '%s' was omitted from tailored content", pinnedID))
			}
		}
	}

	return &ValidationResult{
		SanitizedContent: sanitized,
		Warnings:         warnings,
	}, nil
}

// ValidateStrict runs Validate and returns domain.ErrProvenance if any warnings are emitted.
func (v *Validator) ValidateStrict(content *domain.TailoredContent, snapshot *domain.MasterProfileSnapshot, branch *domain.Branch) error {
	res, err := v.Validate(content, snapshot, branch)
	if err != nil {
		return err
	}
	if len(res.Warnings) > 0 {
		return domain.ErrProvenance
	}
	return nil
}

// sanitizeSkillGroup verifies skill IDs against valid snapshot skills and removes ungrounded entries.
func sanitizeSkillGroup(group domain.TailoredSkillGroup, validSkills map[string]domain.Skill) (domain.TailoredSkillGroup, []string, bool) {
	var warnings []string
	if len(group.SourceSkillIDs) == 0 {
		if len(group.Skills) > 0 {
			warnings = append(warnings, fmt.Sprintf("[UNGROUNDED_SKILL] pruned ungrounded skills from category '%s': missing source skill citations", group.Category))
		}
		return domain.TailoredSkillGroup{}, warnings, false
	}

	var validIDs []string
	var validSkillNames []string

	if len(group.Skills) == len(group.SourceSkillIDs) {
		for i, sid := range group.SourceSkillIDs {
			if _, ok := validSkills[sid]; ok {
				validIDs = append(validIDs, sid)
				validSkillNames = append(validSkillNames, group.Skills[i])
			} else {
				warnings = append(warnings, fmt.Sprintf("[UNGROUNDED_SKILL] pruned ungrounded skill '%s' from category '%s'", sid, group.Category))
			}
		}
	} else {
		for _, sid := range group.SourceSkillIDs {
			if _, ok := validSkills[sid]; ok {
				validIDs = append(validIDs, sid)
			} else {
				warnings = append(warnings, fmt.Sprintf("[UNGROUNDED_SKILL] pruned ungrounded skill '%s' from category '%s'", sid, group.Category))
			}
		}

		validNameSet := make(map[string]bool, len(validIDs))
		for _, sid := range validIDs {
			validNameSet[strings.ToLower(validSkills[sid].Name)] = true
		}
		for _, sName := range group.Skills {
			if validNameSet[strings.ToLower(sName)] {
				validSkillNames = append(validSkillNames, sName)
			}
		}
		if len(validSkillNames) == 0 && len(validIDs) > 0 {
			for _, sid := range validIDs {
				validSkillNames = append(validSkillNames, validSkills[sid].Name)
			}
		}
	}

	if len(validIDs) == 0 {
		return domain.TailoredSkillGroup{}, warnings, false
	}

	return domain.TailoredSkillGroup{
		Category:       group.Category,
		Skills:         validSkillNames,
		SourceSkillIDs: validIDs,
	}, warnings, true
}

// sanitizeBullets evaluates candidate accomplishment bullets for an experience, project, or education item.
// It verifies that each bullet cites at least one fact, verifies that cited facts exist in the master profile,
// validates that the fact belongs to the parent entity under dual-key resolution (matching either canonical ID or short ID),
// and strips ungrounded skill citations.
//
// Parameters:
//   - entityType: Descriptor of the parent entity (e.g. "experience", "project").
//   - entityID: Canonical ID or short ID of the enclosing parent entity.
//   - bullets: Slice of candidate TailoredBullet items to audit.
//   - factRegistry: Dual-key registry mapping fact IDs (canonical and short) to owner metadata.
//   - validSkills: Dual-key map of recognized skills.
//   - warnings: Pointer to slice of warning strings accumulator.
//
// Returns:
//   - Slice of sanitized, fully grounded TailoredBullet items.
func sanitizeBullets(entityType, entityID string, bullets []domain.TailoredBullet, factRegistry map[string]factMetadata, validSkills map[string]domain.Skill, warnings *[]string) []domain.TailoredBullet {
	if bullets == nil {
		return nil
	}

	sanitizedBullets := make([]domain.TailoredBullet, 0, len(bullets))

	for _, bullet := range bullets {
		if len(bullet.SourceFactIDs) == 0 {
			*warnings = append(*warnings, fmt.Sprintf("[UNGROUNDED_BULLET] pruned bullet in %s '%s': missing source fact citations", entityType, entityID))
			continue
		}

		grounded := true
		for _, factID := range bullet.SourceFactIDs {
			meta, ok := factRegistry[factID]
			if !ok {
				*warnings = append(*warnings, fmt.Sprintf("[FABRICATED_FACT] pruned bullet in %s '%s': fact '%s' not found in master profile", entityType, entityID, factID))
				grounded = false
				break
			}
			if meta.OwnerID != entityID && meta.OwnerShortID != entityID {
				*warnings = append(*warnings, fmt.Sprintf("[CROSS_ENTITY_SPOOF] pruned bullet in %s '%s': fact '%s' belongs to owner '%s', not '%s'", entityType, entityID, factID, meta.OwnerID, entityID))
				grounded = false
				break
			}
		}

		if !grounded {
			continue
		}

		var validBulletSkillIDs []string
		if bullet.SourceSkillIDs != nil {
			validBulletSkillIDs = make([]string, 0, len(bullet.SourceSkillIDs))
			for _, skillID := range bullet.SourceSkillIDs {
				if _, ok := validSkills[skillID]; ok {
					validBulletSkillIDs = append(validBulletSkillIDs, skillID)
				} else {
					*warnings = append(*warnings, fmt.Sprintf("[UNGROUNDED_BULLET_SKILL] pruned ungrounded skill '%s' cited on bullet in %s '%s'", skillID, entityType, entityID))
				}
			}
		}

		sanitizedFactIDs := make([]string, len(bullet.SourceFactIDs))
		copy(sanitizedFactIDs, bullet.SourceFactIDs)

		sanitizedBullet := domain.TailoredBullet{
			Text:           bullet.Text,
			SourceFactIDs:  sanitizedFactIDs,
			SourceSkillIDs: validBulletSkillIDs,
		}

		sanitizedBullets = append(sanitizedBullets, sanitizedBullet)
	}

	return sanitizedBullets
}
