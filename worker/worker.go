// Package worker provides a direct-store claiming worker pool, execution pipelines,
// and PDF resume extraction coordination for asynchronous and synchronous AI workloads.
//
// Key components include:
//   - Pool: Concurrent worker pool managing direct-store claiming, lease heartbeats,
//     bounded retries, TTL/lease expiration sweeps with atomic quota settlement, and graceful shutdown
//   - PDFCoordinator: Coordinated synchronous and admission-bounded asynchronous PDF
//     extraction over a unified store and quota lifecycle seam
package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/resumeee-app/core"
)

// Default pool sizing, polling, retry, lease, and shutdown bounds applied when options omit explicit values.
const (
	// defaultConcurrency sets the maximum simultaneous in-flight jobs when WithConcurrency is unset or non-positive.
	defaultConcurrency = 4
	// defaultMaxConcurrentPerTenant caps simultaneous in-flight jobs per tenant when unset.
	defaultMaxConcurrentPerTenant = 2
	// defaultPollInterval controls fallback store polling cadence when WithPollInterval is unset.
	defaultPollInterval = 1 * time.Second
	// defaultSweepInterval controls periodic TTL and stale lease sweeps when WithSweepInterval is unset.
	defaultSweepInterval = 15 * time.Second
	// defaultMaxAttempts sets total execution attempts for transient failures when WithMaxAttempts is unset.
	defaultMaxAttempts = 3
	// defaultRetryBaseDelay sets initial exponential backoff before retrying a transient failure.
	defaultRetryBaseDelay = 250 * time.Millisecond
	// defaultMaxRetryDelay caps exponential backoff between transient retry attempts.
	defaultMaxRetryDelay = 5 * time.Second
	// defaultShutdownTimeout bounds how long Stop waits for in-flight jobs to drain cleanly.
	defaultShutdownTimeout = 30 * time.Second
	// defaultCleanupTimeout bounds detached failure-persistence and quota-refund contexts.
	defaultCleanupTimeout = 10 * time.Second
)

// ErrPoolClosed indicates the worker pool is nil or no longer accepting work.
var ErrPoolClosed = errors.New("worker pool closed")

// Pool claims persisted pending core.Job records directly from a Store,
// enforcing global and per-tenant concurrency limits, lease heartbeats, bounded retries, and periodic sweeps.
type Pool struct {
	concurrency            int
	maxConcurrentPerTenant int
	pollInterval           time.Duration
	sweepInterval          time.Duration
	leaseDuration          time.Duration
	leaseRenewInterval     time.Duration
	maxAttempts            int
	retryBaseDelay         time.Duration
	maxRetryDelay          time.Duration
	shutdownTimeout        time.Duration
	cleanupTimeout         time.Duration
	workerID               string
	clock                  func() time.Time

	mu          sync.Mutex
	started     bool
	stopping    bool
	claimCtx    context.Context
	claimCancel context.CancelFunc
	jobCtx      context.Context
	jobCancel   context.CancelFunc
	timerSeq    uint64
	retryTimers map[uint64]*time.Timer
	stoppedCh   chan struct{}

	claimMu       sync.Mutex
	preferPDFNext bool

	slotMu        sync.Mutex
	activeCount   int
	lastServedSeq map[string]uint64
	dispatchSeq   uint64

	wakeupCh    chan struct{}
	slotFreedCh chan struct{}
	loopWG      sync.WaitGroup
	jobWG       sync.WaitGroup
	logger      *slog.Logger

	store           Store
	resumeExtractor ResumeExtractor
	llmTimeout      time.Duration
	generator       Generator
	validator       ProvenanceValidator
	renderer        TemplateRenderer
}

// New constructs a direct-store claiming worker Pool configured with default bounds and any supplied functional options.
func New(opts ...Option) *Pool {
	p := &Pool{
		concurrency:            defaultConcurrency,
		maxConcurrentPerTenant: defaultMaxConcurrentPerTenant,
		pollInterval:           defaultPollInterval,
		sweepInterval:          defaultSweepInterval,
		leaseDuration:          core.DefaultLeaseDuration,
		maxAttempts:            defaultMaxAttempts,
		retryBaseDelay:         defaultRetryBaseDelay,
		maxRetryDelay:          defaultMaxRetryDelay,
		shutdownTimeout:        defaultShutdownTimeout,
		cleanupTimeout:         defaultCleanupTimeout,
		clock:                  time.Now,
		retryTimers:            make(map[uint64]*time.Timer),
		stoppedCh:              make(chan struct{}),
		lastServedSeq:          make(map[string]uint64),
		wakeupCh:               make(chan struct{}, 1),
		slotFreedCh:            make(chan struct{}, 1),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(p)
		}
	}
	if p.leaseRenewInterval <= 0 {
		p.leaseRenewInterval = p.leaseDuration / 3
		if p.leaseRenewInterval <= 0 {
			p.leaseRenewInterval = time.Millisecond
		}
	}
	if p.workerID == "" {
		p.workerID = "worker-" + uuid.NewString()[:8]
	}
	return p
}

// now returns the current UTC timestamp from the pool's configured clock.
func (p *Pool) now() time.Time {
	if p.clock != nil {
		return p.clock().UTC()
	}
	return time.Now().UTC()
}

// cleanupDuration returns the bounded timeout used for detached cleanup and quota-refund operations.
func (p *Pool) cleanupDuration() time.Duration {
	if p.cleanupTimeout > 0 {
		return p.cleanupTimeout
	}
	return defaultCleanupTimeout
}

// Start launches the dispatcher and periodic expiration/lease sweep loops.
// Calling Start multiple times or after Stop is a safe no-op.
func (p *Pool) Start(ctx context.Context) {
	if p == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	p.mu.Lock()
	if p.started || p.stopping {
		p.mu.Unlock()
		return
	}
	p.started = true
	p.claimCtx, p.claimCancel = context.WithCancel(ctx)
	p.jobCtx, p.jobCancel = context.WithCancel(ctx)
	claimCtx := p.claimCtx
	p.mu.Unlock()

	p.loopWG.Add(2)
	go p.dispatchLoop(claimCtx)
	go p.sweepLoop(claimCtx)

	if p.logger != nil {
		p.logger.Info("worker pool started",
			"worker_id", p.workerID,
			"concurrency", p.concurrency,
			"max_concurrent_per_tenant", p.maxConcurrentPerTenant,
			"poll_interval", p.pollInterval,
			"sweep_interval", p.sweepInterval,
		)
	}
}

// Notify signals the dispatcher loop without blocking that persisted pending work may be ready to claim.
func (p *Pool) Notify() {
	if p == nil || p.wakeupCh == nil {
		return
	}
	select {
	case p.wakeupCh <- struct{}{}:
	default:
	}
}

// Stop gracefully stops claiming new work and waits up to Config.ShutdownTimeout for active jobs to complete.
func (p *Pool) Stop() {
	if p == nil {
		return
	}
	if p.shutdownTimeout > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), p.shutdownTimeout)
		defer cancel()
		_ = p.StopWithContext(ctx)
		return
	}
	_ = p.StopWithContext(context.Background())
}

// StopWithContext halts new claims, stops pending retry timers, waits for dispatcher/sweep loops, and
// drains in-flight jobs up to ctx.Done(). If ctx expires early, active jobs are cancelled and refunded.
func (p *Pool) StopWithContext(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	p.mu.Lock()
	if !p.started {
		p.stopping = true
		p.mu.Unlock()
		return nil
	}
	alreadyStopping := p.stopping
	p.stopping = true
	claimCancel := p.claimCancel
	jobCancel := p.jobCancel
	p.mu.Unlock()

	if alreadyStopping {
		select {
		case <-p.stoppedCh:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	if claimCancel != nil {
		claimCancel()
	}
	p.stopRetryTimers()
	p.loopWG.Wait()

	jobsDone := make(chan struct{})
	go func() {
		p.jobWG.Wait()
		close(jobsDone)
	}()

	var stopErr error
	select {
	case <-jobsDone:
	case <-ctx.Done():
		stopErr = ctx.Err()
		if jobCancel != nil {
			jobCancel()
		}
		// Allow cancelled jobs enough time to finish detached failure persistence and quota refunds.
		timer := time.NewTimer(p.cleanupDuration() + time.Second)
		select {
		case <-jobsDone:
			timer.Stop()
		case <-timer.C:
		}
	}

	if jobCancel != nil {
		jobCancel()
	}
	close(p.stoppedCh)

	if p.logger != nil {
		p.logger.Info("worker pool stopped", "worker_id", p.workerID, "err", stopErr)
	}
	return stopErr
}

// RecoverStaleJobs atomically fails interrupted processing jobs across the unified queue and settles quota.
func (p *Pool) RecoverStaleJobs(ctx context.Context, reason string) error {
	if p == nil || p.store == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	_, err := p.store.RecoverInterrupted(ctx, reason)
	return err
}

// isShuttingDown reports whether the pool is stopping or its job execution context has been cancelled.
func (p *Pool) isShuttingDown() bool {
	p.mu.Lock()
	stopping := p.stopping
	jobCtx := p.jobCtx
	p.mu.Unlock()
	return stopping || (jobCtx != nil && jobCtx.Err() != nil)
}

// dispatchLoop wakes on start, Notify signals, freed worker slots, retry timers, and fallback PollInterval
// ticks to claim persisted work while concurrency capacity remains.
func (p *Pool) dispatchLoop(ctx context.Context) {
	defer p.loopWG.Done()

	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

	for {
		p.drainClaimable(ctx)
		select {
		case <-ctx.Done():
			return
		case <-p.wakeupCh:
		case <-p.slotFreedCh:
		case <-ticker.C:
		}
	}
}

// drainClaimable claims and spawns eligible pending jobs until global concurrency saturates or no jobs qualify.
func (p *Pool) drainClaimable(ctx context.Context) {
	for ctx.Err() == nil && p.hasCapacity() {
		if !p.tryClaimAndLaunch() {
			return
		}
	}
}

// hasCapacity reports whether active in-flight workers are below configured global concurrency.
func (p *Pool) hasCapacity() bool {
	p.slotMu.Lock()
	ok := p.activeCount < p.concurrency
	p.slotMu.Unlock()
	return ok
}

// tryClaimAndLaunch serializes store claims across the unified job queue, updates active
// slot and fair-share accounting before spawning, and alternates preferred job kind each cycle.
func (p *Pool) tryClaimAndLaunch() bool {
	p.claimMu.Lock()
	defer p.claimMu.Unlock()

	if p.store == nil {
		return false
	}
	if p.claimCtx != nil && p.claimCtx.Err() != nil {
		return false
	}

	p.slotMu.Lock()
	if p.activeCount >= p.concurrency {
		p.slotMu.Unlock()
		return false
	}
	seqSnap := copySeqMap(p.lastServedSeq)
	p.slotMu.Unlock()

	preferredKind := core.JobKindBranchTailor
	if p.preferPDFNext {
		preferredKind = core.JobKindPDFParse
	}
	p.preferPDFNext = !p.preferPDFNext

	job, err := p.store.ClaimNext(p.claimCtx, core.ClaimJobRequest{
		WorkerID:               p.workerID,
		Now:                    p.now(),
		LeaseDuration:          p.leaseDuration,
		MaxConcurrentPerTenant: p.maxConcurrentPerTenant,
		LastServedSeq:          seqSnap,
		PreferredKind:          preferredKind,
	})
	if err != nil {
		if p.logger != nil && p.claimCtx.Err() == nil {
			p.logger.Warn("failed to claim pending job", "worker_id", p.workerID, "err", err)
		}
		return false
	}
	if job == nil {
		return false
	}

	p.acquireSlot(job.UserID)
	p.jobWG.Add(1)
	go p.runClaimedJob(job)
	return true
}

// acquireSlot increments active global counts and records fair-share sequence order before spawning.
func (p *Pool) acquireSlot(tenantID string) {
	p.slotMu.Lock()
	p.activeCount++
	p.dispatchSeq++
	p.lastServedSeq[tenantID] = p.dispatchSeq
	p.slotMu.Unlock()
}

// releaseSlot decrements the active global count and notifies the dispatcher that capacity freed.
func (p *Pool) releaseSlot() {
	p.slotMu.Lock()
	if p.activeCount > 0 {
		p.activeCount--
	}
	p.slotMu.Unlock()

	select {
	case p.slotFreedCh <- struct{}{}:
	default:
	}
}

// runClaimedJob dispatches a claimed core.Job by kind under panic recovery and WaitGroup/slot accounting.
func (p *Pool) runClaimedJob(job *core.Job) {
	defer p.jobWG.Done()
	defer p.releaseSlot()

	switch job.Kind {
	case core.JobKindBranchTailor:
		p.safeProcessBranchJob(job)
	case core.JobKindPDFParse:
		p.safeProcessPDFJob(job)
	default:
		p.failClaimedJob(p.jobDescriptor(job, ""), "Unsupported job kind.", true, core.LLMTelemetry{})
	}
}

// sweepLoop periodically expires TTL-elapsed pending jobs and stale processing leases, refunding quotas.
func (p *Pool) sweepLoop(ctx context.Context) {
	defer p.loopWG.Done()

	ticker := time.NewTicker(p.sweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.runSweep(ctx)
		}
	}
}

// runSweep executes one TTL and lease expiration sweep across the unified job queue.
func (p *Pool) runSweep(ctx context.Context) {
	if p.store == nil {
		return
	}
	now := p.now()
	var transitioned int

	if count, err := p.store.ExpirePending(ctx, now, core.ErrJobExpired.Error()); err != nil {
		if p.logger != nil && ctx.Err() == nil {
			p.logger.Warn("failed to expire pending jobs", "err", err)
		}
	} else {
		transitioned += count
	}

	if count, err := p.store.FailExpiredLeases(ctx, now, core.ErrLeaseExpired.Error()); err != nil {
		if p.logger != nil && ctx.Err() == nil {
			p.logger.Warn("failed to expire stale job leases", "err", err)
		}
	} else {
		transitioned += count
	}

	if transitioned > 0 {
		p.Notify()
	}
}

// scheduleRetryTimer registers a tracked wakeup timer to notify the dispatcher once retry backoff elapses.
func (p *Pool) scheduleRetryTimer(delay time.Duration) {
	if delay <= 0 {
		p.Notify()
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.started || p.stopping || (p.claimCtx != nil && p.claimCtx.Err() != nil) {
		return
	}
	if p.retryTimers == nil {
		p.retryTimers = make(map[uint64]*time.Timer)
	}
	p.timerSeq++
	id := p.timerSeq
	timer := time.AfterFunc(delay, func() {
		p.mu.Lock()
		delete(p.retryTimers, id)
		p.mu.Unlock()
		p.Notify()
	})
	p.retryTimers[id] = timer
}

// stopRetryTimers cancels and clears all pending retry wakeup timers during shutdown.
func (p *Pool) stopRetryTimers() {
	p.mu.Lock()
	for id, t := range p.retryTimers {
		t.Stop()
		delete(p.retryTimers, id)
	}
	p.mu.Unlock()
}

// copySeqMap returns a non-nil shallow copy of a string-to-uint64 sequence map.
func copySeqMap(src map[string]uint64) map[string]uint64 {
	dst := make(map[string]uint64, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// Compile-time assertion verifying Pool satisfies JobNotifier.
var _ core.JobNotifier = (*Pool)(nil)
