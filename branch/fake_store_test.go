package branch_test

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/resumeee-app/core"
	"github.com/resumeee-app/core/branch"
	"github.com/resumeee-app/core/memory"
	"github.com/resumeee-app/core/sanitizer"
	"github.com/resumeee-app/core/seed"
	"github.com/resumeee-app/core/store"
)

type testBranchStore struct {
	*store.Store
	*store.Branches
	*store.Profiles
	*store.Presets
	*store.Templates
	*store.Quotas
	*store.Career
	*store.Jobs
	backend *memory.Backend
}

func newSeededBranchStore(ctx context.Context, defaultUserID string) *testBranchStore {
	return newSeededBranchStoreWithClock(ctx, defaultUserID, nil)
}

func newSeededBranchStoreWithClock(ctx context.Context, defaultUserID string, clock func() time.Time) *testBranchStore {
	backend := memory.NewBackend()
	var opts []store.Option
	if clock != nil {
		opts = append(opts, store.WithClock(clock))
	}
	st := store.New(backend, opts...)
	if err := seed.DefaultData(ctx, st, defaultUserID); err != nil {
		panic(fmt.Sprintf("seed.DefaultData failed: %v", err))
	}
	return &testBranchStore{
		Store:     st,
		Branches:  st.Branches(),
		Profiles:  st.Profiles(),
		Presets:   st.Presets(),
		Templates: st.Templates(),
		Quotas:    st.Quotas(),
		Career:    st.Career(),
		Jobs:      st.Jobs(),
		backend:   backend,
	}
}

func (s *testBranchStore) seedCompletedVersion(ctx context.Context, v *core.BranchVersion) error {
	cp := *v
	if cp.TemplateSetIDUsed == "" {
		cp.TemplateSetIDUsed = core.TemplateStandardCleanID
	}
	if cp.SourceType == "" {
		cp.SourceType = core.SourceTypeLLMGeneration
	}
	cp.Status = core.VersionStatusCompleted
	err := s.SaveCompletedVersion(ctx, &cp)
	if err == nil {
		*v = cp
	}
	return err
}

func (s *testBranchStore) seedVersionInState(ctx context.Context, v *core.BranchVersion, status core.VersionStatus, reservationID string) error {
	if status.IsCompleted() {
		return s.seedCompletedVersion(ctx, v)
	}
	userID := v.UserID
	if userID == "" {
		br, err := s.Branch(ctx, "", v.BranchID)
		if err != nil {
			return err
		}
		userID = br.UserID
	}
	now := time.Now().UTC()
	exp := now.Add(core.DefaultJobTTL)
	jobID := v.JobID
	if jobID == "" {
		jobID = "job-" + uuid.New().String()
	}
	verID := v.ID
	if verID == "" {
		verID = "ver-" + uuid.New().String()
	}

	pendingVer := *v
	pendingVer.ID = verID
	pendingVer.UserID = userID
	pendingVer.JobID = jobID
	pendingVer.Status = core.VersionStatusPending
	if pendingVer.TemplateSetIDUsed == "" {
		pendingVer.TemplateSetIDUsed = core.TemplateStandardCleanID
	}
	if pendingVer.SourceType == "" {
		pendingVer.SourceType = core.SourceTypeLLMGeneration
	}

	pendingJob := &core.Job{
		ID:            jobID,
		Kind:          core.JobKindBranchTailor,
		UserID:        userID,
		Status:        core.JobStatusPending,
		ReservationID: reservationID,
		ExpiresAt:     &exp,
		Tailor: &core.BranchTailorSpec{
			BranchID:  pendingVer.BranchID,
			VersionID: verID,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	if _, err := s.SubmitBranchTailor(ctx, pendingJob, &pendingVer, core.PendingJobOptions{
		MaxPendingPerTenant: 100,
		TTL:                 core.DefaultJobTTL,
		Now:                 now,
	}); err != nil {
		return err
	}

	if status == core.VersionStatusPending {
		*v = pendingVer
		return nil
	}

	return s.backend.Write(ctx, func(tx store.Tx) error {
		savedVer, err := tx.Version(ctx, verID)
		if err != nil {
			return err
		}
		savedJob, err := tx.Job(ctx, jobID)
		if err != nil {
			return err
		}

		updatedVer := *savedVer
		updatedVer.Status = status
		updatedVer.UpdatedAt = now
		updatedJob := *savedJob
		updatedJob.UpdatedAt = now

		switch status {
		case core.VersionStatusProcessing:
			lease := now.Add(core.DefaultLeaseDuration)
			updatedJob.Status = core.JobStatusProcessing
			updatedJob.ClaimedBy = "worker-1"
			updatedJob.ClaimedAt = &now
			updatedJob.LeaseExpiresAt = &lease
			updatedJob.AttemptCount = 1
		case core.VersionStatusFailed:
			updatedJob.Status = core.JobStatusFailed
			updatedJob.ErrorMessage = "seeded failure"
			updatedVer.ErrorMessage = "seeded failure"
		case core.VersionStatusCancelled:
			updatedJob.Status = core.JobStatusCancelled
			updatedJob.ErrorMessage = "seeded cancelled"
			updatedVer.ErrorMessage = "seeded cancelled"
		}

		if err := tx.PutVersion(ctx, &updatedVer); err != nil {
			return err
		}
		if err := tx.PutJob(ctx, &updatedJob); err != nil {
			return err
		}
		*v = updatedVer
		return nil
	})
}

func (s *testBranchStore) setLegacyGaplessBranch(ctx context.Context, br *core.Branch, ver *core.BranchVersion) error {
	return s.backend.Write(ctx, func(tx store.Tx) error {
		if err := tx.PutVersion(ctx, ver); err != nil {
			return err
		}
		return tx.PutBranch(ctx, br)
	})
}

type fakePromptCompiler struct{}

func (f *fakePromptCompiler) CompilePrompt(req core.GenerationRequest) (*core.PromptPreview, error) {
	session := sanitizer.NewSessionFromRequest(req)
	obfReq := session.ObfuscateRequest(req)

	var headline, summary string
	if obfReq.MainProfile != nil {
		headline = obfReq.MainProfile.Profile.Headline
		summary = obfReq.MainProfile.Profile.Summary
	}

	userPrompt := fmt.Sprintf("Headline: %s\nSummary: %s\nTarget Role: %s\nRequirements: %s",
		headline,
		summary,
		obfReq.BranchContext.TargetRole,
		obfReq.BranchContext.TargetRequirements,
	)
	sysPrompt := fmt.Sprintf("Rules: %s\nLanguage: %s",
		obfReq.GlobalRules,
		obfReq.TargetLanguage,
	)

	return &core.PromptPreview{
		SystemPrompt: sysPrompt,
		UserPrompt:   userPrompt,
	}, nil
}

func (f *fakePromptCompiler) CompileGapAnalysisPrompt(req core.GenerationRequest) (*core.PromptPreview, error) {
	session := sanitizer.NewSessionFromRequest(req)
	obfBranch := session.ObfuscateBranchContext(req.BranchContext)
	return &core.PromptPreview{
		SystemPrompt: fmt.Sprintf("Workflow: %s\nLanguage: %s", core.PromptWorkflowGapAnalysis, req.TargetLanguage),
		UserPrompt:   fmt.Sprintf("Target Role: %s\nRequirements: %s", obfBranch.TargetRole, obfBranch.TargetRequirements),
	}, nil
}

func (f *fakePromptCompiler) CompileATSExtractionPrompt(req core.ATSSimulationRequest) (*core.PromptPreview, error) {
	session := sanitizer.NewSessionFromSnapshot(req.MainProfile)
	return &core.PromptPreview{
		SystemPrompt: fmt.Sprintf("Workflow: %s\nFormat: %s", core.PromptWorkflowATSExtraction, req.FormatUsed),
		UserPrompt:   session.ObfuscateString(req.ResumeText),
	}, nil
}

func (f *fakePromptCompiler) CompileATSMatchingPrompt(b core.Branch, profile *core.MainProfileSnapshot, extracted *core.ATSExtractedCandidate) (*core.PromptPreview, error) {
	session := sanitizer.NewSessionFromSnapshot(profile)
	obfBranch := session.ObfuscateBranchContext(b)
	obfExtracted := session.ObfuscateExtractedCandidate(extracted)
	var summary string
	if obfExtracted != nil {
		summary = obfExtracted.Summary
	}
	return &core.PromptPreview{
		SystemPrompt: fmt.Sprintf("Workflow: %s", core.PromptWorkflowATSMatching),
		UserPrompt:   fmt.Sprintf("Target Role: %s\nRequirements: %s\nSummary: %s", obfBranch.TargetRole, obfBranch.TargetRequirements, summary),
	}, nil
}

var (
	_ branch.Store          = (*testBranchStore)(nil)
	_ branch.JobStore       = (*testBranchStore)(nil)
	_ branch.ProfileReader  = (*testBranchStore)(nil)
	_ branch.PresetReader   = (*testBranchStore)(nil)
	_ branch.TemplateReader = (*testBranchStore)(nil)
	_ branch.QuotaManager   = (*testBranchStore)(nil)
	_ branch.PromptCompiler = (*fakePromptCompiler)(nil)
)
