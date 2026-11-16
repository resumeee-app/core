package profile

import "github.com/resumeee-app/core"

// FromTailored converts a BranchVersion's TailoredContent and base Profile into a valid JSONResume export.
func FromTailored(profile *core.Profile, tailored *core.TailoredContent) (*core.JSONResume, error) {
	if tailored == nil {
		return nil, core.ErrValidation
	}

	basics := basicsFromProfile(profile)
	if tailored.Summary != "" {
		basics.Summary = tailored.Summary
	}

	res := &core.JSONResume{
		Schema:       defaultSchemaURL,
		Basics:       basics,
		Work:         workFromTailored(tailored.Experiences),
		Education:    educationFromTailored(tailored.Education),
		Certificates: certsFromTailored(tailored.Certifications),
		Skills:       skillsFromTailored(tailored.SkillGroups),
		Projects:     projectsFromTailored(tailored.Projects),
	}

	if profile != nil {
		applyUnmappedMetadata(profile.Metadata, res)
	}
	return res, nil
}

// bulletTexts extracts plain text strings from tailored bullets, returning nil when bullets is empty.
func bulletTexts(bullets []core.TailoredBullet) []string {
	if len(bullets) == 0 {
		return nil
	}
	texts := make([]string, len(bullets))
	for i, b := range bullets {
		texts[i] = b.Text
	}
	return texts
}

// workFromTailored maps tailored experiences into JSON Resume work entries.
func workFromTailored(experiences []core.TailoredExperience) []core.JSONResumeWork {
	workList := make([]core.JSONResumeWork, len(experiences))
	for i, exp := range experiences {
		loc := exp.Location.City
		if loc == "" {
			loc = exp.CompanyLocation.City
		}

		workList[i] = core.JSONResumeWork{
			Name:       exp.Company,
			Company:    exp.Company,
			Position:   exp.RoleTitle,
			StartDate:  exp.StartDate.String(),
			EndDate:    formatOptionalDate(exp.EndDate),
			Summary:    exp.DutiesSummary,
			Highlights: bulletTexts(exp.Bullets),
			Location:   loc,
		}
	}
	return workList
}

// projectsFromTailored maps tailored projects into JSON Resume project entries.
func projectsFromTailored(projects []core.TailoredProject) []core.JSONResumeProject {
	projList := make([]core.JSONResumeProject, len(projects))
	for i, proj := range projects {
		var roles []string
		if proj.Role != "" {
			roles = []string{proj.Role}
		}

		projList[i] = core.JSONResumeProject{
			Name:        proj.Name,
			Description: proj.Summary,
			Highlights:  bulletTexts(proj.Bullets),
			StartDate:   formatOptionalDate(proj.StartDate),
			EndDate:     formatOptionalDate(proj.EndDate),
			URL:         proj.URL,
			Roles:       roles,
		}
	}
	return projList
}

// educationFromTailored maps tailored education items into JSON Resume education entries.
func educationFromTailored(edus []core.TailoredEducation) []core.JSONResumeEducation {
	eduList := make([]core.JSONResumeEducation, len(edus))
	for i, edu := range edus {
		eduList[i] = core.JSONResumeEducation{
			Institution: edu.Institution,
			StudyType:   edu.Degree,
			Area:        edu.Field,
			StartDate:   formatOptionalDate(edu.StartDate),
			EndDate:     formatOptionalDate(edu.EndDate),
			Score:       edu.Score,
			Highlights:  bulletTexts(edu.Bullets),
		}
	}
	return eduList
}

// certsFromTailored maps tailored certifications into JSON Resume certificate entries.
func certsFromTailored(certs []core.TailoredCert) []core.JSONResumeCertificate {
	certList := make([]core.JSONResumeCertificate, len(certs))
	for i, cert := range certs {
		certList[i] = core.JSONResumeCertificate{
			Name:   cert.Name,
			Issuer: cert.Issuer,
			Date:   formatOptionalDate(cert.Date),
			URL:    cert.URL,
		}
	}
	return certList
}

// skillsFromTailored maps tailored skill groups into JSON Resume skill entries.
func skillsFromTailored(groups []core.TailoredSkillGroup) []core.JSONResumeSkill {
	skillList := make([]core.JSONResumeSkill, len(groups))
	for i, sg := range groups {
		skillList[i] = core.JSONResumeSkill{
			Name:     sg.Category,
			Keywords: sg.Skills,
		}
	}
	return skillList
}
