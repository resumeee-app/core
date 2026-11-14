package branch

import (
	"context"
	"fmt"

	"github.com/resumeee-app/resumeee-core"
)

// PromptPreview compiles the system and user prompt templates against the branch's candidate profile,
// rules hierarchy, and target requirements without dispatching any request to an LLM provider.
func (s *Service) PromptPreview(ctx context.Context, userID, branchID string, opts *core.PromptPreviewOptions) (*core.PromptPreview, error) {
	branch, err := s.branches.Branch(ctx, userID, branchID)
	if err != nil {
		return nil, err
	}
	snapshot, err := s.profiles.Snapshot(ctx, userID)
	if err != nil {
		return nil, err
	}
	prof, err := s.profiles.Profile(ctx, userID)
	if err != nil {
		return nil, err
	}

	b := *branch
	applyPromptPreviewOverrides(&b, opts)
	preset, presetInstructions, priorContent := s.loadPreviewPresetAndPrior(ctx, userID, &b)

	req := core.GenerationRequest{
		Model:              b.ModelOverride,
		ReasoningLevel:     b.ReasoningLevel,
		MainProfile:        snapshot,
		GlobalRules:        prof.GlobalWritingRules,
		PresetInstructions: presetInstructions,
		Preset:             preset,
		TargetLanguage:     preset.EffectiveLanguage(),
		BranchContext:      b,
		PriorVersion:       priorContent,
	}

	if s.promptCompiler == nil {
		return nil, fmt.Errorf("%w: prompt compiler not configured", core.ErrValidation)
	}

	preview, err := s.promptCompiler.CompilePrompt(req)
	if err != nil {
		return nil, fmt.Errorf("failed to compile prompt preview: %w", err)
	}
	return preview, nil
}

// applyPromptPreviewOverrides applies optional unsaved UI draft overrides to b.
func applyPromptPreviewOverrides(b *core.Branch, opts *core.PromptPreviewOptions) {
	if opts == nil {
		return
	}
	if opts.TargetRole != nil {
		b.TargetRole = *opts.TargetRole
	}
	if opts.TargetCompany != nil {
		b.TargetCompany = *opts.TargetCompany
	}
	if opts.TargetRequirements != nil {
		b.TargetRequirements = *opts.TargetRequirements
	}
	if opts.BranchInstructions != nil {
		b.BranchInstructions = *opts.BranchInstructions
	}
	if opts.PresetID != nil {
		b.PresetID = opts.PresetID
	}
	if opts.PinnedEntityIDs != nil {
		b.PinnedEntityIDs = opts.PinnedEntityIDs
	}
	if opts.ExcludedEntityIDs != nil {
		b.ExcludedEntityIDs = opts.ExcludedEntityIDs
	}
}

// loadPreviewPresetAndPrior fetches optional preset instructions and active version content for preview compilation.
func (s *Service) loadPreviewPresetAndPrior(ctx context.Context, userID string, b *core.Branch) (*core.Preset, string, *core.TailoredContent) {
	var preset *core.Preset
	var presetInstructions string
	if b.PresetID != nil && *b.PresetID != "" && s.presets != nil {
		p, err := s.presets.Preset(ctx, userID, *b.PresetID)
		if err == nil && p != nil {
			preset = p
			presetInstructions = p.SystemInstructions
		} else if err != nil && s.logger != nil {
			s.logger.Warn("failed to load preset for prompt preview", "branch_id", b.ID, "preset_id", *b.PresetID, "err", err)
		}
	}

	var priorContent *core.TailoredContent
	if b.ActiveVersionID != nil && *b.ActiveVersionID != "" {
		activeVer, err := s.branches.Version(ctx, b.ID, *b.ActiveVersionID)
		if err == nil && activeVer != nil {
			priorContent = activeVer.TailoredContent
		} else if err != nil && s.logger != nil {
			s.logger.Warn("failed to load active version for prompt preview", "branch_id", b.ID, "version_id", *b.ActiveVersionID, "err", err)
		}
	}
	return preset, presetInstructions, priorContent
}
