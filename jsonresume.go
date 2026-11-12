package core

import "encoding/json"

// JSONResume represents the complete standard JSON Resume v1.0.0 schema.
type JSONResume struct {
	Schema       string                  `json:"$schema,omitempty"`
	Basics       JSONResumeBasics        `json:"basics"`
	Work         []JSONResumeWork        `json:"work,omitempty"`
	Volunteer    []JSONResumeVolunteer   `json:"volunteer,omitempty"`
	Education    []JSONResumeEducation   `json:"education,omitempty"`
	Awards       []JSONResumeAward       `json:"awards,omitempty"`
	Certificates []JSONResumeCertificate `json:"certificates,omitempty"`
	Publications []JSONResumePublication `json:"publications,omitempty"`
	Skills       []JSONResumeSkill       `json:"skills,omitempty"`
	Languages    []JSONResumeLanguage    `json:"languages,omitempty"`
	Interests    []JSONResumeInterest    `json:"interests,omitempty"`
	References   []JSONResumeReference   `json:"references,omitempty"`
	Projects     []JSONResumeProject     `json:"projects,omitempty"`
	Meta         *JSONResumeMeta         `json:"meta,omitempty"`

	// Unmapped captures arbitrary extended root properties (e.g. custom vendor blocks)
	Unmapped map[string]json.RawMessage `json:"-"`
}

// JSONResumeBasics captures personal details, contact info, and digital profiles.
type JSONResumeBasics struct {
	Name     string              `json:"name"`
	Label    string              `json:"label,omitempty"`
	Image    string              `json:"image,omitempty"`
	Email    string              `json:"email,omitempty"`
	Phone    string              `json:"phone,omitempty"`
	URL      string              `json:"url,omitempty"`
	Summary  string              `json:"summary,omitempty"`
	Location JSONResumeLocation  `json:"location,omitempty"`
	Profiles []JSONResumeProfile `json:"profiles,omitempty"`
}

// JSONResumeLocation captures postal address and geographic location attributes.
type JSONResumeLocation struct {
	Address     string `json:"address,omitempty"`
	PostalCode  string `json:"postalCode,omitempty"`
	City        string `json:"city,omitempty"`
	CountryCode string `json:"countryCode,omitempty"`
	Region      string `json:"region,omitempty"`
}

// JSONResumeProfile captures a social profile link (LinkedIn, GitHub, etc.).
type JSONResumeProfile struct {
	Network  string `json:"network,omitempty"`
	Username string `json:"username,omitempty"`
	URL      string `json:"url,omitempty"`
}

// JSONResumeWork captures employment history and bulleted achievements.
type JSONResumeWork struct {
	Name       string   `json:"name,omitempty"`    // Official JSON Resume v1.0.0 property
	Company    string   `json:"company,omitempty"` // Common synonym supported for resilience
	Position   string   `json:"position,omitempty"`
	URL        string   `json:"url,omitempty"`
	StartDate  string   `json:"startDate,omitempty"`
	EndDate    string   `json:"endDate,omitempty"`
	Summary    string   `json:"summary,omitempty"`
	Highlights []string `json:"highlights,omitempty"`
	Location   string   `json:"location,omitempty"`
}

// GetCompany returns Company if populated, or Name as fallback.
func (w JSONResumeWork) GetCompany() string {
	if w.Name != "" {
		return w.Name
	}
	return w.Company
}

// JSONResumeVolunteer captures volunteer work experience.
type JSONResumeVolunteer struct {
	Organization string   `json:"organization,omitempty"`
	Position     string   `json:"position,omitempty"`
	URL          string   `json:"url,omitempty"`
	StartDate    string   `json:"startDate,omitempty"`
	EndDate      string   `json:"endDate,omitempty"`
	Summary      string   `json:"summary,omitempty"`
	Highlights   []string `json:"highlights,omitempty"`
}

// JSONResumeEducation captures degrees, academic institutions, and coursework.
type JSONResumeEducation struct {
	Institution string   `json:"institution,omitempty"`
	URL         string   `json:"url,omitempty"`
	Area        string   `json:"area,omitempty"`
	StudyType   string   `json:"studyType,omitempty"`
	StartDate   string   `json:"startDate,omitempty"`
	EndDate     string   `json:"endDate,omitempty"`
	Score       string   `json:"score,omitempty"`
	Courses     []string `json:"courses,omitempty"`
}

// JSONResumeAward captures awards, honors, and notable distinctions.
type JSONResumeAward struct {
	Title   string `json:"title,omitempty"`
	Date    string `json:"date,omitempty"`
	Awarder string `json:"awarder,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// JSONResumeCertificate captures professional certifications and licenses.
type JSONResumeCertificate struct {
	Name   string `json:"name,omitempty"`
	Date   string `json:"date,omitempty"`
	Issuer string `json:"issuer,omitempty"`
	URL    string `json:"url,omitempty"`
}

// JSONResumePublication captures published books, papers, or articles.
type JSONResumePublication struct {
	Name        string `json:"name,omitempty"`
	Publisher   string `json:"publisher,omitempty"`
	ReleaseDate string `json:"releaseDate,omitempty"`
	URL         string `json:"url,omitempty"`
	Summary     string `json:"summary,omitempty"`
}

// JSONResumeSkill captures professional skills, proficiencies, and keywords.
type JSONResumeSkill struct {
	Name     string   `json:"name,omitempty"`
	Level    string   `json:"level,omitempty"`
	Keywords []string `json:"keywords,omitempty"`
}

// JSONResumeLanguage captures spoken and written language competencies.
type JSONResumeLanguage struct {
	Language string `json:"language,omitempty"`
	Fluency  string `json:"fluency,omitempty"`
}

// JSONResumeInterest captures hobbies and personal interests.
type JSONResumeInterest struct {
	Name     string   `json:"name,omitempty"`
	Keywords []string `json:"keywords,omitempty"`
}

// JSONResumeReference captures professional character references.
type JSONResumeReference struct {
	Name      string `json:"name,omitempty"`
	Reference string `json:"reference,omitempty"`
}

// JSONResumeProject captures personal, academic, or professional projects.
type JSONResumeProject struct {
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	Highlights  []string `json:"highlights,omitempty"`
	Keywords    []string `json:"keywords,omitempty"`
	StartDate   string   `json:"startDate,omitempty"`
	EndDate     string   `json:"endDate,omitempty"`
	URL         string   `json:"url,omitempty"`
	Roles       []string `json:"roles,omitempty"`
	Entity      string   `json:"entity,omitempty"`
	Type        string   `json:"type,omitempty"`
}

// JSONResumeMeta captures metadata about the resume document itself.
type JSONResumeMeta struct {
	Canonical    string `json:"canonical,omitempty"`
	Version      string `json:"version,omitempty"`
	LastModified string `json:"lastModified,omitempty"`
}

// UnmarshalJSON implements custom unmarshaling to capture unmapped top-level properties
// into the Unmapped map without data loss.
func (r *JSONResume) UnmarshalJSON(data []byte) error {
	type Alias JSONResume
	aux := (*Alias)(r)
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}

	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawMap); err != nil {
		return err
	}

	knownKeys := map[string]bool{
		"$schema":      true,
		"basics":       true,
		"work":         true,
		"volunteer":    true,
		"education":    true,
		"awards":       true,
		"certificates": true,
		"publications": true,
		"skills":       true,
		"languages":    true,
		"interests":    true,
		"references":   true,
		"projects":     true,
		"meta":         true,
	}

	unmapped := make(map[string]json.RawMessage)
	for k, v := range rawMap {
		if !knownKeys[k] {
			unmapped[k] = v
		}
	}
	if len(unmapped) > 0 {
		r.Unmapped = unmapped
	}
	return nil
}

// MarshalJSON implements custom marshaling to include any unmapped top-level properties.
func (r JSONResume) MarshalJSON() ([]byte, error) {
	type Alias JSONResume
	data, err := json.Marshal((*Alias)(&r))
	if err != nil {
		return nil, err
	}
	if len(r.Unmapped) == 0 {
		return data, nil
	}

	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawMap); err != nil {
		return nil, err
	}
	for k, v := range r.Unmapped {
		if _, exists := rawMap[k]; !exists {
			rawMap[k] = v
		}
	}
	return json.Marshal(rawMap)
}
