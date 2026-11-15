package core

import (
	"errors"
	"time"
)

// LLMTelemetry captures resolved provider identity, upstream HTTP status,
// measured round-trip latency, and extracted token usage for an LLM execution.
type LLMTelemetry struct {
	Provider   string        `json:"provider,omitempty"`
	Model      string        `json:"model,omitempty"`
	StatusCode int           `json:"status_code,omitempty"`
	Latency    time.Duration `json:"latency,omitempty"`
	Usage      TokenUsage    `json:"usage"`
}

// Normalize clamps negative metadata and token counts to zero and backfills TotalTokens on Usage.
func (t LLMTelemetry) Normalize() LLMTelemetry {
	if t.StatusCode < 0 {
		t.StatusCode = 0
	}
	if t.Latency < 0 {
		t.Latency = 0
	}
	t.Usage = t.Usage.Normalize()
	return t
}

// resolveResponseTelemetry returns normalized telemetry with Usage backfilled from usage when Telemetry.Usage is zero.
func resolveResponseTelemetry(telemetry LLMTelemetry, usage TokenUsage) LLMTelemetry {
	t := telemetry.Normalize()
	normalizedUsage := usage.Normalize()
	if t.Usage.IsZero() && !normalizedUsage.IsZero() {
		t.Usage = normalizedUsage
	}
	return t
}

// IsZero reports whether all telemetry metadata and token fields are zero or empty.
func (t LLMTelemetry) IsZero() bool {
	return t.Provider == "" &&
		t.Model == "" &&
		t.StatusCode == 0 &&
		t.Latency == 0 &&
		t.Usage.IsZero()
}

// Merge combines sequential LLM call telemetry within a composite operation,
// accumulating latency and token counts while retaining the latest execution status and identity.
func (t LLMTelemetry) Merge(next LLMTelemetry) LLMTelemetry {
	left := t.Normalize()
	right := next.Normalize()
	merged := LLMTelemetry{
		Provider:   right.Provider,
		Model:      right.Model,
		StatusCode: right.StatusCode,
		Latency:    left.Latency + right.Latency,
		Usage:      left.Usage.Add(right.Usage),
	}
	if merged.Provider == "" {
		merged.Provider = left.Provider
	}
	if merged.Model == "" {
		merged.Model = left.Model
	}
	// Preserve the prior HTTP status only when next represents no upstream call execution.
	if right.IsZero() && left.StatusCode != 0 {
		merged.StatusCode = left.StatusCode
	}
	return merged
}

// LLMTelemetryError associates upstream LLMTelemetry with a failed operation error.
type LLMTelemetryError struct {
	Err       error
	Telemetry LLMTelemetry
}

// Error returns the wrapped error text.
func (e *LLMTelemetryError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

// Unwrap returns the wrapped cause error for errors.Is and errors.As matching.
func (e *LLMTelemetryError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// AttachLLMTelemetry wraps err with telemetry when err is non-nil and telemetry is non-zero,
// avoiding duplicate wrapping when err already exposes equal outermost telemetry.
func AttachLLMTelemetry(err error, telemetry LLMTelemetry) error {
	if err == nil {
		return nil
	}
	normalized := telemetry.Normalize()
	if normalized.IsZero() {
		return err
	}
	if existing, ok := LLMTelemetryFromError(err); ok && existing == normalized {
		return err
	}
	return &LLMTelemetryError{
		Err:       err,
		Telemetry: normalized,
	}
}

// LLMTelemetryFromError extracts the outermost LLMTelemetry attached to err, if present.
func LLMTelemetryFromError(err error) (LLMTelemetry, bool) {
	var telemetryErr *LLMTelemetryError
	if errors.As(err, &telemetryErr) && telemetryErr != nil {
		return telemetryErr.Telemetry.Normalize(), true
	}
	return LLMTelemetry{}, false
}

// ResolveTelemetry prefers explicit non-zero fields in telemetry and backfills any
// missing fields from LLMTelemetry attached to err.
func ResolveTelemetry(telemetry LLMTelemetry, err error) LLMTelemetry {
	resolved := telemetry.Normalize()
	errTel, ok := LLMTelemetryFromError(err)
	if !ok {
		return resolved
	}
	if resolved.Provider == "" {
		resolved.Provider = errTel.Provider
	}
	if resolved.Model == "" {
		resolved.Model = errTel.Model
	}
	if resolved.StatusCode == 0 {
		resolved.StatusCode = errTel.StatusCode
	}
	if resolved.Latency == 0 {
		resolved.Latency = errTel.Latency
	}
	if resolved.Usage.IsZero() {
		resolved.Usage = errTel.Usage
	}
	return resolved.Normalize()
}
