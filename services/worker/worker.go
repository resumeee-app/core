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
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

// GenerationJob encapsulates the parameters of an asynchronous resume generation task.
type GenerationJob = core.GenerationJob

// JobEnqueuer defines the contract for enqueuing generation jobs into the worker pool.
type JobEnqueuer = core.JobEnqueuer

// PDFParseJobTask encapsulates the parameters of an asynchronous PDF resume parsing task.
type PDFParseJobTask = core.PDFParseJobTask

// PDFJobEnqueuer defines the contract for enqueuing PDF parse tasks into the worker pool.
type PDFJobEnqueuer = core.PDFJobEnqueuer

// ResumeExtractor defines the contract for structured resume data extraction from raw text via an LLM.
type ResumeExtractor = core.ResumeExtractor

// LLMService abstracts multi-provider prompt generation and structured response.
type LLMService = core.LLMService

// ProvenanceValidator audits tailored content against career snapshots and exclusion lists.
type ProvenanceValidator interface {
	Validate(content *core.TailoredContent, snapshot *core.MainProfileSnapshot, branch *core.Branch) (*core.ValidationResult, error)
}

// TemplateRenderer executes multi-format Go templates.
type TemplateRenderer interface {
	Render(ctx context.Context, tmpl *core.TemplateSet, profile *core.Profile, content *core.TailoredContent) (*core.RenderOutput, error)
}

// ErrPoolClosed indicates the pool is stopped and cannot accept new jobs.
var ErrPoolClosed = errors.New("worker pool closed")

// PoolConfig specifies construction parameters for a worker pool.
type PoolConfig struct {
	Concurrency            int
	QueueSize              int
	MaxConcurrentPerTenant int
	BranchRepo             core.BranchRepository
	ProfileRepo            core.ProfileRepository
	PresetRepo             core.PresetRepository
	TemplateRepo           core.TemplateRepository
	PDFJobRepo             core.PDFParseJobRepository
	QuotaRepo              core.QuotaRepository
	ResumeExtractor        core.ResumeExtractor
	LLMTimeout             time.Duration
	LLMService             core.LLMService
	Validator              ProvenanceValidator
	Renderer               TemplateRenderer
	Logger                 *slog.Logger
}

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

	branchRepo      core.BranchRepository
	profileRepo     core.ProfileRepository
	presetRepo      core.PresetRepository
	templateRepo    core.TemplateRepository
	pdfJobRepo      core.PDFParseJobRepository
	quotaRepo       core.QuotaRepository
	resumeExtractor core.ResumeExtractor
	llmTimeout      time.Duration
	llmService      core.LLMService
	validator       ProvenanceValidator
	renderer        TemplateRenderer
}

func normalizeTenantID(userID string) string {
	trimmed := strings.TrimSpace(userID)
	if trimmed == "" {
		return "default"
	}
	return trimmed
}

// NewPool constructs a new worker Pool with fair per-tenant scheduling.
func NewPool(cfg PoolConfig) *Pool {
	concurrency := cfg.Concurrency
	if concurrency <= 0 {
		concurrency = 4
	}
	queueSize := cfg.QueueSize
	if queueSize <= 0 {
		queueSize = 100
	}
	maxConcurrent := cfg.MaxConcurrentPerTenant
	if maxConcurrent <= 0 {
		maxConcurrent = 2
	}

	p := &Pool{
		concurrency:            concurrency,
		queueSize:              queueSize,
		maxConcurrentPerTenant: maxConcurrent,
		tenantQueues:           make(map[string][]pendingJob),
		tenantOrder:            make([]string, 0),
		activeTenantJobs:       make(map[string]int),
		logger:                 cfg.Logger,
		branchRepo:             cfg.BranchRepo,
		profileRepo:            cfg.ProfileRepo,
		presetRepo:             cfg.PresetRepo,
		templateRepo:           cfg.TemplateRepo,
		pdfJobRepo:             cfg.PDFJobRepo,
		quotaRepo:              cfg.QuotaRepo,
		resumeExtractor:        cfg.ResumeExtractor,
		llmTimeout:             cfg.LLMTimeout,
		llmService:             cfg.LLMService,
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
		go p.workerLoop(p.ctx, i)
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
func (p *Pool) Enqueue(job GenerationJob) error {
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
func (p *Pool) EnqueuePDFJob(task PDFParseJobTask) error {
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

func (p *Pool) enqueuePendingJob(tenantID string, item pendingJob, debugMsg, warnMsg string, logAttrs ...slog.Attr) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return core.ErrQueueFull
	}

	if p.totalQueued >= p.queueSize {
		if p.logger != nil {
			args := make([]any, len(logAttrs))
			for i, a := range logAttrs {
				args[i] = a
			}
			p.logger.Warn(warnMsg, args...)
		}
		return core.ErrQueueFull
	}

	q, exists := p.tenantQueues[tenantID]
	p.tenantQueues[tenantID] = append(q, item)
	if !exists || len(q) == 0 {
		found := false
		for _, t := range p.tenantOrder {
			if t == tenantID {
				found = true
				break
			}
		}
		if !found {
			p.tenantOrder = append(p.tenantOrder, tenantID)
		}
	}
	p.totalQueued++
	p.cond.Signal()

	if p.logger != nil {
		args := make([]any, len(logAttrs))
		for i, a := range logAttrs {
			args[i] = a
		}
		p.logger.Debug(debugMsg, args...)
	}
	return nil
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

	// Wait for workers to finish draining all queued jobs
	p.wg.Wait()

	if p.cancel != nil {
		p.cancel()
	}

	if p.logger != nil {
		p.logger.Info("worker pool stopped cleanly")
	}
}

// RecoverStaleJobs recovers interrupted jobs left in pending or processing states by failing them with the given reason.
func (p *Pool) RecoverStaleJobs(ctx context.Context, reason string) error {
	var errs []error
	if p.branchRepo != nil {
		if err := p.branchRepo.FailInterruptedVersions(ctx, reason); err != nil {
			errs = append(errs, err)
		}
	}
	if p.pdfJobRepo != nil {
		if err := p.pdfJobRepo.FailInterruptedJobs(ctx, reason); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (p *Pool) workerLoop(ctx context.Context, workerID int) {
	defer p.wg.Done()

	for {
		job, ok := p.nextJob()
		if !ok {
			return
		}
		p.runJob(ctx, job)
	}
}

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

		job, ok := p.popEligibleJobLocked()
		if ok {
			return job, true
		}

		p.cond.Wait()
	}
}

func (p *Pool) popEligibleJobLocked() (pendingJob, bool) {
	n := len(p.tenantOrder)
	if n == 0 {
		return pendingJob{}, false
	}

	for i := 0; i < n; i++ {
		idx := (p.rrCursor + i) % n
		tenantID := p.tenantOrder[idx]
		queue := p.tenantQueues[tenantID]

		if len(queue) == 0 {
			continue
		}

		if p.activeTenantJobs[tenantID] < p.maxConcurrentPerTenant {
			job := queue[0]
			queue[0] = pendingJob{} // clear memory reference for GC
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
	}

	return pendingJob{}, false
}

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

// safeProcessJob executes a generation job with panic recovery to prevent worker goroutines from terminating
// and to guarantee that the offending job is transitioned to a failed state.
func (p *Pool) safeProcessJob(ctx context.Context, job GenerationJob) {
	defer func() {
		if r := recover(); r != nil {
			errStr := fmt.Sprintf("panic recovered in worker: %v", r)
			if p.logger != nil {
				p.logger.Error("panic recovered during job execution",
					"version_id", job.VersionID,
					"panic", r,
				)
			}
			p.failJob(ctx, job.VersionID, errStr)
		}
	}()

	p.processJob(ctx, job)
}
