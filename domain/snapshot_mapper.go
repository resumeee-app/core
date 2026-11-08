package domain

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

	tc := &TailoredContent{
		Summary:        snapshot.Profile.Summary,
		SkillGroups:    make([]TailoredSkillGroup, 0, len(snapshot.Skills)),
		Experiences:    make([]TailoredExperience, 0, len(snapshot.Experiences)),
		Projects:       make([]TailoredProject, 0, len(snapshot.Projects)),
		Education:      make([]TailoredEducation, 0, len(snapshot.Education)),
		Certifications: make([]TailoredCert, 0, len(snapshot.Certifications)),
	}

	// 1. Group skills by Category using all consolidated skills across the snapshot.
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

	for _, cat := range groupOrder {
		g := groups[cat]
		tc.SkillGroups = append(tc.SkillGroups, TailoredSkillGroup{
			Category:       g.category,
			Skills:         g.skills,
			SourceSkillIDs: g.skillIDs,
		})
	}

	// 2. Index snapshot.Facts by OwnerID as fallback if entity child facts are not preloaded.
	factsByOwner := make(map[string][]Fact, len(snapshot.Facts))
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
			StartDate:      cloneDate(proj.StartDate),
			EndDate:        cloneDate(proj.EndDate),
			Summary:        proj.Summary,
			Bullets:        mapFactsToBullets(facts),
			Metadata:       CloneMetadata(proj.Metadata),
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
			StartDate:      cloneDate(edu.StartDate),
			EndDate:        cloneDate(edu.EndDate),
			Location:       edu.Location,
			Score:          edu.Score,
			Bullets:        mapFactsToBullets(facts),
			Metadata:       CloneMetadata(edu.Metadata),
		})
	}

	// 6. Certifications
	for _, cert := range snapshot.Certifications {
		tc.Certifications = append(tc.Certifications, TailoredCert{
			SourceEntityID: cert.ID,
			Name:           cert.Name,
			Issuer:         cert.Issuer,
			Date:           cloneDate(cert.Date),
			URL:            cert.URL,
			Summary:        cert.Summary,
			Metadata:       CloneMetadata(cert.Metadata),
		})
	}

	return tc
}
