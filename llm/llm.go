// Package llm provides transport-agnostic provider orchestration, prompt engineering
// pipelines, structured JSON schema definitions, and completion tracing for generative AI models.
//
// Pure domain logic lives in this package without direct net/http or vendor SDK imports.
// Concrete provider implementations live in subpackages under llm/ (gemini, openai, anthropic,
// ollama, and openaicompat) and implement Client.
package llm

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/resumeee-app/core"
)

// Supported LLM provider identifiers and default model identifiers used across registry resolution.
const (
	// ProviderGemini identifies the Google Gemini backend.
	ProviderGemini = "gemini"

	// ProviderOpenAI identifies the OpenAI backend.
	ProviderOpenAI = "openai"

	// ProviderAnthropic identifies the Anthropic Claude backend.
	ProviderAnthropic = "anthropic"

	// ProviderOllama identifies a local or remote Ollama backend.
	ProviderOllama = "ollama"

	// ProviderCompat identifies a generic OpenAI-compatible HTTP endpoint.
	ProviderCompat = "compat"

	// ProviderCompatAlt is an accepted alias for ProviderCompat.
	ProviderCompatAlt = "openai-compat"

	// defaultGeminiModel is the fallback Gemini model when no override is configured.
	defaultGeminiModel = "gemini-3.8-flash"

	// defaultOpenAIModel is the fallback OpenAI model when no override is configured.
	defaultOpenAIModel = "gpt-4o"

	// defaultAnthropicModel is the fallback Anthropic model when no override is configured.
	defaultAnthropicModel = "claude-sonnet-4-5"

	// defaultOllamaModel is the fallback Ollama model when no override is configured.
	defaultOllamaModel = "llama3.1"

	// defaultCompatModel is the fallback OpenAI-compatible model identifier.
	defaultCompatModel = "default"
)

// DefaultModelForProvider returns the recommended fallback model identifier for a provider name.
func DefaultModelForProvider(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case ProviderGemini:
		return defaultGeminiModel
	case ProviderOpenAI:
		return defaultOpenAIModel
	case ProviderAnthropic:
		return defaultAnthropicModel
	case ProviderOllama:
		return defaultOllamaModel
	case ProviderCompat, ProviderCompatAlt:
		return defaultCompatModel
	default:
		return "default"
	}
}

// providerDisplayPrefix returns the human-readable catalog prefix for a provider identifier.
func providerDisplayPrefix(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case ProviderGemini:
		return "Google Gemini"
	case ProviderOpenAI:
		return "OpenAI"
	case ProviderAnthropic:
		return "Anthropic"
	case ProviderOllama:
		return "Ollama"
	case ProviderCompat, ProviderCompatAlt:
		return "OpenAI Compatible"
	default:
		return name
	}
}

// Request defines parameters for a transport-level LLM text or structured completion call.
type Request struct {
	Model          string
	SystemPrompt   string
	UserPrompt     string
	ResponseSchema *Schema
	SchemaName     string
	ReasoningLevel string
}

// Response defines the result returned by an LLM transport client.
type Response struct {
	Text       string
	RawBody    string
	StatusCode int
	Provider   string
	Model      string
	Latency    time.Duration
	Usage      core.TokenUsage
}

// PopulateTelemetry fills missing provider, model, and zero latency metadata on r and normalizes Usage.
// If r is nil, a new Response carrying the supplied telemetry metadata is allocated.
func (r *Response) PopulateTelemetry(provider, model string, latency time.Duration) *Response {
	if r == nil {
		return &Response{
			Provider: provider,
			Model:    model,
			Latency:  latency,
		}
	}
	if strings.TrimSpace(r.Provider) == "" {
		r.Provider = provider
	}
	if strings.TrimSpace(r.Model) == "" {
		r.Model = model
	}
	if r.Latency == 0 {
		r.Latency = latency
	}
	r.Usage = r.Usage.Normalize()
	return r
}

// Telemetry converts r into normalized core.LLMTelemetry, returning zero telemetry when r is nil.
func (r *Response) Telemetry() core.LLMTelemetry {
	if r == nil {
		return core.LLMTelemetry{}
	}
	return core.LLMTelemetry{
		Provider:   r.Provider,
		Model:      r.Model,
		StatusCode: r.StatusCode,
		Latency:    r.Latency,
		Usage:      r.Usage.Normalize(),
	}
}

// Client defines the vendor-agnostic completion contract implemented by each LLM provider backend.
type Client interface {
	Name() string
	DefaultModel() string
	Models() []core.ModelOption
	SupportsStructuredOutput(model string) bool
	Complete(ctx context.Context, req Request) (*Response, error)
}

// RegistryOption configures optional settings on a Registry instance.
type RegistryOption func(*Registry)

// WithLogger configures the structured logger used by Registry for completion telemetry.
func WithLogger(logger *slog.Logger) RegistryOption {
	return func(r *Registry) {
		if logger != nil {
			r.logger = logger
		}
	}
}

// Registry manages configured LLM Client backends and resolves provider/model selection
// alongside capability-aware completion tracing.
type Registry struct {
	mu              sync.RWMutex
	clients         map[string]Client
	defaultProvider string
	defaultModel    string
	explicitDefault bool
	logger          *slog.Logger
}

// NewRegistry constructs an injectable LLM Client Registry with optional telemetry configuration.
func NewRegistry(opts ...RegistryOption) *Registry {
	r := &Registry{
		clients: make(map[string]Client),
		logger:  slog.Default(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}
	return r
}

// Register adds an LLM Client to the registry and updates default provider selection when unset.
func (r *Registry) Register(client Client) {
	if r == nil || client == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.clients == nil {
		r.clients = make(map[string]Client)
	}
	name := strings.ToLower(strings.TrimSpace(client.Name()))
	r.clients[name] = client

	if !r.explicitDefault {
		r.recomputeDefaultLocked()
	}
}

// recomputeDefaultLocked selects the highest-priority registered client and its default model.
func (r *Registry) recomputeDefaultLocked() {
	priority := []string{ProviderGemini, ProviderOpenAI, ProviderAnthropic, ProviderOllama, ProviderCompat, ProviderCompatAlt}
	for _, pName := range priority {
		if client, ok := r.clients[pName]; ok {
			r.defaultProvider = pName
			if pName == ProviderCompatAlt {
				r.defaultProvider = ProviderCompat
			}
			r.defaultModel = resolvedClientDefaultModel(client, r.defaultProvider)
			return
		}
	}
	if r.defaultProvider == "" {
		for name, client := range r.clients {
			r.defaultProvider = name
			r.defaultModel = resolvedClientDefaultModel(client, name)
			return
		}
	}
}

// resolvedClientDefaultModel returns client.DefaultModel() when non-blank, otherwise the provider fallback.
func resolvedClientDefaultModel(client Client, providerName string) string {
	if client != nil {
		if dm := strings.TrimSpace(client.DefaultModel()); dm != "" {
			return dm
		}
	}
	return DefaultModelForProvider(providerName)
}

// Client returns the named registered Client, resolving compat aliases when needed.
func (r *Registry) Client(name string) (Client, error) {
	if r == nil {
		return nil, ErrNoProviders
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	norm := strings.ToLower(strings.TrimSpace(name))
	if c, ok := r.clients[norm]; ok {
		return c, nil
	}

	switch norm {
	case ProviderCompatAlt:
		if c, ok := r.clients[ProviderCompat]; ok {
			return c, nil
		}
	case ProviderCompat:
		if c, ok := r.clients[ProviderCompatAlt]; ok {
			return c, nil
		}
	}

	return nil, fmt.Errorf("%w: %q", ErrProviderNotFound, name)
}

// List returns a sorted slice of registered provider identifiers.
func (r *Registry) List() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	keys := make([]string, 0, len(r.clients))
	for k := range r.clients {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// DefaultProvider returns the active default provider identifier.
func (r *Registry) DefaultProvider() string {
	if r == nil {
		return ""
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.defaultProvider
}

// DefaultModel returns the active default model identifier.
func (r *Registry) DefaultModel() string {
	if r == nil {
		return ""
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.defaultModel
}

// SetDefault explicitly overrides the registry's default provider and model.
func (r *Registry) SetDefault(provider, model string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.defaultProvider = strings.ToLower(strings.TrimSpace(provider))
	r.defaultModel = strings.TrimSpace(model)
	r.explicitDefault = true
}

// Resolve selects the active Client and model according to request, branch, and registry defaults.
func (r *Registry) Resolve(req core.GenerationRequest, _ *core.Preset, _ *core.Profile) (Client, string, error) {
	if r == nil {
		return nil, "", ErrNoProviders
	}
	r.mu.RLock()
	hasClients := len(r.clients) > 0
	r.mu.RUnlock()

	if !hasClients {
		return nil, "", ErrNoProviders
	}

	client, err := r.resolveClient(req)
	if err != nil {
		return nil, "", err
	}
	modelName := r.resolveModel(req, client)
	return client, modelName, nil
}

// resolveClient resolves the target Client from branch override, registry default, or sorted client list.
func (r *Registry) resolveClient(req core.GenerationRequest) (Client, error) {
	providerName := strings.TrimSpace(req.BranchContext.ProviderOverride)
	if providerName == "" {
		providerName = r.DefaultProvider()
	}
	if providerName == "" {
		list := r.List()
		if len(list) > 0 {
			providerName = list[0]
		}
	}
	return r.Client(providerName)
}

// canonicalProviderName normalizes provider identifiers and collapses accepted aliases to their canonical name.
func canonicalProviderName(name string) string {
	norm := strings.ToLower(strings.TrimSpace(name))
	if norm == ProviderCompatAlt {
		return ProviderCompat
	}
	return norm
}

// resolvedRequestModel selects req.Model when non-blank, falling back to client or provider defaults.
func resolvedRequestModel(client Client, req Request) string {
	if model := strings.TrimSpace(req.Model); model != "" {
		return model
	}
	return resolvedClientDefaultModel(client, client.Name())
}

// resolveModel resolves the target model using request override, branch override, registry default, or client default.
func (r *Registry) resolveModel(req core.GenerationRequest, client Client) string {
	if m := strings.TrimSpace(req.Model); m != "" {
		return m
	}
	if m := strings.TrimSpace(req.BranchContext.ModelOverride); m != "" {
		return m
	}
	if defMod := r.DefaultModel(); defMod != "" && canonicalProviderName(r.DefaultProvider()) == canonicalProviderName(client.Name()) {
		return defMod
	}
	return resolvedClientDefaultModel(client, client.Name())
}

// AvailableModels returns configured model options from all active clients without exposing credentials.
func (r *Registry) AvailableModels() []core.ModelOption {
	if r == nil {
		return []core.ModelOption{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	providers := r.sortedCanonicalProvidersLocked()
	var models []core.ModelOption

	for _, p := range providers {
		client := r.lookupProviderLocked(p)
		if client != nil {
			if customCatalog := client.Models(); len(customCatalog) > 0 {
				models = append(models, r.markCatalogDefaultsLocked(p, client.DefaultModel(), customCatalog)...)
				continue
			}
		}
		models = append(models, r.fallbackModelOptionLocked(p))
	}

	if models == nil {
		return []core.ModelOption{}
	}
	return models
}

// sortedCanonicalProvidersLocked deduplicates compat aliases and returns sorted provider keys.
func (r *Registry) sortedCanonicalProvidersLocked() []string {
	providers := make([]string, 0, len(r.clients))
	seen := make(map[string]bool, len(r.clients))
	for name := range r.clients {
		canonical := canonicalProviderName(name)
		if !seen[canonical] {
			seen[canonical] = true
			providers = append(providers, canonical)
		}
	}
	sort.Strings(providers)
	return providers
}

// lookupProviderLocked fetches the Client matching a canonical provider name under read lock.
func (r *Registry) lookupProviderLocked(canonical string) Client {
	if client, ok := r.clients[canonical]; ok {
		return client
	}
	if canonical == ProviderCompat {
		return r.clients[ProviderCompatAlt]
	}
	return nil
}

// markCatalogDefaultsLocked clones a custom client catalog and marks the matching default model.
func (r *Registry) markCatalogDefaultsLocked(provider, clientDefault string, catalog []core.ModelOption) []core.ModelOption {
	out := make([]core.ModelOption, len(catalog))
	for i, item := range catalog {
		item.IsDefault = r.defaultProvider == provider &&
			(r.defaultModel == item.Model || (r.defaultModel == "" && item.Model == clientDefault))
		out[i] = item
	}
	return out
}

// fallbackModelOptionLocked synthesizes a default ModelOption when a client omits a catalog.
func (r *Registry) fallbackModelOptionLocked(provider string) core.ModelOption {
	defMod := DefaultModelForProvider(provider)
	isCompatDefault := provider == ProviderCompat && r.defaultProvider == ProviderCompatAlt
	isDefault := (r.defaultProvider == provider || isCompatDefault) &&
		(r.defaultModel == defMod || r.defaultModel == "")
	return core.ModelOption{
		Provider:    provider,
		Model:       defMod,
		DisplayName: fmt.Sprintf("%s — %s", providerDisplayPrefix(provider), defMod),
		IsDefault:   isDefault,
	}
}

// Complete dispatches a completion request through client while emitting structured telemetry logs.
func (r *Registry) Complete(ctx context.Context, client Client, operation string, req Request) (resp *Response, retErr error) {
	if r == nil || client == nil {
		return nil, ErrNoProviders
	}

	model := resolvedRequestModel(client, req)
	req.Model = model

	if strings.TrimSpace(operation) == "" {
		operation = "CompleteText"
	}

	logger := r.logger
	trace := logLLMRequest(ctx, logger, client.Name(), model, operation)

	var latency time.Duration
	defer func() {
		logLLMResponseTelemetry(ctx, logger, trace, latency, resp, retErr)
	}()

	resp, err := client.Complete(ctx, req)
	latency = time.Since(trace.startTime)
	if err == nil && resp == nil {
		err = fmt.Errorf("%w: empty completion response from provider %s", ErrInvalidResponse, client.Name())
	}
	resp = resp.PopulateTelemetry(client.Name(), model, latency)
	if err != nil {
		retErr = core.AttachLLMTelemetry(err, resp.Telemetry())
		return resp, retErr
	}
	return resp, nil
}

// CompleteStructured dispatches a capability-aware completion request, omitting ResponseSchema when unsupported.
func (r *Registry) CompleteStructured(ctx context.Context, client Client, operation string, req Request) (string, core.LLMTelemetry, error) {
	if r == nil || client == nil {
		return "", core.LLMTelemetry{}, ErrNoProviders
	}

	model := resolvedRequestModel(client, req)
	req.Model = model

	// Strip transport-level schemas when the selected model requires inline prompt instructions instead.
	if req.ResponseSchema != nil && !client.SupportsStructuredOutput(model) {
		req.ResponseSchema = nil
		req.SchemaName = ""
	}

	resp, err := r.Complete(ctx, client, operation, req)
	telemetry := core.ResolveTelemetry(resp.Telemetry(), err)
	if err != nil {
		return "", telemetry, err
	}
	return strings.TrimSpace(resp.Text), telemetry, nil
}

// _ enforces at compile time that Registry implements core.ModelEnumerator.
var _ core.ModelEnumerator = (*Registry)(nil)
