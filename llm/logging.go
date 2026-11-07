package llm

import (
	"context"
	"log/slog"
	"time"

	"resumeee/internal/domain"
)

// llmTrace coordinates in-flight debugging and timing metadata between request dispatch and response receipt.
type llmTrace struct {
	session   PromptDebugSession
	startTime time.Time
	provider  string
	model     string
	operation string
}

// logLLMRequest emits an info-level structured log record prior to dispatching a call to an LLM provider,
// and initializes a prompt debugging session if a PromptDebugger is configured.
func logLLMRequest(ctx context.Context, logger *slog.Logger, debugger PromptDebugger, provider, model, operation, systemPrompt, userPrompt string) *llmTrace {
	if logger == nil {
		logger = slog.Default()
	}
	logger.LogAttrs(ctx, slog.LevelInfo, "dispatching LLM request",
		slog.String("provider", provider),
		slog.String("model", model),
		slog.String("operation", operation),
	)

	trace := &llmTrace{
		startTime: time.Now(),
		provider:  provider,
		model:     model,
		operation: operation,
	}

	if debugger != nil {
		var tenantID string
		if uid, err := domain.UserIDFromContext(ctx); err == nil {
			tenantID = uid
		}
		session, err := debugger.StartSession(PromptDebugMeta{
			TenantID:     tenantID,
			Provider:     provider,
			Model:        model,
			Operation:    operation,
			SystemPrompt: systemPrompt,
			UserPrompt:   userPrompt,
			Timestamp:    trace.startTime,
		})
		if err != nil {
			logger.Warn("failed to start llm prompt debug session", "operation", operation, "err", err)
		} else {
			trace.session = session
		}
	}

	return trace
}

// logLLMResponse emits an info-level structured log record upon receiving a response or error from an LLM provider,
// and persists the LLM response to markdown if a prompt debugging session is active.
func logLLMResponse(ctx context.Context, logger *slog.Logger, trace *llmTrace, statusCode int, rawResponse string, err error) {
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

	if trace != nil && trace.session != nil {
		writeErr := trace.session.WriteResponse(ResponseDebugMeta{
			Provider:    provider,
			Model:       model,
			Operation:   operation,
			Duration:    duration,
			StatusCode:  statusCode,
			RawResponse: rawResponse,
			Err:         err,
			Timestamp:   time.Now(),
		})
		if writeErr != nil {
			logger.Warn("failed to write llm response debug file", "operation", operation, "err", writeErr)
		}
	}
}

