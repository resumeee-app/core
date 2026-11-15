// Package worker provides an in-memory concurrent worker pool and execution pipeline
// for asynchronous resume generation and background batch synchronization.
//
// Key components include:
//   - Pool: Concurrent worker pool managing worker goroutines and channel queues
//   - Pipeline: Multi-stage resume generation pipeline coordinating prompt construction,
//     LLM invocation, anti-hallucination validation, and version persistence
//   - PDFParsePipeline: Asynchronous background worker for processing uploaded PDF resumes
package worker

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/branch"
)

// Default pool sizing limits applied when Config omits explicit values.
const (
	// defaultConcurrency sets the worker goroutine count when Config.Concurrency is zero or negative.
	defaultConcurrency = 4
	// defaultQueueSize sets the buffered job capacity when Config.QueueSize is zero or negative.
	defaultQueueSize = 100
	// defaultMaxConcurrentPerTenant caps simultaneous in-flight jobs per tenant when unset.
	defaultMaxConcurrentPerTenant = 2
)

// ErrPoolClosed indicates the pool is stopped and cannot accept new jobs.
var ErrPoolClosed = errors.New("worker pool closed")

// Config specifies construction parameters for a worker Pool.
type Config struct {
	Concurrency            int
	QueueSize              int
	MaxConcurrentPerTenant int
	Branches               BranchStore
	Profiles               ProfileReader
	Presets                PresetReader
	Templates              TemplateReader
	PDFJobs                PDFJobStore
	Quotas                 QuotaCommitter
	ResumeExtractor        ResumeExtractor
	LLMTimeout             time.Duration
	Generator              Generator
	Validator              ProvenanceValidator
	Renderer               TemplateRenderer
	Logger                 *slog.Logger
}

// pendingJob represents either a resume generation task or a PDF parse task queued per tenant.
type pendingJob struct {
	isPDF    bool
	genJob   core.GenerationJob
	pdfJob   core.PDFParseJobTask
	tenantID string
}

// Pool implements an in-memory concurrent worker pool processing GenerationJobs and PDFParseJobTasks
// with fair-share per-tenant round-robin scheduling and per-tenant concurrency throttling.
type Pool struct {
	concurrency            int
	queueSize              int
	maxConcurrentPerTenant int

	mu               sync.Mutex
	cond             *sync.Cond
	closed           bool
	started          bool
	totalQueued      int
	tenantQueues     map[string][]pendingJob
	tenantOrder      []string
	rrCursor         int
	activeTenantJobs map[string]int

	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
	logger *slog.Logger

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

// normalizeTenantID returns userID trimmed of whitespace, defaulting empty IDs to "default".
func normalizeTenantID(userID string) string {
	trimmed := strings.TrimSpace(userID)
	if trimmed == "" {
		return "default"
	}
	return trimmed
}

// New constructs a worker Pool with fair-share per-tenant scheduling.
func New(cfg Config) *Pool {
	concurrency := cfg.Concurrency
	if concurrency <= 0 {
		concurrency = defaultConcurrency
	}
	queueSize := cfg.QueueSize
	if queueSize <= 0 {
		queueSize = defaultQueueSize
	}
	maxConcurrent := cfg.MaxConcurrentPerTenant
	if maxConcurrent <= 0 {
		maxConcurrent = defaultMaxConcurrentPerTenant
	}

	p := &Pool{
		concurrency:            concurrency,
		queueSize:              queueSize,
		maxConcurrentPerTenant: maxConcurrent,
		tenantQueues:           make(map[string][]pendingJob),
		tenantOrder:            make([]string, 0),
		activeTenantJobs:       make(map[string]int),
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
	p.cond = sync.NewCond(&p.mu)
	return p
}

// Start launches worker goroutines to process incoming generation and PDF parsing jobs.
func (p *Pool) Start(ctx context.Context) {
	p.mu.Lock()
	if p.started || p.closed {
		p.mu.Unlock()
		return
	}
	p.started = true
	p.ctx, p.cancel = context.WithCancel(ctx)
	p.mu.Unlock()

	go func() {
		<-p.ctx.Done()
		p.mu.Lock()
		p.cond.Broadcast()
		p.mu.Unlock()
	}()

	for i := 0; i < p.concurrency; i++ {
		p.wg.Add(1)
		go p.workerLoop(p.ctx)
	}

	if p.logger != nil {
		p.logger.Info("worker pool started",
			"concurrency", p.concurrency,
			"queue_size", p.queueSize,
			"max_concurrent_per_tenant", p.maxConcurrentPerTenant,
		)
	}
}

// Enqueue submits a generation job to the pool, returning core.ErrQueueFull if saturated.
func (p *Pool) Enqueue(job core.GenerationJob) error {
	tenantID := normalizeTenantID(job.UserID)
	item := pendingJob{
		isPDF:    false,
		genJob:   job,
		tenantID: tenantID,
	}
	return p.enqueuePendingJob(
		tenantID,
		item,
		"job enqueued",
		"worker queue saturated",
		slog.String("version_id", job.VersionID),
		slog.String("branch_id", job.BranchID),
		slog.String("tenant_id", tenantID),
	)
}

// EnqueuePDFJob submits an asynchronous PDF parse task to the pool, returning core.ErrQueueFull if saturated.
func (p *Pool) EnqueuePDFJob(task core.PDFParseJobTask) error {
	tenantID := normalizeTenantID(task.UserID)
	item := pendingJob{
		isPDF:    true,
		pdfJob:   task,
		tenantID: tenantID,
	}
	return p.enqueuePendingJob(
		tenantID,
		item,
		"pdf job enqueued",
		"pdf worker queue saturated",
		slog.String("job_id", task.JobID),
		slog.String("tenant_id", tenantID),
	)
}

// attrsToAny converts typed slog attributes into an untyped slice for structured logger methods.
func attrsToAny(attrs []slog.Attr) []any {
	args := make([]any, len(attrs))
	for i, a := range attrs {
		args[i] = a
	}
	return args
}

// enqueuePendingJob appends item to tenantID's FIFO queue while enforcing total queue capacity.
func (p *Pool) enqueuePendingJob(tenantID string, item pendingJob, debugMsg, warnMsg string, logAttrs ...slog.Attr) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return core.ErrQueueFull
	}
	if p.totalQueued >= p.queueSize {
		if p.logger != nil {
			p.logger.Warn(warnMsg, attrsToAny(logAttrs)...)
		}
		return core.ErrQueueFull
	}

	q, exists := p.tenantQueues[tenantID]
	p.tenantQueues[tenantID] = append(q, item)
	if !exists || len(q) == 0 {
		p.ensureTenantRegisteredLocked(tenantID)
	}
	p.totalQueued++
	p.cond.Signal()

	if p.logger != nil {
		p.logger.Debug(debugMsg, attrsToAny(logAttrs)...)
	}
	return nil
}

// ensureTenantRegisteredLocked appends tenantID to round-robin order if not already present.
func (p *Pool) ensureTenantRegisteredLocked(tenantID string) {
	for _, t := range p.tenantOrder {
		if t == tenantID {
			return
		}
	}
	p.tenantOrder = append(p.tenantOrder, tenantID)
}

// Stop gracefully shuts down the worker pool, draining all pending jobs and waiting for workers to terminate.
func (p *Pool) Stop() {
	p.mu.Lock()
	if !p.started || p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.cond.Broadcast()
	p.mu.Unlock()

	p.wg.Wait()

	if p.cancel != nil {
		p.cancel()
	}
	if p.logger != nil {
		p.logger.Info("worker pool stopped cleanly")
	}
}

// RecoverStaleJobs recovers interrupted jobs left in pending or processing states by failing them with reason.
func (p *Pool) RecoverStaleJobs(ctx context.Context, reason string) error {
	var errs []error
	if p.branches != nil {
		if err := p.branches.FailInterruptedVersions(ctx, reason); err != nil {
			errs = append(errs, err)
		}
	}
	if p.pdfJobs != nil {
		if err := p.pdfJobs.FailInterruptedJobs(ctx, reason); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// workerLoop pulls eligible jobs from the round-robin queue until Stop drains the pool or ctx cancels.
func (p *Pool) workerLoop(ctx context.Context) {
	defer p.wg.Done()

	for {
		job, ok := p.nextJob()
		if !ok {
			return
		}
		p.runJob(ctx, job)
	}
}

// nextJob blocks until an eligible job can be dispatched under per-tenant concurrency limits.
func (p *Pool) nextJob() (pendingJob, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for {
		// When closed, drain all queued jobs across all tenants before terminating workers.
		if p.closed && p.totalQueued == 0 {
			return pendingJob{}, false
		}
		// When not closed, terminate immediately if context was cancelled.
		if !p.closed && p.ctx != nil && p.ctx.Err() != nil {
			return pendingJob{}, false
		}

		if job, ok := p.popEligibleJobLocked(); ok {
			return job, true
		}
		p.cond.Wait()
	}
}

// popEligibleJobLocked scans tenants round-robin starting at rrCursor and pops one runnable job.
func (p *Pool) popEligibleJobLocked() (pendingJob, bool) {
	n := len(p.tenantOrder)
	if n == 0 {
		return pendingJob{}, false
	}

	for i := 0; i < n; i++ {
		idx := (p.rrCursor + i) % n
		tenantID := p.tenantOrder[idx]
		queue := p.tenantQueues[tenantID]
		if len(queue) == 0 || p.activeTenantJobs[tenantID] >= p.maxConcurrentPerTenant {
			continue
		}

		job := queue[0]
		queue[0] = pendingJob{} // Clear slice element so queued payloads can be garbage collected.
		p.tenantQueues[tenantID] = queue[1:]
		p.totalQueued--
		p.activeTenantJobs[tenantID]++

		if len(p.tenantQueues[tenantID]) == 0 {
			delete(p.tenantQueues, tenantID)
			p.tenantOrder = append(p.tenantOrder[:idx], p.tenantOrder[idx+1:]...)
			if len(p.tenantOrder) > 0 {
				p.rrCursor = idx % len(p.tenantOrder)
			} else {
				p.rrCursor = 0
			}
		} else {
			p.rrCursor = (idx + 1) % len(p.tenantOrder)
		}
		return job, true
	}

	return pendingJob{}, false
}

// runJob executes item and decrements active tenant accounting upon completion.
func (p *Pool) runJob(ctx context.Context, item pendingJob) {
	defer func() {
		p.mu.Lock()
		p.activeTenantJobs[item.tenantID]--
		if p.activeTenantJobs[item.tenantID] <= 0 {
			delete(p.activeTenantJobs, item.tenantID)
		}
		p.cond.Broadcast()
		p.mu.Unlock()
	}()

	if item.isPDF {
		p.safeProcessPDFJob(ctx, item.pdfJob)
	} else {
		p.safeProcessJob(ctx, item.genJob)
	}
}

// safeProcessJob executes a generation job with panic recovery so worker goroutines never terminate unhandled
// and any pending quota reservation is released with a sanitized user-facing failure reason.
func (p *Pool) safeProcessJob(ctx context.Context, job core.GenerationJob) {
	defer func() {
		if r := recover(); r != nil {
			if p.logger != nil {
				p.logger.Error("panic recovered during job execution",
					"version_id", job.VersionID,
					"panic", r,
				)
			}
			p.failJobWithQuota(ctx, job.VersionID, job.ReservationID, "An unexpected internal error occurred during resume generation.", core.LLMTelemetry{})
		}
	}()

	p.processJob(ctx, job)
}

// Compile-time assertions verifying Pool satisfies branch and PDF job enqueuing contracts.
var (
	_ branch.JobEnqueuer  = (*Pool)(nil)
	_ core.PDFJobEnqueuer = (*Pool)(nil)
)
