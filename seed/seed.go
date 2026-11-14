// Package seed provides embedded built-in resume templates, presets, global skills,
// and idempotent database and tenant provisioning routines.
package seed

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

// ProfileSeeder defines the narrow profile persistence contract required by seed routines.
type ProfileSeeder interface {
	Profile(ctx context.Context, userID string) (*core.Profile, error)
	UpsertProfile(ctx context.Context, profile *core.Profile) error
	Snapshot(ctx context.Context, userID string) (*core.MainProfileSnapshot, error)
}

// SkillSeeder defines the narrow global skill persistence contract required by seed routines.
type SkillSeeder interface {
	Skill(ctx context.Context, userID, id string) (*core.Skill, error)
	CreateSkill(ctx context.Context, skill *core.Skill) error
}

// BranchSeeder defines the narrow branch and version persistence contract required by seed routines.
type BranchSeeder interface {
	Branch(ctx context.Context, userID, id string) (*core.Branch, error)
	CreateBranch(ctx context.Context, branch *core.Branch) error
	UpdateBranch(ctx context.Context, branch *core.Branch) error
	CreateVersion(ctx context.Context, version *core.BranchVersion) error
}

// TemplateSeeder defines the narrow template persistence contract required by seed routines.
type TemplateSeeder interface {
	Template(ctx context.Context, userID, id string) (*core.TemplateSet, error)
	CreateTemplate(ctx context.Context, tmpl *core.TemplateSet) error
}

// PresetSeeder defines the narrow preset persistence contract required by seed routines.
type PresetSeeder interface {
	Preset(ctx context.Context, userID, id string) (*core.Preset, error)
	CreatePreset(ctx context.Context, preset *core.Preset) error
}

// UserSeeder defines the narrow user persistence contract required by seed routines.
type UserSeeder interface {
	User(ctx context.Context, id string) (*core.User, error)
	UpsertUser(ctx context.Context, user *core.User) error
}

// tenantStripeCount defines the number of striped mutexes serializing per-tenant provisioning.
const tenantStripeCount = 64

// tenantProvisionStripes serializes concurrent onboarding for the same tenant without a global lock.
var tenantProvisionStripes [tenantStripeCount]sync.Mutex

// tenantProvisionLock hashes userID to a fixed mutex stripe to synchronize lazy tenant provisioning.
func tenantProvisionLock(userID string) *sync.Mutex {
	var h uint32 = 2166136261 // FNV-1a 32-bit offset basis
	for i := 0; i < len(userID); i++ {
		h ^= uint32(userID[i])
		h *= 16777619 // FNV-1a 32-bit prime
	}
	return &tenantProvisionStripes[h%tenantStripeCount]
}

// DefaultGlobalSkills returns the canonical, system-seeded global skills available to all tenants.
func DefaultGlobalSkills() []core.Skill {
	now := time.Now().UTC()
	canonical := []struct {
		Name     string
		Slug     string
		Category string
	}{
		// Languages
		{"Go", "go", "Languages"},
		{"Python", "python", "Languages"},
		{"TypeScript", "typescript", "Languages"},
		{"JavaScript", "javascript", "Languages"},
		{"Rust", "rust", "Languages"},
		{"Java", "java", "Languages"},
		{"C++", "c-plus-plus", "Languages"},
		{"SQL", "sql", "Languages"},
		// Cloud & Infrastructure
		{"Docker", "docker", "Cloud & Infrastructure"},
		{"Kubernetes", "kubernetes", "Cloud & Infrastructure"},
		{"AWS", "aws", "Cloud & Infrastructure"},
		{"Google Cloud", "google-cloud", "Cloud & Infrastructure"},
		{"Terraform", "terraform", "Cloud & Infrastructure"},
		{"Linux", "linux", "Cloud & Infrastructure"},
		// Databases
		{"PostgreSQL", "postgresql", "Databases"},
		{"MySQL", "mysql", "Databases"},
		{"SQLite", "sqlite", "Databases"},
		{"Redis", "redis", "Databases"},
		{"MongoDB", "mongodb", "Databases"},
		// Web & Frameworks
		{"React", "react", "Web Technologies"},
		{"Next.js", "nextjs", "Web Technologies"},
		{"Node.js", "nodejs", "Web Technologies"},
		{"GraphQL", "graphql", "Web Technologies"},
		{"REST APIs", "rest-apis", "Web Technologies"},
		// AI & Machine Learning
		{"PyTorch", "pytorch", "AI & Machine Learning"},
		{"TensorFlow", "tensorflow", "AI & Machine Learning"},
		{"LLM Integration", "llm-integration", "AI & Machine Learning"},
		{"LangChain", "langchain", "AI & Machine Learning"},
	}

	skills := make([]core.Skill, len(canonical))
	for i, c := range canonical {
		skills[i] = core.Skill{
			ID:          "global-skill-" + c.Slug,
			ShortID:     "g-" + c.Slug,
			UserID:      core.SystemUserID,
			Name:        c.Name,
			Slug:        c.Slug,
			IsGlobal:    true,
			Category:    c.Category,
			Proficiency: "Proficient",
			CreatedAt:   now,
			UpdatedAt:   now,
		}
	}
	return skills
}

// DefaultPresets returns the system default built-in presets customized for the specified user.
func DefaultPresets(userID string) []core.Preset {
	if userID == "" {
		userID = core.SystemUserID
	}
	now := time.Now().UTC()
	themeStd := core.TemplateStandardCleanID
	themeCV := core.TemplateDetailedImmigrationCVID
	return []core.Preset{
		{
			ID:                 core.Preset1PageTechResumeID,
			UserID:             userID,
			Name:               "1-Page Tech Resume",
			Description:        "Concise tech resume format emphasizing recent high-impact metrics and key skills.",
			SystemInstructions: "Focus strictly on high-impact engineering accomplishments, quantitative outcomes, and relevant technical skill groups. Ensure concise bullet formatting suitable for a 1-page tech resume.",
			SuggestedThemeID:   &themeStd,
			TemplateSetID:      core.TemplateStandardCleanID,
			Language:           core.DefaultLanguage,
			CreatedAt:          now,
			UpdatedAt:          now,
		},
		{
			ID:                 core.PresetImmigrationFullHistoryCVID,
			UserID:             userID,
			Name:               "Immigration Full History CV",
			Description:        "Unabridged career history format for audit, visa, and immigration compliance.",
			SystemInstructions: "Include full, unabridged employment and academic history. Maintain detailed hours per week, company location and verification contacts, supervisor contact information, and full explanatory duty summaries.",
			SuggestedThemeID:   &themeCV,
			TemplateSetID:      core.TemplateDetailedImmigrationCVID,
			Language:           core.DefaultLanguage,
			CreatedAt:          now,
			UpdatedAt:          now,
		},
	}
}

// EnsureTenantProvisioned idempotently ensures a tenant has an upserted user record,
// default profile, and default resume branch with an initial active rendered version.
func EnsureTenantProvisioned(
	ctx context.Context,
	store core.Store,
	userID string,
	userInfo ...*core.User,
) error {
	if store == nil {
		return fmt.Errorf("%w: store cannot be nil", core.ErrValidation)
	}
	return EnsureTenantProvisionedStores(
		ctx,
		store.Profiles(),
		store.Branches(),
		store.Templates(),
		store.Users(),
		userID,
		userInfo...,
	)
}

// EnsureTenantProvisionedStores idempotently provisions tenant entities using explicit store handles.
func EnsureTenantProvisionedStores(
	ctx context.Context,
	profiles ProfileSeeder,
	branches BranchSeeder,
	templates TemplateSeeder,
	users UserSeeder,
	userID string,
	userInfo ...*core.User,
) error {
	if profiles == nil || branches == nil || templates == nil {
		return fmt.Errorf("%w: profile, branch, and template stores cannot be nil", core.ErrValidation)
	}
	if userID == "" {
		return fmt.Errorf("%w: user id cannot be empty", core.ErrValidation)
	}

	mu := tenantProvisionLock(userID)
	mu.Lock()
	defer mu.Unlock()

	if err := ensureUserRecord(ctx, users, userID, userInfo...); err != nil {
		return err
	}
	prof, err := ensureDefaultProfile(ctx, profiles, userID, userInfo...)
	if err != nil {
		return err
	}
	return ensureDefaultBranch(ctx, profiles, branches, templates, prof, userID)
}

// ensureUserRecord upserts the tenant user record when users is non-nil while preserving admin elevation.
func ensureUserRecord(ctx context.Context, users UserSeeder, userID string, userInfo ...*core.User) error {
	if users == nil {
		return nil
	}
	var u *core.User
	if len(userInfo) > 0 && userInfo[0] != nil {
		u = userInfo[0]
		if u.ID == "" {
			u.ID = userID
		}
	} else {
		u = &core.User{
			ID:       userID,
			Role:     core.UserRoleUser,
			IsActive: true,
		}
	}
	if existing, err := users.User(ctx, userID); err == nil && existing != nil {
		if existing.Role == core.UserRoleAdmin && u.Role != core.UserRoleAdmin {
			u.Role = core.UserRoleAdmin
		}
	}
	if err := users.UpsertUser(ctx, u); err != nil {
		return fmt.Errorf("upserting tenant user: %w", err)
	}
	return nil
}

// ensureDefaultProfile creates an initial Profile for userID when one does not yet exist.
func ensureDefaultProfile(ctx context.Context, profiles ProfileSeeder, userID string, userInfo ...*core.User) (*core.Profile, error) {
	if existing, err := profiles.Profile(ctx, userID); err == nil && existing != nil {
		return existing, nil
	}
	fullName := "Default User"
	email := ""
	if len(userInfo) > 0 && userInfo[0] != nil {
		if userInfo[0].FullName != "" {
			fullName = userInfo[0].FullName
		}
		email = userInfo[0].Email
	}
	now := time.Now().UTC()
	defaultProfile := core.Profile{
		ID:                 "profile-" + userID,
		UserID:             userID,
		FullName:           fullName,
		Email:              email,
		Headline:           "Software Engineer",
		MainRevision:       1,
		GlobalWritingRules: "Prioritize measurable outcomes, active voice, and clear technical context.",
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := profiles.UpsertProfile(ctx, &defaultProfile); err != nil {
		return nil, fmt.Errorf("seeding tenant default profile: %w", err)
	}
	return &defaultProfile, nil
}

// ensureDefaultBranch creates the tenant's default branch and initial baseline version if absent.
func ensureDefaultBranch(
	ctx context.Context,
	profiles ProfileSeeder,
	branches BranchSeeder,
	templates TemplateSeeder,
	prof *core.Profile,
	userID string,
) error {
	defaultBranchID := core.DefaultBranchID(userID)
	if existing, err := branches.Branch(ctx, userID, defaultBranchID); err == nil && existing != nil {
		return nil
	}

	tmpl, err := resolveStandardTemplate(ctx, templates, userID)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	defaultBranch := core.Branch{
		ID:                     defaultBranchID,
		UserID:                 userID,
		Name:                   core.DefaultBranchName,
		IsDefault:              true,
		TemplateSetID:          core.TemplateStandardCleanID,
		LastSyncedMainRevision: prof.MainRevision,
		CreatedAt:              now,
		UpdatedAt:              now,
	}
	if err := branches.CreateBranch(ctx, &defaultBranch); err != nil && !errors.Is(err, core.ErrConflict) {
		return fmt.Errorf("seeding default branch: %w", err)
	}

	snapshot, err := profiles.Snapshot(ctx, userID)
	if err != nil {
		return fmt.Errorf("getting snapshot for default branch: %w", err)
	}

	tailoredContent := core.SnapshotToTailoredContent(snapshot)
	htmlOut, mdOut, txtOut := renderDefaultFallback(tmpl, prof, tailoredContent)

	ver := core.BranchVersion{
		ID:                "ver-default-" + userID,
		UserID:            userID,
		ResumeID:          defaultBranch.ID,
		BranchID:          defaultBranch.ID,
		VersionNumber:     1,
		Status:            core.VersionStatusCompleted,
		MainRevisionUsed:  prof.MainRevision,
		TemplateSetIDUsed: core.TemplateStandardCleanID,
		ProviderUsed:      "none",
		ModelUsed:         "none",
		SourceType:        core.SourceTypeMainProfile,
		TailoredContent:   tailoredContent,
		HTMLOutput:        htmlOut,
		MarkdownOutput:    mdOut,
		PlainTextOutput:   txtOut,
		CreatedAt:         now,
		CompletedAt:       &now,
	}
	if err := branches.CreateVersion(ctx, &ver); err != nil && !errors.Is(err, core.ErrConflict) {
		return fmt.Errorf("seeding default branch version: %w", err)
	}

	defaultBranch.ActiveVersionID = &ver.ID
	_ = branches.UpdateBranch(ctx, &defaultBranch)
	return nil
}

// resolveStandardTemplate looks up the tenant's Standard Clean template or falls back to system scope.
func resolveStandardTemplate(ctx context.Context, templates TemplateSeeder, userID string) (*core.TemplateSet, error) {
	tmpl, err := templates.Template(ctx, userID, core.TemplateStandardCleanID)
	if err == nil {
		return tmpl, nil
	}
	tmpl, err = templates.Template(ctx, core.SystemUserID, core.TemplateStandardCleanID)
	if err != nil {
		return nil, fmt.Errorf("finding standard template for default branch: %w", err)
	}
	return tmpl, nil
}

// renderDefaultFallback produces minimal fallback HTML, Markdown, and plain-text outputs during bootstrap.
func renderDefaultFallback(_ *core.TemplateSet, profile *core.Profile, _ *core.TailoredContent) (string, string, string) {
	name := "Default User"
	if profile != nil && profile.FullName != "" {
		name = profile.FullName
	}
	headline := "Software Engineer"
	if profile != nil && profile.Headline != "" {
		headline = profile.Headline
	}
	html := fmt.Sprintf("<!DOCTYPE html><html><head><title>%s - Resume</title></head><body><h1>%s</h1><p>%s</p></body></html>", name, name, headline)
	md := fmt.Sprintf("# %s\n\n%s\n", name, headline)
	txt := fmt.Sprintf("%s\n%s\n", name, headline)
	return html, md, txt
}

// DefaultData idempotently populates built-in templates, presets, global skills,
// default user profile, and default resume branch across any storage backend implementing core.Store.
func DefaultData(
	ctx context.Context,
	store core.Store,
	defaultUserID ...string,
) error {
	if store == nil {
		return fmt.Errorf("%w: store cannot be nil", core.ErrValidation)
	}
	return DefaultDataStores(
		ctx,
		store.Profiles(),
		store.Career(),
		store.Branches(),
		store.Templates(),
		store.Presets(),
		store.Users(),
		defaultUserID...,
	)
}

// DefaultDataStores idempotently populates built-in templates, presets, global skills,
// default user profile, and default resume branch using explicit store handles.
func DefaultDataStores(
	ctx context.Context,
	profiles ProfileSeeder,
	skills SkillSeeder,
	branches BranchSeeder,
	templates TemplateSeeder,
	presets PresetSeeder,
	users UserSeeder,
	defaultUserID ...string,
) error {
	if profiles == nil || skills == nil || branches == nil || templates == nil || presets == nil {
		return fmt.Errorf("%w: profile, career, branch, template, and preset stores cannot be nil", core.ErrValidation)
	}

	targetUserID := "default"
	if len(defaultUserID) > 0 && defaultUserID[0] != "" {
		targetUserID = defaultUserID[0]
	}

	if err := seedSystemTemplates(ctx, templates); err != nil {
		return err
	}
	if err := seedSystemPresets(ctx, presets); err != nil {
		return err
	}
	if err := seedGlobalSkills(ctx, skills); err != nil {
		return err
	}

	return EnsureTenantProvisionedStores(ctx, profiles, branches, templates, users, targetUserID)
}

// seedSystemTemplates idempotently inserts built-in templates under the system tenant.
func seedSystemTemplates(ctx context.Context, templates TemplateSeeder) error {
	for _, tmpl := range DefaultTemplateSets() {
		tmpl.UserID = core.SystemUserID
		existing, err := templates.Template(ctx, core.SystemUserID, tmpl.ID)
		if err != nil || existing == nil {
			if err := templates.CreateTemplate(ctx, &tmpl); err != nil && !errors.Is(err, core.ErrConflict) {
				return fmt.Errorf("seeding template %s: %w", tmpl.Name, err)
			}
		}
	}
	return nil
}

// seedSystemPresets idempotently inserts built-in presets under the system tenant.
func seedSystemPresets(ctx context.Context, presets PresetSeeder) error {
	for _, preset := range DefaultPresets(core.SystemUserID) {
		existing, err := presets.Preset(ctx, core.SystemUserID, preset.ID)
		if err != nil || existing == nil {
			if err := presets.CreatePreset(ctx, &preset); err != nil && !errors.Is(err, core.ErrConflict) {
				return fmt.Errorf("seeding preset %s: %w", preset.Name, err)
			}
		}
	}
	return nil
}

// seedGlobalSkills idempotently inserts canonical global skills under the system tenant.
func seedGlobalSkills(ctx context.Context, skills SkillSeeder) error {
	for _, skill := range DefaultGlobalSkills() {
		existing, err := skills.Skill(ctx, core.SystemUserID, skill.ID)
		if err != nil || existing == nil {
			if err := skills.CreateSkill(ctx, &skill); err != nil && !errors.Is(err, core.ErrConflict) {
				return fmt.Errorf("seeding global skill %s: %w", skill.Name, err)
			}
		}
	}
	return nil
}
