package storetest

import (
	"errors"
	"testing"
	"time"

	"github.com/resumeee-app/core"
)

// RunUsers verifies user persistence, CreatedAt preservation, validation, and isolation.
func RunUsers(t *testing.T, newStore Factory) {
	t.Run("UpsertAndFetch", func(t *testing.T) {
		clk := newTestClock(defaultTestBaseTime)
		st := openStore(t, newStore, clk.Now)
		ctx := runCtx(t)

		if _, err := st.Users().User(ctx, "missing"); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
		if err := st.Users().UpsertUser(ctx, nil); !errors.Is(err, core.ErrValidation) {
			t.Fatalf("expected ErrValidation for nil user, got %v", err)
		}

		u := &core.User{ID: "u-1", Email: "alice@example.com", FullName: "Alice"}
		if err := st.Users().UpsertUser(ctx, u); err != nil {
			t.Fatalf("UpsertUser failed: %v", err)
		}
		createdAt := u.CreatedAt
		if createdAt.IsZero() {
			t.Fatal("expected CreatedAt to be populated")
		}

		// Mutating caller pointer must not leak into stored state.
		u.FullName = "Mutated Caller"
		got, err := st.Users().User(ctx, "u-1")
		if err != nil {
			t.Fatalf("User failed: %v", err)
		}
		if got.FullName != "Alice" {
			t.Fatalf("caller mutation leaked: got %q", got.FullName)
		}

		clk.Advance(time.Hour)
		updated := &core.User{ID: "u-1", Email: "alice+new@example.com", FullName: "Alice Updated"}
		if err := st.Users().UpsertUser(ctx, updated); err != nil {
			t.Fatalf("second UpsertUser failed: %v", err)
		}
		if !updated.CreatedAt.Equal(createdAt) {
			t.Fatalf("expected CreatedAt preserved (%v), got %v", createdAt, updated.CreatedAt)
		}
		if !updated.UpdatedAt.After(createdAt) {
			t.Fatalf("expected UpdatedAt to advance past CreatedAt")
		}
	})
}

// RunPresetsAndTemplates verifies preset and template validation, sorting, visibility, and immutability.
func RunPresetsAndTemplates(t *testing.T, newStore Factory) {
	t.Run("PresetsLifecycle", func(t *testing.T) {
		st := openStore(t, newStore, newTestClock(defaultTestBaseTime).Now)
		ctx := runCtx(t)

		builtin := &core.Preset{
			ID:                 core.Preset1PageTechResumeID,
			UserID:             core.SystemUserID,
			Name:               "1-Page Tech",
			SystemInstructions: "Be concise",
		}
		if err := st.Presets().CreatePreset(ctx, builtin); err != nil {
			t.Fatalf("CreatePreset builtin failed: %v", err)
		}
		if builtin.Language != core.DefaultLanguage {
			t.Fatalf("expected default language %q, got %q", core.DefaultLanguage, builtin.Language)
		}

		u1Preset := &core.Preset{ID: "p-u1", UserID: "u1", Name: "Custom U1", Language: "Spanish"}
		if err := st.Presets().CreatePreset(ctx, u1Preset); err != nil {
			t.Fatalf("CreatePreset u1 failed: %v", err)
		}
		if err := st.Presets().CreatePreset(ctx, &core.Preset{ID: "p-u1", UserID: "u1", Name: "Dup"}); !errors.Is(err, core.ErrConflict) {
			t.Fatalf("expected ErrConflict on duplicate preset ID, got %v", err)
		}

		invalidLang := &core.Preset{ID: "p-bad", UserID: "u1", Name: "Bad", Language: "Klingon"}
		if err := st.Presets().CreatePreset(ctx, invalidLang); !errors.Is(err, core.ErrValidation) {
			t.Fatalf("expected ErrValidation for unsupported language, got %v", err)
		}

		list, err := st.Presets().ListPresets(ctx, "u1")
		if err != nil {
			t.Fatalf("ListPresets failed: %v", err)
		}
		if len(list) != 2 || !list[0].IsBuiltIn() || list[1].ID != "p-u1" {
			t.Fatalf("unexpected preset order/list: %+v", list)
		}

		if _, err := st.Presets().Preset(ctx, "u2", "p-u1"); !errors.Is(err, core.ErrForbidden) {
			t.Fatalf("expected ErrForbidden reading foreign preset, got %v", err)
		}
		if _, err := st.Presets().Preset(ctx, "u2", builtin.ID); err != nil {
			t.Fatalf("expected built-in preset visible to u2: %v", err)
		}

		if err := st.Presets().UpdatePreset(ctx, &core.Preset{ID: builtin.ID, UserID: core.SystemUserID, Name: "Hack"}); !errors.Is(err, core.ErrStrategyImmutable) {
			t.Fatalf("expected ErrStrategyImmutable updating built-in preset, got %v", err)
		}
		if err := st.Presets().DeletePreset(ctx, core.SystemUserID, builtin.ID); !errors.Is(err, core.ErrStrategyImmutable) {
			t.Fatalf("expected ErrStrategyImmutable deleting built-in preset, got %v", err)
		}
		if err := st.Presets().DeletePreset(ctx, "u2", "p-u1"); !errors.Is(err, core.ErrForbidden) {
			t.Fatalf("expected ErrForbidden deleting foreign preset, got %v", err)
		}

		u1Preset.Name = "Custom U1 v2"
		if err := st.Presets().UpdatePreset(ctx, u1Preset); err != nil {
			t.Fatalf("UpdatePreset failed: %v", err)
		}
		if err := st.Presets().DeletePreset(ctx, "u1", "p-u1"); err != nil {
			t.Fatalf("DeletePreset failed: %v", err)
		}
	})

	t.Run("TemplatesLifecycle", func(t *testing.T) {
		st := openStore(t, newStore, newTestClock(defaultTestBaseTime).Now)
		ctx := runCtx(t)

		bad := &core.TemplateSet{
			ID:           "tmpl-bad",
			UserID:       "u1",
			Name:         "Broken",
			HTMLTemplate: "{{ .Broken",
		}
		if err := st.Templates().CreateTemplate(ctx, bad); !errors.Is(err, core.ErrTemplateSyntax) {
			t.Fatalf("expected ErrTemplateSyntax, got %v", err)
		}

		builtin := &core.TemplateSet{
			ID:               core.TemplateStandardCleanID,
			UserID:           core.SystemUserID,
			Name:             "Standard Clean",
			IsBuiltIn:        true,
			HTMLTemplate:     "<html>{{.FullName}}</html>",
			MarkdownTemplate: "# {{.FullName}}",
			TextTemplate:     "{{.FullName}}",
		}
		if err := st.Templates().CreateTemplate(ctx, builtin); err != nil {
			t.Fatalf("CreateTemplate builtin failed: %v", err)
		}

		u1Tmpl := &core.TemplateSet{
			ID:           "tmpl-u1",
			UserID:       "u1",
			Name:         "Z Custom",
			HTMLTemplate: "<div>{{.FullName}}</div>",
		}
		if err := st.Templates().CreateTemplate(ctx, u1Tmpl); err != nil {
			t.Fatalf("CreateTemplate u1 failed: %v", err)
		}

		list, err := st.Templates().ListTemplates(ctx, "u1")
		if err != nil {
			t.Fatalf("ListTemplates failed: %v", err)
		}
		if len(list) != 2 || !list[0].IsBuiltIn {
			t.Fatalf("expected built-in first in template list, got %+v", list)
		}

		if _, err := st.Templates().Template(ctx, "u2", "tmpl-u1"); !errors.Is(err, core.ErrForbidden) {
			t.Fatalf("expected ErrForbidden for foreign template, got %v", err)
		}
		if err := st.Templates().UpdateTemplate(ctx, &core.TemplateSet{ID: builtin.ID, UserID: "u1", Name: "Hack"}); !errors.Is(err, core.ErrStrategyImmutable) {
			t.Fatalf("expected ErrStrategyImmutable updating built-in template, got %v", err)
		}
		if err := st.Templates().DeleteTemplate(ctx, core.SystemUserID, builtin.ID); !errors.Is(err, core.ErrStrategyImmutable) {
			t.Fatalf("expected ErrStrategyImmutable deleting built-in template, got %v", err)
		}

		u1Tmpl.HTMLTemplate = "<section>{{.FullName}}</section>"
		if err := st.Templates().UpdateTemplate(ctx, u1Tmpl); err != nil {
			t.Fatalf("UpdateTemplate failed: %v", err)
		}
		if err := st.Templates().DeleteTemplate(ctx, "u1", u1Tmpl.ID); err != nil {
			t.Fatalf("DeleteTemplate failed: %v", err)
		}
	})
}
