package branch

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/resumeee-app/core"
)

func TestNew_DefaultsAndOptions(t *testing.T) {
	t.Run("applies defaults and ignores nil or non-positive options", func(t *testing.T) {
		svc := New(
			nil,
			WithDailyRequestLimit(0),
			WithDailyRequestLimit(-5),
			WithMonthlyTokenLimit(0),
			WithMonthlyTokenLimit(-10),
			WithMaxPendingPerTenant(0),
			WithMaxPendingPerTenant(-1),
			WithJobTTL(0),
			WithJobTTL(-time.Minute),
			WithCleanupTimeout(0),
			WithCleanupTimeout(-time.Second),
			WithClock(nil),
		)

		if svc.dailyRequestLimit != defaultDailyRequestLimit {
			t.Errorf("dailyRequestLimit = %d, want %d", svc.dailyRequestLimit, defaultDailyRequestLimit)
		}
		if svc.monthlyTokenLimit != defaultMonthlyTokenLimit {
			t.Errorf("monthlyTokenLimit = %d, want %d", svc.monthlyTokenLimit, defaultMonthlyTokenLimit)
		}
		if svc.maxPendingPerTenant != core.DefaultMaxPendingPerTenant {
			t.Errorf("maxPendingPerTenant = %d, want %d", svc.maxPendingPerTenant, core.DefaultMaxPendingPerTenant)
		}
		if svc.jobTTL != core.DefaultJobTTL {
			t.Errorf("jobTTL = %v, want %v", svc.jobTTL, core.DefaultJobTTL)
		}
		if svc.cleanupTimeout != defaultCleanupTimeout {
			t.Errorf("cleanupTimeout = %v, want %v", svc.cleanupTimeout, defaultCleanupTimeout)
		}
		if svc.clock == nil {
			t.Errorf("expected non-nil default clock")
		}
		if svc.atsInFlight == nil {
			t.Errorf("expected initialized atsInFlight map")
		}
	})

	t.Run("applies explicit positive limits, timeouts, logger, and clock", func(t *testing.T) {
		fixedTime := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))

		svc := New(
			WithLogger(logger),
			WithDailyRequestLimit(75),
			WithMonthlyTokenLimit(250000),
			WithMaxPendingPerTenant(4),
			WithJobTTL(45*time.Minute),
			WithCleanupTimeout(15*time.Second),
			WithClock(func() time.Time { return fixedTime }),
		)

		if svc.logger != logger {
			t.Errorf("expected logger to be set")
		}
		if svc.dailyRequestLimit != 75 {
			t.Errorf("dailyRequestLimit = %d, want 75", svc.dailyRequestLimit)
		}
		if svc.monthlyTokenLimit != 250000 {
			t.Errorf("monthlyTokenLimit = %d, want 250000", svc.monthlyTokenLimit)
		}
		if svc.maxPendingPerTenant != 4 {
			t.Errorf("maxPendingPerTenant = %d, want 4", svc.maxPendingPerTenant)
		}
		if svc.jobTTL != 45*time.Minute {
			t.Errorf("jobTTL = %v, want 45m", svc.jobTTL)
		}
		if svc.cleanupTimeout != 15*time.Second {
			t.Errorf("cleanupTimeout = %v, want 15s", svc.cleanupTimeout)
		}
		if !svc.now().Equal(fixedTime) {
			t.Errorf("now() = %v, want %v", svc.now(), fixedTime)
		}
	})
}
