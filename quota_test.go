package core_test

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/resumeee-app/core"
)

type quotaTestContextKey struct{}

type stubQuotaManager struct {
	reserveID string
	onCommit  func(ctx context.Context, reservationID string, telemetry core.LLMTelemetry) error
	onRelease func(ctx context.Context, reservationID, reason string, telemetry core.LLMTelemetry) error

	commitCalls  int
	releaseCalls int
	lastReason   string
}

func (m *stubQuotaManager) ReserveQuota(_ context.Context, userID string, op core.LLMOperationType, estimatedTokens, _, _ int) (*core.LLMUsageRecord, error) {
	id := m.reserveID
	if id == "" {
		id = "res-1"
	}
	return &core.LLMUsageRecord{
		ID:              id,
		UserID:          userID,
		Operation:       op,
		EstimatedTokens: estimatedTokens,
		Status:          core.ReservationStatusPending,
	}, nil
}

func (m *stubQuotaManager) CommitUsage(ctx context.Context, reservationID string, telemetry core.LLMTelemetry) error {
	m.commitCalls++
	if m.onCommit != nil {
		return m.onCommit(ctx, reservationID, telemetry)
	}
	return nil
}

func (m *stubQuotaManager) ReleaseQuota(ctx context.Context, reservationID, reason string, telemetry core.LLMTelemetry) error {
	m.releaseCalls++
	m.lastReason = reason
	if m.onRelease != nil {
		return m.onRelease(ctx, reservationID, reason, telemetry)
	}
	return nil
}

func TestQuotaScope_Run_DetachesActiveParentDuringCommit(t *testing.T) {
	parentCtx, cancelParent := context.WithCancel(context.WithValue(t.Context(), quotaTestContextKey{}, "trace-commit"))
	defer cancelParent()

	const settleTimeout = 2 * time.Second
	var (
		commitErrAfterCancel error
		gotTraceVal          any
		gotDeadline          time.Time
		hasDeadline          bool
	)

	mgr := &stubQuotaManager{
		onCommit: func(ctx context.Context, _ string, _ core.LLMTelemetry) error {
			// Cancel parent while CommitUsage is in flight; settlement must stay detached.
			cancelParent()
			commitErrAfterCancel = ctx.Err()
			gotTraceVal = ctx.Value(quotaTestContextKey{})
			gotDeadline, hasDeadline = ctx.Deadline()
			return nil
		},
	}

	scope := core.NewQuotaScope(mgr, core.WithQuotaScopeSettleTimeout(settleTimeout))
	err := scope.Run(parentCtx, "user-1", core.OperationGapAnalysis, 1000, 500, nil, func(_ context.Context) (core.LLMTelemetry, error) {
		return core.LLMTelemetry{}, nil
	})
	if err != nil {
		t.Fatalf("unexpected Run error: %v", err)
	}
	if mgr.commitCalls != 1 {
		t.Fatalf("expected 1 CommitUsage call, got %d", mgr.commitCalls)
	}
	if commitErrAfterCancel != nil {
		t.Errorf("expected detached commit context to ignore in-flight parent cancellation, got err: %v", commitErrAfterCancel)
	}
	if gotTraceVal != "trace-commit" {
		t.Errorf("expected context value preserved across detach, got %v", gotTraceVal)
	}
	if !hasDeadline {
		t.Fatalf("expected detached commit context to carry bounded deadline")
	}
	remaining := time.Until(gotDeadline)
	if remaining <= 0 || remaining > settleTimeout+time.Second {
		t.Errorf("expected commit deadline within ~%v, got remaining %v", settleTimeout, remaining)
	}
}

func TestQuotaScope_Run_DetachesActiveParentDuringRelease(t *testing.T) {
	parentCtx, cancelParent := context.WithCancel(context.WithValue(t.Context(), quotaTestContextKey{}, "trace-release"))
	defer cancelParent()

	const settleTimeout = 2 * time.Second
	var (
		releaseErrAfterCancel error
		gotTraceVal           any
		hasDeadline           bool
	)

	mgr := &stubQuotaManager{
		onRelease: func(ctx context.Context, _, _ string, _ core.LLMTelemetry) error {
			// Cancel parent while ReleaseQuota is in flight; settlement must stay detached.
			cancelParent()
			releaseErrAfterCancel = ctx.Err()
			gotTraceVal = ctx.Value(quotaTestContextKey{})
			_, hasDeadline = ctx.Deadline()
			return nil
		},
	}

	opErr := errors.New("upstream failed")
	scope := core.NewQuotaScope(mgr, core.WithQuotaScopeSettleTimeout(settleTimeout))
	err := scope.Run(parentCtx, "user-1", core.OperationPDFParse, 1000, 0, nil, func(_ context.Context) (core.LLMTelemetry, error) {
		return core.LLMTelemetry{}, opErr
	})
	if !errors.Is(err, opErr) {
		t.Fatalf("expected %v, got %v", opErr, err)
	}
	if mgr.releaseCalls != 1 {
		t.Fatalf("expected 1 ReleaseQuota call, got %d", mgr.releaseCalls)
	}
	if releaseErrAfterCancel != nil {
		t.Errorf("expected detached release context to ignore in-flight parent cancellation, got err: %v", releaseErrAfterCancel)
	}
	if gotTraceVal != "trace-release" {
		t.Errorf("expected context value preserved across detach, got %v", gotTraceVal)
	}
	if !hasDeadline {
		t.Errorf("expected detached release context to carry bounded deadline")
	}
}

func TestQuotaScope_Run_ReleasesReservationOnPanicAndRethrows(t *testing.T) {
	parentCtx, cancelParent := context.WithCancel(context.WithValue(t.Context(), quotaTestContextKey{}, "trace-panic"))
	defer cancelParent()

	var (
		releaseCtxErr error
		gotTraceVal   any
		hasDeadline   bool
		releasedID    string
	)
	mgr := &stubQuotaManager{
		reserveID: "res-panic",
		onRelease: func(ctx context.Context, reservationID, _ string, _ core.LLMTelemetry) error {
			releaseCtxErr = ctx.Err()
			gotTraceVal = ctx.Value(quotaTestContextKey{})
			_, hasDeadline = ctx.Deadline()
			releasedID = reservationID
			return nil
		},
	}

	scope := core.NewQuotaScope(mgr, core.WithQuotaScopeSettleTimeout(2*time.Second))

	var recovered any
	func() {
		defer func() {
			recovered = recover()
		}()
		_ = scope.Run(parentCtx, "user-1", core.OperationATSSimulation, 1500, 800, nil, func(_ context.Context) (core.LLMTelemetry, error) {
			cancelParent()
			panic("unexpected nil dereference")
		})
	}()

	if recovered != "unexpected nil dereference" {
		t.Fatalf("expected original panic value to propagate, got %v", recovered)
	}
	if mgr.commitCalls != 0 {
		t.Errorf("expected 0 CommitUsage calls on panic, got %d", mgr.commitCalls)
	}
	if mgr.releaseCalls != 1 {
		t.Fatalf("expected 1 ReleaseQuota call on panic, got %d", mgr.releaseCalls)
	}
	if releasedID != "res-panic" {
		t.Errorf("expected reservation res-panic to be released, got %q", releasedID)
	}
	if mgr.lastReason == "" {
		t.Errorf("expected non-empty release reason on panic")
	}
	if releaseCtxErr != nil {
		t.Errorf("expected detached release context on panic after parent cancel, got %v", releaseCtxErr)
	}
	if gotTraceVal != "trace-panic" {
		t.Errorf("expected context value preserved on panic release, got %v", gotTraceVal)
	}
	if !hasDeadline {
		t.Errorf("expected bounded deadline on panic release context")
	}
}

func TestQuotaScope_Run_ReleasesReservationOnReleaseReasonPanic(t *testing.T) {
	mgr := &stubQuotaManager{reserveID: "res-reason-panic"}
	scope := core.NewQuotaScope(mgr)

	var recovered any
	func() {
		defer func() {
			recovered = recover()
		}()
		_ = scope.Run(
			t.Context(),
			"user-1",
			core.OperationPDFParse,
			1000,
			0,
			func(_ error) string { panic("formatter exploded") },
			func(_ context.Context) (core.LLMTelemetry, error) {
				return core.LLMTelemetry{}, errors.New("boom")
			},
		)
	}()

	if recovered != "formatter exploded" {
		t.Fatalf("expected releaseReason panic to propagate, got %v", recovered)
	}
	if mgr.releaseCalls != 1 {
		t.Fatalf("expected 1 ReleaseQuota call when releaseReason panics, got %d", mgr.releaseCalls)
	}
}

func TestQuotaScope_Run_ReleasesReservationOnGoexit(t *testing.T) {
	mgr := &stubQuotaManager{reserveID: "res-goexit"}
	scope := core.NewQuotaScope(mgr)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = scope.Run(t.Context(), "user-1", core.OperationGapAnalysis, 1000, 0, nil, func(_ context.Context) (core.LLMTelemetry, error) {
			runtime.Goexit()
			return core.LLMTelemetry{}, nil
		})
	}()
	<-done

	if mgr.releaseCalls != 1 {
		t.Fatalf("expected 1 ReleaseQuota call on runtime.Goexit, got %d", mgr.releaseCalls)
	}
	if mgr.commitCalls != 0 {
		t.Errorf("expected 0 CommitUsage calls on runtime.Goexit, got %d", mgr.commitCalls)
	}
}

func TestQuotaScope_Run_DoesNotDoubleSettleIfCommitPanics(t *testing.T) {
	mgr := &stubQuotaManager{
		reserveID: "res-commit-panic",
		onCommit: func(_ context.Context, _ string, _ core.LLMTelemetry) error {
			panic("store commit panic")
		},
	}
	scope := core.NewQuotaScope(mgr)

	var recovered any
	func() {
		defer func() {
			recovered = recover()
		}()
		_ = scope.Run(t.Context(), "user-1", core.OperationGapAnalysis, 1000, 0, nil, func(_ context.Context) (core.LLMTelemetry, error) {
			return core.LLMTelemetry{}, nil
		})
	}()

	if recovered != "store commit panic" {
		t.Fatalf("expected CommitUsage panic to propagate, got %v", recovered)
	}
	if mgr.commitCalls != 1 {
		t.Errorf("expected 1 CommitUsage call, got %d", mgr.commitCalls)
	}
	if mgr.releaseCalls != 0 {
		t.Errorf("expected 0 ReleaseQuota calls after CommitUsage was entered, got %d", mgr.releaseCalls)
	}
}
