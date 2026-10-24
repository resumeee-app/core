package llm

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"resumeee/internal/config"
	"resumeee/internal/domain"
)

// Registry manages configured LLM providers and resolves provider/model selection
// according to the 4-tier resolution hierarchy.
type Registry struct {
	mu              sync.RWMutex
	providers       map[string]domain.LLMProvider
	defaultProvider string
	defaultModel    string
}

// NewRegistry initializes an LLM registry based on 12-factor environment configuration.
func NewRegistry(cfg *config.Config) *Registry {
	r := &Registry{
		providers: make(map[string]domain.LLMProvider),
	}

	if cfg == nil {
		return r
	}

	if cfg.GeminiAPIKey != "" {
		r.Register(NewGeminiProvider(cfg.GeminiAPIKey))
	}
	if cfg.OpenAIAPIKey != "" {
		r.Register(NewOpenAIProvider(cfg.OpenAIAPIKey))
	}
	if cfg.AnthropicAPIKey != "" {
		r.Register(NewAnthropicProvider(cfg.AnthropicAPIKey))
	}
	if cfg.OpenAICompatBaseURL != "" {
		r.Register(NewCompatProvider(cfg.OpenAICompatBaseURL, cfg.OpenAICompatAPIKey))
	}

	// Establish system default provider hierarchy
	if _, ok := r.providers[ProviderGemini]; ok {
		r.defaultProvider = ProviderGemini
		r.defaultModel = defaultGeminiModel
	} else if _, ok := r.providers[ProviderOpenAI]; ok {
		r.defaultProvider = ProviderOpenAI
		r.defaultModel = defaultOpenAIModel
	} else if _, ok := r.providers[ProviderAnthropic]; ok {
		r.defaultProvider = ProviderAnthropic
		r.defaultModel = defaultAnthropicModel
	} else if _, ok := r.providers[ProviderCompat]; ok {
		r.defaultProvider = ProviderCompat
		r.defaultModel = defaultCompatModel
	}

	return r
}

// Register adds an LLM provider to the registry.
func (r *Registry) Register(provider domain.LLMProvider) {
	if provider == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	name := strings.ToLower(strings.TrimSpace(provider.Name()))
	r.providers[name] = provider

	if r.defaultProvider == "" {
		r.defaultProvider = name
		r.defaultModel = defaultModelForProvider(name)
	}
}

// Get returns the named provider, handling aliases (such as openai-compat).
func (r *Registry) Get(name string) (domain.LLMProvider, error) {
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
}

// Resolve resolves the active LLMProvider and model according to the 4-tier precedence ladder:
// 1. GenerationRequest overrides (req.Model, req.BranchContext.ProviderOverride)
// 2. Branch overrides (branch.ProviderOverride, branch.ModelOverride)
// 3. Preset overrides (preset.ProviderOverride, preset.ModelOverride)
// 4. Master Profile defaults (profile.DefaultProvider, profile.DefaultModel)
// 5. System fallbacks (Registry default provider and model)
func (r *Registry) Resolve(req domain.GenerationRequest, preset *domain.Preset, profile *domain.Profile) (domain.LLMProvider, string, error) {
	if preset == nil && req.Preset != nil {
		preset = req.Preset
	}
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
	} else if preset != nil && strings.TrimSpace(preset.ProviderOverride) != "" {
		providerName = strings.TrimSpace(preset.ProviderOverride)
	} else if profile != nil && strings.TrimSpace(profile.DefaultProvider) != "" {
		providerName = strings.TrimSpace(profile.DefaultProvider)
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
	} else if preset != nil && strings.TrimSpace(preset.ModelOverride) != "" {
		modelName = strings.TrimSpace(preset.ModelOverride)
	} else if profile != nil && strings.TrimSpace(profile.DefaultModel) != "" {
		modelName = strings.TrimSpace(profile.DefaultModel)
	} else if r.DefaultModel() != "" && r.DefaultProvider() == provider.Name() {
		modelName = r.DefaultModel()
	} else {
		modelName = defaultModelForProvider(provider.Name())
	}

	return provider, modelName, nil
}

func defaultModelForProvider(name string) string {
	switch strings.ToLower(name) {
	case ProviderGemini:
		return defaultGeminiModel
	case ProviderOpenAI:
		return defaultOpenAIModel
	case ProviderAnthropic:
		return defaultAnthropicModel
	case ProviderCompat, ProviderCompatAlt:
		return defaultCompatModel
	default:
		return "default"
	}
}
