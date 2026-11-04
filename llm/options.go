package llm

import (
	"log/slog"
	"net/http"
	"time"
)

// defaultLLMHTTPClient provides a production-tuned HTTP client for LLM provider adapters
// with connection pooling and an upper timeout to prevent stalled requests from hanging workers.
var defaultLLMHTTPClient = &http.Client{
	Timeout: 3 * time.Minute,
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          50,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	},
}

type adapterOptions struct {
	baseURL        string
	httpClient     *http.Client
	defaultModel   string
	logger         *slog.Logger
	promptDebugger PromptDebugger
}

// Option represents a configuration option for LLM adapters.
type Option func(*adapterOptions)

// WithBaseURL overrides the default upstream API base URL.
func WithBaseURL(baseURL string) Option {
	return func(o *adapterOptions) {
		if baseURL != "" {
			o.baseURL = baseURL
		}
	}
}

// WithHTTPClient overrides the default HTTP client (useful for mock servers in tests).
func WithHTTPClient(client *http.Client) Option {
	return func(o *adapterOptions) {
		if client != nil {
			o.httpClient = client
		}
	}
}

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
func applyOptions(opts []Option, defaultBaseURL, defaultModel string) adapterOptions {
	cfg := adapterOptions{
		baseURL:      defaultBaseURL,
		httpClient:   defaultLLMHTTPClient,
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
