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
//
// Parameters:
//   - userID: The target user identifier (defaults to core.SystemUserID if empty).
//
// Returns:
//   - []core.Preset: The list of system built-in presets.
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

const tenantStripeCount = 64

var tenantProvisionStripes [tenantStripeCount]sync.Mutex

func getTenantProvisionLock(userID string) *sync.Mutex {
	var h uint32 = 2166136261 // FNV-1a 32-bit offset basis
	for i := 0; i < len(userID); i++ {
		h ^= uint32(userID[i])
		h *= 16777619 // FNV-1a 32-bit prime
	}
	return &tenantProvisionStripes[h%tenantStripeCount]
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
		return errors.New("store cannot be nil")
	}
	return EnsureTenantProvisionedRepos(
		ctx,
		store.Profile(),
		store.Branch(),
		store.Template(),
		store.User(),
		userID,
		userInfo...,
	)
}

// EnsureTenantProvisionedRepos idempotently provisions tenant entities using explicit repository handles.
func EnsureTenantProvisionedRepos(
	ctx context.Context,
	profileRepo core.ProfileRepository,
	branchRepo core.BranchRepository,
	templateRepo core.TemplateRepository,
	userRepo core.UserRepository,
	userID string,
	userInfo ...*core.User,
) error {
	if userID == "" {
		return fmt.Errorf("%w: user id cannot be empty", core.ErrValidation)
	}

	mu := getTenantProvisionLock(userID)
	mu.Lock()
	defer mu.Unlock()

	// 1. Upsert User record if UserRepository is provided
	if userRepo != nil {
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
		if existing, err := userRepo.GetUser(ctx, userID); err == nil && existing != nil {
			if existing.Role == core.UserRoleAdmin && u.Role != core.UserRoleAdmin {
				u.Role = core.UserRoleAdmin
			}
		}
		if err := userRepo.UpsertUser(ctx, u); err != nil {
			return fmt.Errorf("upserting tenant user: %w", err)
		}
	}

	// 2. Seed Default Profile if missing
	existingProf, err := profileRepo.GetProfile(ctx, userID)
	if err != nil || existingProf == nil {
		fullName := "Default User"
		email := ""
		if len(userInfo) > 0 && userInfo[0] != nil {
			if userInfo[0].FullName != "" {
				fullName = userInfo[0].FullName
			}
			email = userInfo[0].Email
		}
		defaultProfile := core.Profile{
			ID:                 "profile-" + userID,
			UserID:             userID,
			FullName:           fullName,
			Email:              email,
			Headline:           "Software Engineer",
			MainRevision:       1,
			GlobalWritingRules: "Prioritize measurable outcomes, active voice, and clear technical context.",
			CreatedAt:          time.Now().UTC(),
			UpdatedAt:          time.Now().UTC(),
		}
		if err := profileRepo.UpsertProfile(ctx, &defaultProfile); err != nil {
			return fmt.Errorf("seeding tenant default profile: %w", err)
		}
		existingProf = &defaultProfile
	}

	// 3. Seed Default Branch if missing
	defaultBranchID := core.DefaultBranchID(userID)
	existingBranch, err := branchRepo.GetBranch(ctx, userID, defaultBranchID)
	if err != nil || existingBranch == nil {
		tmpl, err := templateRepo.GetTemplate(ctx, userID, core.TemplateStandardCleanID)
		if err != nil {
			tmpl, err = templateRepo.GetTemplate(ctx, core.SystemUserID, core.TemplateStandardCleanID)
			if err != nil {
				return fmt.Errorf("finding standard template for default branch: %w", err)
			}
		}

		now := time.Now().UTC()
		defaultBranch := core.Branch{
			ID:                     defaultBranchID,
			UserID:                 userID,
			Name:                   core.DefaultBranchName,
			IsDefault:              true,
			TemplateSetID:          core.TemplateStandardCleanID,
			LastSyncedMainRevision: existingProf.MainRevision,
			CreatedAt:              now,
			UpdatedAt:              now,
		}
		if err := branchRepo.CreateBranch(ctx, &defaultBranch); err != nil && !errors.Is(err, core.ErrConflict) {
			return fmt.Errorf("seeding default branch: %w", err)
		}

		snapshot, err := profileRepo.GetSnapshot(ctx, userID)
		if err != nil {
			return fmt.Errorf("getting snapshot for default branch: %w", err)
		}

		tailoredContent := core.SnapshotToTailoredContent(snapshot)
		htmlOut, mdOut, txtOut := renderDefaultFallback(tmpl, existingProf, tailoredContent)

		verID := "ver-default-" + userID
		ver := core.BranchVersion{
			ID:                verID,
			UserID:            userID,
			ResumeID:          defaultBranch.ID,
			BranchID:          defaultBranch.ID,
			VersionNumber:     1,
			Status:            core.VersionStatusCompleted,
			MainRevisionUsed:  existingProf.MainRevision,
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
		if err := branchRepo.CreateVersion(ctx, &ver); err != nil && !errors.Is(err, core.ErrConflict) {
			return fmt.Errorf("seeding default branch version: %w", err)
		}

		defaultBranch.ActiveVersionID = &ver.ID
		_ = branchRepo.UpdateBranch(ctx, &defaultBranch)
	}

	return nil
}

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

// SeedDefaultData idempotently populates built-in templates, presets, default user profile,
// and default resume branch across any storage backend implementing the domain repository interfaces.
//
// Parameters:
//   - ctx: The execution context.
//   - profileRepo: The profile repository instance.
//   - branchRepo: The branch repository instance.
//   - templateRepo: The template repository instance.
//   - presetRepo: The preset repository instance.
//   - defaultUserID: Optional target user ID (defaults to "default").
//
// Returns:
//   - error: Non-nil if seeding fails at any step.
func SeedDefaultData(
	ctx context.Context,
	profileRepo core.ProfileRepository,
	branchRepo core.BranchRepository,
	templateRepo core.TemplateRepository,
	presetRepo core.PresetRepository,
	defaultUserID ...string,
) error {
	targetUserID := "default"
	if len(defaultUserID) > 0 && defaultUserID[0] != "" {
		targetUserID = defaultUserID[0]
	}

	// 1. Seed Built-in Template Sets under system
	templates := DefaultTemplateSets()
	for _, tmpl := range templates {
		tmpl.UserID = core.SystemUserID
		existing, err := templateRepo.GetTemplate(ctx, core.SystemUserID, tmpl.ID)
		if err != nil || existing == nil {
			if err := templateRepo.CreateTemplate(ctx, &tmpl); err != nil && !errors.Is(err, core.ErrConflict) {
				return fmt.Errorf("seeding template %s: %w", tmpl.Name, err)
			}
		}
	}

	// 2. Seed Built-in Presets under system
	presets := DefaultPresets(core.SystemUserID)
	for _, preset := range presets {
		existing, err := presetRepo.GetPreset(ctx, core.SystemUserID, preset.ID)
		if err != nil || existing == nil {
			if err := presetRepo.CreatePreset(ctx, &preset); err != nil && !errors.Is(err, core.ErrConflict) {
				return fmt.Errorf("seeding preset %s: %w", preset.Name, err)
			}
		}
	}

	// 2.5 Seed Global Skills under system
	globalSkills := DefaultGlobalSkills()
	for _, skill := range globalSkills {
		existing, err := profileRepo.GetSkill(ctx, core.SystemUserID, skill.ID)
		if err != nil || existing == nil {
			if err := profileRepo.CreateSkill(ctx, &skill); err != nil && !errors.Is(err, core.ErrConflict) {
				return fmt.Errorf("seeding global skill %s: %w", skill.Name, err)
			}
		}
	}

	// 3. Ensure Default Tenant is provisioned
	var userRepo core.UserRepository
	if ur, ok := profileRepo.(core.UserRepository); ok {
		userRepo = ur
	}
	return EnsureTenantProvisionedRepos(ctx, profileRepo, branchRepo, templateRepo, userRepo, targetUserID)
}
