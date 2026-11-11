package llm

import (
	"log/slog"
)

type adapterOptions struct {
	defaultModel   string
	logger         *slog.Logger
	promptDebugger PromptDebugger
}

// Option represents a configuration option for LLM domain providers and registries.
type Option func(*adapterOptions)

// WithDefaultModel overrides the adapter's fallback default model.
func WithDefaultModel(model string) Option {
	return func(o *adapterOptions) {
		if model != "" {
			o.defaultModel = model
		}
	}
}

// WithLogger configures a structured logger for LLM request/response observability.
func WithLogger(logger *slog.Logger) Option {
	return func(o *adapterOptions) {
		if logger != nil {
			o.logger = logger
		}
	}
}

// WithPromptDebugger configures an explicit PromptDebugger instance for persisting prompt and response artifacts.
func WithPromptDebugger(debugger PromptDebugger) Option {
	return func(o *adapterOptions) {
		o.promptDebugger = debugger
	}
}

// WithDebugDir configures a FilePromptDebugger targeting the specified directory.
func WithDebugDir(dir string) Option {
	return func(o *adapterOptions) {
		if dir != "" {
			o.promptDebugger = NewFilePromptDebugger(dir, o.logger)
		}
	}
}

// WithDebugPrompts enables or disables prompt debugging targeting dir (defaulting to "data/llm" if dir is empty).
func WithDebugPrompts(enabled bool, dir string) Option {
	return func(o *adapterOptions) {
		if enabled {
			if dir == "" {
				dir = "data/llm"
			}
			o.promptDebugger = NewFilePromptDebugger(dir, o.logger)
		} else {
			o.promptDebugger = nil
		}
	}
}

// applyOptions initializes adapterOptions with defaults and applies any custom Option functions.
func applyOptions(opts []Option, _, defaultModel string) adapterOptions {
	cfg := adapterOptions{
		defaultModel: defaultModel,
		logger:       slog.Default(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.logger == nil {
		cfg.logger = slog.Default()
	}
	if fpd, ok := cfg.promptDebugger.(*FilePromptDebugger); ok && fpd != nil {
		fpd.logger = cfg.logger
	}
	return cfg
}

