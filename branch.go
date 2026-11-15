package core

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// SourceType represents whether a branch version was produced by LLM or manual edit.
type SourceType string

// Supported provenance sources for creating a BranchVersion.
const (
	// SourceTypeLLMGeneration indicates content synthesized by an LLM tailoring pass.
	SourceTypeLLMGeneration SourceType = "llm_generation"

	// SourceTypeManualEdit indicates user-authored edits to tailored content.
	SourceTypeManualEdit SourceType = "manual_edit"

	// SourceTypeMainProfile indicates a deterministic snapshot projection without LLM alteration.
	SourceTypeMainProfile SourceType = "main_profile"
)

// DefaultBranchName is the canonical display name for a user's default untailored branch.
const DefaultBranchName = "Main Profile (Default)"

// DefaultBranchID returns the deterministic branch ID for a user's default untailored branch.
func DefaultBranchID(userID string) string {
	return "branch-default-" + userID
}

// Branch represents a targeted resume variant tailored for a specific opportunity.
type Branch struct {
	ID                     string     `json:"id"`
	UserID                 string     `json:"user_id"`
	Name                   string     `json:"name"` // e.g., "Stripe - Staff Backend Engineer"
	IsDefault              bool       `json:"is_default"`
	TargetRole             string     `json:"target_role,omitempty"`
	TargetCompany          string     `json:"target_company,omitempty"`
	TargetRequirements     string     `json:"target_requirements,omitempty"` // Job description text
	BranchInstructions     string     `json:"branch_instructions,omitempty"` // Tier 3 custom prompt directives
	PresetID               *string    `json:"preset_id,omitempty"`
	TemplateSetID          string     `json:"template_set_id"`
	ProviderOverride       string     `json:"provider_override,omitempty"`
	ModelOverride          string     `json:"model_override,omitempty"`
	ReasoningLevel         string     `json:"reasoning_level,omitempty"`
	PinnedEntityIDs        []string   `json:"pinned_entity_ids,omitempty"`
	ExcludedEntityIDs      []string   `json:"excluded_entity_ids,omitempty"`
	ActiveVersionID        *string    `json:"active_version_id,omitempty"`
	GapReport              *GapReport `json:"gap_report,omitempty"`
	ATSScore               *ATSScore  `json:"ats_score,omitempty"`
	Epoch                  int64      `json:"epoch"`
	LastSyncedMainRevision int64      `json:"last_synced_main_revision"`
	IsStale                bool       `json:"is_stale"` // Computed: LastSyncedMainRevision < Profile.MainRevision
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

// CanTailor returns true if the branch/resume can be tailored via LLM or manual bullet edits.
// Default branches directly render the main profile and cannot be tailored.
func (b *Branch) CanTailor() bool {
	return !b.IsDefault
}

// IsStaleRevision returns true if the last synced main revision is behind the given main revision.
func (b *Branch) IsStaleRevision(currentMainRevision int64) bool {
	return b.LastSyncedMainRevision < currentMainRevision
}

// PresetName returns an empty string on core.Branch, allowing view models to override it.
func (b Branch) PresetName() string {
	return ""
}

// equalOptionalString reports whether two optional string pointers are both nil or point to equal values.
func equalOptionalString(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// GenerationOrActiveChanged reports whether other alters b's active version selection or generation-defining tailoring configuration.
func (b *Branch) GenerationOrActiveChanged(other *Branch) bool {
	if b == nil || other == nil {
		return b != other
	}
	return !equalOptionalString(b.ActiveVersionID, other.ActiveVersionID) ||
		b.TargetRole != other.TargetRole ||
		b.TargetCompany != other.TargetCompany ||
		b.TargetRequirements != other.TargetRequirements ||
		b.BranchInstructions != other.BranchInstructions ||
		!equalOptionalString(b.PresetID, other.PresetID) ||
		b.TemplateSetID != other.TemplateSetID ||
		b.ProviderOverride != other.ProviderOverride ||
		b.ModelOverride != other.ModelOverride ||
		b.ReasoningLevel != other.ReasoningLevel ||
		!slices.Equal(b.PinnedEntityIDs, other.PinnedEntityIDs) ||
		!slices.Equal(b.ExcludedEntityIDs, other.ExcludedEntityIDs)
}

// VersionStatus represents the lifecycle state of a BranchVersion.
type VersionStatus string

// Lifecycle states for an asynchronous or completed BranchVersion.
const (
	// VersionStatusPending indicates the version is queued for generation.
	VersionStatusPending VersionStatus = "pending"

	// VersionStatusProcessing indicates generation is actively running.
	VersionStatusProcessing VersionStatus = "processing"

	// VersionStatusCompleted indicates generation finished cleanly without validation warnings.
	VersionStatusCompleted VersionStatus = "completed"

	// VersionStatusCompletedWithWarnings indicates generation succeeded with recoverable sanitizer warnings.
	VersionStatusCompletedWithWarnings VersionStatus = "completed_with_warnings"

	// VersionStatusFailed indicates generation terminated with an error.
	VersionStatusFailed VersionStatus = "failed"

	// VersionStatusCancelled indicates generation was cancelled before execution.
	VersionStatusCancelled VersionStatus = "cancelled"
)

// IsTerminal reports whether s represents a finished lifecycle state that cannot transition further.
func (s VersionStatus) IsTerminal() bool {
	switch s {
	case VersionStatusCompleted, VersionStatusCompletedWithWarnings, VersionStatusFailed, VersionStatusCancelled:
		return true
	default:
		return false
	}
}

// IsCompleted reports whether s represents a successful generation output state.
func (s VersionStatus) IsCompleted() bool {
	return s == VersionStatusCompleted || s == VersionStatusCompletedWithWarnings
}

// BranchVersion is an immutable snapshot tracking its generation lifecycle and outputs.
type BranchVersion struct {
	ID                 string           `json:"id"`
	UserID             string           `json:"user_id"`
	BranchID           string           `json:"branch_id"`
	JobID              string           `json:"job_id,omitempty"`
	VersionNumber      int              `json:"version_number"`
	BranchEpoch        int64            `json:"branch_epoch,omitempty"`
	Status             VersionStatus    `json:"status"`
	MainRevisionUsed   int64            `json:"main_revision_used"`
	TemplateSetIDUsed  string           `json:"template_set_id_used"`
	StrategyIDUsed     *string          `json:"strategy_id_used,omitempty"`
	ProviderUsed       string           `json:"provider_used"`
	ModelUsed          string           `json:"model_used"`
	SourceType         SourceType       `json:"source_type"` // "llm_generation", "manual_edit", or "main_profile"
	ParentVersionID    *string          `json:"parent_version_id,omitempty"`
	TailoredContent    *TailoredContent `json:"tailored_content,omitempty"`
	GapReport          *GapReport       `json:"gap_report,omitempty"`
	ATSScore           *ATSScore        `json:"ats_score,omitempty"`
	ValidationWarnings []string         `json:"validation_warnings,omitempty"`
	ErrorMessage       string           `json:"error_message,omitempty"`
	HTMLOutput         string           `json:"html_output,omitempty"`
	MarkdownOutput     string           `json:"markdown_output,omitempty"`
	PlainTextOutput    string           `json:"plain_text_output,omitempty"`
	CreatedAt          time.Time        `json:"created_at"`
	UpdatedAt          time.Time        `json:"updated_at"`
	CompletedAt        *time.Time       `json:"completed_at,omitempty"`
}

// PromptPreview wraps compiled system and user prompt strings ready for LLM consumption.
type PromptPreview struct {
	SystemPrompt string `json:"system_prompt"`
	UserPrompt   string `json:"user_prompt"`
}

// PromptWorkflow identifies which LLM workflow a dry-run prompt preview compiles.
type PromptWorkflow string

// Supported prompt workflows for dry-run prompt previews.
const (
	// PromptWorkflowTailoring previews resume tailoring prompts (default).
	PromptWorkflowTailoring PromptWorkflow = "tailoring"
	// PromptWorkflowGapAnalysis previews qualification gap-analysis prompts.
	PromptWorkflowGapAnalysis PromptWorkflow = "gap_analysis"
	// PromptWorkflowATSExtraction previews Stage 1 ATS resume extraction prompts.
	PromptWorkflowATSExtraction PromptWorkflow = "ats_extraction"
	// PromptWorkflowATSMatching previews Stage 2 ATS job matching prompts.
	PromptWorkflowATSMatching PromptWorkflow = "ats_matching"
)

// NormalizePromptWorkflow canonicalizes raw to a supported PromptWorkflow and defaults empty inputs to tailoring.
func NormalizePromptWorkflow(raw PromptWorkflow) (PromptWorkflow, error) {
	normalized := PromptWorkflow(strings.ToLower(strings.TrimSpace(string(raw))))
	switch normalized {
	case "", "tailor", PromptWorkflowTailoring:
		return PromptWorkflowTailoring, nil
	case PromptWorkflowGapAnalysis:
		return PromptWorkflowGapAnalysis, nil
	case PromptWorkflowATSExtraction:
		return PromptWorkflowATSExtraction, nil
	case PromptWorkflowATSMatching:
		return PromptWorkflowATSMatching, nil
	default:
		return "", fmt.Errorf("%w: unsupported prompt workflow %q", ErrValidation, raw)
	}
}

// PromptPreviewOptions encapsulates optional workflow selection and draft modifications when previewing prompt compilation.
type PromptPreviewOptions struct {
	Workflow           PromptWorkflow         `json:"workflow,omitempty"`
	TargetRole         *string                `json:"target_role,omitempty"`
	TargetCompany      *string                `json:"target_company,omitempty"`
	TargetRequirements *string                `json:"target_requirements,omitempty"`
	BranchInstructions *string                `json:"branch_instructions,omitempty"`
	PresetID           *string                `json:"preset_id,omitempty"`
	PinnedEntityIDs    []string               `json:"pinned_entity_ids,omitempty"`
	ExcludedEntityIDs  []string               `json:"excluded_entity_ids,omitempty"`
	VersionID          string                 `json:"version_id,omitempty"`
	Format             string                 `json:"format,omitempty"`
	ResumeText         string                 `json:"resume_text,omitempty"`
	ExtractedCandidate *ATSExtractedCandidate `json:"extracted_candidate,omitempty"`
}
