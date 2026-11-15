package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

// PDFQuotaReserver reserves and releases tenant LLM quota units during PDF job submission and cancellation.
type PDFQuotaReserver interface {
	ReserveQuota(ctx context.Context, userID string, op core.LLMOperationType, estimatedTokens, dailyLimit, monthlyLimit int) (*core.LLMUsageRecord, error)
	ReleaseQuota(ctx context.Context, reservationID string, reason string, telemetry core.LLMTelemetry) error
}

// QuotaReleaser releases reserved tenant quota units when a pending job is cancelled or rejected.
type QuotaReleaser interface {
	ReleaseQuota(ctx context.Context, reservationID string, reason string, telemetry core.LLMTelemetry) error
}

// PDFSubmitConfig defines stores, admission bounds, TTL, and notification dependencies for SubmitPDFJob.
type PDFSubmitConfig struct {
	Jobs                   JobStore
	Quotas                 PDFQuotaReserver
	Notifier               core.JobNotifier
	DefaultEstimatedTokens int
	MaxPendingPerTenant    int
	MaxGlobalPending       int
	MaxRawTextBytes        int
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
func SubmitPDFJob(ctx context.Context, cfg PDFSubmitConfig, sub PDFJobSubmission) (*core.Job, error) {
	if cfg.Jobs == nil {
		return nil, fmt.Errorf("%w: job store is required", core.ErrValidation)
	}
	userID := strings.TrimSpace(sub.UserID)
	if userID == "" {
		return nil, fmt.Errorf("%w: user id is required", core.ErrValidation)
	}
	if strings.TrimSpace(sub.RawText) == "" {
		return nil, fmt.Errorf("%w: raw text is required", core.ErrValidation)
	}
	maxRawTextBytes := cfg.MaxRawTextBytes
	if maxRawTextBytes <= 0 {
		maxRawTextBytes = core.DefaultMaxPDFRawTextBytes
	}
	if len(sub.RawText) > maxRawTextBytes {
		return nil, fmt.Errorf("%w: raw text exceeds maximum allowed size of %d bytes", core.ErrValidation, maxRawTextBytes)
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
	job := &core.Job{
		ID:            core.GenerateID("pdf"),
		Kind:          core.JobKindPDFParse,
		UserID:        userID,
		Status:        core.JobStatusPending,
		ReservationID: reservationID,
		ExpiresAt:     &expiresAt,
		PDF: &core.PDFParseSpec{
			Filename: sub.Filename,
			Provider: sub.Provider,
			Model:    sub.Model,
			RawText:  sub.RawText,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	err := cfg.Jobs.SubmitPDFParse(ctx, job, core.PendingJobOptions{
		MaxPendingPerTenant: maxPendingPerTenant,
		MaxGlobalPending:    cfg.MaxGlobalPending,
		MaxRawTextBytes:     maxRawTextBytes,
		TTL:                 ttl,
		Now:                 now,
	})
	if err != nil {
		if cfg.Quotas != nil && reservationID != "" {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
			defer cancel()
			_ = cfg.Quotas.ReleaseQuota(cleanupCtx, reservationID, sanitizePDFSubmitFailureReason(err), core.LLMTelemetry{})
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
	jobs JobStore,
	quotas QuotaReleaser,
	userID string,
	jobID string,
	cleanupTimeout time.Duration,
) (*core.Job, error) {
	if jobs == nil {
		return nil, fmt.Errorf("%w: job store is required", core.ErrValidation)
	}
	trimmedUser := strings.TrimSpace(userID)
	trimmedJob := strings.TrimSpace(jobID)
	if trimmedUser == "" || trimmedJob == "" {
		return nil, fmt.Errorf("%w: user id and job id are required", core.ErrValidation)
	}

	cancelled, err := jobs.CancelJob(ctx, trimmedUser, trimmedJob, "cancelled by user")
	if err != nil {
		return nil, err
	}

	if quotas != nil && cancelled != nil && cancelled.ReservationID != "" {
		if cleanupTimeout <= 0 {
			cleanupTimeout = defaultCleanupTimeout
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		_ = quotas.ReleaseQuota(cleanupCtx, cancelled.ReservationID, "cancelled by user", core.LLMTelemetry{})
	}
	return cancelled, nil
}

// sanitizePDFSubmitFailureReason maps a pending PDF job admission error to a bounded non-sensitive quota release reason.
func sanitizePDFSubmitFailureReason(err error) string {
	switch {
	case errors.Is(err, core.ErrPendingLimitExceeded):
		return "pending job limit exceeded"
	case errors.Is(err, core.ErrValidation):
		return "invalid pdf job submission"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "pdf job submission cancelled or timed out"
	default:
		return "pdf job submission rejected"
	}
}
