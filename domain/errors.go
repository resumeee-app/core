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
)
