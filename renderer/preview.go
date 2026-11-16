package renderer

import (
	"context"
	"fmt"
	htmltemplate "html/template"

	"github.com/resumeee-app/core"
)

// DefaultSeededProfileName identifies the default placeholder name seeded upon database initialization.
const DefaultSeededProfileName = "Default User"

// ProfileReader defines the narrow profile lookup contract used by preview rendering helpers.
type ProfileReader interface {
	Profile(ctx context.Context, userID string) (*core.Profile, error)
	Snapshot(ctx context.Context, userID string) (*core.MainProfileSnapshot, error)
}

// TemplateReader defines the narrow template lookup contract used by ResolveVersionOutputs.
type TemplateReader interface {
	Template(ctx context.Context, userID, id string) (*core.TemplateSet, error)
}

// TemplateRenderer defines the rendering contract accepted by multi-format preview helpers.
type TemplateRenderer interface {
	Render(ctx context.Context, tmpl *core.TemplateSet, profile *core.Profile, content *core.TailoredContent) (*core.RenderOutput, error)
	RenderFormat(format core.OutputFormat, tmplStr string, data any) (string, error)
}

// HasCustomProfileData returns true if the profile contains non-empty custom user data rather than unconfigured defaults.
func HasCustomProfileData(prof *core.Profile, snap *core.MainProfileSnapshot) bool {
	if prof == nil {
		return false
	}
	if prof.FullName != "" && prof.FullName != DefaultSeededProfileName {
		return true
	}
	if snap != nil && (len(snap.Experiences) > 0 || len(snap.Skills) > 0 || len(snap.Projects) > 0 || len(snap.Education) > 0) {
		return true
	}
	return false
}

// BuildPreviewRenderContext constructs a comprehensive execution context for template preview rendering.
// Prefers actual user profile facts when present, merging with synthetic sample data for empty sections.
func BuildPreviewRenderContext(ctx context.Context, profiles ProfileReader, userID string, tmpl *core.TemplateSet) *RenderContext {
	synth := SyntheticRenderContext()
	if tmpl != nil && tmpl.CSSStyles != "" {
		synth.CSSStyles = htmltemplate.CSS(tmpl.CSSStyles)
	}
	if profiles == nil {
		return synth
	}

	prof, err := profiles.Profile(ctx, userID)
	if err != nil || prof == nil {
		return synth
	}

	snapshot, _ := profiles.Snapshot(ctx, userID)
	if !HasCustomProfileData(prof, snapshot) {
		return synth
	}

	var tailored *core.TailoredContent
	if snapshot != nil {
		tailored = core.SnapshotToTailoredContent(snapshot)
	}
	userCtx := NewRenderContext(tmpl, prof, tailored)
	fillMissingSections(userCtx, synth)
	return userCtx
}

// fillMissingSections backfills empty user sections with synthetic sample entries for rich template previews.
func fillMissingSections(userCtx, synth *RenderContext) {
	if len(userCtx.Experiences) == 0 {
		userCtx.Experiences = synth.Experiences
	}
	if len(userCtx.SkillGroups) == 0 {
		userCtx.SkillGroups = synth.SkillGroups
	}
	if len(userCtx.Projects) == 0 {
		userCtx.Projects = synth.Projects
	}
	if len(userCtx.Education) == 0 {
		userCtx.Education = synth.Education
	}
	if len(userCtx.Certifications) == 0 {
		userCtx.Certifications = synth.Certifications
	}
	if userCtx.Summary == "" {
		userCtx.Summary = synth.Summary
	}
}

// RenderAllFormats compiles and executes all template formats (HTML, Markdown, PlainText) using the provided context.
func RenderAllFormats(r TemplateRenderer, tmpl *core.TemplateSet, renderCtx *RenderContext) (core.RenderOutput, error) {
	if tmpl == nil || r == nil {
		return core.RenderOutput{}, nil
	}

	var out core.RenderOutput
	if tmpl.HTMLTemplate != "" {
		htmlOut, err := r.RenderFormat(core.OutputFormatHTML, tmpl.HTMLTemplate, renderCtx)
		if err != nil {
			return core.RenderOutput{}, fmt.Errorf("html template rendering failed: %w", err)
		}
		out.HTML = StripScriptTags(htmlOut)
	}

	if tmpl.MarkdownTemplate != "" {
		mdOut, err := r.RenderFormat(core.OutputFormatMarkdown, tmpl.MarkdownTemplate, renderCtx)
		if err != nil {
			return core.RenderOutput{}, fmt.Errorf("markdown template rendering failed: %w", err)
		}
		out.Markdown = mdOut
	}

	if tmpl.TextTemplate != "" {
		txtOut, err := r.RenderFormat(core.OutputFormatPlainText, tmpl.TextTemplate, renderCtx)
		if err != nil {
			return core.RenderOutput{}, fmt.Errorf("text template rendering failed: %w", err)
		}
		out.PlainText = txtOut
	}

	return out, nil
}

// ResolveVersionOutputs resolves or dynamically re-renders resume outputs incorporating targetTemplateID.
func ResolveVersionOutputs(
	ctx context.Context,
	r TemplateRenderer,
	templates TemplateReader,
	profiles ProfileReader,
	userID string,
	b *core.Branch,
	ver *core.BranchVersion,
	targetTemplateID string,
) core.RenderOutput {
	if ver == nil {
		return core.RenderOutput{}
	}
	out := core.RenderOutput{
		HTML:      ver.HTMLOutput,
		Markdown:  ver.MarkdownOutput,
		PlainText: ver.PlainTextOutput,
	}

	resolvedTemplateID := resolveTargetTemplateID(b, ver, targetTemplateID)
	isDifferentTemplate := resolvedTemplateID != "" && resolvedTemplateID != ver.TemplateSetIDUsed
	missingOutputs := out.HTML == "" || out.Markdown == "" || out.PlainText == ""
	if (!isDifferentTemplate && !missingOutputs) || r == nil || templates == nil || resolvedTemplateID == "" {
		return out
	}

	tmpl, err := templates.Template(ctx, userID, resolvedTemplateID)
	if err != nil || tmpl == nil {
		return out
	}

	var prof *core.Profile
	if profiles != nil {
		prof, _ = profiles.Profile(ctx, userID)
	}
	if ver.TailoredContent != nil {
		if rendered, rErr := r.Render(ctx, tmpl, prof, ver.TailoredContent); rErr == nil && rendered != nil {
			return *rendered
		}
		return out
	}
	return renderMissingFormats(r, tmpl, NewRenderContext(tmpl, prof, nil), out)
}

// resolveTargetTemplateID chooses the effective template ID from explicit request, branch, and version defaults.
func resolveTargetTemplateID(b *core.Branch, ver *core.BranchVersion, targetTemplateID string) string {
	if targetTemplateID != "" {
		return targetTemplateID
	}
	if b != nil && b.TemplateSetID != "" {
		return b.TemplateSetID
	}
	return ver.TemplateSetIDUsed
}

// renderMissingFormats renders any missing HTML, Markdown, or plain-text output formats individually.
func renderMissingFormats(r TemplateRenderer, tmpl *core.TemplateSet, renderCtx *RenderContext, out core.RenderOutput) core.RenderOutput {
	if out.HTML == "" && tmpl.HTMLTemplate != "" {
		if hOut, err := r.RenderFormat(core.OutputFormatHTML, tmpl.HTMLTemplate, renderCtx); err == nil {
			out.HTML = hOut
		}
	}
	if out.Markdown == "" && tmpl.MarkdownTemplate != "" {
		if mOut, err := r.RenderFormat(core.OutputFormatMarkdown, tmpl.MarkdownTemplate, renderCtx); err == nil {
			out.Markdown = mOut
		}
	}
	if out.PlainText == "" && tmpl.TextTemplate != "" {
		if tOut, err := r.RenderFormat(core.OutputFormatPlainText, tmpl.TextTemplate, renderCtx); err == nil {
			out.PlainText = tOut
		}
	}
	return out
}

// Compile-time check ensuring *Renderer implements TemplateRenderer.
var _ TemplateRenderer = (*Renderer)(nil)
