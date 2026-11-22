package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/resumeee-app/core"
)

// ErrEmptyPDFExtraction indicates the configured ResumeExtractor succeeded with no JSONResume result.
var ErrEmptyPDFExtraction = errors.New("resume extractor returned no result")

// PDFParseRequest captures the user identity and raw document payload for synchronous or asynchronous PDF parsing.
type PDFParseRequest struct {
	UserID   string
	Filename string
	Provider string
	Model    string
	RawText  string
}

// PDFCoordinatorConfig configures quota limits, admission caps, TTL, and clock overrides for PDFCoordinator.
type PDFCoordinatorConfig struct {
	DailyLimit          int
	MonthlyLimit        int
	EstimatedTokens     int
	FallbackTokens      int
	MaxPendingPerTenant int
	MaxGlobalPending    int
	MaxRawTextBytes     int
	TTL                 time.Duration
	CleanupTimeout      time.Duration
	DisableQuotas       bool
	Logger              *slog.Logger
	Clock               func() time.Time
}

// PDFCoordinator coordinates synchronous and asynchronous PDF resume parsing over a unified store and quota seam.
type PDFCoordinator struct {
	store     Store
	extractor ResumeExtractor
	notifier  core.JobNotifier
	scope     *core.QuotaScope
	cfg       PDFCoordinatorConfig
}

// NewPDFCoordinator constructs a PDFCoordinator configured with defaults for unset positive limits and timeouts.
func NewPDFCoordinator(store Store, extractor ResumeExtractor, notifier core.JobNotifier, cfg PDFCoordinatorConfig) *PDFCoordinator {
	if cfg.DailyLimit <= 0 {
		cfg.DailyLimit = core.DefaultDailyRequestLimit
	}
	if cfg.MonthlyLimit <= 0 {
		cfg.MonthlyLimit = core.DefaultMonthlyTokenLimit
	}
	if cfg.EstimatedTokens <= 0 {
		cfg.EstimatedTokens = defaultPDFExtractionTokens
	}
	if cfg.FallbackTokens <= 0 {
		cfg.FallbackTokens = defaultPDFExtractionTokens
	}
	if cfg.MaxPendingPerTenant <= 0 {
		cfg.MaxPendingPerTenant = core.DefaultMaxPendingPerTenant
	}
	if cfg.MaxRawTextBytes <= 0 {
		cfg.MaxRawTextBytes = core.DefaultMaxPDFRawTextBytes
	}
	if cfg.TTL <= 0 {
		cfg.TTL = core.DefaultJobTTL
	}
	if cfg.CleanupTimeout <= 0 {
		cfg.CleanupTimeout = defaultCleanupTimeout
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}

	var scope *core.QuotaScope
	if !cfg.DisableQuotas && store != nil {
		scope = core.NewQuotaScope(
			store,
			core.WithQuotaScopeLimits(cfg.DailyLimit, cfg.MonthlyLimit),
			core.WithQuotaScopeSettleTimeout(cfg.CleanupTimeout),
			core.WithQuotaScopeLogger(cfg.Logger),
		)
	}

	return &PDFCoordinator{
		store:     store,
		extractor: extractor,
		notifier:  notifier,
		scope:     scope,
		cfg:       cfg,
	}
}

// validatePDFRequest validates tenant ownership, raw text presence, and payload size bounds.
func (c *PDFCoordinator) validatePDFRequest(req PDFParseRequest) (string, error) {
	userID := strings.TrimSpace(req.UserID)
	if userID == "" {
		return "", fmt.Errorf("%w: user id is required", core.ErrValidation)
	}
	if strings.TrimSpace(req.RawText) == "" {
		return "", fmt.Errorf("%w: raw text is required", core.ErrValidation)
	}
	if len(req.RawText) > c.cfg.MaxRawTextBytes {
		return "", fmt.Errorf("%w: raw text exceeds maximum allowed size of %d bytes", core.ErrValidation, c.cfg.MaxRawTextBytes)
	}
	return userID, nil
}

// ParseSync validates inputs, reserves tenant quota (when enabled), invokes ResumeExtractor,
// commits usage or releases quota with a sanitized reason, and returns the extracted JSONResume.
func (c *PDFCoordinator) ParseSync(ctx context.Context, req PDFParseRequest) (*core.JSONResume, error) {
	if c == nil || c.extractor == nil {
		return nil, fmt.Errorf("%w: resume extractor is required", core.ErrValidation)
	}
	userID, err := c.validatePDFRequest(req)
	if err != nil {
		return nil, err
	}

	var extracted *core.JSONResume
	err = c.scope.Run(
		ctx,
		userID,
		core.OperationPDFParse,
		c.cfg.EstimatedTokens,
		c.cfg.FallbackTokens,
		pdfSyncReleaseReason,
		func(callCtx context.Context) (core.LLMTelemetry, error) {
			res, tel, extractErr := c.extractor.ExtractResume(callCtx, req.RawText, req.Provider, req.Model)
			if extractErr == nil && res == nil {
				extractErr = ErrEmptyPDFExtraction
			}
			if extractErr != nil {
				return tel, extractErr
			}
			extracted = res
			return tel, nil
		},
	)
	if err != nil {
		return nil, err
	}
	return extracted, nil
}

// SubmitAsync atomically validates admission caps, reserves quota (when enabled), persists a pending
// pdf_parse Job, and wakes background workers via JobNotifier.
func (c *PDFCoordinator) SubmitAsync(ctx context.Context, req PDFParseRequest) (*core.Job, error) {
	if c == nil || c.store == nil {
		return nil, fmt.Errorf("%w: store is required", core.ErrValidation)
	}
	userID, err := c.validatePDFRequest(req)
	if err != nil {
		return nil, err
	}

	now := c.cfg.Clock().UTC()
	expiresAt := now.Add(c.cfg.TTL)
	job := &core.Job{
		ID:        core.GenerateID("pdf"),
		Kind:      core.JobKindPDFParse,
		UserID:    userID,
		Status:    core.JobStatusPending,
		ExpiresAt: &expiresAt,
		PDF: &core.PDFParseSpec{
			Filename: req.Filename,
			Provider: req.Provider,
			Model:    req.Model,
			RawText:  req.RawText,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	var quotaSpec *core.QuotaReservationSpec
	if !c.cfg.DisableQuotas {
		quotaSpec = &core.QuotaReservationSpec{
			Operation:       core.OperationPDFParse,
			EstimatedTokens: c.cfg.EstimatedTokens,
			DailyLimit:      c.cfg.DailyLimit,
			MonthlyLimit:    c.cfg.MonthlyLimit,
		}
	}

	if err := c.store.SubmitPDFParse(ctx, job, core.PendingJobOptions{
		MaxPendingPerTenant: c.cfg.MaxPendingPerTenant,
		MaxGlobalPending:    c.cfg.MaxGlobalPending,
		MaxRawTextBytes:     c.cfg.MaxRawTextBytes,
		TTL:                 c.cfg.TTL,
		Now:                 now,
		Quota:               quotaSpec,
	}); err != nil {
		return nil, err
	}

	if c.notifier != nil {
		c.notifier.Notify()
	}
	return job, nil
}

// pdfSyncReleaseReason maps synchronous extraction errors onto non-sensitive diagnostics for quota records.
func pdfSyncReleaseReason(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "resume extraction timed out"
	case errors.Is(err, core.ErrValidation):
		return "document validation failed during extraction"
	default:
		return "resume extraction failed"
	}
}
