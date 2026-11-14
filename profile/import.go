package profile

import (
	"regexp"
	"strings"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

// ToSnapshot converts a parsed JSONResume document into a domain MainProfileSnapshot,
// generating relational entities with compact short IDs, child facts with skill associations,
// and preserving unmapped sections.
func ToSnapshot(resume *core.JSONResume, userID string, existingProfile *core.Profile, revision int64) (*core.MainProfileSnapshot, error) {
	return ToSnapshotWithGlobalSkills(resume, userID, existingProfile, revision, nil)
}

// ToSnapshotWithGlobalSkills converts a validated JSON Resume AST into a full MainProfileSnapshot,
// resolving candidate skills against the global skills catalog before creating custom tenant skills.
func ToSnapshotWithGlobalSkills(resume *core.JSONResume, userID string, existingProfile *core.Profile, revision int64, globalSkills []core.Skill) (*core.MainProfileSnapshot, error) {
	if resume == nil {
		return nil, core.ErrValidation
	}
	if userID == "" {
		userID = "default"
	}

	now := time.Now().UTC()
	prof := buildProfileFromResume(resume, userID, existingProfile, revision, now)
	skills := buildSkillsFromResume(resume.Skills, userID, globalSkills, now)
	matcher := newSkillMatcher(skills)

	var allFacts []core.Fact
	experiences, expFacts := buildWorkFromResume(resume.Work, userID, matcher, now)
	allFacts = append(allFacts, expFacts...)
	projects, projFacts := buildProjectsFromResume(resume.Projects, userID, matcher, now)
	allFacts = append(allFacts, projFacts...)

	return &core.MainProfileSnapshot{
		Profile:        prof,
		MainRevision:   revision,
		Skills:         skills,
		Experiences:    experiences,
		Projects:       projects,
		Education:      buildEducationFromResume(resume.Education, userID, now),
		Certifications: buildCertsFromResume(resume.Certificates, userID, now),
		Facts:          allFacts,
		ExportedAt:     now,
	}, nil
}

// collectUnmappedResumeSections preserves non-core JSON Resume sections for lossless round-trip export.
func collectUnmappedResumeSections(resume *core.JSONResume) map[string]any {
	unmapped := make(map[string]any)
	if len(resume.Volunteer) > 0 {
		unmapped["volunteer"] = resume.Volunteer
	}
	if len(resume.Awards) > 0 {
		unmapped["awards"] = resume.Awards
	}
	if len(resume.Publications) > 0 {
		unmapped["publications"] = resume.Publications
	}
	if len(resume.Languages) > 0 {
		unmapped["languages"] = resume.Languages
	}
	if len(resume.Interests) > 0 {
		unmapped["interests"] = resume.Interests
	}
	if len(resume.References) > 0 {
		unmapped["references"] = resume.References
	}
	if resume.Meta != nil {
		unmapped["meta"] = resume.Meta
	}
	if resume.Schema != "" {
		unmapped["$schema"] = resume.Schema
	}
	if len(resume.Unmapped) > 0 {
		unmapped["extra_unmapped"] = resume.Unmapped
	}
	return unmapped
}

// buildProfileFromResume constructs a domain Profile while retaining existing rules and metadata.
func buildProfileFromResume(resume *core.JSONResume, userID string, existingProfile *core.Profile, revision int64, now time.Time) core.Profile {
	profileID := core.GenerateID("prof")
	rules := ""
	metadata := make(map[string]any)

	if existingProfile != nil {
		if existingProfile.ID != "" {
			profileID = existingProfile.ID
		}
		rules = existingProfile.GlobalWritingRules
		for k, v := range existingProfile.Metadata {
			metadata[k] = v
		}
	}

	if resume.Basics.Image != "" {
		metadata["basics_image"] = resume.Basics.Image
	}
	if resume.Basics.URL != "" {
		metadata["basics_url"] = resume.Basics.URL
	}
	if unmapped := collectUnmappedResumeSections(resume); len(unmapped) > 0 {
		metadata["jsonresume_unmapped"] = unmapped
	}

	profiles := make([]core.SocialLink, len(resume.Basics.Profiles))
	for i, p := range resume.Basics.Profiles {
		profiles[i] = core.SocialLink(p)
	}

	return core.Profile{
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
		CreatedAt:          now,
		UpdatedAt:          now,
	}
}

// buildSkillsFromResume normalizes imported JSON Resume skills against globalSkills when matched.
func buildSkillsFromResume(rawSkills []core.JSONResumeSkill, userID string, globalSkills []core.Skill, now time.Time) []core.Skill {
	globalByKey := make(map[string]core.Skill, len(globalSkills)*2)
	for _, gs := range globalSkills {
		globalByKey[strings.ToLower(strings.TrimSpace(gs.Name))] = gs
		if gs.Slug != "" {
			globalByKey[strings.ToLower(gs.Slug)] = gs
		}
	}

	skills := make([]core.Skill, len(rawSkills))
	for i, s := range rawSkills {
		cleanName := strings.TrimSpace(s.Name)
		lookupKey := strings.ToLower(cleanName)
		slug := core.Slugify(cleanName)
		canonicalName := s.Name

		if matched, found := globalByKey[lookupKey]; found {
			canonicalName = matched.Name
			slug = matched.Slug
		} else if matched, found := globalByKey[slug]; found {
			canonicalName = matched.Name
			slug = matched.Slug
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
			CreatedAt:   now,
			UpdatedAt:   now,
		}
	}
	return skills
}

// parseOptionalDate parses a JSON Resume date string into a pointer, returning nil on empty/invalid input.
func parseOptionalDate(raw string) *core.Date {
	if raw == "" {
		return nil
	}
	if d, err := core.ParseDate(raw); err == nil && !d.IsZero() {
		return &d
	}
	return nil
}

// parseEndDate parses an end date string while interpreting empty or "present" as an ongoing interval.
func parseEndDate(raw string) (*core.Date, bool) {
	clean := strings.TrimSpace(raw)
	if clean == "" || strings.EqualFold(clean, "present") {
		return nil, true
	}
	if ed, err := core.ParseDate(clean); err == nil && !ed.IsZero() {
		return &ed, false
	}
	return nil, true
}

// buildFactsForHighlights converts highlight strings into owned Fact entries linked with matched skills.
func buildFactsForHighlights(highlights []string, userID, ownerID string, ownerType core.OwnerType, matcher *skillMatcher, now time.Time) []core.Fact {
	var facts []core.Fact
	for _, h := range highlights {
		facts = append(facts, core.Fact{
			ID:        core.GenerateID("fact"),
			ShortID:   core.GenerateShortID("fact"),
			UserID:    userID,
			OwnerID:   ownerID,
			OwnerType: ownerType,
			Statement: h,
			Skills:    matcher.findMatchingSkills(h),
			CreatedAt: now,
			UpdatedAt: now,
		})
	}
	return facts
}

// buildWorkFromResume maps JSON Resume work entries to domain WorkExperiences and child facts.
func buildWorkFromResume(work []core.JSONResumeWork, userID string, matcher *skillMatcher, now time.Time) ([]core.WorkExperience, []core.Fact) {
	experiences := make([]core.WorkExperience, len(work))
	var allFacts []core.Fact

	for i, w := range work {
		expID := core.GenerateID("exp")
		company := w.CompanyName()
		endDate, isCurrent := parseEndDate(w.EndDate)
		parsedStart, _ := core.ParseDate(w.StartDate)

		expMeta := make(map[string]any)
		if w.URL != "" {
			expMeta["url"] = w.URL
		}
		if w.Location != "" {
			expMeta["location"] = w.Location
		}

		expFacts := buildFactsForHighlights(w.Highlights, userID, expID, core.OwnerTypeWorkExperience, matcher, now)
		allFacts = append(allFacts, expFacts...)

		expSkills := matcher.findMatchingSkills(w.Summary + " " + w.Position + " " + company)
		for _, f := range expFacts {
			expSkills = core.MergeUniqueSkills(expSkills, f.Skills)
		}

		experiences[i] = core.WorkExperience{
			ID:             expID,
			ShortID:        core.GenerateShortID("exp"),
			UserID:         userID,
			Company:        company,
			RoleTitle:      w.Position,
			EmploymentType: core.EmploymentTypeFullTime,
			StartDate:      parsedStart,
			EndDate:        endDate,
			IsCurrent:      isCurrent,
			Location:       core.Location{City: w.Location},
			DutiesSummary:  w.Summary,
			Facts:          expFacts,
			Skills:         expSkills,
			Metadata:       expMeta,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
	}
	return experiences, allFacts
}

// buildProjectsFromResume maps JSON Resume projects into domain Projects and child facts.
func buildProjectsFromResume(rawProjects []core.JSONResumeProject, userID string, matcher *skillMatcher, now time.Time) ([]core.Project, []core.Fact) {
	projects := make([]core.Project, len(rawProjects))
	var allFacts []core.Fact

	for i, p := range rawProjects {
		projID := core.GenerateID("proj")
		endDate, _ := parseEndDate(p.EndDate)
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

		projFacts := buildFactsForHighlights(p.Highlights, userID, projID, core.OwnerTypeProject, matcher, now)
		allFacts = append(allFacts, projFacts...)

		projSkills := matcher.findMatchingSkills(p.Description + " " + strings.Join(p.Keywords, " "))
		for _, f := range projFacts {
			projSkills = core.MergeUniqueSkills(projSkills, f.Skills)
		}

		projects[i] = core.Project{
			ID:        projID,
			ShortID:   core.GenerateShortID("proj"),
			UserID:    userID,
			Name:      p.Name,
			Role:      role,
			URL:       p.URL,
			StartDate: parseOptionalDate(p.StartDate),
			EndDate:   endDate,
			Summary:   p.Description,
			Facts:     projFacts,
			Skills:    projSkills,
			Metadata:  projMeta,
			CreatedAt: now,
			UpdatedAt: now,
		}
	}
	return projects, allFacts
}

// buildEducationFromResume maps JSON Resume education items into domain Education entries.
func buildEducationFromResume(rawEdu []core.JSONResumeEducation, userID string, now time.Time) []core.Education {
	educations := make([]core.Education, len(rawEdu))
	for i, e := range rawEdu {
		endDate, _ := parseEndDate(e.EndDate)
		eduMeta := make(map[string]any)
		if e.URL != "" {
			eduMeta["url"] = e.URL
		}
		if len(e.Courses) > 0 {
			eduMeta["courses"] = e.Courses
		}

		educations[i] = core.Education{
			ID:          core.GenerateID("edu"),
			ShortID:     core.GenerateShortID("edu"),
			UserID:      userID,
			Institution: e.Institution,
			Degree:      e.StudyType,
			Field:       e.Area,
			StartDate:   parseOptionalDate(e.StartDate),
			EndDate:     endDate,
			Score:       e.Score,
			Metadata:    eduMeta,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
	}
	return educations
}

// buildCertsFromResume maps JSON Resume certificate entries into domain Certifications.
func buildCertsFromResume(rawCerts []core.JSONResumeCertificate, userID string, now time.Time) []core.Certification {
	certifications := make([]core.Certification, len(rawCerts))
	for i, c := range rawCerts {
		certifications[i] = core.Certification{
			ID:        core.GenerateID("cert"),
			ShortID:   core.GenerateShortID("cert"),
			UserID:    userID,
			Name:      c.Name,
			Issuer:    c.Issuer,
			Date:      parseOptionalDate(c.Date),
			URL:       c.URL,
			CreatedAt: now,
			UpdatedAt: now,
		}
	}
	return certifications
}

// skillEntry pairs a core.Skill with a precompiled word-boundary regexp over its name and keywords.
type skillEntry struct {
	skill   core.Skill
	pattern *regexp.Regexp
}

// skillMatcher matches unstructured achievement text against normalized candidate skills.
type skillMatcher struct {
	entries []skillEntry
}

// newSkillMatcher compiles regex boundary patterns for skill names and keywords to associate skills with bullet text.
func newSkillMatcher(skills []core.Skill) *skillMatcher {
	var entries []skillEntry
	for _, s := range skills {
		terms := append([]string{s.Name}, s.Keywords...)
		var validTerms []string
		for _, t := range terms {
			t = strings.TrimSpace(t)
			if t != "" {
				validTerms = append(validTerms, regexp.QuoteMeta(t))
			}
		}
		if len(validTerms) > 0 {
			patternStr := `(?i)\b(?:` + strings.Join(validTerms, "|") + `)\b`
			if re, err := regexp.Compile(patternStr); err == nil {
				entries = append(entries, skillEntry{skill: s, pattern: re})
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
		if entry.pattern.MatchString(text) && !seen[entry.skill.ID] {
			seen[entry.skill.ID] = true
			matched = append(matched, entry.skill)
		}
	}
	return matched
}
