package llm

import (
	"github.com/resumeee-app/resumeee-core"
)

// LeanProfile represents a sanitized profile header containing only candidate headline and professional summary.
// It strips internal tenant IDs, configuration parameters, database timestamps, and non-tailoring PII (contact details, name, social links, location).
type LeanProfile struct {
	Headline string `json:"headline,omitempty"`
	Summary  string `json:"summary,omitempty"`
}

// LeanSkill captures skill competency without internal metadata or tenant ownership fields.
type LeanSkill struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Category        string   `json:"category,omitempty"`
	Proficiency     string   `json:"proficiency,omitempty"`
	YearsExperience float64  `json:"years_experience,omitempty"`
	Keywords        []string `json:"keywords,omitempty"`
}

// LeanFact captures verifiable achievements with short IDs for provenance grounding.
type LeanFact struct {
	ID        string      `json:"id"`
	OwnerID   string      `json:"owner_id,omitempty"`
	OwnerType string      `json:"owner_type,omitempty"`
	Statement string      `json:"statement"`
	Impact    string      `json:"impact,omitempty"`
	Skills    []LeanSkill `json:"skills,omitempty"`
}

// LeanExperience captures professional work history without internal timestamps, tenant attributes, or non-tailoring administrative metadata.
type LeanExperience struct {
	ID             string                `json:"id"`
	Company        string                `json:"company"`
	RoleTitle      string                `json:"role_title"`
	EmploymentType core.EmploymentType `json:"employment_type,omitempty"`
	StartDate      core.Date           `json:"start_date"`
	EndDate        *core.Date          `json:"end_date,omitempty"`
	IsCurrent      bool                  `json:"is_current,omitempty"`
	DutiesSummary  string                `json:"duties_summary,omitempty"`
	Facts          []LeanFact            `json:"facts,omitempty"`
	Skills         []LeanSkill           `json:"skills,omitempty"`
}

// LeanProject captures project deliverables without database metadata or tenant IDs.
type LeanProject struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Role      string       `json:"role,omitempty"`
	URL       string       `json:"url,omitempty"`
	StartDate *core.Date `json:"start_date,omitempty"`
	EndDate   *core.Date `json:"end_date,omitempty"`
	Summary   string       `json:"summary,omitempty"`
	Facts     []LeanFact   `json:"facts,omitempty"`
	Skills    []LeanSkill  `json:"skills,omitempty"`
}

// LeanEducation captures academic history without internal IDs, timestamps, or location metadata.
type LeanEducation struct {
	ID          string       `json:"id"`
	Institution string       `json:"institution"`
	Degree      string       `json:"degree,omitempty"`
	Field       string       `json:"field,omitempty"`
	StartDate   *core.Date `json:"start_date,omitempty"`
	EndDate     *core.Date `json:"end_date,omitempty"`
	Score       string       `json:"score,omitempty"`
	Facts       []LeanFact   `json:"facts,omitempty"`
}

// LeanCertification captures credentials with short identifier for provenance.
type LeanCertification struct {
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	Issuer  string       `json:"issuer,omitempty"`
	Date    *core.Date `json:"date,omitempty"`
	URL     string       `json:"url,omitempty"`
	Summary string       `json:"summary,omitempty"`
}

// LeanMainProfile represents the projected, minimal main profile structure for LLM prompts.
// It excludes internal database fields (user_id, created_at, updated_at, is_global, metadata,
// main_revision, exported_at) while retaining effective short IDs
// for provenance validation and grounding.
type LeanMainProfile struct {
	Profile        LeanProfile         `json:"profile"`
	Skills         []LeanSkill         `json:"skills,omitempty"`
	Experiences    []LeanExperience    `json:"experiences,omitempty"`
	Projects       []LeanProject       `json:"projects,omitempty"`
	Education      []LeanEducation     `json:"education,omitempty"`
	Certifications []LeanCertification `json:"certifications,omitempty"`
	Facts          []LeanFact          `json:"facts,omitempty"`
}

// projectLeanSkills converts a slice of core.Skill entities into LeanSkill DTOs with short IDs.
func projectLeanSkills(skills []core.Skill) []LeanSkill {
	if len(skills) == 0 {
		return nil
	}
	res := make([]LeanSkill, len(skills))
	for i, s := range skills {
		res[i] = LeanSkill{
			ID:              s.EffectiveShortID(),
			Name:            s.Name,
			Category:        s.Category,
			Proficiency:     s.Proficiency,
			YearsExperience: s.YearsExperience,
			Keywords:        s.Keywords,
		}
	}
	return res
}

// projectLeanFact converts a single core.Fact into a LeanFact DTO with a specified owner short ID.
func projectLeanFact(f core.Fact, ownerID string) LeanFact {
	var ownerType string
	if f.OwnerType != "" {
		ownerType = string(f.OwnerType)
	}
	return LeanFact{
		ID:        f.EffectiveShortID(),
		OwnerID:   ownerID,
		OwnerType: ownerType,
		Statement: f.Statement,
		Impact:    f.Impact,
		Skills:    projectLeanSkills(f.Skills),
	}
}

// projectLeanFacts converts a slice of core.Fact entities into LeanFact DTOs with short IDs.
func projectLeanFacts(facts []core.Fact, ownerShortID string) []LeanFact {
	if len(facts) == 0 {
		return nil
	}
	res := make([]LeanFact, len(facts))
	for i, f := range facts {
		ownerID := ownerShortID
		if ownerID == "" {
			ownerID = f.OwnerID
		}
		res[i] = projectLeanFact(f, ownerID)
	}
	return res
}

// ProjectLeanMainProfile clones and transforms a MainProfileSnapshot so that all entities
// and facts are projected into a minimal, lean data transfer object. All internal multi-tenancy
// (user_id), database audit timestamps (created_at, updated_at), database flags (is_global, slug),
// snapshot tracking (main_revision, exported_at), and arbitrary metadata maps are stripped,
// while compact short identifiers are retained under "id" for provenance citations.
//
// Parameters:
//   - snap: Authoritative MainProfileSnapshot to project.
//   - idMap: Map associating canonical UUIDs with compact short IDs.
//
// Returns:
//   - A new LeanMainProfile instance, or nil if snap is nil.
func ProjectLeanMainProfile(snap *core.MainProfileSnapshot, idMap map[string]string) *LeanMainProfile {
	if snap == nil {
		return nil
	}

	lean := &LeanMainProfile{
		Profile: LeanProfile{
			Headline: snap.Profile.Headline,
			Summary:  snap.Profile.Summary,
		},
	}

	if len(snap.Skills) > 0 {
		lean.Skills = projectLeanSkills(snap.Skills)
	}

	if len(snap.Experiences) > 0 {
		exps := make([]LeanExperience, len(snap.Experiences))
		for i, exp := range snap.Experiences {
			shortID := exp.EffectiveShortID()
			exps[i] = LeanExperience{
				ID:             shortID,
				Company:        exp.Company,
				RoleTitle:      exp.RoleTitle,
				EmploymentType: exp.EmploymentType,
				StartDate:      exp.StartDate,
				EndDate:        exp.EndDate,
				IsCurrent:      exp.IsCurrent,
				DutiesSummary:  exp.DutiesSummary,
				Facts:          projectLeanFacts(exp.Facts, shortID),
				Skills:         projectLeanSkills(exp.Skills),
			}
		}
		lean.Experiences = exps
	}

	if len(snap.Projects) > 0 {
		projs := make([]LeanProject, len(snap.Projects))
		for i, proj := range snap.Projects {
			shortID := proj.EffectiveShortID()
			projs[i] = LeanProject{
				ID:        shortID,
				Name:      proj.Name,
				Role:      proj.Role,
				URL:       proj.URL,
				StartDate: proj.StartDate,
				EndDate:   proj.EndDate,
				Summary:   proj.Summary,
				Facts:     projectLeanFacts(proj.Facts, shortID),
				Skills:    projectLeanSkills(proj.Skills),
			}
		}
		lean.Projects = projs
	}

	if len(snap.Education) > 0 {
		edus := make([]LeanEducation, len(snap.Education))
		for i, edu := range snap.Education {
			shortID := edu.EffectiveShortID()
			edus[i] = LeanEducation{
				ID:          shortID,
				Institution: edu.Institution,
				Degree:      edu.Degree,
				Field:       edu.Field,
				StartDate:   edu.StartDate,
				EndDate:     edu.EndDate,
				Score:       edu.Score,
				Facts:       projectLeanFacts(edu.Facts, shortID),
			}
		}
		lean.Education = edus
	}

	if len(snap.Certifications) > 0 {
		certs := make([]LeanCertification, len(snap.Certifications))
		for i, cert := range snap.Certifications {
			certs[i] = LeanCertification{
				ID:      cert.EffectiveShortID(),
				Name:    cert.Name,
				Issuer:  cert.Issuer,
				Date:    cert.Date,
				URL:     cert.URL,
				Summary: cert.Summary,
			}
		}
		lean.Certifications = certs
	}

	if len(snap.Facts) > 0 {
		facts := make([]LeanFact, len(snap.Facts))
		for i, f := range snap.Facts {
			ownerShort := resolveShortID(f.OwnerID, idMap)
			facts[i] = projectLeanFact(f, ownerShort)
		}
		lean.Facts = facts
	}

	return lean
}
