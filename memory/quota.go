package memory

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

// Default tenant quota limits applied when no custom quota exists yet.
const (
	// defaultDailyRequestLimit caps daily LLM requests for newly provisioned tenants.
	defaultDailyRequestLimit = 50
	// defaultMonthlyTokenLimit caps monthly LLM tokens for newly provisioned tenants.
	defaultMonthlyTokenLimit = 1000000
	// defaultUsageListLimit bounds usage history queries when callers omit a positive limit.
	defaultUsageListLimit = 50
)

// Quota fetches or initializes the tenant's quota window state.
func (s *Store) Quota(_ context.Context, userID string) (*core.TenantQuota, error) {
	if userID == "" {
		return nil, fmt.Errorf("%w: user id cannot be empty", core.ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	quota := s.getOrCreateQuotaLocked(userID, 0, 0, now)
	return clone(quota), nil
}

// ReserveQuota atomically checks daily/monthly limits and opens a pending LLM usage reservation.
func (s *Store) ReserveQuota(
	_ context.Context,
	userID string,
	op core.LLMOperationType,
	estimatedTokens int,
	dailyLimit int,
	monthlyLimit int,
) (*core.LLMUsageRecord, error) {
	if userID == "" {
		return nil, fmt.Errorf("%w: user id cannot be empty", core.ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	quota := s.getOrCreateQuotaLocked(userID, dailyLimit, monthlyLimit, now)
	if quota.DailyRequestsUsed+1 > quota.DailyRequestLimit {
		return nil, core.ErrQuotaExceeded
	}
	if quota.MonthlyTokensUsed+estimatedTokens > quota.MonthlyTokenLimit {
		return nil, core.ErrQuotaExceeded
	}

	quota.DailyRequestsUsed++
	quota.MonthlyTokensUsed += estimatedTokens
	quota.UpdatedAt = now

	record := &core.LLMUsageRecord{
		ID:                 core.GenerateID("usage"),
		UserID:             userID,
		Operation:          op,
		EstimatedTokens:    estimatedTokens,
		Status:             core.ReservationStatusPending,
		DailyWindowStart:   quota.DailyWindowStart,
		MonthlyWindowStart: quota.MonthlyWindowStart,
		CreatedAt:          now,
	}
	s.usageRecords[record.ID] = record

	return clone(record), nil
}

// CommitUsage reconciles estimated tokens against actual reported usage for a reservation.
func (s *Store) CommitUsage(_ context.Context, reservationID string, usage core.TokenUsage) error {
	if reservationID == "" {
		return fmt.Errorf("%w: reservation id cannot be empty", core.ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.usageRecords[reservationID]
	if !ok {
		return core.ErrNotFound
	}
	if record.Status != core.ReservationStatusPending {
		return nil
	}

	now := s.now()
	if quota, ok := s.quotas[record.UserID]; ok {
		quota.ResetWindowsIfExpired(now)
		if !record.MonthlyWindowStart.IsZero() && quota.MonthlyWindowStart.Equal(record.MonthlyWindowStart) {
			delta := usage.TotalTokens - record.EstimatedTokens
			quota.AdjustMonthlyTokens(delta)
		}
		quota.UpdatedAt = now
	}

	record.Status = core.ReservationStatusCommitted
	record.PromptTokens = usage.PromptTokens
	record.CompletionTokens = usage.CompletionTokens
	record.TotalTokens = usage.TotalTokens
	record.CommittedAt = &now
	return nil
}

// ReleaseQuota refunds a pending reservation back into the tenant's quota window.
func (s *Store) ReleaseQuota(_ context.Context, reservationID string, errMsg string) error {
	if reservationID == "" {
		return fmt.Errorf("%w: reservation id cannot be empty", core.ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.usageRecords[reservationID]
	if !ok {
		return core.ErrNotFound
	}
	if record.Status != core.ReservationStatusPending {
		return nil
	}

	now := s.now()
	if quota, ok := s.quotas[record.UserID]; ok {
		quota.ResetWindowsIfExpired(now)
		if !record.DailyWindowStart.IsZero() && quota.DailyWindowStart.Equal(record.DailyWindowStart) {
			quota.DailyRequestsUsed--
			if quota.DailyRequestsUsed < 0 {
				quota.DailyRequestsUsed = 0
			}
		}
		if !record.MonthlyWindowStart.IsZero() && quota.MonthlyWindowStart.Equal(record.MonthlyWindowStart) {
			quota.AdjustMonthlyTokens(-record.EstimatedTokens)
		}
		quota.UpdatedAt = now
	}

	record.Status = core.ReservationStatusReleased
	record.ErrorMessage = errMsg
	return nil
}

// ListUsageRecords returns up to limit recent usage records for userID ordered newest-first.
func (s *Store) ListUsageRecords(_ context.Context, userID string, limit int) ([]core.LLMUsageRecord, error) {
	if userID == "" {
		return nil, fmt.Errorf("%w: user id cannot be empty", core.ErrValidation)
	}
	if limit <= 0 {
		limit = defaultUsageListLimit
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var list []core.LLMUsageRecord
	for _, rec := range s.usageRecords {
		if rec.UserID == userID {
			list = append(list, *clone(rec))
		}
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].CreatedAt.After(list[j].CreatedAt)
	})

	if len(list) > limit {
		list = list[:limit]
	}
	return list, nil
}

// getOrCreateQuotaLocked loads or initializes a tenant's quota record and resets expired windows.
func (s *Store) getOrCreateQuotaLocked(userID string, dailyLimit, monthlyLimit int, now time.Time) *core.TenantQuota {
	quota, ok := s.quotas[userID]
	if !ok {
		dLimit := defaultDailyRequestLimit
		if dailyLimit > 0 {
			dLimit = dailyLimit
		}
		mLimit := defaultMonthlyTokenLimit
		if monthlyLimit > 0 {
			mLimit = monthlyLimit
		}
		quota = &core.TenantQuota{
			UserID:             userID,
			DailyRequestLimit:  dLimit,
			DailyWindowStart:   now,
			MonthlyTokenLimit:  mLimit,
			MonthlyWindowStart: now,
			UpdatedAt:          now,
		}
		s.quotas[userID] = quota
		return quota
	}

	if quota.ResetWindowsIfExpired(now) {
		quota.UpdatedAt = now
	}
	if dailyLimit > 0 && quota.DailyRequestLimit != dailyLimit {
		quota.DailyRequestLimit = dailyLimit
	}
	if monthlyLimit > 0 && quota.MonthlyTokenLimit != monthlyLimit {
		quota.MonthlyTokenLimit = monthlyLimit
	}
	return quota
}
