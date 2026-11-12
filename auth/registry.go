package auth

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Registry manages registered authentication providers and resolves active selection.
type Registry struct {
	mu              sync.RWMutex
	providers       map[string]Provider
	defaultProvider string
}

// NewRegistry constructs an empty auth.Registry.
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]Provider),
	}
}

// Register adds an auth.Provider to the registry. The provider name is normalized to lowercase.
func (r *Registry) Register(provider Provider) {
	if r == nil || provider == nil {
		return
	}
	name := strings.ToLower(strings.TrimSpace(provider.Name()))
	if name == "" {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.providers[name] = provider
	if r.defaultProvider == "" {
		r.defaultProvider = name
	}
}

// Get retrieves a registered provider by name (case-insensitive).
func (r *Registry) Get(name string) (Provider, error) {
	if r == nil {
		return nil, errors.New("nil auth registry")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	norm := strings.ToLower(strings.TrimSpace(name))
	if norm == "" {
		return nil, errors.New("empty auth provider name")
	}
	p, ok := r.providers[norm]
	if !ok {
		return nil, fmt.Errorf("auth provider %q not registered", name)
	}
	return p, nil
}

// Default returns the default auth provider.
func (r *Registry) Default() (Provider, error) {
	if r == nil {
		return nil, errors.New("nil auth registry")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.defaultProvider == "" {
		return nil, errors.New("no default auth provider configured")
	}
	p, ok := r.providers[r.defaultProvider]
	if !ok {
		return nil, fmt.Errorf("default auth provider %q not registered", r.defaultProvider)
	}
	return p, nil
}

// SetDefault designates the default auth provider name.
func (r *Registry) SetDefault(name string) error {
	if r == nil {
		return errors.New("nil auth registry")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	norm := strings.ToLower(strings.TrimSpace(name))
	if norm == "" {
		return errors.New("empty default provider name")
	}
	if _, ok := r.providers[norm]; !ok {
		return fmt.Errorf("cannot set default: auth provider %q not registered", name)
	}
	r.defaultProvider = norm
	return nil
}

// ClearDefault clears the designated default provider.
func (r *Registry) ClearDefault() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.defaultProvider = ""
}

// List returns a sorted list of registered provider names.
func (r *Registry) List() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
