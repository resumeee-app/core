// Package worker provides a direct-store claiming worker pool and execution pipelines
// for asynchronous resume generation and PDF resume extraction.
//
// Key components include:
//   - Pool: Concurrent worker pool managing direct-store claiming, lease heartbeats,
//     bounded retries, TTL/lease expiration sweeps, and graceful shutdown
//   - SubmitPDFJob and CancelPDFJob: Admission-bounded helpers for persisting and cancelling
//     asynchronous PDF parse jobs with atomic quota lifecycle management
package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/branch"
)

// Default pool sizing, polling, retry, lease, and shutdown bounds applied when Config omits explicit values.
const (
	// defaultConcurrency sets the maximum simultaneous in-flight jobs when Config.Concurrency is zero or negative.
	defaultConcurrency = 4
	// defaultMaxConcurrentPerTenant caps simultaneous in-flight jobs per tenant when unset.
	defaultMaxConcurrentPerTenant = 2
	// defaultPollInterval controls fallback store polling cadence when Config.PollInterval is unset.
	defaultPollInterval = 1 * time.Second
	// defaultSweepInterval controls periodic TTL and stale lease sweeps when Config.SweepInterval is unset.
	defaultSweepInterval = 15 * time.Second
	// defaultMaxAttempts sets total execution attempts for transient failures when Config.MaxAttempts is unset.
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

// Config specifies construction parameters and dependencies for a direct-store claiming worker Pool.
type Config struct {
	Concurrency            int
	MaxConcurrentPerTenant int
	// SeparateQueues indicates that Branches and PDFJobs use distinct non-shared stores.
	// When false (default), ClaimJobRequest.OtherActiveByTenant is left nil so shared stores
	// compute cross-queue active leases directly from persisted state across all pools.
	// When true and both Branches and PDFJobs are configured, local sibling active counts
	// are passed via OtherActiveByTenant to enforce MaxConcurrentPerTenant across separate stores.
	SeparateQueues     bool
	PollInterval       time.Duration
	SweepInterval      time.Duration
	LeaseDuration      time.Duration
	LeaseRenewInterval time.Duration
	MaxAttempts        int
	RetryBaseDelay     time.Duration
	MaxRetryDelay      time.Duration
	ShutdownTimeout    time.Duration
	CleanupTimeout     time.Duration
	WorkerID           string
	Clock              func() time.Time
	Branches           BranchStore
	Profiles           ProfileReader
	Presets            PresetReader
	Templates          TemplateReader
	PDFJobs            PDFJobStore
	Quotas             QuotaCommitter
	ResumeExtractor    ResumeExtractor
	LLMTimeout         time.Duration
	Generator          Generator
	Validator          ProvenanceValidator
	Renderer           TemplateRenderer
	Logger             *slog.Logger
}

// Pool claims persisted pending BranchVersion and PDFParseJob records directly from stores,
// enforcing global and per-tenant concurrency limits, lease heartbeats, bounded retries, and periodic sweeps.
type Pool struct {
	concurrency            int
	maxConcurrentPerTenant int
	separateQueues         bool
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

	slotMu         sync.Mutex
	activeCount    int
	activeVersions map[string]int
	activePDFJobs  map[string]int
	lastServedSeq  map[string]uint64
	dispatchSeq    uint64

	wakeupCh    chan struct{}
	slotFreedCh chan struct{}
	loopWG      sync.WaitGroup
	jobWG       sync.WaitGroup
	logger      *slog.Logger

	branches        BranchStore
	profiles        ProfileReader
	presets         PresetReader
	templates       TemplateReader
	pdfJobs         PDFJobStore
	quotas          QuotaCommitter
	resumeExtractor ResumeExtractor
	llmTimeout      time.Duration
	generator       Generator
	validator       ProvenanceValidator
	renderer        TemplateRenderer
}

// New constructs a direct-store claiming worker Pool configured with cfg.
func New(cfg Config) *Pool {
	concurrency := cfg.Concurrency
	if concurrency <= 0 {
		concurrency = defaultConcurrency
	}
	maxConcurrent := cfg.MaxConcurrentPerTenant
	if maxConcurrent <= 0 {
		maxConcurrent = defaultMaxConcurrentPerTenant
	}
	pollInterval := cfg.PollInterval
	if pollInterval <= 0 {
		pollInterval = defaultPollInterval
	}
	sweepInterval := cfg.SweepInterval
	if sweepInterval <= 0 {
		sweepInterval = defaultSweepInterval
	}
	leaseDuration := cfg.LeaseDuration
	if leaseDuration <= 0 {
		leaseDuration = core.DefaultLeaseDuration
	}
	leaseRenewInterval := cfg.LeaseRenewInterval
	if leaseRenewInterval <= 0 {
		leaseRenewInterval = leaseDuration / 3
		if leaseRenewInterval <= 0 {
			leaseRenewInterval = time.Millisecond
		}
	}
	maxAttempts := cfg.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultMaxAttempts
	}
	retryBaseDelay := cfg.RetryBaseDelay
	if retryBaseDelay <= 0 {
		retryBaseDelay = defaultRetryBaseDelay
	}
	maxRetryDelay := cfg.MaxRetryDelay
	if maxRetryDelay <= 0 {
		maxRetryDelay = defaultMaxRetryDelay
	}
	shutdownTimeout := cfg.ShutdownTimeout
	if shutdownTimeout <= 0 {
		shutdownTimeout = defaultShutdownTimeout
	}
	cleanupTimeout := cfg.CleanupTimeout
	if cleanupTimeout <= 0 {
		cleanupTimeout = defaultCleanupTimeout
	}
	workerID := cfg.WorkerID
	if workerID == "" {
		workerID = "worker-" + uuid.NewString()[:8]
	}
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}

	return &Pool{
		concurrency:            concurrency,
		maxConcurrentPerTenant: maxConcurrent,
		separateQueues:         cfg.SeparateQueues,
		pollInterval:           pollInterval,
		sweepInterval:          sweepInterval,
		leaseDuration:          leaseDuration,
		leaseRenewInterval:     leaseRenewInterval,
		maxAttempts:            maxAttempts,
		retryBaseDelay:         retryBaseDelay,
		maxRetryDelay:          maxRetryDelay,
		shutdownTimeout:        shutdownTimeout,
		cleanupTimeout:         cleanupTimeout,
		workerID:               workerID,
		clock:                  clock,
		retryTimers:            make(map[uint64]*time.Timer),
		stoppedCh:              make(chan struct{}),
		activeVersions:         make(map[string]int),
		activePDFJobs:          make(map[string]int),
		lastServedSeq:          make(map[string]uint64),
		wakeupCh:               make(chan struct{}, 1),
		slotFreedCh:            make(chan struct{}, 1),
		logger:                 cfg.Logger,
		branches:               cfg.Branches,
		profiles:               cfg.Profiles,
		presets:                cfg.Presets,
		templates:              cfg.Templates,
		pdfJobs:                cfg.PDFJobs,
		quotas:                 cfg.Quotas,
		resumeExtractor:        cfg.ResumeExtractor,
		llmTimeout:             cfg.LLMTimeout,
		generator:              cfg.Generator,
		validator:              cfg.Validator,
		renderer:               cfg.Renderer,
	}
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

// Enqueue wakes the direct-store dispatcher via Notify while leaving job state solely in persistent stores.
func (p *Pool) Enqueue(_ core.GenerationJob) error {
	if p == nil {
		return ErrPoolClosed
	}
	p.Notify()
	return nil
}

// EnqueuePDFJob wakes the direct-store dispatcher via Notify while leaving PDF job state solely in persistent stores.
func (p *Pool) EnqueuePDFJob(_ core.PDFParseJobTask) error {
	if p == nil {
		return ErrPoolClosed
	}
	p.Notify()
	return nil
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

// RecoverStaleJobs fails interrupted processing jobs and expired pending jobs across branch versions and
// PDF jobs, releasing all returned quota reservations via bounded detached cleanup contexts.
func (p *Pool) RecoverStaleJobs(ctx context.Context, reason string) error {
	if p == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var (
		errs     []error
		released []core.ReleasedReservation
	)
	if p.branches != nil {
		rel, err := p.branches.RecoverInterruptedVersions(ctx, reason)
		released = append(released, rel...)
		if err != nil {
			errs = append(errs, err)
		}
	}
	if p.pdfJobs != nil {
		rel, err := p.pdfJobs.RecoverInterruptedJobs(ctx, reason)
		released = append(released, rel...)
		if err != nil {
			errs = append(errs, err)
		}
	}
	p.releaseReservationsDetached(released)
	return errors.Join(errs...)
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

// tryClaimAndLaunch serializes store claims across BranchVersion and PDFParseJob queues, updates active
// slot and fair-share accounting before spawning, and alternates preferred store order each cycle.
func (p *Pool) tryClaimAndLaunch() bool {
	p.claimMu.Lock()
	defer p.claimMu.Unlock()

	if p.claimCtx != nil && p.claimCtx.Err() != nil {
		return false
	}

	p.slotMu.Lock()
	if p.activeCount >= p.concurrency {
		p.slotMu.Unlock()
		return false
	}
	var activeVersionsSnap, activePDFSnap map[string]int
	if p.separateQueues && p.branches != nil && p.pdfJobs != nil {
		activeVersionsSnap = copyIntMap(p.activeVersions)
		activePDFSnap = copyIntMap(p.activePDFJobs)
	}
	seqSnap := copySeqMap(p.lastServedSeq)
	p.slotMu.Unlock()

	preferPDF := p.preferPDFNext
	p.preferPDFNext = !p.preferPDFNext

	if preferPDF {
		if p.tryLaunchPDF(activeVersionsSnap, seqSnap) {
			return true
		}
		return p.tryLaunchVersion(activePDFSnap, seqSnap)
	}
	if p.tryLaunchVersion(activePDFSnap, seqSnap) {
		return true
	}
	return p.tryLaunchPDF(activeVersionsSnap, seqSnap)
}

// tryLaunchVersion attempts to claim one BranchVersion and launch its execution goroutine.
func (p *Pool) tryLaunchVersion(otherActivePDF map[string]int, seqSnap map[string]uint64) bool {
	if p.branches == nil {
		return false
	}
	req := core.ClaimJobRequest{
		WorkerID:               p.workerID,
		Now:                    p.now(),
		LeaseDuration:          p.leaseDuration,
		MaxConcurrentPerTenant: p.maxConcurrentPerTenant,
		OtherActiveByTenant:    otherActivePDF,
		LastServedSeq:          seqSnap,
	}
	ver, err := p.branches.ClaimNextVersion(p.claimCtx, req)
	if err != nil {
		if p.logger != nil && p.claimCtx.Err() == nil {
			p.logger.Warn("failed to claim pending branch version", "worker_id", p.workerID, "err", err)
		}
		return false
	}
	if ver == nil {
		return false
	}

	tenantID := ver.UserID
	p.acquireSlot(tenantID, false)
	p.jobWG.Add(1)
	go p.runClaimedVersion(ver, tenantID)
	return true
}

// tryLaunchPDF attempts to claim one PDFParseJob and launch its execution goroutine.
func (p *Pool) tryLaunchPDF(otherActiveVersions map[string]int, seqSnap map[string]uint64) bool {
	if p.pdfJobs == nil {
		return false
	}
	req := core.ClaimJobRequest{
		WorkerID:               p.workerID,
		Now:                    p.now(),
		LeaseDuration:          p.leaseDuration,
		MaxConcurrentPerTenant: p.maxConcurrentPerTenant,
		OtherActiveByTenant:    otherActiveVersions,
		LastServedSeq:          seqSnap,
	}
	job, err := p.pdfJobs.ClaimNextJob(p.claimCtx, req)
	if err != nil {
		if p.logger != nil && p.claimCtx.Err() == nil {
			p.logger.Warn("failed to claim pending pdf parse job", "worker_id", p.workerID, "err", err)
		}
		return false
	}
	if job == nil {
		return false
	}

	tenantID := job.UserID
	p.acquireSlot(tenantID, true)
	p.jobWG.Add(1)
	go p.runClaimedPDFJob(job, tenantID)
	return true
}

// acquireSlot increments active global/per-tenant counts and records fair-share sequence order before spawning.
func (p *Pool) acquireSlot(tenantID string, isPDF bool) {
	p.slotMu.Lock()
	p.activeCount++
	if isPDF {
		p.activePDFJobs[tenantID]++
	} else {
		p.activeVersions[tenantID]++
	}
	p.dispatchSeq++
	p.lastServedSeq[tenantID] = p.dispatchSeq
	p.slotMu.Unlock()
}

// releaseSlot decrements active global/per-tenant counts and notifies the dispatcher that capacity freed.
func (p *Pool) releaseSlot(tenantID string, isPDF bool) {
	p.slotMu.Lock()
	if p.activeCount > 0 {
		p.activeCount--
	}
	if isPDF {
		p.activePDFJobs[tenantID]--
		if p.activePDFJobs[tenantID] <= 0 {
			delete(p.activePDFJobs, tenantID)
		}
	} else {
		p.activeVersions[tenantID]--
		if p.activeVersions[tenantID] <= 0 {
			delete(p.activeVersions, tenantID)
		}
	}
	p.slotMu.Unlock()

	select {
	case p.slotFreedCh <- struct{}{}:
	default:
	}
}

// runClaimedVersion executes a claimed BranchVersion under panic recovery and WaitGroup/slot accounting.
func (p *Pool) runClaimedVersion(ver *core.BranchVersion, tenantID string) {
	defer p.jobWG.Done()
	defer p.releaseSlot(tenantID, false)
	p.safeProcessVersion(ver)
}

// runClaimedPDFJob executes a claimed PDFParseJob under panic recovery and WaitGroup/slot accounting.
func (p *Pool) runClaimedPDFJob(job *core.PDFParseJob, tenantID string) {
	defer p.jobWG.Done()
	defer p.releaseSlot(tenantID, true)
	p.safeProcessPDFJob(job)
}

// safeProcessVersion runs a claimed generation version with panic recovery so worker slots and quotas never leak.
func (p *Pool) safeProcessVersion(ver *core.BranchVersion) {
	defer func() {
		if r := recover(); r != nil {
			versionID := ""
			if ver != nil {
				versionID = ver.ID
			}
			if p.logger != nil {
				p.logger.Error("panic recovered during job execution",
					"version_id", versionID,
					"worker_id", p.workerID,
					"panic", r,
				)
			}
			p.failClaimedJob(p.versionJobDescriptor(ver, ""), "An unexpected internal error occurred during resume generation.", true, core.LLMTelemetry{})
		}
	}()

	p.processClaimedVersion(ver)
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

// runSweep executes one TTL and lease expiration sweep across branch versions and PDF jobs.
func (p *Pool) runSweep(ctx context.Context) {
	now := p.now()
	var released []core.ReleasedReservation

	if p.branches != nil {
		if rel, err := p.branches.ExpirePendingVersions(ctx, now, core.ErrJobExpired.Error()); err != nil {
			if p.logger != nil && ctx.Err() == nil {
				p.logger.Warn("failed to expire pending branch versions", "err", err)
			}
		} else {
			released = append(released, rel...)
		}

		if rel, err := p.branches.FailExpiredVersionLeases(ctx, now, core.ErrLeaseExpired.Error()); err != nil {
			if p.logger != nil && ctx.Err() == nil {
				p.logger.Warn("failed to expire stale branch version leases", "err", err)
			}
		} else {
			released = append(released, rel...)
		}
	}

	if p.pdfJobs != nil {
		if rel, err := p.pdfJobs.ExpirePendingJobs(ctx, now, core.ErrJobExpired.Error()); err != nil {
			if p.logger != nil && ctx.Err() == nil {
				p.logger.Warn("failed to expire pending pdf parse jobs", "err", err)
			}
		} else {
			released = append(released, rel...)
		}

		if rel, err := p.pdfJobs.FailExpiredJobLeases(ctx, now, core.ErrLeaseExpired.Error()); err != nil {
			if p.logger != nil && ctx.Err() == nil {
				p.logger.Warn("failed to expire stale pdf job leases", "err", err)
			}
		} else {
			released = append(released, rel...)
		}
	}

	if len(released) > 0 {
		p.releaseReservationsDetached(released)
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

// releaseReservationDetached refunds a single quota reservation using a bounded detached context.
func (p *Pool) releaseReservationDetached(reservationID, reason string) {
	if p.quotas == nil || reservationID == "" {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), p.cleanupDuration())
	defer cancel()
	if err := p.quotas.ReleaseQuota(cleanupCtx, reservationID, reason, core.LLMTelemetry{}); err != nil && p.logger != nil {
		p.logger.Warn("failed to release quota reservation", "reservation_id", reservationID, "reason", reason, "err", err)
	}
}

// releaseReservationsDetached refunds all quota reservations returned by sweep or crash-recovery calls.
func (p *Pool) releaseReservationsDetached(released []core.ReleasedReservation) {
	if p.quotas == nil || len(released) == 0 {
		return
	}
	for _, res := range released {
		if res.ReservationID == "" {
			continue
		}
		p.releaseReservationDetached(res.ReservationID, res.Reason)
	}
}

// copyIntMap returns a non-nil shallow copy of a string-to-int map.
func copyIntMap(src map[string]int) map[string]int {
	dst := make(map[string]int, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// copySeqMap returns a non-nil shallow copy of a string-to-uint64 sequence map.
func copySeqMap(src map[string]uint64) map[string]uint64 {
	dst := make(map[string]uint64, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// Compile-time assertions verifying Pool satisfies notification and legacy enqueuing contracts.
var (
	_ core.JobNotifier    = (*Pool)(nil)
	_ branch.JobEnqueuer  = (*Pool)(nil)
	_ core.PDFJobEnqueuer = (*Pool)(nil)
)
