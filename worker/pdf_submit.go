package worker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

// PDFQuotaReserver reserves and releases tenant LLM quota units during PDF job submission and cancellation.
type PDFQuotaReserver interface {
	ReserveQuota(ctx context.Context, userID string, op core.LLMOperationType, estimatedTokens, dailyLimit, monthlyLimit int) (*core.LLMUsageRecord, error)
	ReleaseQuota(ctx context.Context, reservationID string, reason string) error
}

// QuotaReleaser releases reserved tenant quota units when a pending job is cancelled or rejected.
type QuotaReleaser interface {
	ReleaseQuota(ctx context.Context, reservationID string, reason string) error
}

// PDFSubmitConfig defines stores, admission bounds, TTL, and notification dependencies for SubmitPDFJob.
type PDFSubmitConfig struct {
	PDFJobs                PDFJobStore
	Quotas                 PDFQuotaReserver
	Notifier               core.JobNotifier
	DefaultEstimatedTokens int
	MaxPendingPerTenant    int
	MaxGlobalPending       int
	JobTTL                 time.Duration
	CleanupTimeout         time.Duration
	Clock                  func() time.Time
}

// PDFJobSubmission specifies the candidate payload and quota ceiling parameters for a new PDF parse job.
type PDFJobSubmission struct {
	UserID       string
	Filename     string
	Provider     string
	Model        string
	RawText      string
	DailyLimit   int
	MonthlyLimit int
}

// SubmitPDFJob validates submission inputs, reserves quota when configured, persists a pending job under
// atomic admission caps, refunds quota via a detached context on rejection, and wakes background workers.
func SubmitPDFJob(ctx context.Context, cfg PDFSubmitConfig, sub PDFJobSubmission) (*core.PDFParseJob, error) {
	if cfg.PDFJobs == nil {
		return nil, fmt.Errorf("%w: pdf job store is required", core.ErrValidation)
	}
	userID := strings.TrimSpace(sub.UserID)
	if userID == "" {
		return nil, fmt.Errorf("%w: user id is required", core.ErrValidation)
	}
	if strings.TrimSpace(sub.RawText) == "" {
		return nil, fmt.Errorf("%w: raw text is required", core.ErrValidation)
	}

	estimatedTokens := cfg.DefaultEstimatedTokens
	if estimatedTokens <= 0 {
		estimatedTokens = defaultPDFExtractionTokens
	}
	maxPendingPerTenant := cfg.MaxPendingPerTenant
	if maxPendingPerTenant <= 0 {
		maxPendingPerTenant = core.DefaultMaxPendingPerTenant
	}
	ttl := cfg.JobTTL
	if ttl <= 0 {
		ttl = core.DefaultJobTTL
	}
	cleanupTimeout := cfg.CleanupTimeout
	if cleanupTimeout <= 0 {
		cleanupTimeout = defaultCleanupTimeout
	}
	now := time.Now().UTC()
	if cfg.Clock != nil {
		now = cfg.Clock().UTC()
	}

	var reservationID string
	if cfg.Quotas != nil {
		rec, err := cfg.Quotas.ReserveQuota(ctx, userID, core.OperationPDFParse, estimatedTokens, sub.DailyLimit, sub.MonthlyLimit)
		if err != nil {
			return nil, err
		}
		if rec != nil {
			reservationID = rec.ID
		}
	}

	expiresAt := now.Add(ttl)
	job := &core.PDFParseJob{
		ID:            core.GenerateID("pdf"),
		UserID:        userID,
		Status:        core.PDFParseJobStatusPending,
		Filename:      sub.Filename,
		Provider:      sub.Provider,
		Model:         sub.Model,
		RawText:       sub.RawText,
		ReservationID: reservationID,
		ExpiresAt:     &expiresAt,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	err := cfg.PDFJobs.SubmitPendingJob(ctx, job, core.PendingJobOptions{
		MaxPendingPerTenant: maxPendingPerTenant,
		MaxGlobalPending:    cfg.MaxGlobalPending,
		TTL:                 ttl,
		Now:                 now,
	})
	if err != nil {
		if cfg.Quotas != nil && reservationID != "" {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
			defer cancel()
			_ = cfg.Quotas.ReleaseQuota(cleanupCtx, reservationID, err.Error())
		}
		return nil, err
	}

	if cfg.Notifier != nil {
		cfg.Notifier.Notify()
	}
	return job, nil
}

// CancelPDFJob cancels an owned pending PDF parse job and releases its reserved quota via detached context.
func CancelPDFJob(
	ctx context.Context,
	pdfJobs PDFJobStore,
	quotas QuotaReleaser,
	userID string,
	jobID string,
	cleanupTimeout time.Duration,
) (*core.PDFParseJob, error) {
	if pdfJobs == nil {
		return nil, fmt.Errorf("%w: pdf job store is required", core.ErrValidation)
	}
	trimmedUser := strings.TrimSpace(userID)
	trimmedJob := strings.TrimSpace(jobID)
	if trimmedUser == "" || trimmedJob == "" {
		return nil, fmt.Errorf("%w: user id and job id are required", core.ErrValidation)
	}

	cancelled, err := pdfJobs.CancelJob(ctx, trimmedUser, trimmedJob, "cancelled by user")
	if err != nil {
		return nil, err
	}

	if quotas != nil && cancelled != nil && cancelled.ReservationID != "" {
		if cleanupTimeout <= 0 {
			cleanupTimeout = defaultCleanupTimeout
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		_ = quotas.ReleaseQuota(cleanupCtx, cancelled.ReservationID, "cancelled by user")
	}
	return cancelled, nil
}
