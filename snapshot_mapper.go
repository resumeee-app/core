package core

import "strings"

// cloneDate returns a defensive copy of a Date pointer.
func cloneDate(d *Date) *Date {
	if d == nil {
		return nil
	}
	cp := *d
	return &cp
}

// cloneContactDetail returns a defensive copy of a ContactDetail pointer.
func cloneContactDetail(c *ContactDetail) *ContactDetail {
	if c == nil {
		return nil
	}
	cp := *c
	return &cp
}

// cloneInt returns a defensive copy of an int pointer.
func cloneInt(i *int) *int {
	if i == nil {
		return nil
	}
	cp := *i
	return &cp
}

// SnapshotToTailoredContent deterministically projects a MainProfileSnapshot into TailoredContent
// preserving 100% fidelity with zero LLM alteration, omissions, or prompt overrides.
func SnapshotToTailoredContent(snapshot *MainProfileSnapshot) *TailoredContent {
	if snapshot == nil {
		return &TailoredContent{
			SkillGroups:    []TailoredSkillGroup{},
			Experiences:    []TailoredExperience{},
			Projects:       []TailoredProject{},
			Education:      []TailoredEducation{},
			Certifications: []TailoredCert{},
		}
	}

	factsByOwner := indexFactsByOwner(snapshot.Facts)
	return &TailoredContent{
		Summary:        snapshot.Profile.Summary,
		SkillGroups:    buildTailoredSkillGroups(snapshot),
		Experiences:    mapExperiencesToTailored(snapshot.Experiences, factsByOwner),
		Projects:       mapProjectsToTailored(snapshot.Projects, factsByOwner),
		Education:      mapEducationToTailored(snapshot.Education, factsByOwner),
		Certifications: mapCertsToTailored(snapshot.Certifications),
	}
}

// buildTailoredSkillGroups consolidates snapshot skills grouped by category in deterministic order.
func buildTailoredSkillGroups(snapshot *MainProfileSnapshot) []TailoredSkillGroup {
	type groupEntry struct {
		category string
		skills   []string
		skillIDs []string
	}

	allSkills := snapshot.AllSkills()
	groupOrder := make([]string, 0, len(allSkills))
	groups := make(map[string]*groupEntry, len(allSkills))

	for _, skill := range allSkills {
		cat := strings.TrimSpace(skill.Category)
		if cat == "" {
			cat = "General Skills"
		}
		if _, exists := groups[cat]; !exists {
			groupOrder = append(groupOrder, cat)
			groups[cat] = &groupEntry{
				category: cat,
				skills:   make([]string, 0, 4),
				skillIDs: make([]string, 0, 4),
			}
		}
		groups[cat].skills = append(groups[cat].skills, skill.Name)
		groups[cat].skillIDs = append(groups[cat].skillIDs, skill.ID)
	}

	result := make([]TailoredSkillGroup, 0, len(groupOrder))
	for _, cat := range groupOrder {
		g := groups[cat]
		result = append(result, TailoredSkillGroup{
			Category:       g.category,
			Skills:         g.skills,
			SourceSkillIDs: g.skillIDs,
		})
	}
	return result
}

// indexFactsByOwner indexes standalone facts by their polymorphic OwnerID as a fallback when nested slices are empty.
func indexFactsByOwner(facts []Fact) map[string][]Fact {
	factsByOwner := make(map[string][]Fact, len(facts))
	for _, f := range facts {
		factsByOwner[f.OwnerID] = append(factsByOwner[f.OwnerID], f)
	}
	return factsByOwner
}

// resolveEntityFacts selects inline facts when preloaded, falling back to snapshot-level indexed facts.
func resolveEntityFacts(inline []Fact, ownerID string, factsByOwner map[string][]Fact) []Fact {
	if len(inline) == 0 && len(factsByOwner) > 0 {
		return factsByOwner[ownerID]
	}
	return inline
}

// mapFactsToBullets converts domain Fact records into TailoredBullet items with source provenance IDs.
func mapFactsToBullets(facts []Fact) []TailoredBullet {
	bullets := make([]TailoredBullet, 0, len(facts))
	for _, f := range facts {
		var skillIDs []string
		if len(f.Skills) > 0 {
			skillIDs = make([]string, 0, len(f.Skills))
			for _, s := range f.Skills {
				skillIDs = append(skillIDs, s.ID)
			}
		}
		bullets = append(bullets, TailoredBullet{
			Text:           f.Statement,
			SourceFactIDs:  []string{f.ID},
			SourceSkillIDs: skillIDs,
		})
	}
	return bullets
}

// mapExperiencesToTailored projects domain work experiences into TailoredExperience entries.
func mapExperiencesToTailored(exps []WorkExperience, factsByOwner map[string][]Fact) []TailoredExperience {
	result := make([]TailoredExperience, 0, len(exps))
	for _, exp := range exps {
		facts := resolveEntityFacts(exp.Facts, exp.ID, factsByOwner)
		result = append(result, TailoredExperience{
			SourceEntityID:    exp.ID,
			Company:           exp.Company,
			RoleTitle:         exp.RoleTitle,
			EmploymentType:    exp.EmploymentType,
			StartDate:         exp.StartDate,
			EndDate:           cloneDate(exp.EndDate),
			IsCurrent:         exp.IsCurrent,
			Location:          exp.Location,
			CompanyLocation:   exp.CompanyLocation,
			CompanyContact:    cloneContactDetail(exp.CompanyContact),
			HoursPerWeek:      cloneInt(exp.HoursPerWeek),
			SupervisorName:    exp.SupervisorName,
			SupervisorContact: exp.SupervisorContact,
			DutiesSummary:     exp.DutiesSummary,
			Bullets:           mapFactsToBullets(facts),
			Metadata:          CloneMetadata(exp.Metadata),
		})
	}
	return result
}

// mapProjectsToTailored projects domain projects into TailoredProject entries.
func mapProjectsToTailored(projs []Project, factsByOwner map[string][]Fact) []TailoredProject {
	result := make([]TailoredProject, 0, len(projs))
	for _, proj := range projs {
		facts := resolveEntityFacts(proj.Facts, proj.ID, factsByOwner)
		result = append(result, TailoredProject{
			SourceEntityID: proj.ID,
			Name:           proj.Name,
			Role:           proj.Role,
			URL:            proj.URL,
			StartDate:      cloneDate(proj.StartDate),
			EndDate:        cloneDate(proj.EndDate),
			Summary:        proj.Summary,
			Bullets:        mapFactsToBullets(facts),
			Metadata:       CloneMetadata(proj.Metadata),
		})
	}
	return result
}

// mapEducationToTailored projects domain education entries into TailoredEducation records.
func mapEducationToTailored(edus []Education, factsByOwner map[string][]Fact) []TailoredEducation {
	result := make([]TailoredEducation, 0, len(edus))
	for _, edu := range edus {
		facts := resolveEntityFacts(edu.Facts, edu.ID, factsByOwner)
		result = append(result, TailoredEducation{
			SourceEntityID: edu.ID,
			Institution:    edu.Institution,
			Degree:         edu.Degree,
			Field:          edu.Field,
			StartDate:      cloneDate(edu.StartDate),
			EndDate:        cloneDate(edu.EndDate),
			Location:       edu.Location,
			Score:          edu.Score,
			Bullets:        mapFactsToBullets(facts),
			Metadata:       CloneMetadata(edu.Metadata),
		})
	}
	return result
}

// mapCertsToTailored projects domain certifications into TailoredCert entries.
func mapCertsToTailored(certs []Certification) []TailoredCert {
	result := make([]TailoredCert, 0, len(certs))
	for _, cert := range certs {
		result = append(result, TailoredCert{
			SourceEntityID: cert.ID,
			Name:           cert.Name,
			Issuer:         cert.Issuer,
			Date:           cloneDate(cert.Date),
			URL:            cert.URL,
			Summary:        cert.Summary,
			Metadata:       CloneMetadata(cert.Metadata),
		})
	}
	return result
}
