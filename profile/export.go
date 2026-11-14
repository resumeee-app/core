package profile

import (
	"encoding/json"

	"github.com/resumeee-app/resumeee-core"
)

// defaultSchemaURL is the standard JSON Resume v1.0.0 schema URI emitted on exports.
const defaultSchemaURL = "https://raw.githubusercontent.com/jsonresume/resume-schema/v1.0.0/schema.json"

// FromSnapshot converts a MainProfileSnapshot back into a valid JSONResume document,
// restoring unmapped sections and child highlights.
func FromSnapshot(snapshot *core.MainProfileSnapshot) (*core.JSONResume, error) {
	if snapshot == nil {
		return nil, core.ErrValidation
	}

	expFactsMap, projFactsMap := indexSnapshotFacts(snapshot.Facts)
	res := &core.JSONResume{
		Schema:       defaultSchemaURL,
		Basics:       basicsFromProfile(&snapshot.Profile),
		Work:         workFromSnapshot(snapshot.Experiences, expFactsMap),
		Education:    educationFromSnapshot(snapshot.Education),
		Certificates: certsFromSnapshot(snapshot.Certifications),
		Skills:       skillsFromSnapshot(snapshot.AllSkills()),
		Projects:     projectsFromSnapshot(snapshot.Projects, projFactsMap),
	}

	applyUnmappedMetadata(snapshot.Profile.Metadata, res)
	return res, nil
}

// basicsFromProfile populates a JSONResumeBasics struct from a domain Profile.
func basicsFromProfile(profile *core.Profile) core.JSONResumeBasics {
	if profile == nil {
		return core.JSONResumeBasics{}
	}
	profiles := make([]core.JSONResumeProfile, len(profile.Profiles))
	for i, p := range profile.Profiles {
		profiles[i] = core.JSONResumeProfile(p)
	}

	basics := core.JSONResumeBasics{
		Name:    profile.FullName,
		Label:   profile.Headline,
		Email:   profile.Email,
		Phone:   profile.Phone,
		Summary: profile.Summary,
		Location: core.JSONResumeLocation{
			Address:     profile.Location.Address,
			PostalCode:  profile.Location.PostalCode,
			City:        profile.Location.City,
			CountryCode: profile.Location.CountryCode,
			Region:      profile.Location.Region,
		},
		Profiles: profiles,
	}

	if profile.Metadata != nil {
		if img, ok := profile.Metadata["basics_image"].(string); ok {
			basics.Image = img
		}
		if u, ok := profile.Metadata["basics_url"].(string); ok {
			basics.URL = u
		}
	}
	return basics
}

// indexSnapshotFacts groups standalone fact statements by owner ID as a fallback for empty nested Facts slices.
func indexSnapshotFacts(facts []core.Fact) (map[string][]string, map[string][]string) {
	expFacts := make(map[string][]string)
	projFacts := make(map[string][]string)
	for _, f := range facts {
		switch f.OwnerType {
		case core.OwnerTypeWorkExperience:
			expFacts[f.OwnerID] = append(expFacts[f.OwnerID], f.Statement)
		case core.OwnerTypeProject:
			projFacts[f.OwnerID] = append(projFacts[f.OwnerID], f.Statement)
		}
	}
	return expFacts, projFacts
}

// formatOptionalDate returns the serialized date string or "" when d is nil or zero.
func formatOptionalDate(d *core.Date) string {
	if d == nil || d.IsZero() {
		return ""
	}
	return d.String()
}

// workFromSnapshot exports snapshot WorkExperiences as JSON Resume work entries.
func workFromSnapshot(experiences []core.WorkExperience, expFactsMap map[string][]string) []core.JSONResumeWork {
	workList := make([]core.JSONResumeWork, len(experiences))
	for i, exp := range experiences {
		endDate := ""
		if !exp.IsCurrent {
			endDate = formatOptionalDate(exp.EndDate)
		}

		var highlights []string
		if len(exp.Facts) > 0 {
			for _, f := range exp.Facts {
				highlights = append(highlights, f.Statement)
			}
		} else {
			highlights = expFactsMap[exp.ID]
		}

		urlStr := ""
		locStr := exp.Location.City
		if exp.Metadata != nil {
			if u, ok := exp.Metadata["url"].(string); ok {
				urlStr = u
			}
			if l, ok := exp.Metadata["location"].(string); ok {
				locStr = l
			}
		}

		workList[i] = core.JSONResumeWork{
			Name:       exp.Company,
			Company:    exp.Company,
			Position:   exp.RoleTitle,
			URL:        urlStr,
			StartDate:  exp.StartDate.String(),
			EndDate:    endDate,
			Summary:    exp.DutiesSummary,
			Highlights: highlights,
			Location:   locStr,
		}
	}
	return workList
}

// projectsFromSnapshot exports snapshot Projects as JSON Resume project entries.
func projectsFromSnapshot(projects []core.Project, projFactsMap map[string][]string) []core.JSONResumeProject {
	projList := make([]core.JSONResumeProject, len(projects))
	for i, proj := range projects {
		var highlights []string
		if len(proj.Facts) > 0 {
			for _, f := range proj.Facts {
				highlights = append(highlights, f.Statement)
			}
		} else {
			highlights = projFactsMap[proj.ID]
		}

		var roles, keywords []string
		var entity, pType string
		if proj.Metadata != nil {
			roles = extractStringSlice(proj.Metadata["roles"])
			keywords = extractStringSlice(proj.Metadata["keywords"])
			if e, ok := proj.Metadata["entity"].(string); ok {
				entity = e
			}
			if t, ok := proj.Metadata["type"].(string); ok {
				pType = t
			}
		}
		if len(roles) == 0 && proj.Role != "" {
			roles = []string{proj.Role}
		}

		projList[i] = core.JSONResumeProject{
			Name:        proj.Name,
			Description: proj.Summary,
			Highlights:  highlights,
			Keywords:    keywords,
			StartDate:   formatOptionalDate(proj.StartDate),
			EndDate:     formatOptionalDate(proj.EndDate),
			URL:         proj.URL,
			Roles:       roles,
			Entity:      entity,
			Type:        pType,
		}
	}
	return projList
}

// educationFromSnapshot exports snapshot Education items as JSON Resume education entries.
func educationFromSnapshot(edus []core.Education) []core.JSONResumeEducation {
	eduList := make([]core.JSONResumeEducation, len(edus))
	for i, edu := range edus {
		urlStr := ""
		var courses []string
		if edu.Metadata != nil {
			if u, ok := edu.Metadata["url"].(string); ok {
				urlStr = u
			}
			courses = extractStringSlice(edu.Metadata["courses"])
		}

		eduList[i] = core.JSONResumeEducation{
			Institution: edu.Institution,
			URL:         urlStr,
			Area:        edu.Field,
			StudyType:   edu.Degree,
			StartDate:   formatOptionalDate(edu.StartDate),
			EndDate:     formatOptionalDate(edu.EndDate),
			Score:       edu.Score,
			Courses:     courses,
		}
	}
	return eduList
}

// certsFromSnapshot exports snapshot Certifications as JSON Resume certificate entries.
func certsFromSnapshot(certs []core.Certification) []core.JSONResumeCertificate {
	certList := make([]core.JSONResumeCertificate, len(certs))
	for i, cert := range certs {
		certList[i] = core.JSONResumeCertificate{
			Name:   cert.Name,
			Date:   formatOptionalDate(cert.Date),
			Issuer: cert.Issuer,
			URL:    cert.URL,
		}
	}
	return certList
}

// skillsFromSnapshot exports consolidated snapshot skills as JSON Resume skills.
func skillsFromSnapshot(allSkills []core.Skill) []core.JSONResumeSkill {
	skillList := make([]core.JSONResumeSkill, len(allSkills))
	for i, skill := range allSkills {
		skillList[i] = core.JSONResumeSkill{
			Name:     skill.Name,
			Level:    skill.Proficiency,
			Keywords: skill.Keywords,
		}
	}
	return skillList
}

// applyUnmappedMetadata restores preserved non-core JSON Resume sections from Profile metadata.
func applyUnmappedMetadata(meta map[string]any, res *core.JSONResume) {
	if meta == nil {
		return
	}
	rawUnmapped, ok := meta["jsonresume_unmapped"]
	if !ok || rawUnmapped == nil {
		return
	}
	data, err := json.Marshal(rawUnmapped)
	if err != nil {
		return
	}
	var aux struct {
		Schema        string                       `json:"$schema"`
		Volunteer     []core.JSONResumeVolunteer   `json:"volunteer"`
		Awards        []core.JSONResumeAward       `json:"awards"`
		Publications  []core.JSONResumePublication `json:"publications"`
		Languages     []core.JSONResumeLanguage    `json:"languages"`
		Interests     []core.JSONResumeInterest    `json:"interests"`
		References    []core.JSONResumeReference   `json:"references"`
		Meta          *core.JSONResumeMeta         `json:"meta"`
		ExtraUnmapped map[string]json.RawMessage   `json:"extra_unmapped"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return
	}
	if aux.Schema != "" {
		res.Schema = aux.Schema
	}
	if len(aux.Volunteer) > 0 {
		res.Volunteer = aux.Volunteer
	}
	if len(aux.Awards) > 0 {
		res.Awards = aux.Awards
	}
	if len(aux.Publications) > 0 {
		res.Publications = aux.Publications
	}
	if len(aux.Languages) > 0 {
		res.Languages = aux.Languages
	}
	if len(aux.Interests) > 0 {
		res.Interests = aux.Interests
	}
	if len(aux.References) > 0 {
		res.References = aux.References
	}
	if aux.Meta != nil {
		res.Meta = aux.Meta
	}
	if len(aux.ExtraUnmapped) > 0 {
		res.Unmapped = aux.ExtraUnmapped
	}
}

// extractStringSlice safely extracts a slice of strings from either []string or []any.
func extractStringSlice(val any) []string {
	if val == nil {
		return nil
	}
	switch v := val.(type) {
	case []string:
		return v
	case []any:
		var result []string
		for _, item := range v {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	default:
		return nil
	}
}
