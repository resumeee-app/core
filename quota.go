package core

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

// Default tenant quota ceilings, settlement timeout, and usage history query bounds.
const (
	// DefaultDailyRequestLimit caps daily LLM requests for newly provisioned tenants.
	DefaultDailyRequestLimit = 50

	// DefaultMonthlyTokenLimit caps monthly LLM tokens for newly provisioned tenants.
	DefaultMonthlyTokenLimit = 1000000

	// DefaultUsageListLimit bounds usage history queries when callers omit a positive limit.
	DefaultUsageListLimit = 50

	// defaultQuotaSettleTimeout bounds detached quota commit and release calls regardless of caller cancellation.
	defaultQuotaSettleTimeout = 10 * time.Second

	// maxQuotaErrorMessageRunes bounds sanitized diagnostic strings written onto released usage records.
	maxQuotaErrorMessageRunes = 256

	// panicQuotaReleaseReason records why a reservation was refunded when a scoped callback panics or aborts.
	panicQuotaReleaseReason = "operation panicked"
)

// QuotaReservationSpec carries the parameters required to atomically reserve quota for an AI operation.
type QuotaReservationSpec struct {
	Operation       LLMOperationType
	EstimatedTokens int
	DailyLimit      int
	MonthlyLimit    int
}

// QuotaManager reserves, commits, and releases tenant LLM quota reservations and call telemetry.
type QuotaManager interface {
	ReserveQuota(ctx context.Context, userID string, op LLMOperationType, estimatedTokens, dailyLimit, monthlyLimit int) (*LLMUsageRecord, error)
	CommitUsage(ctx context.Context, reservationID string, telemetry LLMTelemetry) error
	ReleaseQuota(ctx context.Context, reservationID, reason string, telemetry LLMTelemetry) error
}

// QuotaScopeOption configures optional settlement behavior on a QuotaScope.
type QuotaScopeOption func(*QuotaScope)

// WithQuotaScopeLimits sets positive daily request and monthly token ceilings enforced by QuotaScope reservations.
func WithQuotaScopeLimits(dailyLimit, monthlyLimit int) QuotaScopeOption {
	return func(s *QuotaScope) {
		if dailyLimit > 0 {
			s.dailyLimit = dailyLimit
		}
		if monthlyLimit > 0 {
			s.monthlyLimit = monthlyLimit
		}
	}
}

// WithQuotaScopeSettleTimeout sets the detached timeout budget used when settling reservations.
func WithQuotaScopeSettleTimeout(timeout time.Duration) QuotaScopeOption {
	return func(s *QuotaScope) {
		if timeout > 0 {
			s.settleTimeout = timeout
		}
	}
}

// WithQuotaScopeLogger configures structured diagnostic logging for QuotaScope settlement failures.
func WithQuotaScopeLogger(logger *slog.Logger) QuotaScopeOption {
	return func(s *QuotaScope) {
		s.logger = logger
	}
}

// QuotaScope manages reserve, detached commit, and detached release lifecycles for synchronous AI operations.
type QuotaScope struct {
	quotas        QuotaManager
	dailyLimit    int
	monthlyLimit  int
	settleTimeout time.Duration
	logger        *slog.Logger
}

// NewQuotaScope constructs a QuotaScope backed by quotas (may be nil to skip quota enforcement).
func NewQuotaScope(quotas QuotaManager, opts ...QuotaScopeOption) *QuotaScope {
	s := &QuotaScope{
		quotas:        quotas,
		dailyLimit:    DefaultDailyRequestLimit,
		monthlyLimit:  DefaultMonthlyTokenLimit,
		settleTimeout: defaultQuotaSettleTimeout,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

// FormatQuotaErrorMessage flattens whitespace and bounds failure diagnostics stored on released usage records.
func FormatQuotaErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	clean := strings.Join(strings.Fields(err.Error()), " ")
	runes := []rune(clean)
	if len(runes) > maxQuotaErrorMessageRunes {
		return string(runes[:maxQuotaErrorMessageRunes]) + "..."
	}
	return clean
}

// Run reserves quota before invoking fn (when QuotaManager is configured) and settles the reservation
// using a bounded detached context so caller timeouts or panics never leak quota.
func (s *QuotaScope) Run(
	ctx context.Context,
	userID string,
	op LLMOperationType,
	estimatedTokens int,
	fallbackTokens int,
	releaseReason func(error) string,
	fn func(context.Context) (LLMTelemetry, error),
) error {
	if fn == nil {
		return nil
	}
	if s == nil || s.quotas == nil {
		_, err := fn(ctx)
		return err
	}

	reserveCtx := ctx
	if reserveCtx == nil {
		reserveCtx = context.Background()
	}
	rec, err := s.quotas.ReserveQuota(reserveCtx, userID, op, estimatedTokens, s.dailyLimit, s.monthlyLimit)
	if err != nil {
		return err
	}

	settled := false
	if rec != nil && rec.ID != "" {
		// Guard against panics or runtime.Goexit inside fn (or releaseReason) before normal settlement runs.
		defer func() {
			if !settled {
				s.release(reserveCtx, rec.ID, panicQuotaReleaseReason, LLMTelemetry{})
			}
		}()
	}

	telemetry, retErr := fn(reserveCtx)
	if rec == nil || rec.ID == "" {
		return retErr
	}

	normalized := ResolveTelemetry(telemetry, retErr)
	if retErr != nil {
		reason := FormatQuotaErrorMessage(retErr)
		if releaseReason != nil {
			if custom := strings.TrimSpace(releaseReason(retErr)); custom != "" {
				reason = custom
			}
		}
		settled = true
		s.release(reserveCtx, rec.ID, reason, normalized)
		return retErr
	}

	if fallbackTokens > 0 {
		normalized.Usage = normalized.Usage.WithFallback(fallbackTokens)
	}
	settled = true
	settleCtx, cancel := s.settleContext(reserveCtx)
	defer cancel()
	if cErr := s.quotas.CommitUsage(settleCtx, rec.ID, normalized); cErr != nil && s.logger != nil {
		s.logger.Error("failed to commit quota usage", "reservation_id", rec.ID, "err", cErr)
	}
	return nil
}

// release refunds reservationID under a detached bounded context and logs any persistence failure.
func (s *QuotaScope) release(parent context.Context, reservationID, reason string, telemetry LLMTelemetry) {
	settleCtx, cancel := s.settleContext(parent)
	defer cancel()
	if rErr := s.quotas.ReleaseQuota(settleCtx, reservationID, reason, telemetry); rErr != nil && s.logger != nil {
		s.logger.Error("failed to release quota reservation", "reservation_id", reservationID, "reason", reason, "err", rErr)
	}
}

// settleContext returns a detached bounded context derived from parent for post-operation quota settlement.
func (s *QuotaScope) settleContext(parent context.Context) (context.Context, context.CancelFunc) {
	timeout := s.settleTimeout
	if timeout <= 0 {
		timeout = defaultQuotaSettleTimeout
	}
	base := parent
	if base == nil {
		base = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(base), timeout)
}

// LLMOperationType identifies the category of LLM generation task for quota enforcement.
type LLMOperationType string

// Supported LLM operation categories tracked by quota accounting.
const (
	// OperationResumeTailoring covers structured resume generation passes.
	OperationResumeTailoring LLMOperationType = "resume_tailoring"

	// OperationGapAnalysis covers job-requirement gap analysis passes.
	OperationGapAnalysis LLMOperationType = "gap_analysis"

	// OperationPDFParse covers unstructured resume extraction passes.
	OperationPDFParse LLMOperationType = "pdf_parse"

	// OperationATSSimulation covers simulated ATS extraction and matching passes.
	OperationATSSimulation LLMOperationType = "ats_simulation"
)

// ReservationStatus tracks the lifecycle of an atomic LLM quota reservation.
type ReservationStatus string

// Lifecycle states for an atomic LLM quota reservation record.
const (
	// ReservationStatusPending indicates tokens are provisionally reserved for an in-flight call.
	ReservationStatusPending ReservationStatus = "pending"

	// ReservationStatusCommitted indicates actual token usage has been finalized.
	ReservationStatusCommitted ReservationStatus = "committed"

	// ReservationStatusReleased indicates a failed or aborted reservation was refunded.
	ReservationStatusReleased ReservationStatus = "released"
)

// TenantQuota tracks aggregate daily requests and monthly token ceilings for a tenant.
type TenantQuota struct {
	UserID             string    `json:"user_id"`
	DailyRequestsUsed  int       `json:"daily_requests_used"`
	DailyRequestLimit  int       `json:"daily_request_limit"`
	DailyWindowStart   time.Time `json:"daily_window_start"`
	MonthlyTokensUsed  int       `json:"monthly_tokens_used"`
	MonthlyTokenLimit  int       `json:"monthly_token_limit"`
	MonthlyWindowStart time.Time `json:"monthly_window_start"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// ResetWindowsIfExpired checks daily and monthly window expirations and resets counters if expired.
// Returns true if either window was reset.
func (q *TenantQuota) ResetWindowsIfExpired(now time.Time) bool {
	var reset bool
	if now.Sub(q.DailyWindowStart) >= 24*time.Hour {
		q.DailyRequestsUsed = 0
		q.DailyWindowStart = now
		reset = true
	}
	if now.Sub(q.MonthlyWindowStart) >= 30*24*time.Hour {
		q.MonthlyTokensUsed = 0
		q.MonthlyWindowStart = now
		reset = true
	}
	return reset
}

// AdjustMonthlyTokens applies a token delta (e.g. actual usage - estimate) and clamps to non-negative.
func (q *TenantQuota) AdjustMonthlyTokens(delta int) {
	q.MonthlyTokensUsed += delta
	if q.MonthlyTokensUsed < 0 {
		q.MonthlyTokensUsed = 0
	}
}

// ReleaseReservation decrements daily requests and refunds estimated tokens, clamping to non-negative.
func (q *TenantQuota) ReleaseReservation(estimatedTokens int) {
	q.DailyRequestsUsed--
	if q.DailyRequestsUsed < 0 {
		q.DailyRequestsUsed = 0
	}
	q.MonthlyTokensUsed -= estimatedTokens
	if q.MonthlyTokensUsed < 0 {
		q.MonthlyTokensUsed = 0
	}
}

// LLMUsageRecord records an individual LLM operation reservation, provider telemetry,
// token metrics, and terminal status.
type LLMUsageRecord struct {
	ID                 string            `json:"id"`
	UserID             string            `json:"user_id"`
	Operation          LLMOperationType  `json:"operation"`
	Provider           string            `json:"provider,omitempty"`
	Model              string            `json:"model,omitempty"`
	StatusCode         int               `json:"status_code,omitempty"`
	Latency            time.Duration     `json:"latency,omitempty"`
	EstimatedTokens    int               `json:"estimated_tokens"`
	PromptTokens       int               `json:"prompt_tokens"`
	CompletionTokens   int               `json:"completion_tokens"`
	TotalTokens        int               `json:"total_tokens"`
	Status             ReservationStatus `json:"status"`
	ErrorMessage       string            `json:"error_message,omitempty"`
	DailyWindowStart   time.Time         `json:"daily_window_start"`
	MonthlyWindowStart time.Time         `json:"monthly_window_start"`
	CreatedAt          time.Time         `json:"created_at"`
	CommittedAt        *time.Time        `json:"committed_at,omitempty"`
}

// ApplyTelemetry copies normalized provider telemetry and token counters onto r.
func (r *LLMUsageRecord) ApplyTelemetry(normalized LLMTelemetry) {
	r.Provider = normalized.Provider
	r.Model = normalized.Model
	r.StatusCode = normalized.StatusCode
	r.Latency = normalized.Latency
	r.PromptTokens = normalized.Usage.PromptTokens
	r.CompletionTokens = normalized.Usage.CompletionTokens
	r.TotalTokens = normalized.Usage.TotalTokens
}
