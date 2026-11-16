package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/resumeee-app/core"
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

func TestLLMTrace_RegistryAndService_TelemetryAndPrivacy(t *testing.T) {
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
		name              string
		providerName      string
		model             string
		wantOperation     string
		wantStatusCode    int
		wantMinDurationMS int64
		wantErr           error
		invoke            func(t *testing.T, reg *Registry, client Client) error
		completeFn        func(ctx context.Context, req Request) (*Response, error)
	}{
		{
			name:           "GenerateTailoredResume success logs metadata without payload or disk artifacts",
			providerName:   ProviderGemini,
			model:          "gemini-2.5-pro",
			wantOperation:  "GenerateTailoredResume",
			wantStatusCode: 200,
			completeFn: func(_ context.Context, _ Request) (*Response, error) {
				return &Response{
					Text:       sampleRespStr,
					RawBody:    secretRawBody,
					StatusCode: 200,
					Usage:      core.TokenUsage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
				}, nil
			},
			invoke: func(t *testing.T, reg *Registry, _ Client) error {
				t.Helper()
				svc := NewService(reg)
				req := sampleRequest()
				req.Model = "gemini-2.5-pro"
				req.MainProfile.Facts = append(req.MainProfile.Facts, core.Fact{
					ID:        "fact-secret",
					Statement: secretCandidateFact,
				})
				req.BranchContext.TargetRequirements = secretJobReq
				res, callErr := svc.GenerateTailoredResume(context.Background(), req)
				if callErr == nil {
					if res == nil {
						t.Fatal("expected non-nil GenerationResponse")
					}
					if res.Telemetry.Provider != ProviderGemini || res.Telemetry.Model != "gemini-2.5-pro" || res.Telemetry.StatusCode != 200 || res.Telemetry.Usage.TotalTokens != 30 {
						t.Errorf("unexpected response telemetry: %+v", res.Telemetry)
					}
				}
				return callErr
			},
		},
		{
			name:           "GenerateTailoredResume upstream error logs status and error without leaking payload",
			providerName:   ProviderOpenAI,
			model:          "gpt-4o",
			wantOperation:  "GenerateTailoredResume",
			wantStatusCode: 429,
			wantErr:        ErrRateLimited,
			completeFn: func(_ context.Context, _ Request) (*Response, error) {
				return &Response{
					RawBody:    secretRawBody,
					StatusCode: 429,
				}, ErrRateLimited
			},
			invoke: func(t *testing.T, reg *Registry, _ Client) error {
				t.Helper()
				svc := NewService(reg)
				req := sampleRequest()
				req.Model = "gpt-4o"
				req.MainProfile.Facts = append(req.MainProfile.Facts, core.Fact{
					ID:        "fact-secret",
					Statement: secretCandidateFact,
				})
				req.BranchContext.TargetRequirements = secretJobReq
				_, callErr := svc.GenerateTailoredResume(context.Background(), req)
				if errTel, ok := core.LLMTelemetryFromError(callErr); !ok || errTel.Provider != ProviderOpenAI || errTel.Model != "gpt-4o" || errTel.StatusCode != 429 {
					t.Errorf("expected attached error telemetry (openai/gpt-4o/429), got (%+v, ok=%v)", errTel, ok)
				}
				return callErr
			},
		},
		{
			name:              "CompleteStructured unstructured success logs metadata without leaking prompts or response",
			providerName:      ProviderAnthropic,
			model:             "claude-3-7-sonnet-20250219",
			wantOperation:     "CompleteText",
			wantStatusCode:    200,
			wantMinDurationMS: 1,
			completeFn: func(_ context.Context, _ Request) (*Response, error) {
				time.Sleep(2 * time.Millisecond)
				return &Response{
					Text:       "extracted output SECRET_SUMMARY_OUTPUT_1122",
					RawBody:    secretRawBody,
					StatusCode: 200,
					Usage:      core.TokenUsage{PromptTokens: 15, CompletionTokens: 25, TotalTokens: 40},
				}, nil
			},
			invoke: func(t *testing.T, reg *Registry, client Client) error {
				t.Helper()
				text, telemetry, callErr := reg.CompleteStructured(context.Background(), client, "CompleteText", Request{
					Model:        "claude-3-7-sonnet-20250219",
					SystemPrompt: secretSystemPrompt,
					UserPrompt:   secretUserPrompt,
				})
				if callErr == nil {
					if !strings.Contains(text, "extracted output") {
						t.Errorf("unexpected completion text %q", text)
					}
					if telemetry.Usage.TotalTokens != 40 {
						t.Errorf("expected 40 total tokens, got %d", telemetry.Usage.TotalTokens)
					}
					if telemetry.Provider != ProviderAnthropic || telemetry.Model != "claude-3-7-sonnet-20250219" || telemetry.StatusCode != 200 {
						t.Errorf("unexpected telemetry metadata: %+v", telemetry)
					}
					if telemetry.Latency < 2*time.Millisecond {
						t.Errorf("expected measured latency >= 2ms, got %v", telemetry.Latency)
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
			wantStatusCode: 503,
			wantErr:        ErrUpstream,
			completeFn: func(_ context.Context, _ Request) (*Response, error) {
				return &Response{
					RawBody:    secretRawBody,
					StatusCode: 503,
				}, ErrUpstream
			},
			invoke: func(t *testing.T, reg *Registry, client Client) error {
				t.Helper()
				_, telemetry, callErr := reg.CompleteStructured(context.Background(), client, "CompleteText", Request{
					Model:        "llama3.3",
					SystemPrompt: secretSystemPrompt,
					UserPrompt:   secretUserPrompt,
				})
				if telemetry.Provider != ProviderCompat || telemetry.Model != "llama3.3" || telemetry.StatusCode != 503 {
					t.Errorf("expected error telemetry metadata (compat/llama3.3/503), got %+v", telemetry)
				}
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

			reg := NewRegistry(WithLogger(logger))
			reg.Register(client)
			callErr := tc.invoke(t, reg, client)

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
			if receipt.DurationMS == nil || *receipt.DurationMS < tc.wantMinDurationMS {
				t.Errorf("expected duration_ms >= %d in receipt log, got %v", tc.wantMinDurationMS, receipt.DurationMS)
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
	logLLMResponseTelemetry(ctx, nil, trace, 0, &Response{StatusCode: 200, Latency: 15 * time.Millisecond}, nil)

	// Nil trace emits zero-valued metadata when latency and Response are also nil.
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logLLMResponseTelemetry(ctx, logger, nil, 0, nil, errors.New("gateway timeout"))
	logLLMResponseTelemetry(ctx, logger, nil, 0, &Response{StatusCode: 502, Latency: 12 * time.Millisecond}, errors.New("gateway timeout"))

	records := parseLogs(t, logBuf.String())
	if len(records) != 2 {
		t.Fatalf("expected 2 log records for nil trace responses, got %d", len(records))
	}
	if records[0].StatusCode == nil || *records[0].StatusCode != 0 || records[0].DurationMS == nil || *records[0].DurationMS != 0 {
		t.Errorf("expected zero status/duration for nil trace + nil response, got %+v", records[0])
	}
	if records[1].StatusCode == nil || *records[1].StatusCode != 502 {
		t.Errorf("expected status_code 502, got %v", records[1].StatusCode)
	}
	if records[1].DurationMS == nil || *records[1].DurationMS != 12 {
		t.Errorf("expected duration_ms 12 from Response.Latency, got %v", records[1].DurationMS)
	}
	if records[1].Error != "gateway timeout" {
		t.Errorf("expected error 'gateway timeout', got %q", records[1].Error)
	}
}
