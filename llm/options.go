package llm

import (
	"log/slog"
	"net/http"
)

type adapterOptions struct {
	baseURL      string
	httpClient   *http.Client
	defaultModel string
	logger       *slog.Logger
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

func applyOptions(opts []Option, defaultBaseURL, defaultModel string) adapterOptions {
	cfg := adapterOptions{
		baseURL:      defaultBaseURL,
		httpClient:   http.DefaultClient,
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
	return cfg
}
