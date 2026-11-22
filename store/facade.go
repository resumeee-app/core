package store

import (
	"context"
	"time"

	"github.com/resumeee-app/core"
)

// ListBranches promotes Branches.ListBranches onto Store.
func (s *Store) ListBranches(ctx context.Context, userID string) ([]core.Branch, error) {
	return s.branches.ListBranches(ctx, userID)
}

// Branch promotes Branches.Branch onto Store.
func (s *Store) Branch(ctx context.Context, userID, id string) (*core.Branch, error) {
	return s.branches.Branch(ctx, userID, id)
}

// CreateBranch promotes Branches.CreateBranch onto Store.
func (s *Store) CreateBranch(ctx context.Context, branch *core.Branch) error {
	return s.branches.CreateBranch(ctx, branch)
}

// UpdateBranch promotes Branches.UpdateBranch onto Store.
func (s *Store) UpdateBranch(ctx context.Context, branch *core.Branch) error {
	return s.branches.UpdateBranch(ctx, branch)
}

// DeleteBranch promotes Branches.DeleteBranch onto Store.
func (s *Store) DeleteBranch(ctx context.Context, userID, id string) error {
	return s.branches.DeleteBranch(ctx, userID, id)
}

// Version promotes Branches.Version onto Store.
func (s *Store) Version(ctx context.Context, branchID, versionID string) (*core.BranchVersion, error) {
	return s.branches.Version(ctx, branchID, versionID)
}

// VersionByNumber promotes Branches.VersionByNumber onto Store.
func (s *Store) VersionByNumber(ctx context.Context, branchID string, versionNumber int) (*core.BranchVersion, error) {
	return s.branches.VersionByNumber(ctx, branchID, versionNumber)
}

// VersionsByIDs promotes Branches.VersionsByIDs onto Store.
func (s *Store) VersionsByIDs(ctx context.Context, versionIDs []string) ([]core.BranchVersion, error) {
	return s.branches.VersionsByIDs(ctx, versionIDs)
}

// ListVersions promotes Branches.ListVersions onto Store.
func (s *Store) ListVersions(ctx context.Context, branchID string) ([]core.BranchVersion, error) {
	return s.branches.ListVersions(ctx, branchID)
}

// SaveCompletedVersion promotes Branches.SaveCompletedVersion onto Store.
func (s *Store) SaveCompletedVersion(ctx context.Context, version *core.BranchVersion) error {
	return s.branches.SaveCompletedVersion(ctx, version)
}

// SetVersionGapReport promotes Branches.SetVersionGapReport onto Store.
func (s *Store) SetVersionGapReport(ctx context.Context, versionID string, report *core.GapReport) error {
	return s.branches.SetVersionGapReport(ctx, versionID, report)
}

// SetVersionATSScore promotes Branches.SetVersionATSScore onto Store.
func (s *Store) SetVersionATSScore(ctx context.Context, versionID string, atsScore *core.ATSScore) error {
	return s.branches.SetVersionATSScore(ctx, versionID, atsScore)
}

// Profile promotes Profiles.Profile onto Store.
func (s *Store) Profile(ctx context.Context, userID string) (*core.Profile, error) {
	return s.profiles.Profile(ctx, userID)
}

// UpsertProfile promotes Profiles.UpsertProfile onto Store.
func (s *Store) UpsertProfile(ctx context.Context, profile *core.Profile) error {
	return s.profiles.UpsertProfile(ctx, profile)
}

// Snapshot promotes Profiles.Snapshot onto Store.
func (s *Store) Snapshot(ctx context.Context, userID string) (*core.MainProfileSnapshot, error) {
	return s.profiles.Snapshot(ctx, userID)
}

// ReplaceCareerGraph promotes Profiles.ReplaceCareerGraph onto Store.
func (s *Store) ReplaceCareerGraph(ctx context.Context, userID string, snapshot *core.MainProfileSnapshot) error {
	return s.profiles.ReplaceCareerGraph(ctx, userID, snapshot)
}

// Preset promotes Presets.Preset onto Store.
func (s *Store) Preset(ctx context.Context, userID, id string) (*core.Preset, error) {
	return s.presets.Preset(ctx, userID, id)
}

// ListPresets promotes Presets.ListPresets onto Store.
func (s *Store) ListPresets(ctx context.Context, userID string) ([]core.Preset, error) {
	return s.presets.ListPresets(ctx, userID)
}

// Template promotes Templates.Template onto Store.
func (s *Store) Template(ctx context.Context, userID, id string) (*core.TemplateSet, error) {
	return s.templates.Template(ctx, userID, id)
}

// ListTemplates promotes Templates.ListTemplates onto Store.
func (s *Store) ListTemplates(ctx context.Context, userID string) ([]core.TemplateSet, error) {
	return s.templates.ListTemplates(ctx, userID)
}

// SubmitBranchTailor promotes Jobs.SubmitBranchTailor onto Store.
func (s *Store) SubmitBranchTailor(ctx context.Context, job *core.Job, version *core.BranchVersion, opts core.PendingJobOptions) error {
	return s.jobs.SubmitBranchTailor(ctx, job, version, opts)
}

// SubmitPDFParse promotes Jobs.SubmitPDFParse onto Store.
func (s *Store) SubmitPDFParse(ctx context.Context, job *core.Job, opts core.PendingJobOptions) error {
	return s.jobs.SubmitPDFParse(ctx, job, opts)
}

// SupersedeBranchTailor promotes Jobs.SupersedeBranchTailor onto Store.
func (s *Store) SupersedeBranchTailor(ctx context.Context, branchID, reason string) error {
	return s.jobs.SupersedeBranchTailor(ctx, branchID, reason)
}

// AbortBranchJobs promotes Jobs.AbortBranchJobs onto Store.
func (s *Store) AbortBranchJobs(ctx context.Context, branchID, reason string) error {
	return s.jobs.AbortBranchJobs(ctx, branchID, reason)
}

// Job promotes Jobs.Job onto Store.
func (s *Store) Job(ctx context.Context, userID, jobID string) (*core.Job, error) {
	return s.jobs.Job(ctx, userID, jobID)
}

// ClaimNext promotes Jobs.ClaimNext onto Store.
func (s *Store) ClaimNext(ctx context.Context, req core.ClaimJobRequest) (*core.Job, error) {
	return s.jobs.ClaimNext(ctx, req)
}

// RenewLease promotes Jobs.RenewLease onto Store.
func (s *Store) RenewLease(ctx context.Context, jobID, claimedBy string, leaseExpiresAt time.Time) error {
	return s.jobs.RenewLease(ctx, jobID, claimedBy, leaseExpiresAt)
}

// RequeueForRetry promotes Jobs.RequeueForRetry onto Store.
func (s *Store) RequeueForRetry(ctx context.Context, jobID, claimedBy string, nextRetryAt time.Time, errMsg string) error {
	return s.jobs.RequeueForRetry(ctx, jobID, claimedBy, nextRetryAt, errMsg)
}

// CompleteBranchTailor promotes Jobs.CompleteBranchTailor onto Store.
func (s *Store) CompleteBranchTailor(ctx context.Context, jobID, claimedBy string, completed *core.BranchVersion) error {
	return s.jobs.CompleteBranchTailor(ctx, jobID, claimedBy, completed)
}

// CompletePDFParse promotes Jobs.CompletePDFParse onto Store.
func (s *Store) CompletePDFParse(ctx context.Context, jobID, claimedBy string, result *core.JSONResume) error {
	return s.jobs.CompletePDFParse(ctx, jobID, claimedBy, result)
}

// FailClaimed promotes Jobs.FailClaimed onto Store.
func (s *Store) FailClaimed(ctx context.Context, req core.FailClaimedRequest) error {
	return s.jobs.FailClaimed(ctx, req)
}

// ExpirePending promotes Jobs.ExpirePending onto Store.
func (s *Store) ExpirePending(ctx context.Context, now time.Time, reason string) (int, error) {
	return s.jobs.ExpirePending(ctx, now, reason)
}

// FailExpiredLeases promotes Jobs.FailExpiredLeases onto Store.
func (s *Store) FailExpiredLeases(ctx context.Context, now time.Time, reason string) (int, error) {
	return s.jobs.FailExpiredLeases(ctx, now, reason)
}

// RecoverInterrupted promotes Jobs.RecoverInterrupted onto Store.
func (s *Store) RecoverInterrupted(ctx context.Context, reason string) (int, error) {
	return s.jobs.RecoverInterrupted(ctx, reason)
}

// ReserveQuota promotes Quotas.ReserveQuota onto Store.
func (s *Store) ReserveQuota(ctx context.Context, userID string, op core.LLMOperationType, estimatedTokens, dailyLimit, monthlyLimit int) (*core.LLMUsageRecord, error) {
	return s.quotas.ReserveQuota(ctx, userID, op, estimatedTokens, dailyLimit, monthlyLimit)
}

// CommitUsage promotes Quotas.CommitUsage onto Store.
func (s *Store) CommitUsage(ctx context.Context, reservationID string, telemetry core.LLMTelemetry) error {
	return s.quotas.CommitUsage(ctx, reservationID, telemetry)
}

// ReleaseQuota promotes Quotas.ReleaseQuota onto Store.
func (s *Store) ReleaseQuota(ctx context.Context, reservationID, reason string, telemetry core.LLMTelemetry) error {
	return s.quotas.ReleaseQuota(ctx, reservationID, reason, telemetry)
}

// Quota promotes Quotas.Quota onto Store.
func (s *Store) Quota(ctx context.Context, userID string) (*core.TenantQuota, error) {
	return s.quotas.Quota(ctx, userID)
}

// ListUsageRecords promotes Quotas.ListUsageRecords onto Store.
func (s *Store) ListUsageRecords(ctx context.Context, userID string, limit int) ([]core.LLMUsageRecord, error) {
	return s.quotas.ListUsageRecords(ctx, userID, limit)
}
