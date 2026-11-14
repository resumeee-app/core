package llm

import (
	"log/slog"
	"net/http"
	"time"
)

// DefaultLLMTimeout defines the default upper timeout boundary for generative LLM requests.
const DefaultLLMTimeout = 10 * time.Minute

// defaultLLMHTTPClient provides a production-tuned HTTP client for LLM provider adapters
// with connection pooling and an upper timeout to prevent stalled requests from hanging workers.
var defaultLLMHTTPClient = &http.Client{
	Timeout: DefaultLLMTimeout,
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
	baseURL      string
	httpClient   *http.Client
	defaultModel string
	logger       *slog.Logger
	timeout      time.Duration
}

// Option represents a configuration option for LLM transport clients, domain providers, and registries.
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

// WithTimeout overrides the default HTTP client timeout for LLM requests.
func WithTimeout(timeout time.Duration) Option {
	return func(o *adapterOptions) {
		if timeout > 0 {
			o.timeout = timeout
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
	if cfg.timeout > 0 {
		if cfg.httpClient == defaultLLMHTTPClient {
			cloned := *defaultLLMHTTPClient
			cloned.Timeout = cfg.timeout
			cfg.httpClient = &cloned
		} else if cfg.httpClient != nil {
			cfg.httpClient.Timeout = cfg.timeout
		}
	}
	return cfg
}
