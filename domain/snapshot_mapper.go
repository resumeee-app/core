package domain

import "strings"

// SnapshotToTailoredContent deterministically projects a MasterProfileSnapshot into TailoredContent
// preserving 100% fidelity with zero LLM alteration, omissions, or prompt overrides.
func SnapshotToTailoredContent(snapshot *MasterProfileSnapshot) *TailoredContent {
	tc := &TailoredContent{
		SkillGroups:    []TailoredSkillGroup{},
		Experiences:    []TailoredExperience{},
		Projects:       []TailoredProject{},
		Education:      []TailoredEducation{},
		Certifications: []TailoredCert{},
	}

	if snapshot == nil {
		return tc
	}

	tc.Summary = snapshot.Profile.Summary

	// 1. Group skills by Category, preserving order of appearance.
	type groupEntry struct {
		category string
		skills   []string
		skillIDs []string
	}
	var groupOrder []string
	groups := make(map[string]*groupEntry)

	for _, skill := range snapshot.Skills {
		cat := strings.TrimSpace(skill.Category)
		if cat == "" {
			cat = "General Skills"
		}
		if _, exists := groups[cat]; !exists {
			groupOrder = append(groupOrder, cat)
			groups[cat] = &groupEntry{
				category: cat,
				skills:   []string{},
				skillIDs: []string{},
			}
		}
		groups[cat].skills = append(groups[cat].skills, skill.Name)
		groups[cat].skillIDs = append(groups[cat].skillIDs, skill.ID)
	}

	for _, cat := range groupOrder {
		g := groups[cat]
		tc.SkillGroups = append(tc.SkillGroups, TailoredSkillGroup{
			Category:       g.category,
			Skills:         g.skills,
			SourceSkillIDs: g.skillIDs,
		})
	}

	// 2. Index snapshot.Facts by OwnerID as fallback if entity child facts are not preloaded.
	factsByOwner := make(map[string][]Fact)
	for _, f := range snapshot.Facts {
		factsByOwner[f.OwnerID] = append(factsByOwner[f.OwnerID], f)
	}

	// Helper to map a slice of domain.Fact to []TailoredBullet
	mapFactsToBullets := func(facts []Fact) []TailoredBullet {
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

	// 3. Experiences
	for _, exp := range snapshot.Experiences {
		facts := exp.Facts
		if len(facts) == 0 && len(snapshot.Facts) > 0 {
			facts = factsByOwner[exp.ID]
		}
		tc.Experiences = append(tc.Experiences, TailoredExperience{
			SourceEntityID:    exp.ID,
			Company:           exp.Company,
			RoleTitle:         exp.RoleTitle,
			StartDate:         exp.StartDate,
			EndDate:           exp.EndDate,
			Location:          exp.Location,
			CompanyLocation:   exp.CompanyLocation,
			CompanyContact:    exp.CompanyContact,
			HoursPerWeek:      exp.HoursPerWeek,
			SupervisorName:    exp.SupervisorName,
			SupervisorContact: exp.SupervisorContact,
			DutiesSummary:     exp.DutiesSummary,
			Bullets:           mapFactsToBullets(facts),
		})
	}

	// 4. Projects
	for _, proj := range snapshot.Projects {
		facts := proj.Facts
		if len(facts) == 0 && len(snapshot.Facts) > 0 {
			facts = factsByOwner[proj.ID]
		}
		tc.Projects = append(tc.Projects, TailoredProject{
			SourceEntityID: proj.ID,
			Name:           proj.Name,
			Role:           proj.Role,
			URL:            proj.URL,
			StartDate:      proj.StartDate,
			EndDate:        proj.EndDate,
			Summary:        proj.Summary,
			Bullets:        mapFactsToBullets(facts),
		})
	}

	// 5. Education
	for _, edu := range snapshot.Education {
		facts := edu.Facts
		if len(facts) == 0 && len(snapshot.Facts) > 0 {
			facts = factsByOwner[edu.ID]
		}
		tc.Education = append(tc.Education, TailoredEducation{
			SourceEntityID: edu.ID,
			Institution:    edu.Institution,
			Degree:         edu.Degree,
			Field:          edu.Field,
			StartDate:      edu.StartDate,
			EndDate:        edu.EndDate,
			Score:          edu.Score,
			Bullets:        mapFactsToBullets(facts),
		})
	}

	// 6. Certifications
	for _, cert := range snapshot.Certifications {
		tc.Certifications = append(tc.Certifications, TailoredCert{
			SourceEntityID: cert.ID,
			Name:           cert.Name,
			Issuer:         cert.Issuer,
			Date:           cert.Date,
			URL:            cert.URL,
		})
	}

	return tc
}
