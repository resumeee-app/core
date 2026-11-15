package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

// Quotas enforces atomic tenant quota limits, window rollovers, and usage telemetry accounting.
type Quotas struct {
	store *Store
}

// Quota retrieves or initializes the tenant's quota state with any expired windows reset.
func (q *Quotas) Quota(ctx context.Context, userID string) (*core.TenantQuota, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, fmt.Errorf("%w: user id cannot be empty", core.ErrValidation)
	}

	var out *core.TenantQuota
	err := q.store.backend.Write(ctx, func(tx Tx) error {
		now := q.store.now()
		quota, changed, err := loadOrInitQuota(ctx, tx, userID, 0, 0, now)
		if err != nil {
			return err
		}
		if changed {
			if err := tx.PutQuota(ctx, quota); err != nil {
				return err
			}
		}
		out = cloneValue(quota)
		return nil
	})
	return out, err
}

// ReserveQuota atomically verifies daily and monthly limits and opens a pending usage reservation.
func (q *Quotas) ReserveQuota(
	ctx context.Context,
	userID string,
	op core.LLMOperationType,
	estimatedTokens int,
	dailyLimit int,
	monthlyLimit int,
) (*core.LLMUsageRecord, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, fmt.Errorf("%w: user id cannot be empty", core.ErrValidation)
	}

	var out *core.LLMUsageRecord
	err := q.store.backend.Write(ctx, func(tx Tx) error {
		now := q.store.now()
		quota, _, err := loadOrInitQuota(ctx, tx, userID, dailyLimit, monthlyLimit, now)
		if err != nil {
			return err
		}
		if quota.DailyRequestsUsed+1 > quota.DailyRequestLimit {
			return core.ErrQuotaExceeded
		}
		if quota.MonthlyTokensUsed+estimatedTokens > quota.MonthlyTokenLimit {
			return core.ErrQuotaExceeded
		}

		quota.DailyRequestsUsed++
		quota.MonthlyTokensUsed += estimatedTokens
		quota.UpdatedAt = now

		rec := &core.LLMUsageRecord{
			ID:                 core.GenerateID("usage"),
			UserID:             userID,
			Operation:          op,
			EstimatedTokens:    estimatedTokens,
			Status:             core.ReservationStatusPending,
			DailyWindowStart:   quota.DailyWindowStart,
			MonthlyWindowStart: quota.MonthlyWindowStart,
			CreatedAt:          now,
		}
		if err := tx.PutQuota(ctx, quota); err != nil {
			return err
		}
		if err := tx.PutUsageRecord(ctx, rec); err != nil {
			return err
		}
		out = cloneValue(rec)
		return nil
	})
	return out, err
}

// CommitUsage reconciles estimated tokens against actual usage and stores call telemetry idempotently.
func (q *Quotas) CommitUsage(ctx context.Context, reservationID string, telemetry core.LLMTelemetry) error {
	if strings.TrimSpace(reservationID) == "" {
		return fmt.Errorf("%w: reservation id cannot be empty", core.ErrValidation)
	}
	normalized := telemetry.Normalize()

	return q.store.backend.Write(ctx, func(tx Tx) error {
		rec, err := tx.GetUsageRecord(ctx, reservationID)
		if err != nil {
			return err
		}
		if rec.Status != core.ReservationStatusPending {
			return nil
		}

		quota, qErr := tx.GetQuota(ctx, rec.UserID)
		if qErr != nil && !errors.Is(qErr, core.ErrNotFound) {
			return qErr
		}

		now := q.store.now()
		if quota != nil {
			quota.ResetWindowsIfExpired(now)
			if !rec.MonthlyWindowStart.IsZero() && quota.MonthlyWindowStart.Equal(rec.MonthlyWindowStart) {
				quota.AdjustMonthlyTokens(normalized.Usage.TotalTokens - rec.EstimatedTokens)
			}
			quota.UpdatedAt = now
			if err := tx.PutQuota(ctx, quota); err != nil {
				return err
			}
		}

		rec.Status = core.ReservationStatusCommitted
		rec.ApplyTelemetry(normalized)
		rec.CommittedAt = &now
		return tx.PutUsageRecord(ctx, rec)
	})
}

// ReleaseQuota refunds a pending reservation back into active quota windows while recording call telemetry.
func (q *Quotas) ReleaseQuota(ctx context.Context, reservationID, errMsg string, telemetry core.LLMTelemetry) error {
	if strings.TrimSpace(reservationID) == "" {
		return fmt.Errorf("%w: reservation id cannot be empty", core.ErrValidation)
	}
	normalized := telemetry.Normalize()

	return q.store.backend.Write(ctx, func(tx Tx) error {
		rec, err := tx.GetUsageRecord(ctx, reservationID)
		if err != nil {
			return err
		}
		if rec.Status != core.ReservationStatusPending {
			return nil
		}

		quota, qErr := tx.GetQuota(ctx, rec.UserID)
		if qErr != nil && !errors.Is(qErr, core.ErrNotFound) {
			return qErr
		}

		now := q.store.now()
		if quota != nil {
			quota.ResetWindowsIfExpired(now)
			if !rec.DailyWindowStart.IsZero() && quota.DailyWindowStart.Equal(rec.DailyWindowStart) {
				quota.DailyRequestsUsed--
				if quota.DailyRequestsUsed < 0 {
					quota.DailyRequestsUsed = 0
				}
			}
			if !rec.MonthlyWindowStart.IsZero() && quota.MonthlyWindowStart.Equal(rec.MonthlyWindowStart) {
				quota.AdjustMonthlyTokens(-rec.EstimatedTokens)
			}
			quota.UpdatedAt = now
			if err := tx.PutQuota(ctx, quota); err != nil {
				return err
			}
		}

		rec.Status = core.ReservationStatusReleased
		rec.ApplyTelemetry(normalized)
		rec.ErrorMessage = errMsg
		return tx.PutUsageRecord(ctx, rec)
	})
}

// ListUsageRecords returns up to limit recent usage records for userID ordered newest-first.
func (q *Quotas) ListUsageRecords(ctx context.Context, userID string, limit int) ([]core.LLMUsageRecord, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, fmt.Errorf("%w: user id cannot be empty", core.ErrValidation)
	}
	if limit <= 0 {
		limit = core.DefaultUsageListLimit
	}

	var list []core.LLMUsageRecord
	err := q.store.backend.Read(ctx, func(tx ReadTx) error {
		raw, listErr := tx.ListUsageRecords(ctx, userID, limit)
		if listErr != nil {
			return listErr
		}
		list = cloneValue(raw)
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(list, func(i, j int) bool {
		if !list[i].CreatedAt.Equal(list[j].CreatedAt) {
			return list[i].CreatedAt.After(list[j].CreatedAt)
		}
		return list[i].ID < list[j].ID
	})
	if len(list) > limit {
		list = list[:limit]
	}
	return list, nil
}

// loadOrInitQuota reads or creates a tenant quota record, resetting expired windows and applying limit overrides.
func loadOrInitQuota(ctx context.Context, tx ReadTx, userID string, dailyLimit, monthlyLimit int, now time.Time) (*core.TenantQuota, bool, error) {
	quota, err := tx.GetQuota(ctx, userID)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, false, err
	}

	if quota == nil {
		dLimit := core.DefaultDailyRequestLimit
		if dailyLimit > 0 {
			dLimit = dailyLimit
		}
		mLimit := core.DefaultMonthlyTokenLimit
		if monthlyLimit > 0 {
			mLimit = monthlyLimit
		}
		return &core.TenantQuota{
			UserID:             userID,
			DailyRequestLimit:  dLimit,
			DailyWindowStart:   now,
			MonthlyTokenLimit:  mLimit,
			MonthlyWindowStart: now,
			UpdatedAt:          now,
		}, true, nil
	}

	changed := false
	if quota.ResetWindowsIfExpired(now) {
		quota.UpdatedAt = now
		changed = true
	}
	if dailyLimit > 0 && quota.DailyRequestLimit != dailyLimit {
		quota.DailyRequestLimit = dailyLimit
		changed = true
	}
	if monthlyLimit > 0 && quota.MonthlyTokenLimit != monthlyLimit {
		quota.MonthlyTokenLimit = monthlyLimit
		changed = true
	}
	return quota, changed, nil
}
