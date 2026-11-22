package storetest_test

import (
	"github.com/resumeee-app/core"
	"github.com/resumeee-app/core/branch"
	"github.com/resumeee-app/core/profile"
	"github.com/resumeee-app/core/seed"
	"github.com/resumeee-app/core/store"
	"github.com/resumeee-app/core/worker"
)

// Compile-time checks verifying Store and sub-repositories satisfy all consumer-defined interfaces across packages.
var (
	_ core.QuotaManager = (*store.Store)(nil)
	_ branch.Store      = (*store.Store)(nil)
	_ worker.Store      = (*store.Store)(nil)

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
