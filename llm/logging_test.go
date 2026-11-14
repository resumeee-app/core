package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

type parsedLogRecord struct {
	Level      string `json:"level"`
	Msg        string `json:"msg"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Operation  string `json:"operation"`
	DurationMS *int64 `json:"duration_ms"`
	StatusCode *int   `json:"status_code"`
	Error      string `json:"error"`
}

func parseLogs(t *testing.T, logOutput string) []parsedLogRecord {
	t.Helper()
	var records []parsedLogRecord
	scanner := bufio.NewScanner(strings.NewReader(strings.TrimSpace(logOutput)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var r parsedLogRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("failed to parse log line %q: %v", line, err)
		}
		records = append(records, r)
	}
	return records
}

func TestLLMLogging_ClientProvider(t *testing.T) {
	respBytes, err := json.Marshal(sampleResponse)
	if err != nil {
		t.Fatalf("failed to marshal sample response: %v", err)
	}
	sampleRespStr := string(respBytes)

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	client := &fakeTransportClient{
		name:         ProviderGemini,
		defaultModel: defaultGeminiModel,
		completeFn: func(_ context.Context, _ core.LLMCompletionRequest) (*core.LLMCompletionResponse, error) {
			return &core.LLMCompletionResponse{
				Text:       sampleRespStr,
				RawBody:    sampleRespStr,
				StatusCode: http.StatusOK,
				Usage:      core.TokenUsage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
			}, nil
		},
	}

	provider := NewClientProvider(client, WithLogger(logger))

	res, err := provider.GenerateTailoredResume(context.Background(), sampleRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil response")
	}

	records := parseLogs(t, logBuf.String())
	if len(records) < 2 {
		t.Fatalf("expected at least 2 log records (dispatch + receive), got %d", len(records))
	}
	if records[0].Provider != ProviderGemini || records[0].Operation != "GenerateTailoredResume" {
		t.Errorf("unexpected dispatch log record: %+v", records[0])
	}
	if records[1].StatusCode == nil || *records[1].StatusCode != http.StatusOK {
		t.Errorf("expected status_code 200 in response log, got %+v", records[1])
	}
	if records[1].DurationMS == nil || *records[1].DurationMS < 0 {
		t.Errorf("expected non-negative duration_ms in response log, got %+v", records[1])
	}

	// Verify that prompt content and response bodies are not leaked into structured log attributes
	rawLog := logBuf.String()
	if strings.Contains(rawLog, "system_prompt") || strings.Contains(rawLog, "user_prompt") {
		t.Errorf("structured logs must not log prompt content, got: %s", rawLog)
	}
	if strings.Contains(rawLog, "Jane Doe") || strings.Contains(rawLog, "Acme Corp") {
		t.Errorf("structured logs must not contain sensitive candidate prompt data, got: %s", rawLog)
	}
}

func TestLLMLogging_CompleteTextWithUsage(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	client := &fakeTransportClient{
		name:         ProviderOpenAI,
		defaultModel: defaultOpenAIModel,
		completeFn: func(_ context.Context, _ core.LLMCompletionRequest) (*core.LLMCompletionResponse, error) {
			return &core.LLMCompletionResponse{
				Text:       "Secret completion content",
				RawBody:    "Secret completion content",
				StatusCode: http.StatusOK,
				Usage:      core.TokenUsage{PromptTokens: 5, CompletionTokens: 15, TotalTokens: 20},
			}, nil
		},
	}

	provider := NewClientProvider(client, WithLogger(logger))
	text, usage, err := provider.CompleteTextWithUsage(context.Background(), "System prompt with secrets", "User prompt with candidate info", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "Secret completion content" {
		t.Errorf("expected completion text %q, got %q", "Secret completion content", text)
	}
	if usage.TotalTokens != 20 {
		t.Errorf("expected 20 tokens, got %d", usage.TotalTokens)
	}

	records := parseLogs(t, logBuf.String())
	if len(records) != 2 {
		t.Fatalf("expected 2 log records, got %d", len(records))
	}
	if records[0].Operation != "CompleteText" || records[0].Provider != ProviderOpenAI {
		t.Errorf("unexpected dispatch record: %+v", records[0])
	}
	if records[1].StatusCode == nil || *records[1].StatusCode != http.StatusOK {
		t.Errorf("unexpected receive record: %+v", records[1])
	}

	rawLog := logBuf.String()
	if strings.Contains(rawLog, "System prompt with secrets") || strings.Contains(rawLog, "User prompt with candidate info") {
		t.Errorf("prompts must not be logged in structured logs, got: %s", rawLog)
	}
	if strings.Contains(rawLog, "Secret completion content") {
		t.Errorf("response body must not be logged in structured logs, got: %s", rawLog)
	}
}

func TestLLMLogging_UnitTable(t *testing.T) {
	tests := []struct {
		name          string
		provider      string
		model         string
		operation     string
		statusCode    int
		respErr       error
		expectedLevel string
		expectErrAttr bool
	}{
		{
			name:          "successful request",
			provider:      "gemini",
			model:         "gemini-2.5-flash",
			operation:     "test-op",
			statusCode:    200,
			respErr:       nil,
			expectedLevel: "INFO",
			expectErrAttr: false,
		},
		{
			name:          "failed request with error",
			provider:      "openai",
			model:         "gpt-4o",
			operation:     "failing-op",
			statusCode:    500,
			respErr:       errors.New("upstream failure"),
			expectedLevel: "INFO",
			expectErrAttr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

			trace := logLLMRequest(context.Background(), logger, tc.provider, tc.model, tc.operation)
			if trace == nil {
				t.Fatal("expected non-nil trace")
			}
			time.Sleep(2 * time.Millisecond)
			logLLMResponse(context.Background(), logger, trace, tc.statusCode, tc.respErr)

			records := parseLogs(t, buf.String())
			if len(records) != 2 {
				t.Fatalf("expected 2 log records, got %d", len(records))
			}

			// Verify dispatch record
			if records[0].Provider != tc.provider || records[0].Model != tc.model || records[0].Operation != tc.operation {
				t.Errorf("unexpected dispatch record: %+v", records[0])
			}
			// Verify response record
			if records[1].StatusCode == nil || *records[1].StatusCode != tc.statusCode {
				t.Errorf("unexpected response status code: %+v", records[1])
			}
			if tc.expectErrAttr && records[1].Error == "" {
				t.Errorf("expected error attribute in response record, got none: %+v", records[1])
			}
			if !tc.expectErrAttr && records[1].Error != "" {
				t.Errorf("unexpected error attribute: %s", records[1].Error)
			}
		})
	}
}

func TestLLMLogging_NilSafety(t *testing.T) {
	// Must not panic with nil logger or nil trace
	trace := logLLMRequest(context.Background(), nil, "provider", "model", "op")
	if trace == nil {
		t.Fatal("expected non-nil trace even with nil logger")
	}
	logLLMResponse(context.Background(), nil, trace, http.StatusOK, nil)
	logLLMResponse(context.Background(), nil, nil, http.StatusInternalServerError, errors.New("err"))
}
