package core

import (
	"context"
	"errors"
	"net"
)

// taggedError pairs a canonical error message with category sentinels so callers can match
// either the specific sentinel or its parent classification via errors.Is.
type taggedError struct {
	msg  string
	tags []error
}

// Error returns the canonical error message for taggedError.
func (e *taggedError) Error() string {
	return e.msg
}

// Unwrap exposes the underlying classification sentinels for errors.Is traversal.
func (e *taggedError) Unwrap() []error {
	return e.tags
}

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

	// ErrPermanentFailure marks failures that must terminate immediately without retrying.
	ErrPermanentFailure = errors.New("permanent non-retryable failure")

	// ErrTransientFailure marks temporary failures eligible for bounded retries within job TTL.
	ErrTransientFailure = errors.New("transient retryable failure")

	// ErrUpstreamQuota indicates upstream LLM provider account billing or credit exhaustion.
	ErrUpstreamQuota = &taggedError{
		msg:  "upstream provider billing or quota exhausted",
		tags: []error{ErrPermanentFailure},
	}

	// ErrPendingLimitExceeded indicates a tenant or system reached its persisted pending job admission cap.
	ErrPendingLimitExceeded = &taggedError{
		msg:  "pending job admission limit exceeded",
		tags: []error{ErrQueueFull},
	}

	// ErrJobExpired indicates a queued job passed its expiration deadline before completing.
	ErrJobExpired = &taggedError{
		msg:  "queued job expired",
		tags: []error{ErrPermanentFailure},
	}

	// ErrJobCancelled indicates a pending job was cancelled prior to execution.
	ErrJobCancelled = &taggedError{
		msg:  "job cancelled",
		tags: []error{ErrPermanentFailure},
	}

	// ErrLeaseExpired indicates an active worker lease timed out before completion.
	ErrLeaseExpired = errors.New("worker lease expired")
)

// ErrorClass categorizes asynchronous job failures into fail-fast or retryable classes.
type ErrorClass string

// Canonical error classes returned by ClassifyError.
const (
	// ErrorClassPermanent identifies deterministic, auth, validation, cancelled, or unknown failures that must fail fast.
	ErrorClassPermanent ErrorClass = "permanent"

	// ErrorClassUpstreamQuota identifies upstream provider billing or credit exhaustion failures that must fail fast.
	ErrorClassUpstreamQuota ErrorClass = "upstream_quota"

	// ErrorClassTransient identifies temporary upstream or timeout failures eligible for bounded retries.
	ErrorClassTransient ErrorClass = "transient"
)

// ClassifyError inspects err and returns its ErrorClass for worker retry vs fail-fast decisions.
// Unrecognized errors default to ErrorClassPermanent so unexpected bugs never trigger blind retries.
func ClassifyError(err error) ErrorClass {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return ErrorClassPermanent
	}
	if errors.Is(err, ErrUpstreamQuota) {
		return ErrorClassUpstreamQuota
	}
	if errors.Is(err, ErrPermanentFailure) ||
		errors.Is(err, ErrValidation) ||
		errors.Is(err, ErrUnauthorized) ||
		errors.Is(err, ErrForbidden) ||
		errors.Is(err, ErrNotFound) ||
		errors.Is(err, ErrConflict) ||
		errors.Is(err, ErrProvenance) ||
		errors.Is(err, ErrTemplateSyntax) ||
		errors.Is(err, ErrQuotaExceeded) {
		return ErrorClassPermanent
	}
	if errors.Is(err, ErrTransientFailure) || errors.Is(err, context.DeadlineExceeded) {
		return ErrorClassTransient
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return ErrorClassTransient
	}
	return ErrorClassPermanent
}

// IsRetryableError reports whether err is classified as transient and eligible for bounded retry.
func IsRetryableError(err error) bool {
	return ClassifyError(err) == ErrorClassTransient
}
