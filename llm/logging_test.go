package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"

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

var allowedTelemetryLogKeys = map[string]bool{
	"time":        true,
	"level":       true,
	"msg":         true,
	"provider":    true,
	"model":       true,
	"operation":   true,
	"duration_ms": true,
	"status_code": true,
	"error":       true,
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

		var rawMap map[string]any
		if err := json.Unmarshal([]byte(line), &rawMap); err != nil {
			t.Fatalf("failed to parse raw log map %q: %v", line, err)
		}
		for key := range rawMap {
			if !allowedTelemetryLogKeys[key] {
				t.Errorf("unexpected attribute key %q in structured log record: %s", key, line)
			}
		}

		var r parsedLogRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("failed to parse log line %q: %v", line, err)
		}
		records = append(records, r)
	}
	return records
}

func TestLLMLogging_ClientProvider_TelemetryAndPrivacy(t *testing.T) {
	const (
		secretSystemPrompt  = "SECRET_SYSTEM_PROMPT_SENTINEL_9182"
		secretUserPrompt    = "SECRET_USER_PROMPT_SENTINEL_7364"
		secretCandidateFact = "SECRET_CANDIDATE_FACT_SENTINEL_5541"
		secretJobReq        = "SECRET_JOB_REQUIREMENT_SENTINEL_3320"
		secretRawBody       = `{"secret_raw_body_sentinel":"DO_NOT_LOG_OR_PERSIST_8844"}`
	)

	respPayload := sampleResponse
	respPayload.TailoredContent.Summary = "Tailored summary sentinel SECRET_SUMMARY_OUTPUT_1122"
	respBytes, err := json.Marshal(respPayload)
	if err != nil {
		t.Fatalf("failed to marshal sample response: %v", err)
	}
	sampleRespStr := string(respBytes)

	cases := []struct {
		name           string
		providerName   string
		model          string
		wantOperation  string
		wantStatusCode int
		wantErr        error
		invoke         func(t *testing.T, prov *ClientProvider) error
		completeFn     func(ctx context.Context, req core.LLMCompletionRequest) (*core.LLMCompletionResponse, error)
	}{
		{
			name:           "GenerateTailoredResume success logs metadata without payload or disk artifacts",
			providerName:   ProviderGemini,
			model:          "gemini-2.5-pro",
			wantOperation:  "GenerateTailoredResume",
			wantStatusCode: http.StatusOK,
			completeFn: func(_ context.Context, _ core.LLMCompletionRequest) (*core.LLMCompletionResponse, error) {
				return &core.LLMCompletionResponse{
					Text:       sampleRespStr,
					RawBody:    secretRawBody,
					StatusCode: http.StatusOK,
					Usage:      core.TokenUsage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
				}, nil
			},
			invoke: func(t *testing.T, prov *ClientProvider) error {
				t.Helper()
				req := sampleRequest()
				req.Model = "gemini-2.5-pro"
				req.MainProfile.Facts = append(req.MainProfile.Facts, core.Fact{
					ID:        "fact-secret",
					Statement: secretCandidateFact,
				})
				req.BranchContext.TargetRequirements = secretJobReq
				res, callErr := prov.GenerateTailoredResume(context.Background(), req)
				if callErr == nil && res == nil {
					t.Fatal("expected non-nil GenerationResponse")
				}
				return callErr
			},
		},
		{
			name:           "GenerateTailoredResume upstream error logs status and error without leaking payload",
			providerName:   ProviderOpenAI,
			model:          "gpt-4o",
			wantOperation:  "GenerateTailoredResume",
			wantStatusCode: http.StatusTooManyRequests,
			wantErr:        ErrRateLimited,
			completeFn: func(_ context.Context, _ core.LLMCompletionRequest) (*core.LLMCompletionResponse, error) {
				return &core.LLMCompletionResponse{
					RawBody:    secretRawBody,
					StatusCode: http.StatusTooManyRequests,
				}, ErrRateLimited
			},
			invoke: func(t *testing.T, prov *ClientProvider) error {
				t.Helper()
				req := sampleRequest()
				req.Model = "gpt-4o"
				req.MainProfile.Facts = append(req.MainProfile.Facts, core.Fact{
					ID:        "fact-secret",
					Statement: secretCandidateFact,
				})
				req.BranchContext.TargetRequirements = secretJobReq
				_, callErr := prov.GenerateTailoredResume(context.Background(), req)
				return callErr
			},
		},
		{
			name:           "CompleteStructured unstructured success logs metadata without leaking prompts or response",
			providerName:   ProviderAnthropic,
			model:          "claude-3-7-sonnet-20250219",
			wantOperation:  "CompleteText",
			wantStatusCode: http.StatusOK,
			completeFn: func(_ context.Context, _ core.LLMCompletionRequest) (*core.LLMCompletionResponse, error) {
				return &core.LLMCompletionResponse{
					Text:       "extracted output SECRET_SUMMARY_OUTPUT_1122",
					RawBody:    secretRawBody,
					StatusCode: http.StatusOK,
					Usage:      core.TokenUsage{PromptTokens: 15, CompletionTokens: 25, TotalTokens: 40},
				}, nil
			},
			invoke: func(t *testing.T, prov *ClientProvider) error {
				t.Helper()
				text, usage, callErr := prov.CompleteStructured(context.Background(), "CompleteText", core.LLMCompletionRequest{
					Model:        "claude-3-7-sonnet-20250219",
					SystemPrompt: secretSystemPrompt,
					UserPrompt:   secretUserPrompt,
				})
				if callErr == nil {
					if !strings.Contains(text, "extracted output") {
						t.Errorf("unexpected completion text %q", text)
					}
					if usage.TotalTokens != 40 {
						t.Errorf("expected 40 total tokens, got %d", usage.TotalTokens)
					}
				}
				return callErr
			},
		},
		{
			name:           "CompleteStructured unstructured upstream failure logs error without leaking prompts or response",
			providerName:   ProviderCompat,
			model:          "llama3.3",
			wantOperation:  "CompleteText",
			wantStatusCode: http.StatusServiceUnavailable,
			wantErr:        ErrUpstream,
			completeFn: func(_ context.Context, _ core.LLMCompletionRequest) (*core.LLMCompletionResponse, error) {
				return &core.LLMCompletionResponse{
					RawBody:    secretRawBody,
					StatusCode: http.StatusServiceUnavailable,
				}, ErrUpstream
			},
			invoke: func(t *testing.T, prov *ClientProvider) error {
				t.Helper()
				_, _, callErr := prov.CompleteStructured(context.Background(), "CompleteText", core.LLMCompletionRequest{
					Model:        "llama3.3",
					SystemPrompt: secretSystemPrompt,
					UserPrompt:   secretUserPrompt,
				})
				return callErr
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workDir := t.TempDir()
			t.Chdir(workDir)

			var logBuf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

			client := &fakeTransportClient{
				name:         tc.providerName,
				defaultModel: tc.model,
				completeFn:   tc.completeFn,
			}

			provider := NewClientProvider(client, WithLogger(logger))
			callErr := tc.invoke(t, provider)

			if tc.wantErr == nil {
				if callErr != nil {
					t.Fatalf("unexpected error: %v", callErr)
				}
			} else {
				if !errors.Is(callErr, tc.wantErr) {
					t.Fatalf("expected error %v, got %v", tc.wantErr, callErr)
				}
			}

			rawLogs := logBuf.String()
			for _, forbidden := range []string{
				secretSystemPrompt,
				secretUserPrompt,
				secretCandidateFact,
				secretJobReq,
				secretRawBody,
				"DO_NOT_LOG_OR_PERSIST_8844",
				"SECRET_SUMMARY_OUTPUT_1122",
			} {
				if strings.Contains(rawLogs, forbidden) {
					t.Errorf("structured logs leaked sensitive prompt/response payload %q:\n%s", forbidden, rawLogs)
				}
			}

			records := parseLogs(t, rawLogs)
			if len(records) != 2 {
				t.Fatalf("expected exactly 2 log records (dispatch + receive), got %d:\n%s", len(records), rawLogs)
			}

			dispatch := records[0]
			if dispatch.Level != "INFO" || dispatch.Msg != "dispatching LLM request" {
				t.Errorf("unexpected dispatch record header: %+v", dispatch)
			}
			if dispatch.Provider != tc.providerName || dispatch.Model != tc.model || dispatch.Operation != tc.wantOperation {
				t.Errorf("unexpected dispatch metadata: got provider=%q model=%q op=%q", dispatch.Provider, dispatch.Model, dispatch.Operation)
			}

			receipt := records[1]
			if receipt.Level != "INFO" || receipt.Msg != "received LLM response" {
				t.Errorf("unexpected receipt record header: %+v", receipt)
			}
			if receipt.Provider != tc.providerName || receipt.Model != tc.model || receipt.Operation != tc.wantOperation {
				t.Errorf("unexpected receipt metadata: got provider=%q model=%q op=%q", receipt.Provider, receipt.Model, receipt.Operation)
			}
			if receipt.DurationMS == nil || *receipt.DurationMS < 0 {
				t.Errorf("expected non-negative duration_ms in receipt log, got %v", receipt.DurationMS)
			}
			if receipt.StatusCode == nil || *receipt.StatusCode != tc.wantStatusCode {
				t.Errorf("expected status_code %d in receipt log, got %v", tc.wantStatusCode, receipt.StatusCode)
			}
			if tc.wantErr == nil && receipt.Error != "" {
				t.Errorf("expected empty error field on success, got %q", receipt.Error)
			}
			if tc.wantErr != nil && receipt.Error != tc.wantErr.Error() {
				t.Errorf("expected error field %q, got %q", tc.wantErr.Error(), receipt.Error)
			}

			entries, readErr := os.ReadDir(workDir)
			if readErr != nil {
				t.Fatalf("failed to inspect workDir: %v", readErr)
			}
			if len(entries) != 0 {
				t.Errorf("expected zero prompt/response debug files written to disk, found %d entries", len(entries))
			}
		})
	}
}

func TestLLMLogging_DefensiveNilSafety(t *testing.T) {
	ctx := context.Background()

	// Nil logger falls back to slog.Default() without panicking.
	trace := logLLMRequest(ctx, nil, ProviderGemini, "gemini-2.5-flash", "CompleteText")
	if trace == nil {
		t.Fatal("expected non-nil llmTrace")
	}
	logLLMResponse(ctx, nil, trace, http.StatusOK, nil)

	// Nil trace emits zero-valued metadata safely without panicking.
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logLLMResponse(ctx, logger, nil, http.StatusBadGateway, errors.New("gateway timeout"))

	records := parseLogs(t, logBuf.String())
	if len(records) != 1 {
		t.Fatalf("expected 1 log record for nil trace response, got %d", len(records))
	}
	if records[0].StatusCode == nil || *records[0].StatusCode != http.StatusBadGateway {
		t.Errorf("expected status_code 502, got %v", records[0].StatusCode)
	}
	if records[0].DurationMS == nil || *records[0].DurationMS != 0 {
		t.Errorf("expected duration_ms 0 for nil trace, got %v", records[0].DurationMS)
	}
	if records[0].Error != "gateway timeout" {
		t.Errorf("expected error 'gateway timeout', got %q", records[0].Error)
	}
}
