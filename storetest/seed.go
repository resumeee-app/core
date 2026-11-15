package storetest

import (
	"errors"
	"sync"
	"testing"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/seed"
)

// RunSeeding verifies idempotent DefaultData and concurrent EnsureTenantProvisioned behavior.
func RunSeeding(t *testing.T, newStore Factory) {
	t.Run("DefaultDataAndTenantProvisioning", func(t *testing.T) {
		st := openStore(t, newStore, nil)
		ctx := runCtx(t)

		if err := seed.DefaultData(ctx, nil); !errors.Is(err, core.ErrValidation) {
			t.Fatalf("expected ErrValidation for nil store, got %v", err)
		}
		if err := seed.DefaultData(ctx, st, "default"); err != nil {
			t.Fatalf("DefaultData failed: %v", err)
		}
		if err := seed.DefaultData(ctx, st, "default"); err != nil {
			t.Fatalf("idempotent second DefaultData failed: %v", err)
		}

		presets, err := st.Presets().ListPresets(ctx, "default")
		if err != nil || len(presets) < 2 {
			t.Fatalf("expected built-in presets, got %d (err=%v)", len(presets), err)
		}
		tmpls, err := st.Templates().ListTemplates(ctx, "default")
		if err != nil || len(tmpls) < 5 {
			t.Fatalf("expected built-in templates, got %d (err=%v)", len(tmpls), err)
		}
		skills, err := st.Career().ListGlobalSkills(ctx)
		if err != nil || len(skills) == 0 {
			t.Fatalf("expected global skills seeded, got %d (err=%v)", len(skills), err)
		}
		defBranch, err := st.Branches().Branch(ctx, "default", core.DefaultBranchID("default"))
		if err != nil || defBranch.ActiveVersionID == nil {
			t.Fatalf("expected default branch with active version, got %+v (err=%v)", defBranch, err)
		}

		// Concurrent lazy tenant onboarding for the same user must remain safe and idempotent.
		const newUID = "tenant-concurrent"
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs <- seed.EnsureTenantProvisioned(ctx, st, newUID, &core.User{
					ID:       newUID,
					Email:    "tenant@example.com",
					FullName: "Concurrent User",
				})
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("EnsureTenantProvisioned failed: %v", err)
			}
		}

		br, err := st.Branches().Branch(ctx, newUID, core.DefaultBranchID(newUID))
		if err != nil || br.ActiveVersionID == nil {
			t.Fatalf("expected onboarded default branch for %s, got %+v (err=%v)", newUID, br, err)
		}
	})
}
