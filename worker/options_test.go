package worker

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/resumeee-app/core"
)

func TestNew_DefaultsAndOptions(t *testing.T) {
	t.Run("applies defaults and ignores nil or non-positive options", func(t *testing.T) {
		p := New(
			nil,
			WithConcurrency(0),
			WithConcurrency(-2),
			WithMaxConcurrentPerTenant(0),
			WithPollInterval(0),
			WithSweepInterval(-time.Second),
			WithLeaseDuration(0),
			WithLeaseRenewInterval(0),
			WithMaxAttempts(0),
			WithRetryBaseDelay(-time.Millisecond),
			WithMaxRetryDelay(0),
			WithShutdownTimeout(0),
			WithCleanupTimeout(0),
			WithWorkerID(""),
			WithClock(nil),
		)

		if p.concurrency != defaultConcurrency {
			t.Errorf("concurrency = %d, want %d", p.concurrency, defaultConcurrency)
		}
		if p.maxConcurrentPerTenant != defaultMaxConcurrentPerTenant {
			t.Errorf("maxConcurrentPerTenant = %d, want %d", p.maxConcurrentPerTenant, defaultMaxConcurrentPerTenant)
		}
		if p.pollInterval != defaultPollInterval {
			t.Errorf("pollInterval = %v, want %v", p.pollInterval, defaultPollInterval)
		}
		if p.sweepInterval != defaultSweepInterval {
			t.Errorf("sweepInterval = %v, want %v", p.sweepInterval, defaultSweepInterval)
		}
		if p.leaseDuration != core.DefaultLeaseDuration {
			t.Errorf("leaseDuration = %v, want %v", p.leaseDuration, core.DefaultLeaseDuration)
		}
		if want := core.DefaultLeaseDuration / 3; p.leaseRenewInterval != want {
			t.Errorf("leaseRenewInterval = %v, want %v", p.leaseRenewInterval, want)
		}
		if p.maxAttempts != defaultMaxAttempts {
			t.Errorf("maxAttempts = %d, want %d", p.maxAttempts, defaultMaxAttempts)
		}
		if p.retryBaseDelay != defaultRetryBaseDelay {
			t.Errorf("retryBaseDelay = %v, want %v", p.retryBaseDelay, defaultRetryBaseDelay)
		}
		if p.maxRetryDelay != defaultMaxRetryDelay {
			t.Errorf("maxRetryDelay = %v, want %v", p.maxRetryDelay, defaultMaxRetryDelay)
		}
		if p.shutdownTimeout != defaultShutdownTimeout {
			t.Errorf("shutdownTimeout = %v, want %v", p.shutdownTimeout, defaultShutdownTimeout)
		}
		if p.cleanupTimeout != defaultCleanupTimeout {
			t.Errorf("cleanupTimeout = %v, want %v", p.cleanupTimeout, defaultCleanupTimeout)
		}
		if !strings.HasPrefix(p.workerID, "worker-") {
			t.Errorf("workerID = %q, want worker- prefix", p.workerID)
		}
		if p.clock == nil {
			t.Errorf("expected non-nil default clock")
		}
	})

	t.Run("applies explicit overrides and derives leaseRenewInterval from custom leaseDuration", func(t *testing.T) {
		fixedTime := time.Date(2026, 11, 15, 13, 0, 0, 0, time.UTC)
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))

		p := New(
			WithConcurrency(8),
			WithMaxConcurrentPerTenant(3),
			WithPollInterval(200*time.Millisecond),
			WithSweepInterval(5*time.Second),
			WithLeaseDuration(90*time.Second),
			WithMaxAttempts(5),
			WithRetryBaseDelay(100*time.Millisecond),
			WithMaxRetryDelay(2*time.Second),
			WithShutdownTimeout(12*time.Second),
			WithCleanupTimeout(4*time.Second),
			WithWorkerID("worker-custom-1"),
			WithLLMTimeout(3*time.Minute),
			WithLogger(logger),
			WithClock(func() time.Time { return fixedTime }),
		)

		if p.concurrency != 8 || p.maxConcurrentPerTenant != 3 {
			t.Errorf("unexpected concurrency settings: %d / %d", p.concurrency, p.maxConcurrentPerTenant)
		}
		if p.pollInterval != 200*time.Millisecond || p.sweepInterval != 5*time.Second {
			t.Errorf("unexpected intervals: poll=%v sweep=%v", p.pollInterval, p.sweepInterval)
		}
		if p.leaseDuration != 90*time.Second || p.leaseRenewInterval != 30*time.Second {
			t.Errorf("unexpected lease timings: duration=%v renew=%v", p.leaseDuration, p.leaseRenewInterval)
		}
		if p.maxAttempts != 5 || p.retryBaseDelay != 100*time.Millisecond || p.maxRetryDelay != 2*time.Second {
			t.Errorf("unexpected retry config: attempts=%d base=%v max=%v", p.maxAttempts, p.retryBaseDelay, p.maxRetryDelay)
		}
		if p.shutdownTimeout != 12*time.Second || p.cleanupTimeout != 4*time.Second || p.llmTimeout != 3*time.Minute {
			t.Errorf("unexpected timeouts: shutdown=%v cleanup=%v llm=%v", p.shutdownTimeout, p.cleanupTimeout, p.llmTimeout)
		}
		if p.workerID != "worker-custom-1" || p.logger != logger || !p.now().Equal(fixedTime) {
			t.Errorf("unexpected identity/logger/clock state")
		}
	})

	t.Run("PDFCoordinator SubmitAsync rejects nil store even with config", func(t *testing.T) {
		coord := NewPDFCoordinator(nil, nil, nil, PDFCoordinatorConfig{
			MaxRawTextBytes: 1024,
		})
		_, err := coord.SubmitAsync(
			t.Context(),
			PDFParseRequest{UserID: "u1", RawText: "resume"},
		)
		if !errors.Is(err, core.ErrValidation) {
			t.Fatalf("expected ErrValidation for nil store, got %v", err)
		}
	})
}
