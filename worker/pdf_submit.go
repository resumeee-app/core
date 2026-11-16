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

// PDFSubmitOption configures optional quota reservation, admission bounds, TTL, and notification settings on SubmitPDFJob.
type PDFSubmitOption func(*pdfSubmitOptions)

// pdfSubmitOptions holds resolved optional configuration parameters for SubmitPDFJob.
type pdfSubmitOptions struct {
	quotas              PDFQuotaReserver
	notifier            core.JobNotifier
	estimatedTokens     int
	maxPendingPerTenant int
	maxGlobalPending    int
	maxRawTextBytes     int
	ttl                 time.Duration
	cleanupTimeout      time.Duration
	clock               func() time.Time
}

// WithPDFQuotas configures optional quota reservation and release tracking for SubmitPDFJob.
func WithPDFQuotas(quotas PDFQuotaReserver) PDFSubmitOption {
	return func(o *pdfSubmitOptions) {
		o.quotas = quotas
	}
}

// WithPDFNotifier configures an optional worker pool wakeup notifier for SubmitPDFJob.
func WithPDFNotifier(notifier core.JobNotifier) PDFSubmitOption {
	return func(o *pdfSubmitOptions) {
		o.notifier = notifier
	}
}

// WithPDFEstimatedTokens overrides the estimated quota token reservation when tokens > 0.
func WithPDFEstimatedTokens(tokens int) PDFSubmitOption {
	return func(o *pdfSubmitOptions) {
		if tokens > 0 {
			o.estimatedTokens = tokens
		}
	}
}

// WithPDFMaxPendingPerTenant overrides the per-tenant queued/in-flight job cap when limit > 0.
func WithPDFMaxPendingPerTenant(limit int) PDFSubmitOption {
	return func(o *pdfSubmitOptions) {
		if limit > 0 {
			o.maxPendingPerTenant = limit
		}
	}
}

// WithPDFMaxGlobalPending sets an optional global queued/in-flight job cap when limit > 0.
func WithPDFMaxGlobalPending(limit int) PDFSubmitOption {
	return func(o *pdfSubmitOptions) {
		if limit > 0 {
			o.maxGlobalPending = limit
		}
	}
}

// WithPDFMaxRawTextBytes overrides the maximum accepted raw text byte payload when limit > 0.
func WithPDFMaxRawTextBytes(limit int) PDFSubmitOption {
	return func(o *pdfSubmitOptions) {
		if limit > 0 {
			o.maxRawTextBytes = limit
		}
	}
}

// WithPDFJobTTL overrides the job expiration duration when ttl > 0.
func WithPDFJobTTL(ttl time.Duration) PDFSubmitOption {
	return func(o *pdfSubmitOptions) {
		if ttl > 0 {
			o.ttl = ttl
		}
	}
}

// WithPDFCleanupTimeout overrides the detached quota-refund cleanup timeout when timeout > 0.
func WithPDFCleanupTimeout(timeout time.Duration) PDFSubmitOption {
	return func(o *pdfSubmitOptions) {
		if timeout > 0 {
			o.cleanupTimeout = timeout
		}
	}
}

// WithPDFClock overrides the wall clock function used by SubmitPDFJob when clock is non-nil.
func WithPDFClock(clock func() time.Time) PDFSubmitOption {
	return func(o *pdfSubmitOptions) {
		if clock != nil {
			o.clock = clock
		}
	}
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
func SubmitPDFJob(ctx context.Context, jobs JobStore, sub PDFJobSubmission, opts ...PDFSubmitOption) (*core.Job, error) {
	if jobs == nil {
		return nil, fmt.Errorf("%w: job store is required", core.ErrValidation)
	}
	o := pdfSubmitOptions{
		estimatedTokens:     defaultPDFExtractionTokens,
		maxPendingPerTenant: core.DefaultMaxPendingPerTenant,
		maxRawTextBytes:     core.DefaultMaxPDFRawTextBytes,
		ttl:                 core.DefaultJobTTL,
		cleanupTimeout:      defaultCleanupTimeout,
		clock:               time.Now,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	userID := strings.TrimSpace(sub.UserID)
	if userID == "" {
		return nil, fmt.Errorf("%w: user id is required", core.ErrValidation)
	}
	if strings.TrimSpace(sub.RawText) == "" {
		return nil, fmt.Errorf("%w: raw text is required", core.ErrValidation)
	}
	if len(sub.RawText) > o.maxRawTextBytes {
		return nil, fmt.Errorf("%w: raw text exceeds maximum allowed size of %d bytes", core.ErrValidation, o.maxRawTextBytes)
	}

	now := o.clock().UTC()

	var reservationID string
	if o.quotas != nil {
		rec, err := o.quotas.ReserveQuota(ctx, userID, core.OperationPDFParse, o.estimatedTokens, sub.DailyLimit, sub.MonthlyLimit)
		if err != nil {
			return nil, err
		}
		if rec != nil {
			reservationID = rec.ID
		}
	}

	expiresAt := now.Add(o.ttl)
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

	err := jobs.SubmitPDFParse(ctx, job, core.PendingJobOptions{
		MaxPendingPerTenant: o.maxPendingPerTenant,
		MaxGlobalPending:    o.maxGlobalPending,
		MaxRawTextBytes:     o.maxRawTextBytes,
		TTL:                 o.ttl,
		Now:                 now,
	})
	if err != nil {
		if o.quotas != nil && reservationID != "" {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), o.cleanupTimeout)
			defer cancel()
			_ = o.quotas.ReleaseQuota(cleanupCtx, reservationID, sanitizePDFSubmitFailureReason(err), core.LLMTelemetry{})
		}
		return nil, err
	}

	if o.notifier != nil {
		o.notifier.Notify()
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
