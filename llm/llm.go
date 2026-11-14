// Package llm provides client adapters, prompt engineering pipelines, schema enforcement,
// and provider registry resolution for generative AI model providers.
//
// Supported model provider backends include:
//   - Google Gemini (gemini-2.5-pro, gemini-2.5-flash, gemini-3.8-flash)
//   - OpenAI (gpt-4o, gpt-4o-mini, gpt-4-turbo)
//   - Anthropic (claude-sonnet-4-5, claude-3-5-haiku)
//   - Ollama (llama3.1, local structured outputs via /api/chat)
//   - OpenAI-compatible endpoints (LocalAI, vLLM, DeepSeek)
//
// The package enforces structured JSON outputs conforming to core.TailoredContent
// schemas.
package llm

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/resumeee-app/resumeee-core"
)

// ProviderConfig holds credentials and endpoint configuration for initializing transport clients.
type ProviderConfig struct {
	DefaultProvider string
	DefaultModel    string
	GeminiAPIKey    string
	GeminiModel     string
	OpenAIAPIKey    string
	OpenAIModel     string
	AnthropicAPIKey string
	AnthropicModel  string
	OllamaBaseURL   string
	OllamaAPIKey    string
	OllamaModel     string
	CompatBaseURL   string
	CompatAPIKey    string
	CompatModel     string
}

// RegisterClients registers all configured transport clients into a Registry.
func RegisterClients(reg *Registry, cfg ProviderConfig, clientOpts ...Option) {
	if reg == nil {
		return
	}

	if key := strings.TrimSpace(cfg.GeminiAPIKey); key != "" {
		opts := append([]Option{}, clientOpts...)
		if m := strings.TrimSpace(cfg.GeminiModel); m != "" {
			opts = append(opts, WithDefaultModel(m))
		}
		reg.RegisterClient(NewGeminiProvider(key, opts...))
	}

	if key := strings.TrimSpace(cfg.OpenAIAPIKey); key != "" {
		opts := append([]Option{}, clientOpts...)
		if m := strings.TrimSpace(cfg.OpenAIModel); m != "" {
			opts = append(opts, WithDefaultModel(m))
		}
		reg.RegisterClient(NewOpenAIProvider(key, opts...))
	}

	if key := strings.TrimSpace(cfg.AnthropicAPIKey); key != "" {
		opts := append([]Option{}, clientOpts...)
		if m := strings.TrimSpace(cfg.AnthropicModel); m != "" {
			opts = append(opts, WithDefaultModel(m))
		}
		reg.RegisterClient(NewAnthropicProvider(key, opts...))
	}

	if baseURL := strings.TrimSpace(cfg.OllamaBaseURL); baseURL != "" {
		opts := append([]Option{}, clientOpts...)
		if m := strings.TrimSpace(cfg.OllamaModel); m != "" {
			opts = append(opts, WithDefaultModel(m))
		}
		reg.RegisterClient(NewOllamaProvider(baseURL, strings.TrimSpace(cfg.OllamaAPIKey), opts...))
	}

	if baseURL := strings.TrimSpace(cfg.CompatBaseURL); baseURL != "" {
		opts := append([]Option{}, clientOpts...)
		if m := strings.TrimSpace(cfg.CompatModel); m != "" {
			opts = append(opts, WithDefaultModel(m))
		}
		reg.RegisterClient(NewCompatProvider(baseURL, strings.TrimSpace(cfg.CompatAPIKey), opts...))
	}

	if def := strings.TrimSpace(cfg.DefaultProvider); def != "" {
		reg.SetDefault(def, strings.TrimSpace(cfg.DefaultModel))
	}
}

// Registry manages configured LLM providers and resolves provider/model selection
// according to the 4-tier resolution hierarchy.
type Registry struct {
	mu              sync.RWMutex
	providers       map[string]core.LLMProvider
	defaultProvider string
	defaultModel    string
	explicitDefault bool
	opts            []Option
}

// NewRegistry constructs an injectable LLM provider Registry.
// Providers are registered via RegisterClient, RegisterClients, or Register.
func NewRegistry(opts ...Option) *Registry {
	return &Registry{
		providers: make(map[string]core.LLMProvider),
		opts:      opts,
	}
}

// RegisterClient wraps a transport-only core.LLMClient with prompt compilation and observability,
// and registers it in the Registry.
func (r *Registry) RegisterClient(client core.LLMClient, extraOpts ...Option) {
	if client == nil {
		return
	}
	combinedOpts := append(append([]Option{}, r.opts...), extraOpts...)
	r.Register(NewClientProvider(client, combinedOpts...))
}

// Register adds an LLMProvider to the registry and updates the system default provider hierarchy.
func (r *Registry) Register(provider core.LLMProvider) {
	if provider == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	name := strings.ToLower(strings.TrimSpace(provider.Name()))
	r.providers[name] = provider

	if !r.explicitDefault {
		r.recomputeDefaultLocked()
	}
}

func (r *Registry) recomputeDefaultLocked() {
	priority := []string{ProviderGemini, ProviderOpenAI, ProviderAnthropic, ProviderOllama, ProviderCompat, ProviderCompatAlt}
	for _, pName := range priority {
		if prov, ok := r.providers[pName]; ok {
			r.defaultProvider = pName
			if pName == ProviderCompatAlt {
				r.defaultProvider = ProviderCompat
			}
			if mcp, ok := prov.(ModelCatalogProvider); ok && strings.TrimSpace(mcp.DefaultModel()) != "" {
				r.defaultModel = mcp.DefaultModel()
			} else {
				r.defaultModel = defaultModelForProvider(r.defaultProvider)
			}
			return
		}
	}
	if r.defaultProvider == "" {
		for name, prov := range r.providers {
			r.defaultProvider = name
			if mcp, ok := prov.(ModelCatalogProvider); ok && strings.TrimSpace(mcp.DefaultModel()) != "" {
				r.defaultModel = mcp.DefaultModel()
			} else {
				r.defaultModel = defaultModelForProvider(name)
			}
			return
		}
	}
}

// Get returns the named provider, handling aliases (such as openai-compat).
func (r *Registry) Get(name string) (core.LLMProvider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	norm := strings.ToLower(strings.TrimSpace(name))
	if p, ok := r.providers[norm]; ok {
		return p, nil
	}

	// Alias resolution: openai-compat <-> compat
	switch norm {
	case ProviderCompatAlt:
		if p, ok := r.providers[ProviderCompat]; ok {
			return p, nil
		}
	case ProviderCompat:
		if p, ok := r.providers[ProviderCompatAlt]; ok {
			return p, nil
		}
	}

	return nil, fmt.Errorf("%w: %q", ErrProviderNotFound, name)
}

// List returns a sorted list of registered provider names.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	keys := make([]string, 0, len(r.providers))
	for k := range r.providers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// DefaultProvider returns the configured default provider name.
func (r *Registry) DefaultProvider() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.defaultProvider
}

// DefaultModel returns the configured default model.
func (r *Registry) DefaultModel() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.defaultModel
}

// SetDefault explicitly overrides the default provider and model.
func (r *Registry) SetDefault(provider, model string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.defaultProvider = strings.ToLower(strings.TrimSpace(provider))
	r.defaultModel = strings.TrimSpace(model)
	r.explicitDefault = true
}

// Resolve resolves the active LLMProvider and model according to the precedence ladder:
// 1. GenerationRequest overrides (req.Model, req.BranchContext.ProviderOverride)
// 2. Branch overrides (branch.ProviderOverride, branch.ModelOverride)
// 3. System fallbacks (Registry default provider and model)
func (r *Registry) Resolve(req core.GenerationRequest, _ *core.Preset, _ *core.Profile) (core.LLMProvider, string, error) {
	r.mu.RLock()
	hasProviders := len(r.providers) > 0
	r.mu.RUnlock()

	if !hasProviders {
		return nil, "", ErrNoProviders
	}

	// 1. Resolve Provider
	providerName := ""
	if p := strings.TrimSpace(req.BranchContext.ProviderOverride); p != "" {
		providerName = p
	} else {
		providerName = r.DefaultProvider()
	}

	if providerName == "" {
		list := r.List()
		if len(list) > 0 {
			providerName = list[0]
		}
	}

	provider, err := r.Get(providerName)
	if err != nil {
		return nil, "", err
	}

	// 2. Resolve Model
	modelName := ""
	if m := strings.TrimSpace(req.Model); m != "" {
		modelName = m
	} else if m := strings.TrimSpace(req.BranchContext.ModelOverride); m != "" {
		modelName = m
	} else if r.DefaultModel() != "" && r.DefaultProvider() == provider.Name() {
		modelName = r.DefaultModel()
	} else if mcp, ok := provider.(ModelCatalogProvider); ok && strings.TrimSpace(mcp.DefaultModel()) != "" {
		modelName = mcp.DefaultModel()
	} else {
		modelName = defaultModelForProvider(provider.Name())
	}

	return provider, modelName, nil
}

// defaultModelForProvider returns the recommended fallback model identifier for a given provider name.
func defaultModelForProvider(name string) string {
	switch strings.ToLower(name) {
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

func providerDisplayPrefix(name string) string {
	switch strings.ToLower(name) {
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

func providerSupportsStructuredOutput(p core.LLMProvider) bool {
	capProvider, ok := p.(core.StructuredOutputCapable)
	return ok && capProvider.SupportsStructuredOutput()
}

// AvailableModels returns a list of configured default models from active providers.
// Only providers registered in the registry (i.e. having active credentials) are included.
// The list is safe for public exposure and contains zero credentials or secrets.
func (r *Registry) AvailableModels() []core.ModelOption {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var models []core.ModelOption

	providers := make([]string, 0, len(r.providers))
	seen := make(map[string]bool)
	for name := range r.providers {
		canonical := name
		if canonical == ProviderCompatAlt {
			canonical = ProviderCompat
		}
		if !seen[canonical] {
			seen[canonical] = true
			providers = append(providers, canonical)
		}
	}
	sort.Strings(providers)

	for _, p := range providers {
		prov, ok := r.providers[p]
		if !ok && p == ProviderCompat {
			prov = r.providers[ProviderCompatAlt]
		}
		if mcp, ok := prov.(ModelCatalogProvider); ok {
			if customCatalog := mcp.Models(); len(customCatalog) > 0 {
				defMod := mcp.DefaultModel()
				for _, item := range customCatalog {
					cp := item
					cp.IsDefault = (r.defaultProvider == p && (r.defaultModel == cp.Model || (r.defaultModel == "" && cp.Model == defMod)))
					models = append(models, cp)
				}
				continue
			}
		}

		defMod := defaultModelForProvider(p)
		isDefault := ((r.defaultProvider == p || (p == ProviderCompat && r.defaultProvider == ProviderCompatAlt)) &&
			(r.defaultModel == defMod || r.defaultModel == ""))
		models = append(models, core.ModelOption{
			Provider:    p,
			Model:       defMod,
			DisplayName: fmt.Sprintf("%s — %s", providerDisplayPrefix(p), defMod),
			IsDefault:   isDefault,
		})
	}

	if models == nil {
		models = []core.ModelOption{}
	}
	return models
}

// Compile-time interface assertions ensuring all concrete provider adapters satisfy core.LLMClient before registration in Registry.
var (
	_ core.LLMClient               = (*GeminiProvider)(nil)
	_ core.LLMClient               = (*OpenAIProvider)(nil)
	_ core.LLMClient               = (*AnthropicProvider)(nil)
	_ core.LLMClient               = (*OllamaProvider)(nil)
	_ core.LLMClient               = (*CompatProvider)(nil)
	_ core.StructuredOutputCapable = (*ClientProvider)(nil)
)
