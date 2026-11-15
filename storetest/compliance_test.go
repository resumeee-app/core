package storetest_test

import (
	"github.com/resumeee-app/resumeee-core/branch"
	"github.com/resumeee-app/resumeee-core/profile"
	"github.com/resumeee-app/resumeee-core/seed"
	"github.com/resumeee-app/resumeee-core/store"
	"github.com/resumeee-app/resumeee-core/worker"
)

// Compile-time checks verifying store sub-stores satisfy all consumer-defined interfaces across packages.
var (
	_ branch.Store          = (*store.Branches)(nil)
	_ branch.JobStore       = (*store.Jobs)(nil)
	_ branch.ProfileReader  = (*store.Profiles)(nil)
	_ branch.PresetReader   = (*store.Presets)(nil)
	_ branch.TemplateReader = (*store.Templates)(nil)
	_ branch.QuotaManager   = (*store.Quotas)(nil)

	_ worker.BranchReader     = (*store.Branches)(nil)
	_ worker.JobStore         = (*store.Jobs)(nil)
	_ worker.ProfileReader    = (*store.Profiles)(nil)
	_ worker.PresetReader     = (*store.Presets)(nil)
	_ worker.TemplateReader   = (*store.Templates)(nil)
	_ worker.QuotaCommitter   = (*store.Quotas)(nil)
	_ worker.PDFQuotaReserver = (*store.Quotas)(nil)
	_ worker.QuotaReleaser    = (*store.Quotas)(nil)

	_ profile.Store        = (*store.Profiles)(nil)
	_ profile.SkillCatalog = (*store.Career)(nil)
	_ profile.BranchReader = (*store.Branches)(nil)

	_ seed.UserSeeder     = (*store.Users)(nil)
	_ seed.PresetSeeder   = (*store.Presets)(nil)
	_ seed.TemplateSeeder = (*store.Templates)(nil)
	_ seed.ProfileSeeder  = (*store.Profiles)(nil)
	_ seed.SkillSeeder    = (*store.Career)(nil)
	_ seed.BranchSeeder   = (*store.Branches)(nil)
)
