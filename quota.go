package core

import (
	"context"
	"time"
)

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

// LLMUsageRecord records an individual LLM operation reservation, token metrics, and terminal status.
type LLMUsageRecord struct {
	ID                 string            `json:"id"`
	UserID             string            `json:"user_id"`
	Operation          LLMOperationType  `json:"operation"`
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

// QuotaStore provides atomic accounting and persistence for tenant LLM quotas and usage records.
type QuotaStore interface {
	Quota(ctx context.Context, userID string) (*TenantQuota, error)
	ReserveQuota(ctx context.Context, userID string, op LLMOperationType, estimatedTokens, dailyLimit, monthlyLimit int) (*LLMUsageRecord, error)
	CommitUsage(ctx context.Context, reservationID string, usage TokenUsage) error
	ReleaseQuota(ctx context.Context, reservationID string, errMsg string) error
	ListUsageRecords(ctx context.Context, userID string, limit int) ([]LLMUsageRecord, error)
}
