package llm

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

// maxTraceErrorRunes bounds the sanitized error summary emitted to structured logs.
const maxTraceErrorRunes = 256

// llmTrace coordinates in-flight timing and request metadata between request dispatch and response receipt.
type llmTrace struct {
	startTime time.Time
	provider  string
	model     string
	operation string
}

// elapsed returns the duration elapsed since trace started, or zero when trace is nil or unstarted.
func (t *llmTrace) elapsed() time.Duration {
	if t == nil || t.startTime.IsZero() {
		return 0
	}
	return time.Since(t.startTime)
}

// logLLMRequest emits an info-level structured log record prior to dispatching a call to an LLM provider.
func logLLMRequest(ctx context.Context, logger *slog.Logger, provider, model, operation string) *llmTrace {
	if logger == nil {
		logger = slog.Default()
	}
	logger.LogAttrs(ctx, slog.LevelInfo, "dispatching LLM request",
		slog.String("provider", provider),
		slog.String("model", model),
		slog.String("operation", operation),
	)

	return &llmTrace{
		startTime: time.Now(),
		provider:  provider,
		model:     model,
		operation: operation,
	}
}

// logLLMResponseTelemetry emits a structured response log record using measured latency and response metadata.
func logLLMResponseTelemetry(ctx context.Context, logger *slog.Logger, trace *llmTrace, latency time.Duration, resp *Response, err error) {
	var statusCode int
	if resp != nil {
		statusCode = resp.StatusCode
		if latency == 0 && resp.Latency > 0 {
			latency = resp.Latency
		}
	}
	if latency == 0 {
		latency = trace.elapsed()
	}
	logLLMResponseRecord(ctx, logger, trace, latency, statusCode, err)
}

// formatTraceError flattens whitespace and bounds error text before writing structured trace logs.
func formatTraceError(err error) string {
	if err == nil {
		return ""
	}
	clean := strings.Join(strings.Fields(err.Error()), " ")
	runes := []rune(clean)
	if len(runes) > maxTraceErrorRunes {
		return string(runes[:maxTraceErrorRunes]) + "..."
	}
	return clean
}

// logLLMResponseRecord writes the canonical structured response log entry without leaking prompt or body payloads.
func logLLMResponseRecord(ctx context.Context, logger *slog.Logger, trace *llmTrace, duration time.Duration, statusCode int, err error) {
	if logger == nil {
		logger = slog.Default()
	}

	var (
		provider  string
		model     string
		operation string
	)
	if trace != nil {
		provider = trace.provider
		model = trace.model
		operation = trace.operation
	}

	attrs := []slog.Attr{
		slog.String("provider", provider),
		slog.String("model", model),
		slog.String("operation", operation),
		slog.Int64("duration_ms", duration.Milliseconds()),
		slog.Int("status_code", statusCode),
	}
	if err != nil {
		attrs = append(attrs, slog.String("error", formatTraceError(err)))
	}
	logger.LogAttrs(ctx, slog.LevelInfo, "received LLM response", attrs...)
}
