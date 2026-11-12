package profile

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

const defaultSchemaURL = "https://raw.githubusercontent.com/jsonresume/resume-schema/v1.0.0/schema.json"

// ToSnapshot converts a parsed JSONResume document into a domain MainProfileSnapshot,
// generating relational entities with compact short IDs, child facts with skill associations,
// and preserving unmapped sections.
//
// Parameters:
//   - resume: The parsed JSONResume document structure.
//   - userID: Tenant identifier scoping the profile snapshot (defaults to "default").
//   - existingProfile: Baseline profile to inherit configuration and metadata from (optional).
//   - revision: Main revision sequence number.
//
// Returns:
//   - Pointer to MainProfileSnapshot with generated IDs and short IDs, or core.ErrValidation.
func ToSnapshot(resume *JSONResume, userID string, existingProfile *core.Profile, revision int64) (*core.MainProfileSnapshot, error) {
	return ToSnapshotWithGlobalSkills(resume, userID, existingProfile, revision, nil)
}

// ToSnapshotWithGlobalSkills converts a validated JSON Resume AST into a full MainProfileSnapshot,
// resolving candidate skills against the global skills catalog before creating custom tenant skills.
func ToSnapshotWithGlobalSkills(resume *JSONResume, userID string, existingProfile *core.Profile, revision int64, globalSkills []core.Skill) (*core.MainProfileSnapshot, error) {
	if resume == nil {
		return nil, core.ErrValidation
	}
	if userID == "" {
		userID = "default"
	}

	// 1. Construct Profile root
	profileID := core.GenerateID("prof")
	rules := ""
	metadata := make(map[string]any)

	if existingProfile != nil {
		if existingProfile.ID != "" {
			profileID = existingProfile.ID
		}
		rules = existingProfile.GlobalWritingRules
		if existingProfile.Metadata != nil {
			for k, v := range existingProfile.Metadata {
				metadata[k] = v
			}
		}
	}

	if resume.Basics.Image != "" {
		metadata["basics_image"] = resume.Basics.Image
	}
	if resume.Basics.URL != "" {
		metadata["basics_url"] = resume.Basics.URL
	}

	// Preserve unmapped sections (volunteer, awards, publications, languages, interests, references, meta, etc.)
	unmappedData := make(map[string]any)
	if len(resume.Volunteer) > 0 {
		unmappedData["volunteer"] = resume.Volunteer
	}
	if len(resume.Awards) > 0 {
		unmappedData["awards"] = resume.Awards
	}
	if len(resume.Publications) > 0 {
		unmappedData["publications"] = resume.Publications
	}
	if len(resume.Languages) > 0 {
		unmappedData["languages"] = resume.Languages
	}
	if len(resume.Interests) > 0 {
		unmappedData["interests"] = resume.Interests
	}
	if len(resume.References) > 0 {
		unmappedData["references"] = resume.References
	}
	if resume.Meta != nil {
		unmappedData["meta"] = resume.Meta
	}
	if resume.Schema != "" {
		unmappedData["$schema"] = resume.Schema
	}
	if len(resume.Unmapped) > 0 {
		unmappedData["extra_unmapped"] = resume.Unmapped
	}
	if len(unmappedData) > 0 {
		metadata["jsonresume_unmapped"] = unmappedData
	}

	profiles := make([]core.SocialLink, len(resume.Basics.Profiles))
	for i, p := range resume.Basics.Profiles {
		profiles[i] = core.SocialLink(p)
	}

	prof := core.Profile{
		ID:       profileID,
		UserID:   userID,
		FullName: resume.Basics.Name,
		Headline: resume.Basics.Label,
		Email:    resume.Basics.Email,
		Phone:    resume.Basics.Phone,
		Location: core.Location{
			Address:     resume.Basics.Location.Address,
			PostalCode:  resume.Basics.Location.PostalCode,
			City:        resume.Basics.Location.City,
			CountryCode: resume.Basics.Location.CountryCode,
			Region:      resume.Basics.Location.Region,
		},
		Profiles:           profiles,
		Summary:            resume.Basics.Summary,
		GlobalWritingRules: rules,
		MainRevision:       revision,
		Metadata:           metadata,
		CreatedAt:          time.Now().UTC(),
		UpdatedAt:          time.Now().UTC(),
	}

	// 2. Map Skills and build matching index
	globalSkillsByName := make(map[string]core.Skill)
	for _, gs := range globalSkills {
		globalSkillsByName[strings.ToLower(strings.TrimSpace(gs.Name))] = gs
		if gs.Slug != "" {
			globalSkillsByName[strings.ToLower(gs.Slug)] = gs
		}
	}

	skills := make([]core.Skill, len(resume.Skills))
	for i, s := range resume.Skills {
		cleanName := strings.TrimSpace(s.Name)
		lookupKey := strings.ToLower(cleanName)
		slug := core.Slugify(cleanName)

		canonicalName := s.Name
		if matchedGlobal, found := globalSkillsByName[lookupKey]; found {
			canonicalName = matchedGlobal.Name
			slug = matchedGlobal.Slug
		} else if matchedGlobal, found := globalSkillsByName[slug]; found {
			canonicalName = matchedGlobal.Name
			slug = matchedGlobal.Slug
		}

		skills[i] = core.Skill{
			ID:          core.GenerateID("skill"),
			ShortID:     core.GenerateShortID("skill"),
			UserID:      userID,
			Name:        canonicalName,
			Slug:        slug,
			IsGlobal:    false,
			Proficiency: s.Level,
			Keywords:    s.Keywords,
			CreatedAt:   time.Now().UTC(),
			UpdatedAt:   time.Now().UTC(),
		}
	}
	matcher := newSkillMatcher(skills)

	var allFacts []core.Fact

	// 3. Map Work Experiences and child Facts
	experiences := make([]core.WorkExperience, len(resume.Work))
	for i, w := range resume.Work {
		expID := core.GenerateID("exp")
		expShortID := core.GenerateShortID("exp")
		company := w.GetCompany()
		var endDate *core.Date
		isCurrent := true
		cleanEnd := strings.TrimSpace(w.EndDate)
		if cleanEnd != "" && !strings.EqualFold(cleanEnd, "present") {
			if ed, err := core.ParseDate(cleanEnd); err == nil && !ed.IsZero() {
				endDate = &ed
				isCurrent = false
			}
		}

		parsedStart, _ := core.ParseDate(w.StartDate)

		expMeta := make(map[string]any)
		if w.URL != "" {
			expMeta["url"] = w.URL
		}
		if w.Location != "" {
			expMeta["location"] = w.Location
		}

		var expFacts []core.Fact
		for _, h := range w.Highlights {
			matched := matcher.findMatchingSkills(h)
			fact := core.Fact{
				ID:        core.GenerateID("fact"),
				ShortID:   core.GenerateShortID("fact"),
				UserID:    userID,
				OwnerID:   expID,
				OwnerType: core.OwnerTypeWorkExperience,
				Statement: h,
				Skills:    matched,
				CreatedAt: time.Now().UTC(),
				UpdatedAt: time.Now().UTC(),
			}
			expFacts = append(expFacts, fact)
			allFacts = append(allFacts, fact)
		}

		// Experience-level skills: union of fact skills + skills mentioned in summary / position
		expSkills := matcher.findMatchingSkills(w.Summary + " " + w.Position + " " + company)
		for _, f := range expFacts {
			expSkills = mergeUniqueSkills(expSkills, f.Skills)
		}

		experiences[i] = core.WorkExperience{
			ID:             expID,
			ShortID:        expShortID,
			UserID:         userID,
			Company:        company,
			RoleTitle:      w.Position,
			EmploymentType: core.EmploymentTypeFullTime,
			StartDate:      parsedStart,
			EndDate:        endDate,
			IsCurrent:      isCurrent,
			Location: core.Location{
				City: w.Location,
			},
			DutiesSummary: w.Summary,
			Facts:         expFacts,
			Skills:        expSkills,
			Metadata:      expMeta,
			CreatedAt:     time.Now().UTC(),
			UpdatedAt:     time.Now().UTC(),
		}
	}

	// 4. Map Projects and child Facts
	projects := make([]core.Project, len(resume.Projects))
	for i, p := range resume.Projects {
		projID := core.GenerateID("proj")
		projShortID := core.GenerateShortID("proj")
		var startDate *core.Date
		if p.StartDate != "" {
			if sd, err := core.ParseDate(p.StartDate); err == nil && !sd.IsZero() {
				startDate = &sd
			}
		}
		var endDate *core.Date
		cleanEnd := strings.TrimSpace(p.EndDate)
		if cleanEnd != "" && !strings.EqualFold(cleanEnd, "present") {
			if ed, err := core.ParseDate(cleanEnd); err == nil && !ed.IsZero() {
				endDate = &ed
			}
		}
		role := ""
		if len(p.Roles) > 0 {
			role = strings.Join(p.Roles, ", ")
		}

		projMeta := make(map[string]any)
		if len(p.Keywords) > 0 {
			projMeta["keywords"] = p.Keywords
		}
		if p.Entity != "" {
			projMeta["entity"] = p.Entity
		}
		if p.Type != "" {
			projMeta["type"] = p.Type
		}
		if len(p.Roles) > 0 {
			projMeta["roles"] = p.Roles
		}

		var projFacts []core.Fact
		for _, h := range p.Highlights {
			matched := matcher.findMatchingSkills(h)
			fact := core.Fact{
				ID:        core.GenerateID("fact"),
				ShortID:   core.GenerateShortID("fact"),
				UserID:    userID,
				OwnerID:   projID,
				OwnerType: core.OwnerTypeProject,
				Statement: h,
				Skills:    matched,
				CreatedAt: time.Now().UTC(),
				UpdatedAt: time.Now().UTC(),
			}
			projFacts = append(projFacts, fact)
			allFacts = append(allFacts, fact)
		}

		projSkills := matcher.findMatchingSkills(p.Description + " " + strings.Join(p.Keywords, " "))
		for _, f := range projFacts {
			projSkills = mergeUniqueSkills(projSkills, f.Skills)
		}

		projects[i] = core.Project{
			ID:        projID,
			ShortID:   projShortID,
			UserID:    userID,
			Name:      p.Name,
			Role:      role,
			URL:       p.URL,
			StartDate: startDate,
			EndDate:   endDate,
			Summary:   p.Description,
			Facts:     projFacts,
			Skills:    projSkills,
			Metadata:  projMeta,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
	}

	// 5. Map Education
	educations := make([]core.Education, len(resume.Education))
	for i, e := range resume.Education {
		eduID := core.GenerateID("edu")
		eduShortID := core.GenerateShortID("edu")
		var startDate *core.Date
		if e.StartDate != "" {
			if sd, err := core.ParseDate(e.StartDate); err == nil && !sd.IsZero() {
				startDate = &sd
			}
		}
		var endDate *core.Date
		cleanEnd := strings.TrimSpace(e.EndDate)
		if cleanEnd != "" && !strings.EqualFold(cleanEnd, "present") {
			if ed, err := core.ParseDate(cleanEnd); err == nil && !ed.IsZero() {
				endDate = &ed
			}
		}
		eduMeta := make(map[string]any)
		if e.URL != "" {
			eduMeta["url"] = e.URL
		}
		if len(e.Courses) > 0 {
			eduMeta["courses"] = e.Courses
		}

		educations[i] = core.Education{
			ID:          eduID,
			ShortID:     eduShortID,
			UserID:      userID,
			Institution: e.Institution,
			Degree:      e.StudyType,
			Field:       e.Area,
			StartDate:   startDate,
			EndDate:     endDate,
			Score:       e.Score,
			Metadata:    eduMeta,
			CreatedAt:   time.Now().UTC(),
			UpdatedAt:   time.Now().UTC(),
		}
	}

	// 6. Map Certifications
	certifications := make([]core.Certification, len(resume.Certificates))
	for i, c := range resume.Certificates {
		certID := core.GenerateID("cert")
		certShortID := core.GenerateShortID("cert")
		var certDate *core.Date
		if c.Date != "" {
			if cd, err := core.ParseDate(c.Date); err == nil && !cd.IsZero() {
				certDate = &cd
			}
		}
		certifications[i] = core.Certification{
			ID:        certID,
			ShortID:   certShortID,
			UserID:    userID,
			Name:      c.Name,
			Issuer:    c.Issuer,
			Date:      certDate,
			URL:       c.URL,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
	}

	return &core.MainProfileSnapshot{
		Profile:        prof,
		MainRevision:   revision,
		Skills:         skills,
		Experiences:    experiences,
		Projects:       projects,
		Education:      educations,
		Certifications: certifications,
		Facts:          allFacts,
		ExportedAt:     time.Now().UTC(),
	}, nil
}

// FromSnapshot converts a MainProfileSnapshot back into a valid JSONResume document,
// restoring unmapped sections and child highlights.
func FromSnapshot(snapshot *core.MainProfileSnapshot) (*JSONResume, error) {
	if snapshot == nil {
		return nil, core.ErrValidation
	}

	schemaURL := defaultSchemaURL

	// 1. Basics
	profiles := make([]JSONResumeProfile, len(snapshot.Profile.Profiles))
	for i, p := range snapshot.Profile.Profiles {
		profiles[i] = JSONResumeProfile(p)
	}

	basics := JSONResumeBasics{
		Name:    snapshot.Profile.FullName,
		Label:   snapshot.Profile.Headline,
		Email:   snapshot.Profile.Email,
		Phone:   snapshot.Profile.Phone,
		Summary: snapshot.Profile.Summary,
		Location: JSONResumeLocation{
			Address:     snapshot.Profile.Location.Address,
			PostalCode:  snapshot.Profile.Location.PostalCode,
			City:        snapshot.Profile.Location.City,
			CountryCode: snapshot.Profile.Location.CountryCode,
			Region:      snapshot.Profile.Location.Region,
		},
		Profiles: profiles,
	}

	if snapshot.Profile.Metadata != nil {
		if img, ok := snapshot.Profile.Metadata["basics_image"].(string); ok {
			basics.Image = img
		}
		if u, ok := snapshot.Profile.Metadata["basics_url"].(string); ok {
			basics.URL = u
		}
	}

	// 2. Work Experiences
	// Index snapshot.Facts by owner ID as fallback if exp.Facts is empty
	expFactsMap := make(map[string][]string)
	projFactsMap := make(map[string][]string)
	for _, f := range snapshot.Facts {
		switch f.OwnerType {
		case core.OwnerTypeWorkExperience:
			expFactsMap[f.OwnerID] = append(expFactsMap[f.OwnerID], f.Statement)
		case core.OwnerTypeProject:
			projFactsMap[f.OwnerID] = append(projFactsMap[f.OwnerID], f.Statement)
		}
	}

	workList := make([]JSONResumeWork, len(snapshot.Experiences))
	for i, exp := range snapshot.Experiences {
		endDate := ""
		if exp.EndDate != nil && !exp.EndDate.IsZero() && !exp.IsCurrent {
			endDate = exp.EndDate.String()
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

		workList[i] = JSONResumeWork{
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

	// 3. Projects
	projList := make([]JSONResumeProject, len(snapshot.Projects))
	for i, proj := range snapshot.Projects {
		startDate := ""
		if proj.StartDate != nil && !proj.StartDate.IsZero() {
			startDate = proj.StartDate.String()
		}
		endDate := ""
		if proj.EndDate != nil && !proj.EndDate.IsZero() {
			endDate = proj.EndDate.String()
		}

		var highlights []string
		if len(proj.Facts) > 0 {
			for _, f := range proj.Facts {
				highlights = append(highlights, f.Statement)
			}
		} else {
			highlights = projFactsMap[proj.ID]
		}

		var roles []string
		var keywords []string
		entity := ""
		pType := ""

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

		projList[i] = JSONResumeProject{
			Name:        proj.Name,
			Description: proj.Summary,
			Highlights:  highlights,
			Keywords:    keywords,
			StartDate:   startDate,
			EndDate:     endDate,
			URL:         proj.URL,
			Roles:       roles,
			Entity:      entity,
			Type:        pType,
		}
	}

	// 4. Education
	eduList := make([]JSONResumeEducation, len(snapshot.Education))
	for i, edu := range snapshot.Education {
		startDate := ""
		if edu.StartDate != nil && !edu.StartDate.IsZero() {
			startDate = edu.StartDate.String()
		}
		endDate := ""
		if edu.EndDate != nil && !edu.EndDate.IsZero() {
			endDate = edu.EndDate.String()
		}
		urlStr := ""
		var courses []string
		if edu.Metadata != nil {
			if u, ok := edu.Metadata["url"].(string); ok {
				urlStr = u
			}
			courses = extractStringSlice(edu.Metadata["courses"])
		}

		eduList[i] = JSONResumeEducation{
			Institution: edu.Institution,
			URL:         urlStr,
			Area:        edu.Field,
			StudyType:   edu.Degree,
			StartDate:   startDate,
			EndDate:     endDate,
			Score:       edu.Score,
			Courses:     courses,
		}
	}

	// 5. Certifications
	certList := make([]JSONResumeCertificate, len(snapshot.Certifications))
	for i, cert := range snapshot.Certifications {
		dateStr := ""
		if cert.Date != nil && !cert.Date.IsZero() {
			dateStr = cert.Date.String()
		}
		certList[i] = JSONResumeCertificate{
			Name:   cert.Name,
			Date:   dateStr,
			Issuer: cert.Issuer,
			URL:    cert.URL,
		}
	}

	// 6. Skills: export all consolidated skills from the snapshot
	allSnapshotSkills := snapshot.AllSkills()
	skillList := make([]JSONResumeSkill, len(allSnapshotSkills))
	for i, skill := range allSnapshotSkills {
		skillList[i] = JSONResumeSkill{
			Name:     skill.Name,
			Level:    skill.Proficiency,
			Keywords: skill.Keywords,
		}
	}

	res := &JSONResume{
		Schema:       schemaURL,
		Basics:       basics,
		Work:         workList,
		Education:    eduList,
		Certificates: certList,
		Skills:       skillList,
		Projects:     projList,
	}

	// 7. Restore unmapped sections from Profile.Metadata["jsonresume_unmapped"]
	if snapshot.Profile.Metadata != nil {
		if rawUnmapped, ok := snapshot.Profile.Metadata["jsonresume_unmapped"]; ok && rawUnmapped != nil {
			data, err := json.Marshal(rawUnmapped)
			if err == nil {
				var aux struct {
					Schema        string                     `json:"$schema"`
					Volunteer     []JSONResumeVolunteer      `json:"volunteer"`
					Awards        []JSONResumeAward          `json:"awards"`
					Publications  []JSONResumePublication    `json:"publications"`
					Languages     []JSONResumeLanguage       `json:"languages"`
					Interests     []JSONResumeInterest       `json:"interests"`
					References    []JSONResumeReference      `json:"references"`
					Meta          *JSONResumeMeta            `json:"meta"`
					ExtraUnmapped map[string]json.RawMessage `json:"extra_unmapped"`
				}
				if err := json.Unmarshal(data, &aux); err == nil {
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
			}
		}
	}

	return res, nil
}

// FromTailored converts a BranchVersion's TailoredContent and base Profile into a valid JSONResume export.
func FromTailored(profile *core.Profile, tailored *core.TailoredContent) (*JSONResume, error) {
	if tailored == nil {
		return nil, core.ErrValidation
	}

	schemaURL := defaultSchemaURL
	var basics JSONResumeBasics

	if profile != nil {
		profiles := make([]JSONResumeProfile, len(profile.Profiles))
		for i, p := range profile.Profiles {
			profiles[i] = JSONResumeProfile(p)
		}

		basics = JSONResumeBasics{
			Name:    profile.FullName,
			Label:   profile.Headline,
			Email:   profile.Email,
			Phone:   profile.Phone,
			Summary: profile.Summary,
			Location: JSONResumeLocation{
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
	}

	// Override summary with tailored summary if present
	if tailored.Summary != "" {
		basics.Summary = tailored.Summary
	}

	// 1. Work from TailoredExperiences
	workList := make([]JSONResumeWork, len(tailored.Experiences))
	for i, exp := range tailored.Experiences {
		endDate := ""
		if exp.EndDate != nil && !exp.EndDate.IsZero() {
			endDate = exp.EndDate.String()
		}
		var highlights []string
		for _, b := range exp.Bullets {
			highlights = append(highlights, b.Text)
		}

		loc := exp.Location.City
		if loc == "" {
			loc = exp.CompanyLocation.City
		}

		workList[i] = JSONResumeWork{
			Name:       exp.Company,
			Company:    exp.Company,
			Position:   exp.RoleTitle,
			StartDate:  exp.StartDate.String(),
			EndDate:    endDate,
			Summary:    exp.DutiesSummary,
			Highlights: highlights,
			Location:   loc,
		}
	}

	// 2. Projects from TailoredProjects
	projList := make([]JSONResumeProject, len(tailored.Projects))
	for i, proj := range tailored.Projects {
		startDate := ""
		if proj.StartDate != nil && !proj.StartDate.IsZero() {
			startDate = proj.StartDate.String()
		}
		endDate := ""
		if proj.EndDate != nil && !proj.EndDate.IsZero() {
			endDate = proj.EndDate.String()
		}
		var highlights []string
		for _, b := range proj.Bullets {
			highlights = append(highlights, b.Text)
		}
		var roles []string
		if proj.Role != "" {
			roles = []string{proj.Role}
		}

		projList[i] = JSONResumeProject{
			Name:        proj.Name,
			Description: proj.Summary,
			Highlights:  highlights,
			StartDate:   startDate,
			EndDate:     endDate,
			URL:         proj.URL,
			Roles:       roles,
		}
	}

	// 3. Education from TailoredEducation
	eduList := make([]JSONResumeEducation, len(tailored.Education))
	for i, edu := range tailored.Education {
		startDate := ""
		if edu.StartDate != nil && !edu.StartDate.IsZero() {
			startDate = edu.StartDate.String()
		}
		endDate := ""
		if edu.EndDate != nil && !edu.EndDate.IsZero() {
			endDate = edu.EndDate.String()
		}
		eduList[i] = JSONResumeEducation{
			Institution: edu.Institution,
			StudyType:   edu.Degree,
			Area:        edu.Field,
			StartDate:   startDate,
			EndDate:     endDate,
			Score:       edu.Score,
		}
	}

	// 4. Certifications from TailoredCert
	certList := make([]JSONResumeCertificate, len(tailored.Certifications))
	for i, cert := range tailored.Certifications {
		dateStr := ""
		if cert.Date != nil && !cert.Date.IsZero() {
			dateStr = cert.Date.String()
		}
		certList[i] = JSONResumeCertificate{
			Name:   cert.Name,
			Issuer: cert.Issuer,
			Date:   dateStr,
			URL:    cert.URL,
		}
	}

	// 5. Skills from TailoredSkillGroups
	skillList := make([]JSONResumeSkill, len(tailored.SkillGroups))
	for i, sg := range tailored.SkillGroups {
		skillList[i] = JSONResumeSkill{
			Name:     sg.Category,
			Keywords: sg.Skills,
		}
	}

	res := &JSONResume{
		Schema:       schemaURL,
		Basics:       basics,
		Work:         workList,
		Education:    eduList,
		Certificates: certList,
		Skills:       skillList,
		Projects:     projList,
	}

	// Restore unmapped sections if profile was provided
	if profile != nil && profile.Metadata != nil {
		if rawUnmapped, ok := profile.Metadata["jsonresume_unmapped"]; ok && rawUnmapped != nil {
			data, err := json.Marshal(rawUnmapped)
			if err == nil {
				var aux struct {
					Schema        string                     `json:"$schema"`
					Volunteer     []JSONResumeVolunteer      `json:"volunteer"`
					Awards        []JSONResumeAward          `json:"awards"`
					Publications  []JSONResumePublication    `json:"publications"`
					Languages     []JSONResumeLanguage       `json:"languages"`
					Interests     []JSONResumeInterest       `json:"interests"`
					References    []JSONResumeReference      `json:"references"`
					Meta          *JSONResumeMeta            `json:"meta"`
					ExtraUnmapped map[string]json.RawMessage `json:"extra_unmapped"`
				}
				if err := json.Unmarshal(data, &aux); err == nil {
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
			}
		}
	}

	return res, nil
}

// Helper types & functions for skill matching

type skillEntry struct {
	skill   core.Skill
	pattern *regexp.Regexp
}

type skillMatcher struct {
	entries []skillEntry
}

// newSkillMatcher compiles regex boundary patterns for skill names and keywords to associate skills with bullet text.
func newSkillMatcher(skills []core.Skill) *skillMatcher {
	var entries []skillEntry
	for _, s := range skills {
		terms := []string{s.Name}
		terms = append(terms, s.Keywords...)

		var validTerms []string
		for _, t := range terms {
			t = strings.TrimSpace(t)
			if t != "" {
				validTerms = append(validTerms, regexp.QuoteMeta(t))
			}
		}

		if len(validTerms) > 0 {
			patternStr := `(?i)\b(?:` + strings.Join(validTerms, "|") + `)\b`
			re, err := regexp.Compile(patternStr)
			if err == nil {
				entries = append(entries, skillEntry{
					skill:   s,
					pattern: re,
				})
			}
		}
	}
	return &skillMatcher{entries: entries}
}

// findMatchingSkills scans text against compiled skill patterns and returns matched unique skills.
func (m *skillMatcher) findMatchingSkills(text string) []core.Skill {
	if m == nil || strings.TrimSpace(text) == "" {
		return nil
	}

	var matched []core.Skill
	seen := make(map[string]bool)

	for _, entry := range m.entries {
		if entry.pattern.MatchString(text) {
			if !seen[entry.skill.ID] {
				seen[entry.skill.ID] = true
				matched = append(matched, entry.skill)
			}
		}
	}
	return matched
}

// mergeUniqueSkills combines two skill slices and removes duplicates by skill ID using core.MergeUniqueSkills.
func mergeUniqueSkills(a, b []core.Skill) []core.Skill {
	return core.MergeUniqueSkills(a, b)
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
