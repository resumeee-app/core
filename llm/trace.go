package llm

import (
	"context"
	"log/slog"
	"time"
)

// llmTrace coordinates in-flight timing and request metadata between request dispatch and response receipt.
type llmTrace struct {
	startTime time.Time
	provider  string
	model     string
	operation string
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

// logLLMResponse emits an info-level structured log record upon receiving a response or error from an LLM provider.
func logLLMResponse(ctx context.Context, logger *slog.Logger, trace *llmTrace, statusCode int, err error) {
	if logger == nil {
		logger = slog.Default()
	}

	var (
		provider  string
		model     string
		operation string
		duration  time.Duration
	)
	if trace != nil {
		provider = trace.provider
		model = trace.model
		operation = trace.operation
		if !trace.startTime.IsZero() {
			duration = time.Since(trace.startTime)
		}
	}

	attrs := []slog.Attr{
		slog.String("provider", provider),
		slog.String("model", model),
		slog.String("operation", operation),
		slog.Int64("duration_ms", duration.Milliseconds()),
		slog.Int("status_code", statusCode),
	}
	if err != nil {
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	logger.LogAttrs(ctx, slog.LevelInfo, "received LLM response", attrs...)
}
