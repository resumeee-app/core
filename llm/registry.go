package llm

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/resumeee-app/resumeee-core/domain"
)

// DefaultLLMDebugDir is the default directory for writing prompt debug artifacts.
const DefaultLLMDebugDir = "data/llm"

// Registry manages configured LLM providers and resolves provider/model selection
// according to the 4-tier resolution hierarchy.
type Registry struct {
	mu              sync.RWMutex
	providers       map[string]domain.LLMProvider
	defaultProvider string
	defaultModel    string
	explicitDefault bool
	debugger        PromptDebugger
	opts            []Option
}

// NewRegistry constructs an injectable LLM provider Registry.
// Providers are registered via RegisterClient or Register.
func NewRegistry(opts ...Option) *Registry {
	baseOpts := applyOptions(opts, "", "")
	return &Registry{
		providers: make(map[string]domain.LLMProvider),
		debugger:  baseOpts.promptDebugger,
		opts:      opts,
	}
}

// RegisterClient wraps a transport-only domain.LLMClient with prompt compilation and observability,
// and registers it in the Registry.
func (r *Registry) RegisterClient(client domain.LLMClient, extraOpts ...Option) {
	if client == nil {
		return
	}
	combinedOpts := append(append([]Option{}, r.opts...), extraOpts...)
	r.Register(NewClientProvider(client, combinedOpts...))
}

// Register adds an LLMProvider to the registry and updates the system default provider hierarchy.
func (r *Registry) Register(provider domain.LLMProvider) {
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
	priority := []string{ProviderGemini, ProviderOpenAI, ProviderAnthropic, ProviderCompat, ProviderCompatAlt}
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

// PromptDebugger returns the configured PromptDebugger instance, or nil if prompt debugging is disabled.
func (r *Registry) PromptDebugger() PromptDebugger {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.debugger
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
// 3. Main Profile defaults (profile.DefaultProvider, profile.DefaultModel)
// 4. System fallbacks (Registry default provider and model)
func (r *Registry) Resolve(req domain.GenerationRequest, _ *domain.Preset, profile *domain.Profile) (domain.LLMProvider, string, error) {
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
	} else if profile != nil && strings.TrimSpace(profile.DefaultModel) != "" {
		modelName = strings.TrimSpace(profile.DefaultModel)
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
