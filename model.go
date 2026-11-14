package core

import (
	"fmt"
	"strings"
)

// ModelOption represents a configured LLM provider and model pairing safe for public enumeration.
type ModelOption struct {
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	DisplayName string `json:"display_name"`
	IsDefault   bool   `json:"is_default"`
}

// ModelEnumerator defines the interface for enumerating available configured LLM models.
type ModelEnumerator interface {
	AvailableModels() []ModelOption
}

// ValidateReasoningLevel verifies that the given reasoning effort level is empty or
// matches one of the accepted enum values ("none", "low", "medium", "high").
func ValidateReasoningLevel(level string) error {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "", "none", "low", "medium", "high":
		return nil
	default:
		return fmt.Errorf("%w: reasoning_level must be one of: none, low, medium, high", ErrValidation)
	}
}

// ValidateModelOverride verifies that the specified model override and optional provider override
// correspond to an available LLM model option registered in the enumerator catalog.
func ValidateModelOverride(enumerator ModelEnumerator, providerOverride, modelOverride string) error {
	if enumerator == nil || modelOverride == "" {
		return nil
	}

	available := enumerator.AvailableModels()
	if len(available) == 0 {
		return nil
	}

	var found bool
	for _, m := range available {
		if strings.EqualFold(m.Model, modelOverride) {
			if providerOverride == "" || strings.EqualFold(m.Provider, providerOverride) {
				found = true
				break
			}
		}
	}

	if !found {
		return fmt.Errorf("%w: model_override is not available for the specified provider", ErrValidation)
	}

	return nil
}
