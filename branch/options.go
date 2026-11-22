package branch

import (
	"log/slog"
	"time"

	"github.com/resumeee-app/core"
)

// Option configures optional dependencies and runtime bounds on a branch Service.
type Option func(*Service)

// WithStore sets the cohesive persistence and quota Store on Service.
func WithStore(store Store) Option {
	return func(s *Service) {
		s.store = store
	}
}

// WithJobNotifier sets the asynchronous job wakeup notifier on Service.
func WithJobNotifier(notifier core.JobNotifier) Option {
	return func(s *Service) {
		s.jobNotifier = notifier
	}
}

// WithValidator sets the provenance validator on Service.
func WithValidator(validator ProvenanceValidator) Option {
	return func(s *Service) {
		s.validator = validator
	}
}

// WithRenderer sets the resume template renderer on Service.
func WithRenderer(renderer TemplateRenderer) Option {
	return func(s *Service) {
		s.renderer = renderer
	}
}

// WithGenerator sets the synchronous analysis and ATS simulation generator on Service.
func WithGenerator(generator Generator) Option {
	return func(s *Service) {
		s.generator = generator
	}
}

// WithPromptCompiler sets the sanitized prompt preview compiler on Service.
func WithPromptCompiler(compiler PromptCompiler) Option {
	return func(s *Service) {
		s.promptCompiler = compiler
	}
}

// WithLogger sets the structured logger on Service.
func WithLogger(logger *slog.Logger) Option {
	return func(s *Service) {
		s.logger = logger
	}
}

// WithDailyRequestLimit overrides the per-tenant 24-hour LLM request quota ceiling when limit > 0.
func WithDailyRequestLimit(limit int) Option {
	return func(s *Service) {
		if limit > 0 {
			s.dailyRequestLimit = limit
		}
	}
}

// WithMonthlyTokenLimit overrides the per-tenant 30-day LLM token quota ceiling when limit > 0.
func WithMonthlyTokenLimit(limit int) Option {
	return func(s *Service) {
		if limit > 0 {
			s.monthlyTokenLimit = limit
		}
	}
}

// WithMaxPendingPerTenant overrides the maximum concurrent queued/in-flight jobs per tenant when limit > 0.
func WithMaxPendingPerTenant(limit int) Option {
	return func(s *Service) {
		if limit > 0 {
			s.maxPendingPerTenant = limit
		}
	}
}

// WithJobTTL overrides the expiration duration applied to submitted async generation jobs when ttl > 0.
func WithJobTTL(ttl time.Duration) Option {
	return func(s *Service) {
		if ttl > 0 {
			s.jobTTL = ttl
		}
	}
}

// WithCleanupTimeout overrides the bounded timeout for detached post-commit cleanup contexts when timeout > 0.
func WithCleanupTimeout(timeout time.Duration) Option {
	return func(s *Service) {
		if timeout > 0 {
			s.cleanupTimeout = timeout
		}
	}
}

// WithClock overrides the wall clock function on Service when clock is non-nil.
func WithClock(clock func() time.Time) Option {
	return func(s *Service) {
		if clock != nil {
			s.clock = clock
		}
	}
}
