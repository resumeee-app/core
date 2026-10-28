package llm

import (
	"context"
	"log/slog"
	"time"
)

// logLLMRequest emits an info-level structured log record prior to dispatching a call to an LLM provider.
func logLLMRequest(ctx context.Context, logger *slog.Logger, provider, model, operation, systemPrompt, userPrompt string) {
	if logger == nil {
		logger = slog.Default()
	}
	logger.LogAttrs(ctx, slog.LevelInfo, "dispatching LLM request",
		slog.String("provider", provider),
		slog.String("model", model),
		slog.String("operation", operation),
		slog.String("system_prompt", systemPrompt),
		slog.String("user_prompt", userPrompt),
	)
}

// logLLMResponse emits an info-level structured log record upon receiving a response or error from an LLM provider.
func logLLMResponse(ctx context.Context, logger *slog.Logger, provider, model, operation string, duration time.Duration, statusCode int, rawResponse string, err error) {
	if logger == nil {
		logger = slog.Default()
	}
	attrs := []slog.Attr{
		slog.String("provider", provider),
		slog.String("model", model),
		slog.String("operation", operation),
		slog.Int64("duration_ms", duration.Milliseconds()),
		slog.Int("status_code", statusCode),
		slog.String("response", rawResponse),
	}
	if err != nil {
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	logger.LogAttrs(ctx, slog.LevelInfo, "received LLM response", attrs...)
}
