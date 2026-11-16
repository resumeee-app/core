package branch

import (
	"context"
	"fmt"
	"strings"

	"github.com/resumeee-app/core"
)

// PromptPreview compiles the system and user prompt templates for the requested workflow against
// the branch's candidate profile, rules hierarchy, and target requirements without invoking an LLM.
func (s *Service) PromptPreview(ctx context.Context, userID, branchID string, opts *core.PromptPreviewOptions) (*core.PromptPreview, error) {
	if err := requireUserID(userID); err != nil {
		return nil, err
	}
	workflow, err := previewWorkflow(opts)
	if err != nil {
		return nil, err
	}
	if s.promptCompiler == nil {
		return nil, fmt.Errorf("%w: prompt compiler not configured", core.ErrValidation)
	}

	branch, err := s.branches.Branch(ctx, userID, branchID)
	if err != nil {
		return nil, err
	}
	if branch.UserID != userID {
		return nil, core.ErrNotFound
	}
	snapshot, err := s.profiles.Snapshot(ctx, userID)
	if err != nil {
		return nil, err
	}

	b := *branch
	applyPromptPreviewOverrides(&b, opts)

	var preview *core.PromptPreview
	switch workflow {
	case core.PromptWorkflowTailoring:
		preview, err = s.compileTailoringPreview(ctx, userID, &b, snapshot, opts)
	case core.PromptWorkflowGapAnalysis:
		preview, err = s.promptCompiler.CompileGapAnalysisPrompt(core.GenerationRequest{
			Model:          b.ModelOverride,
			ReasoningLevel: b.ReasoningLevel,
			MainProfile:    snapshot,
			BranchContext:  b,
		})
	case core.PromptWorkflowATSExtraction:
		preview, err = s.compileATSExtractionPreview(ctx, userID, &b, snapshot, opts)
	case core.PromptWorkflowATSMatching:
		var extracted *core.ATSExtractedCandidate
		if opts != nil {
			extracted = opts.ExtractedCandidate
		}
		preview, err = s.promptCompiler.CompileATSMatchingPrompt(b, snapshot, extracted)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to compile prompt preview: %w", err)
	}
	return preview, nil
}

// previewWorkflow normalizes the requested preview workflow, defaulting nil or empty options to tailoring.
func previewWorkflow(opts *core.PromptPreviewOptions) (core.PromptWorkflow, error) {
	if opts == nil {
		return core.PromptWorkflowTailoring, nil
	}
	return core.NormalizePromptWorkflow(opts.Workflow)
}

// compileTailoringPreview loads user writing rules, preset instructions, and prior tailored content
// before compiling a tailoring prompt preview.
func (s *Service) compileTailoringPreview(ctx context.Context, userID string, b *core.Branch, snapshot *core.MainProfileSnapshot, opts *core.PromptPreviewOptions) (*core.PromptPreview, error) {
	prof, err := s.profiles.Profile(ctx, userID)
	if err != nil {
		return nil, err
	}
	preset, presetInstructions, priorContent, err := s.loadPreviewPresetAndPrior(ctx, userID, b, opts)
	if err != nil {
		return nil, err
	}
	return s.promptCompiler.CompilePrompt(core.GenerationRequest{
		Model:              b.ModelOverride,
		ReasoningLevel:     b.ReasoningLevel,
		MainProfile:        snapshot,
		GlobalRules:        prof.GlobalWritingRules,
		PresetInstructions: presetInstructions,
		Preset:             preset,
		TargetLanguage:     preset.EffectiveLanguage(),
		BranchContext:      *b,
		PriorVersion:       priorContent,
	})
}

// compileATSExtractionPreview gathers candidate snapshot and resume text inputs and compiles Stage 1 ATS extraction prompts.
func (s *Service) compileATSExtractionPreview(ctx context.Context, userID string, b *core.Branch, snapshot *core.MainProfileSnapshot, opts *core.PromptPreviewOptions) (*core.PromptPreview, error) {
	resumeText, formatUsed, err := s.resolvePreviewResumeText(ctx, userID, b, snapshot, opts)
	if err != nil {
		return nil, err
	}
	return s.promptCompiler.CompileATSExtractionPrompt(core.ATSSimulationRequest{
		Model:          b.ModelOverride,
		ReasoningLevel: b.ReasoningLevel,
		ResumeText:     resumeText,
		FormatUsed:     formatUsed,
		BranchContext:  *b,
		MainProfile:    snapshot,
	})
}

// resolvePreviewResumeText obtains rendered or draft resume text for dry-run ATS extraction previews.
func (s *Service) resolvePreviewResumeText(ctx context.Context, userID string, b *core.Branch, snapshot *core.MainProfileSnapshot, opts *core.PromptPreviewOptions) (string, string, error) {
	var requestedFormat, explicitVersionID, draftResumeText string
	if opts != nil {
		requestedFormat = opts.Format
		explicitVersionID = opts.VersionID
		draftResumeText = opts.ResumeText
	}

	formatUsed, err := core.NormalizeATSFormat(requestedFormat)
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(draftResumeText) != "" {
		return draftResumeText, formatUsed, nil
	}

	ver, err := s.resolveCompletedVersion(
		ctx,
		b,
		explicitVersionID,
		"branch has no completed version or draft resume text to preview",
		"ATS preview requires a completed version",
	)
	if err != nil {
		return "", "", err
	}

	return s.resolveVersionResumeText(ctx, userID, b, ver, snapshot, formatUsed)
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
func (s *Service) loadPreviewPresetAndPrior(ctx context.Context, userID string, b *core.Branch, opts *core.PromptPreviewOptions) (*core.Preset, string, *core.TailoredContent, error) {
	explicitPreset := opts != nil && opts.PresetID != nil && strings.TrimSpace(*opts.PresetID) != ""
	var preset *core.Preset
	var presetInstructions string
	if b.PresetID != nil && *b.PresetID != "" && s.presets != nil {
		p, err := s.presets.Preset(ctx, userID, *b.PresetID)
		if err != nil {
			if explicitPreset {
				return nil, "", nil, fmt.Errorf("failed to load preset %s: %w", *b.PresetID, err)
			}
			if s.logger != nil {
				s.logger.Warn("failed to load preset for prompt preview", "branch_id", b.ID, "preset_id", *b.PresetID, "err", err)
			}
		} else if p != nil {
			if p.UserID != userID && !p.IsBuiltIn() {
				return nil, "", nil, fmt.Errorf("%w: preset %s belongs to another tenant", core.ErrForbidden, *b.PresetID)
			}
			preset = p
			presetInstructions = p.SystemInstructions
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
	return preset, presetInstructions, priorContent, nil
}
