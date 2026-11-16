package memory_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/resumeee-app/core"
	"github.com/resumeee-app/core/memory"
	"github.com/resumeee-app/core/seed"
)

// profileOnlySeeder exposes only seed.ProfileSeeder to verify DefaultDataStores
// uses the explicit UserSeeder parameter without type assertions.
type profileOnlySeeder struct {
	seed.ProfileSeeder
}

// TestDefaultDataStores_DecoupledUserSeeder verifies DefaultData and DefaultDataStores
// validate nil inputs and provision users strictly via explicit seeder arguments.
func TestDefaultDataStores_DecoupledUserSeeder(t *testing.T) {
	ctx := context.Background()
	st := memory.New()
	profileSeeder := profileOnlySeeder{ProfileSeeder: st.Profiles()}

	if err := seed.DefaultData(ctx, nil); !errors.Is(err, core.ErrValidation) {
		t.Fatalf("expected ErrValidation for nil store, got %v", err)
	}
	if err := seed.DefaultDataStores(ctx, nil, st.Career(), st.Branches(), st.Templates(), st.Presets(), st.Users()); !errors.Is(err, core.ErrValidation) {
		t.Fatalf("expected ErrValidation for nil profile seeder, got %v", err)
	}

	if err := seed.DefaultDataStores(ctx, profileSeeder, st.Career(), st.Branches(), st.Templates(), st.Presets(), st.Users(), "decoupled-user"); err != nil {
		t.Fatalf("DefaultDataStores failed: %v", err)
	}

	user, err := st.Users().User(ctx, "decoupled-user")
	if err != nil || user.ID != "decoupled-user" {
		t.Fatalf("expected provisioned user decoupled-user, got %+v (err=%v)", user, err)
	}
}

// TestDefaultData_FallbackEscapesHTML verifies renderDefaultFallback escapes HTML special characters
// in HTMLOutput while keeping Markdown and PlainText unescaped.
func TestDefaultData_FallbackEscapesHTML(t *testing.T) {
	ctx := context.Background()
	st := memory.New()
	userID := "user-html-escape"

	rawName := `Alice <script>alert(1)</script> & Co`
	rawHeadline := `Lead <b>Architect</b>`
	if err := st.Profiles().UpsertProfile(ctx, &core.Profile{
		ID:       "prof-escape",
		UserID:   userID,
		FullName: rawName,
		Headline: rawHeadline,
	}); err != nil {
		t.Fatalf("UpsertProfile failed: %v", err)
	}

	if err := seed.DefaultData(ctx, st, userID); err != nil {
		t.Fatalf("DefaultData failed: %v", err)
	}

	branches, err := st.Branches().ListBranches(ctx, userID)
	if err != nil || len(branches) != 1 || branches[0].ActiveVersionID == nil {
		t.Fatalf("expected seeded default branch with active version, got err=%v branches=%+v", err, branches)
	}

	ver, err := st.Branches().Version(ctx, branches[0].ID, *branches[0].ActiveVersionID)
	if err != nil {
		t.Fatalf("Version failed: %v", err)
	}

	if strings.Contains(ver.HTMLOutput, "<script>") || strings.Contains(ver.HTMLOutput, "<b>Architect</b>") {
		t.Fatalf("expected HTMLOutput to escape markup tags, got: %s", ver.HTMLOutput)
	}
	if !strings.Contains(ver.HTMLOutput, "&lt;script&gt;") || !strings.Contains(ver.MarkdownOutput, rawName) || !strings.Contains(ver.PlainTextOutput, rawName) {
		t.Fatalf("unexpected fallback render outputs: html=%q md=%q txt=%q", ver.HTMLOutput, ver.MarkdownOutput, ver.PlainTextOutput)
	}
}
