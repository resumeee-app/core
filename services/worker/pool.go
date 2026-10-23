package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"resumeee/internal/domain"
)

// ErrPoolClosed indicates the pool is stopped and cannot accept new jobs.
var ErrPoolClosed = errors.New("worker pool closed")

// PoolConfig specifies construction parameters for a worker pool.
type PoolConfig struct {
	Concurrency  int
	QueueSize    int
	BranchRepo   domain.BranchRepository
	ProfileRepo  domain.ProfileRepository
	PresetRepo   domain.PresetRepository
	TemplateRepo domain.TemplateRepository
	LLMService   LLMService
	Validator    ProvenanceValidator
	Renderer     TemplateRenderer
	Logger       *slog.Logger
}

// Pool implements an in-memory concurrent worker pool processing GenerationJobs from a bounded buffered channel.
type Pool struct {
	concurrency int
	queueSize   int
	jobQueue    chan GenerationJob
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
	logger      *slog.Logger

	mu      sync.RWMutex
	closed  bool
	started bool

	branchRepo   domain.BranchRepository
	profileRepo  domain.ProfileRepository
	presetRepo   domain.PresetRepository
	templateRepo domain.TemplateRepository
	llmService   LLMService
	validator    ProvenanceValidator
	renderer     TemplateRenderer
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
		concurrency:  concurrency,
		queueSize:    queueSize,
		jobQueue:     make(chan GenerationJob, queueSize),
		logger:       cfg.Logger,
		branchRepo:   cfg.BranchRepo,
		profileRepo:  cfg.ProfileRepo,
		presetRepo:   cfg.PresetRepo,
		templateRepo: cfg.TemplateRepo,
		llmService:   cfg.LLMService,
		validator:    cfg.Validator,
		renderer:     cfg.Renderer,
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

// Stop gracefully shuts down the worker pool, draining all pending jobs and waiting for workers to terminate.
func (p *Pool) Stop() {
	p.mu.Lock()
	if !p.started || p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	close(p.jobQueue)
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

func (p *Pool) workerLoop(ctx context.Context, workerID int) {
	defer p.wg.Done()

	for job := range p.jobQueue {
		p.processJob(ctx, job)
	}
}
