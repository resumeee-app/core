package core

import (
	"context"
	"strings"
	"time"
)

// Built-in Preset and Tailoring Strategy IDs.
const (
	// Preset1PageTechResumeID defines the identifier for the built-in 1-Page Tech Resume preset.
	Preset1PageTechResumeID = "builtin-1-page-tech-resume"

	// PresetImmigrationFullHistoryCVID defines the identifier for the built-in Immigration Full History CV preset.
	PresetImmigrationFullHistoryCVID = "builtin-immigration-full-history-cv"
)

// Preset defines reusable prompt engineering directives, reasoning depth,
// and density rules for LLM resume tailoring.
//
// Invariant: A Preset defines how content is synthesized and prioritized;
// it has zero mandatory coupling to visual templates or CSS layouts.
type Preset struct {
	ID                 string `json:"id"`
	UserID             string `json:"user_id"`
	Name               string `json:"name"` // e.g., "1-Page SWE", "Executive Leadership"
	Description        string `json:"description,omitempty"`
	SystemInstructions string `json:"system_instructions"` // Tier 2 LLM instructions

	// SuggestedThemeID optionally provides a recommended visual layout in creation wizards.
	// Nullable; when nil, the application defaults to the system standard theme.
	SuggestedThemeID *string `json:"suggested_theme_id"`

	// TemplateSetID is an alias retained for backwards compatibility with legacy UI/forms.
	TemplateSetID string `json:"template_set_id,omitempty"`

	Language  string    `json:"language"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// EffectiveLanguage returns the configured target language for tailoring, falling back to DefaultLanguage.
func (p *Preset) EffectiveLanguage() string {
	if p == nil || strings.TrimSpace(p.Language) == "" {
		return DefaultLanguage
	}
	return strings.TrimSpace(p.Language)
}

// Normalize synchronizes language defaults and template/theme aliases.
func (p *Preset) Normalize() error {
	if p == nil {
		return nil
	}
	if p.Language == "" {
		p.Language = DefaultLanguage
	}
	if p.SuggestedThemeID == nil && p.TemplateSetID != "" {
		themeID := p.TemplateSetID
		p.SuggestedThemeID = &themeID
	}
	if p.TemplateSetID == "" && p.SuggestedThemeID != nil {
		p.TemplateSetID = *p.SuggestedThemeID
	}
	return nil
}

// IsBuiltIn returns true if the preset is a system-provided built-in preset.
// Built-in presets are immutable and protected against update or deletion.
func (p Preset) IsBuiltIn() bool {
	return strings.HasPrefix(p.ID, "builtin-") || p.UserID == SystemUserID
}

// TemplateSetName returns an empty string on core.Preset, allowing view models to override it.
func (p Preset) TemplateSetName() string {
	return ""
}

// PresetStore defines persistence operations for Tier-2 reusable instructions.
type PresetStore interface {
	ListPresets(ctx context.Context, userID string) ([]Preset, error)
	Preset(ctx context.Context, userID, id string) (*Preset, error)
	CreatePreset(ctx context.Context, preset *Preset) error
	UpdatePreset(ctx context.Context, preset *Preset) error
	DeletePreset(ctx context.Context, userID, id string) error
}
