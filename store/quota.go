package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/resumeee-app/core"
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
		ledger := newQuotaTxLedger(q.store.now())
		if _, err := ledger.loadQuota(ctx, tx, userID, dailyLimit, monthlyLimit, true); err != nil {
			return err
		}
		rec, err := ledger.reserve(userID, core.QuotaReservationSpec{
			Operation:       op,
			EstimatedTokens: estimatedTokens,
			DailyLimit:      dailyLimit,
			MonthlyLimit:    monthlyLimit,
		})
		if err != nil {
			return err
		}
		if err := ledger.flush(ctx, tx); err != nil {
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
		rec, err := tx.UsageRecord(ctx, reservationID)
		if err != nil {
			return err
		}
		if rec.Status != core.ReservationStatusPending {
			return nil
		}

		quota, qErr := tx.Quota(ctx, rec.UserID)
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

	return q.store.backend.Write(ctx, func(tx Tx) error {
		ledger := newQuotaTxLedger(q.store.now())
		rec, err := ledger.loadRecord(ctx, tx, reservationID, false)
		if err != nil {
			return err
		}
		if rec == nil || rec.Status != core.ReservationStatusPending {
			return nil
		}
		if _, qErr := ledger.loadQuota(ctx, tx, rec.UserID, 0, 0, false); qErr != nil {
			return qErr
		}
		ledger.release(reservationID, errMsg, telemetry)
		return ledger.flush(ctx, tx)
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
	quota, err := tx.Quota(ctx, userID)
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

// quotaTxLedger stages read-before-write quota and usage record transitions within a single Tx.
type quotaTxLedger struct {
	now          time.Time
	quotas       map[string]*core.TenantQuota
	dirtyQuotas  map[string]bool
	records      map[string]*core.LLMUsageRecord
	dirtyRecords map[string]bool
}

// newQuotaTxLedger constructs an empty per-transaction quota ledger stamped at now.
func newQuotaTxLedger(now time.Time) *quotaTxLedger {
	return &quotaTxLedger{
		now:          now,
		quotas:       make(map[string]*core.TenantQuota),
		dirtyQuotas:  make(map[string]bool),
		records:      make(map[string]*core.LLMUsageRecord),
		dirtyRecords: make(map[string]bool),
	}
}

// loadQuota loads or initializes userID's quota into the transaction cache before writes execute.
func (l *quotaTxLedger) loadQuota(ctx context.Context, tx ReadTx, userID string, dailyLimit, monthlyLimit int, createIfMissing bool) (*core.TenantQuota, error) {
	if userID == "" {
		return nil, nil
	}
	if cached, ok := l.quotas[userID]; ok {
		if dailyLimit > 0 && cached.DailyRequestLimit != dailyLimit {
			cached.DailyRequestLimit = dailyLimit
			l.dirtyQuotas[userID] = true
		}
		if monthlyLimit > 0 && cached.MonthlyTokenLimit != monthlyLimit {
			cached.MonthlyTokenLimit = monthlyLimit
			l.dirtyQuotas[userID] = true
		}
		return cached, nil
	}

	if createIfMissing {
		quota, changed, err := loadOrInitQuota(ctx, tx, userID, dailyLimit, monthlyLimit, l.now)
		if err != nil {
			return nil, err
		}
		cp := cloneValue(quota)
		l.quotas[userID] = cp
		if changed {
			l.dirtyQuotas[userID] = true
		}
		return cp, nil
	}

	quota, err := tx.Quota(ctx, userID)
	if errors.Is(err, core.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	cp := cloneValue(quota)
	if cp.ResetWindowsIfExpired(l.now) {
		cp.UpdatedAt = l.now
		l.dirtyQuotas[userID] = true
	}
	l.quotas[userID] = cp
	return cp, nil
}

// loadRecord loads reservationID into the cache, optionally ignoring missing synthetic records.
func (l *quotaTxLedger) loadRecord(ctx context.Context, tx ReadTx, reservationID string, ignoreMissing bool) (*core.LLMUsageRecord, error) {
	if reservationID == "" {
		return nil, nil
	}
	if cached, ok := l.records[reservationID]; ok {
		return cached, nil
	}

	rec, err := tx.UsageRecord(ctx, reservationID)
	if err != nil {
		if ignoreMissing && errors.Is(err, core.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	cp := cloneValue(rec)
	l.records[reservationID] = cp
	return cp, nil
}

// loadPendingReleaseTargets preloads usage records and owning tenant quotas for jobs whose reservations may release.
func (l *quotaTxLedger) loadPendingReleaseTargets(ctx context.Context, tx ReadTx, jobs []*core.Job) error {
	for _, job := range jobs {
		if job == nil || job.ReservationID == "" {
			continue
		}
		rec, err := l.loadRecord(ctx, tx, job.ReservationID, true)
		if err != nil {
			return err
		}
		if rec != nil && rec.Status == core.ReservationStatusPending {
			if _, err := l.loadQuota(ctx, tx, rec.UserID, 0, 0, false); err != nil {
				return err
			}
		}
	}
	return nil
}

// reserve enforces quota ceilings on preloaded userID quota and stages a pending LLMUsageRecord.
func (l *quotaTxLedger) reserve(userID string, spec core.QuotaReservationSpec) (*core.LLMUsageRecord, error) {
	quota := l.quotas[userID]
	if quota == nil {
		return nil, fmt.Errorf("%w: tenant quota not loaded", core.ErrValidation)
	}
	if quota.DailyRequestsUsed+1 > quota.DailyRequestLimit {
		return nil, core.ErrQuotaExceeded
	}
	if quota.MonthlyTokensUsed+spec.EstimatedTokens > quota.MonthlyTokenLimit {
		return nil, core.ErrQuotaExceeded
	}

	quota.DailyRequestsUsed++
	quota.MonthlyTokensUsed += spec.EstimatedTokens
	quota.UpdatedAt = l.now
	l.dirtyQuotas[userID] = true

	rec := &core.LLMUsageRecord{
		ID:                 core.GenerateID("usage"),
		UserID:             userID,
		Operation:          spec.Operation,
		EstimatedTokens:    spec.EstimatedTokens,
		Status:             core.ReservationStatusPending,
		DailyWindowStart:   quota.DailyWindowStart,
		MonthlyWindowStart: quota.MonthlyWindowStart,
		CreatedAt:          l.now,
	}
	l.records[rec.ID] = rec
	l.dirtyRecords[rec.ID] = true
	return rec, nil
}

// release idempotently refunds a pending reservation in memory and records call telemetry.
func (l *quotaTxLedger) release(reservationID, errMsg string, telemetry core.LLMTelemetry) {
	if reservationID == "" {
		return
	}
	rec := l.records[reservationID]
	if rec == nil || rec.Status != core.ReservationStatusPending {
		return
	}

	if quota := l.quotas[rec.UserID]; quota != nil {
		changed := false
		if quota.ResetWindowsIfExpired(l.now) {
			changed = true
		}
		if !rec.DailyWindowStart.IsZero() && quota.DailyWindowStart.Equal(rec.DailyWindowStart) {
			quota.DailyRequestsUsed--
			if quota.DailyRequestsUsed < 0 {
				quota.DailyRequestsUsed = 0
			}
			changed = true
		}
		if !rec.MonthlyWindowStart.IsZero() && quota.MonthlyWindowStart.Equal(rec.MonthlyWindowStart) {
			quota.AdjustMonthlyTokens(-rec.EstimatedTokens)
			changed = true
		}
		if changed {
			quota.UpdatedAt = l.now
			l.dirtyQuotas[rec.UserID] = true
		}
	}

	normalized := telemetry.Normalize()
	rec.Status = core.ReservationStatusReleased
	rec.ApplyTelemetry(normalized)
	rec.ErrorMessage = errMsg
	l.dirtyRecords[reservationID] = true
}

// flush persists all modified TenantQuota and LLMUsageRecord entries to tx.
func (l *quotaTxLedger) flush(ctx context.Context, tx Tx) error {
	for userID, dirty := range l.dirtyQuotas {
		if !dirty {
			continue
		}
		if q := l.quotas[userID]; q != nil {
			if err := tx.PutQuota(ctx, q); err != nil {
				return err
			}
		}
	}
	for recID, dirty := range l.dirtyRecords {
		if !dirty {
			continue
		}
		if rec := l.records[recID]; rec != nil {
			if err := tx.PutUsageRecord(ctx, rec); err != nil {
				return err
			}
		}
	}
	return nil
}
