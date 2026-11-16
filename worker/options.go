package worker

import (
	"log/slog"
	"time"
)

// Option configures optional dependencies, concurrency limits, and timing knobs on a worker Pool.
type Option func(*Pool)

// WithConcurrency sets the maximum number of simultaneous in-flight worker goroutines when concurrency > 0.
func WithConcurrency(concurrency int) Option {
	return func(p *Pool) {
		if concurrency > 0 {
			p.concurrency = concurrency
		}
	}
}

// WithMaxConcurrentPerTenant caps simultaneous in-flight jobs claimed for a single tenant when limit > 0.
func WithMaxConcurrentPerTenant(limit int) Option {
	return func(p *Pool) {
		if limit > 0 {
			p.maxConcurrentPerTenant = limit
		}
	}
}

// WithPollInterval sets the fallback job store polling cadence when interval > 0.
func WithPollInterval(interval time.Duration) Option {
	return func(p *Pool) {
		if interval > 0 {
			p.pollInterval = interval
		}
	}
}

// WithSweepInterval sets the periodic TTL and stale-lease sweep interval when interval > 0.
func WithSweepInterval(interval time.Duration) Option {
	return func(p *Pool) {
		if interval > 0 {
			p.sweepInterval = interval
		}
	}
}

// WithLeaseDuration sets the claim lease TTL granted per job attempt when d > 0.
func WithLeaseDuration(d time.Duration) Option {
	return func(p *Pool) {
		if d > 0 {
			p.leaseDuration = d
		}
	}
}

// WithLeaseRenewInterval overrides the lease heartbeat interval when d > 0.
func WithLeaseRenewInterval(d time.Duration) Option {
	return func(p *Pool) {
		if d > 0 {
			p.leaseRenewInterval = d
		}
	}
}

// WithMaxAttempts sets the bounded attempt limit for transient upstream failures when attempts > 0.
func WithMaxAttempts(attempts int) Option {
	return func(p *Pool) {
		if attempts > 0 {
			p.maxAttempts = attempts
		}
	}
}

// WithRetryBaseDelay sets the initial exponential backoff delay before retrying a transient failure when d > 0.
func WithRetryBaseDelay(d time.Duration) Option {
	return func(p *Pool) {
		if d > 0 {
			p.retryBaseDelay = d
		}
	}
}

// WithMaxRetryDelay caps the exponential backoff delay between transient retry attempts when d > 0.
func WithMaxRetryDelay(d time.Duration) Option {
	return func(p *Pool) {
		if d > 0 {
			p.maxRetryDelay = d
		}
	}
}

// WithShutdownTimeout bounds how long Stop waits for in-flight jobs before canceling attempt contexts when d > 0.
func WithShutdownTimeout(d time.Duration) Option {
	return func(p *Pool) {
		if d > 0 {
			p.shutdownTimeout = d
		}
	}
}

// WithCleanupTimeout bounds detached failure-persistence and quota-refund cleanup contexts when d > 0.
func WithCleanupTimeout(d time.Duration) Option {
	return func(p *Pool) {
		if d > 0 {
			p.cleanupTimeout = d
		}
	}
}

// WithWorkerID overrides the unique worker identifier used for job lease ownership when id is non-empty.
func WithWorkerID(id string) Option {
	return func(p *Pool) {
		if id != "" {
			p.workerID = id
		}
	}
}

// WithClock overrides the wall clock function on Pool when clock is non-nil.
func WithClock(clock func() time.Time) Option {
	return func(p *Pool) {
		if clock != nil {
			p.clock = clock
		}
	}
}

// WithJobs sets the JobStore used to claim, heartbeat, complete, fail, and sweep jobs on Pool.
func WithJobs(jobs JobStore) Option {
	return func(p *Pool) {
		p.jobs = jobs
	}
}

// WithBranches sets the BranchReader on Pool.
func WithBranches(branches BranchReader) Option {
	return func(p *Pool) {
		p.branches = branches
	}
}

// WithProfiles sets the ProfileReader on Pool.
func WithProfiles(profiles ProfileReader) Option {
	return func(p *Pool) {
		p.profiles = profiles
	}
}

// WithPresets sets the PresetReader on Pool.
func WithPresets(presets PresetReader) Option {
	return func(p *Pool) {
		p.presets = presets
	}
}

// WithTemplates sets the TemplateReader on Pool.
func WithTemplates(templates TemplateReader) Option {
	return func(p *Pool) {
		p.templates = templates
	}
}

// WithQuotas sets the QuotaCommitter on Pool.
func WithQuotas(quotas QuotaCommitter) Option {
	return func(p *Pool) {
		p.quotas = quotas
	}
}

// WithResumeExtractor sets the PDF raw-text resume extractor on Pool.
func WithResumeExtractor(extractor ResumeExtractor) Option {
	return func(p *Pool) {
		p.resumeExtractor = extractor
	}
}

// WithLLMTimeout sets the per-attempt LLM timeout budget on Pool.
func WithLLMTimeout(timeout time.Duration) Option {
	return func(p *Pool) {
		p.llmTimeout = timeout
	}
}

// WithGenerator sets the LLM tailoring generator on Pool.
func WithGenerator(generator Generator) Option {
	return func(p *Pool) {
		p.generator = generator
	}
}

// WithValidator sets the provenance validator on Pool.
func WithValidator(validator ProvenanceValidator) Option {
	return func(p *Pool) {
		p.validator = validator
	}
}

// WithRenderer sets the resume template renderer on Pool.
func WithRenderer(renderer TemplateRenderer) Option {
	return func(p *Pool) {
		p.renderer = renderer
	}
}

// WithLogger sets the structured logger on Pool.
func WithLogger(logger *slog.Logger) Option {
	return func(p *Pool) {
		p.logger = logger
	}
}
