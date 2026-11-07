package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"resumeee/internal/domain"
)

// ErrPoolClosed indicates the pool is stopped and cannot accept new jobs.
var ErrPoolClosed = errors.New("worker pool closed")

// PoolConfig specifies construction parameters for a worker pool.
type PoolConfig struct {
	Concurrency     int
	QueueSize       int
	BranchRepo      domain.BranchRepository
	ProfileRepo     domain.ProfileRepository
	PresetRepo      domain.PresetRepository
	TemplateRepo    domain.TemplateRepository
	PDFJobRepo      domain.PDFParseJobRepository
	QuotaRepo       domain.QuotaRepository
	ResumeExtractor ResumeExtractor
	LLMTimeout      time.Duration
	LLMService      LLMService
	Validator       ProvenanceValidator
	Renderer        TemplateRenderer
	Logger          *slog.Logger
}

// Pool implements an in-memory concurrent worker pool processing GenerationJobs and PDFParseJobTasks from bounded buffered channels.
type Pool struct {
	concurrency int
	queueSize   int
	jobQueue    chan GenerationJob
	pdfJobQueue chan PDFParseJobTask
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
	logger      *slog.Logger

	mu      sync.RWMutex
	closed  bool
	started bool

	branchRepo      domain.BranchRepository
	profileRepo     domain.ProfileRepository
	presetRepo      domain.PresetRepository
	templateRepo    domain.TemplateRepository
	pdfJobRepo      domain.PDFParseJobRepository
	quotaRepo       domain.QuotaRepository
	resumeExtractor ResumeExtractor
	llmTimeout      time.Duration
	llmService      LLMService
	validator       ProvenanceValidator
	renderer        TemplateRenderer
}

// NewPool constructs a new worker Pool.
func NewPool(cfg PoolConfig) *Pool {
	concurrency := cfg.Concurrency
	if concurrency <= 0 {
		concurrency = 4
	}
	queueSize := cfg.QueueSize
	if queueSize <= 0 {
		queueSize = 100
	}

	return &Pool{
		concurrency:     concurrency,
		queueSize:       queueSize,
		jobQueue:        make(chan GenerationJob, queueSize),
		pdfJobQueue:     make(chan PDFParseJobTask, queueSize),
		logger:          cfg.Logger,
		branchRepo:      cfg.BranchRepo,
		profileRepo:     cfg.ProfileRepo,
		presetRepo:      cfg.PresetRepo,
		templateRepo:    cfg.TemplateRepo,
		pdfJobRepo:      cfg.PDFJobRepo,
		quotaRepo:       cfg.QuotaRepo,
		resumeExtractor: cfg.ResumeExtractor,
		llmTimeout:      cfg.LLMTimeout,
		llmService:      cfg.LLMService,
		validator:       cfg.Validator,
		renderer:        cfg.Renderer,
	}
}

// Start launches worker goroutines to process incoming generation jobs.
func (p *Pool) Start(ctx context.Context) {
	p.mu.Lock()
	if p.started || p.closed {
		p.mu.Unlock()
		return
	}
	p.started = true
	p.ctx, p.cancel = context.WithCancel(ctx)
	p.mu.Unlock()

	for i := 0; i < p.concurrency; i++ {
		p.wg.Add(1)
		go p.workerLoop(p.ctx, i)
	}

	if p.logger != nil {
		p.logger.Info("worker pool started", "concurrency", p.concurrency, "queue_size", p.queueSize)
	}
}

// Enqueue submits a generation job to the pool, returning domain.ErrQueueFull if saturated.
func (p *Pool) Enqueue(job GenerationJob) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.closed {
		return domain.ErrQueueFull
	}

	select {
	case p.jobQueue <- job:
		if p.logger != nil {
			p.logger.Debug("job enqueued", "version_id", job.VersionID, "branch_id", job.BranchID)
		}
		return nil
	default:
		if p.logger != nil {
			p.logger.Warn("worker queue saturated", "version_id", job.VersionID)
		}
		return domain.ErrQueueFull
	}
}

// EnqueuePDFJob submits an asynchronous PDF parse task to the pool, returning domain.ErrQueueFull if saturated.
func (p *Pool) EnqueuePDFJob(task PDFParseJobTask) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.closed {
		return domain.ErrQueueFull
	}

	select {
	case p.pdfJobQueue <- task:
		if p.logger != nil {
			p.logger.Debug("pdf job enqueued", "job_id", task.JobID)
		}
		return nil
	default:
		if p.logger != nil {
			p.logger.Warn("pdf worker queue saturated", "job_id", task.JobID)
		}
		return domain.ErrQueueFull
	}
}

// Stop gracefully shuts down the worker pool, draining all pending jobs and waiting for workers to terminate.
func (p *Pool) Stop() {
	p.mu.Lock()
	if !p.started || p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	close(p.jobQueue)
	close(p.pdfJobQueue)
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

// workerLoop drains jobs from jobQueue and pdfJobQueue channels until they are closed during shutdown.
func (p *Pool) workerLoop(ctx context.Context, workerID int) {
	defer p.wg.Done()

	for {
		select {
		case job, ok := <-p.jobQueue:
			if !ok {
				// jobQueue closed, drain remaining pdf jobs if any
				for task := range p.pdfJobQueue {
					p.safeProcessPDFJob(ctx, task)
				}
				return
			}
			p.safeProcessJob(ctx, job)
		case task, ok := <-p.pdfJobQueue:
			if !ok {
				// pdfJobQueue closed, drain remaining generation jobs if any
				for job := range p.jobQueue {
					p.safeProcessJob(ctx, job)
				}
				return
			}
			p.safeProcessPDFJob(ctx, task)
		case <-ctx.Done():
			return
		}
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
