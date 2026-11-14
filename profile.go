package core

import (
	"context"
	"time"
)

// ContactType represents the communication medium for a contact detail.
type ContactType string

// Supported contact communication channels for verification records.
const (
	// ContactTypeEmail denotes an electronic mail endpoint.
	ContactTypeEmail ContactType = "Email"

	// ContactTypePhone denotes a telephone endpoint.
	ContactTypePhone ContactType = "Phone"
)

// ContactDetail represents a structured, typed contact endpoint.
type ContactDetail struct {
	Type  ContactType `json:"type"`
	Value string      `json:"value"`
}

// Location captures standard postal and geographical location attributes.
type Location struct {
	Address     string `json:"address,omitempty"`
	PostalCode  string `json:"postal_code,omitempty"`
	City        string `json:"city,omitempty"`
	CountryCode string `json:"country_code,omitempty"`
	Region      string `json:"region,omitempty"`
}

// IsZero reports whether all location attributes are empty.
func (l Location) IsZero() bool {
	return l.Address == "" && l.PostalCode == "" && l.City == "" && l.CountryCode == "" && l.Region == ""
}

// SocialLink represents an external digital presence or portfolio URL.
type SocialLink struct {
	Network  string `json:"network"`
	Username string `json:"username"`
	URL      string `json:"url"`
}

// Profile represents the user's Main Profile root and Tier-1 global LLM configuration.
type Profile struct {
	ID                 string         `json:"id"`
	UserID             string         `json:"user_id"`
	FullName           string         `json:"full_name"`
	Headline           string         `json:"headline"`
	Email              string         `json:"email"`
	Phone              string         `json:"phone"`
	Location           Location       `json:"location"`
	Profiles           []SocialLink   `json:"profiles"`
	Summary            string         `json:"summary"`
	GlobalWritingRules string         `json:"global_writing_rules"` // Tier 1 LLM instructions
	MainRevision       int64          `json:"main_revision"`
	Metadata           map[string]any `json:"metadata,omitempty"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
}

// MainProfileSnapshot is an aggregate capture of the entire career graph at a specific MainRevision.
type MainProfileSnapshot struct {
	Profile        Profile          `json:"profile"`
	MainRevision   int64            `json:"main_revision"`
	Skills         []Skill          `json:"skills"`
	Experiences    []WorkExperience `json:"experiences"`
	Projects       []Project        `json:"projects"`
	Education      []Education      `json:"education"`
	Certifications []Certification  `json:"certifications"`
	Facts          []Fact           `json:"facts"`
	ExportedAt     time.Time        `json:"exported_at"`
}

// ProfileStore defines persistence operations for candidate profile roots, revisions, and snapshots.
type ProfileStore interface {
	Profile(ctx context.Context, userID string) (*Profile, error)
	UpsertProfile(ctx context.Context, profile *Profile) error
	IncrementMainRevision(ctx context.Context, userID string) (int64, error)
	Snapshot(ctx context.Context, userID string) (*MainProfileSnapshot, error)
	ReplaceCareerGraph(ctx context.Context, userID string, snapshot *MainProfileSnapshot) error
}
