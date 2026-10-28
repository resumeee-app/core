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
func NewRegistry(cfg *config.Config, opts ...Option) *Registry {
	r := &Registry{
		providers: make(map[string]domain.LLMProvider),
	}

	if cfg == nil {
		return r
	}

	if cfg.GeminiAPIKey != "" {
		r.Register(NewGeminiProvider(cfg.GeminiAPIKey, opts...))
	}
	if cfg.OpenAIAPIKey != "" {
		r.Register(NewOpenAIProvider(cfg.OpenAIAPIKey, opts...))
	}
	if cfg.AnthropicAPIKey != "" {
		r.Register(NewAnthropicProvider(cfg.AnthropicAPIKey, opts...))
	}
	if cfg.OpenAICompatBaseURL != "" {
		r.Register(NewCompatProvider(cfg.OpenAICompatBaseURL, cfg.OpenAICompatAPIKey, opts...))
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

// AvailableModels returns a list of configured models from active providers.
// Only providers registered in the registry (i.e. having active credentials) are included.
// The list is safe for public exposure and contains zero credentials or secrets.
func (r *Registry) AvailableModels() []domain.ModelOption {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var models []domain.ModelOption

	// Providers sorted alphabetically: anthropic, compat, gemini, openai
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
		switch p {
		case ProviderAnthropic:
			catalog := []struct {
				model       string
				displayName string
			}{
				{"claude-3-7-sonnet-20250219", "Anthropic — claude-3-7-sonnet-20250219"},
				{"claude-3-5-sonnet-20241022", "Anthropic — claude-3-5-sonnet-20241022"},
				{"claude-3-5-haiku-20241022", "Anthropic — claude-3-5-haiku-20241022"},
			}
			for _, item := range catalog {
				isDefault := (r.defaultProvider == ProviderAnthropic && (r.defaultModel == item.model || (r.defaultModel == "" && item.model == defaultAnthropicModel)))
				models = append(models, domain.ModelOption{
					Provider:    ProviderAnthropic,
					Model:       item.model,
					DisplayName: item.displayName,
					IsDefault:   isDefault,
				})
			}
		case ProviderCompat:
			isDefault := ((r.defaultProvider == ProviderCompat || r.defaultProvider == ProviderCompatAlt) && (r.defaultModel == defaultCompatModel || r.defaultModel == ""))
			models = append(models, domain.ModelOption{
				Provider:    ProviderCompat,
				Model:       defaultCompatModel,
				DisplayName: "OpenAI Compatible — default",
				IsDefault:   isDefault,
			})
		case ProviderGemini:
			catalog := []struct {
				model       string
				displayName string
			}{
				{"gemini-4-argon", "Google Gemini — gemini-4-argon"},
				{"gemini-3.1-pro-preview", "Google Gemini — gemini-3.1-pro-preview"},
				{"gemini-3.1-pro-preview-customtools", "Google Gemini — gemini-3.1-pro-preview-customtools"},
				{"gemini-3.8-flash", "Google Gemini — gemini-3.8-flash"},
				{"gemini-3.6-flash", "Google Gemini — gemini-3.6-flash"},
				{"gemini-flash-latest", "Google Gemini — gemini-flash-latest"},
				{"gemini-3.8-flash-lite", "Google Gemini — gemini-3.8-flash-lite"},
				{"gemini-3.5-flash-lite", "Google Gemini — gemini-3.5-flash-lite"},
				{"gemini-flash-lite-latest", "Google Gemini — gemini-flash-lite-latest"},
				{"gemini-3.8-live", "Google Gemini — gemini-3.8-live"},
				{"gemini-3.8-live-extended-thinking", "Google Gemini — gemini-3.8-live-extended-thinking"},
				{"gemini-3.8-flash-tts", "Google Gemini — gemini-3.8-flash-tts"},
				{"gemini-3.8-flash-lite-tts", "Google Gemini — gemini-3.8-flash-lite-tts"},
				{"gemini-2.5-pro", "Google Gemini — gemini-2.5-pro"},
				{"gemini-2.5-flash", "Google Gemini — gemini-2.5-flash"},
				{"gemini-2.5-flash-lite", "Google Gemini — gemini-2.5-flash-lite"},
				{"gemini-embedding-2", "Google Gemini — gemini-embedding-2"},
				{"gemini-embedding-001", "Google Gemini — gemini-embedding-001"},
				{"text-embedding-004", "Google Gemini — text-embedding-004"},
				{"imagen-3.0-generate-002", "Google Gemini — imagen-3.0-generate-002"},
			}
			for _, item := range catalog {
				isDefault := (r.defaultProvider == ProviderGemini && (r.defaultModel == item.model || (r.defaultModel == "" && item.model == defaultGeminiModel)))
				models = append(models, domain.ModelOption{
					Provider:    ProviderGemini,
					Model:       item.model,
					DisplayName: item.displayName,
					IsDefault:   isDefault,
				})
			}
		case ProviderOpenAI:
			catalog := []struct {
				model       string
				displayName string
			}{
				{"gpt-4o", "OpenAI — gpt-4o"},
				{"gpt-4o-mini", "OpenAI — gpt-4o-mini"},
				{"o3-mini", "OpenAI — o3-mini"},
				{"o1", "OpenAI — o1"},
			}
			for _, item := range catalog {
				isDefault := (r.defaultProvider == ProviderOpenAI && (r.defaultModel == item.model || (r.defaultModel == "" && item.model == defaultOpenAIModel)))
				models = append(models, domain.ModelOption{
					Provider:    ProviderOpenAI,
					Model:       item.model,
					DisplayName: item.displayName,
					IsDefault:   isDefault,
				})
			}
		}
	}

	if models == nil {
		models = []domain.ModelOption{}
	}
	return models
}

