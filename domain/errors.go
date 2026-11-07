package domain

import "errors"

// Sentinel domain errors for Resumeee.
var (
	// ErrNotFound is returned when a requested domain entity cannot be located.
	ErrNotFound = errors.New("resource not found")

	// ErrValidation is returned when entity attributes violate schema invariants.
	ErrValidation = errors.New("domain validation error")

	// ErrProvenance is returned when a tailored bullet cites nonexistent facts or skills ("No Lies").
	ErrProvenance = errors.New("provenance validation failed")

	// ErrTemplateSyntax is returned when a template definition contains invalid Go template syntax.
	ErrTemplateSyntax = errors.New("invalid template syntax")

	// ErrQueueFull is returned when asynchronous worker pool job capacity is saturated.
	ErrQueueFull = errors.New("worker queue full")

	// ErrDefaultBranchImmutable is returned when attempting to delete or tailor the default branch.
	ErrDefaultBranchImmutable = errors.New("default branch cannot be deleted or tailored")

	// ErrConflict is returned when an entity violates a unique constraint or concurrency version precondition.
	ErrConflict = errors.New("resource conflict")

	// ErrStrategyImmutable is returned when attempting to modify or delete a built-in strategy.
	ErrStrategyImmutable = errors.New("built-in strategy cannot be modified or deleted")

	// ErrForbidden is returned when a user attempts to access an unauthorized or foreign resource.
	ErrForbidden = errors.New("access forbidden")

	// ErrQuotaExceeded is returned when a tenant's LLM operation quota (daily requests or monthly tokens) is saturated.
	ErrQuotaExceeded = errors.New("llm operation quota exceeded")

	// ErrGlobalSkillImmutable is returned when attempting to modify or delete a system-seeded global skill.
	ErrGlobalSkillImmutable = errors.New("global skill cannot be modified or deleted")

	// ErrUnauthorized is returned when an API request lacks valid authentication credentials.
	ErrUnauthorized = errors.New("unauthorized")
)

