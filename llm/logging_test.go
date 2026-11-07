package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"resumeee/internal/config"
	"resumeee/internal/domain"
)

type parsedLogRecord struct {
	Level        string `json:"level"`
	Msg          string `json:"msg"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	Operation    string `json:"operation"`
	SystemPrompt string `json:"system_prompt"`
	UserPrompt   string `json:"user_prompt"`
	DurationMS   *int64 `json:"duration_ms"`
	StatusCode   *int   `json:"status_code"`
	Response     string `json:"response"`
	Error        string `json:"error"`
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

func TestLLMLogging_Table(t *testing.T) {
	const secretAPIKey = "super-secret-test-key-xyz-987654321"

	respBytes, err := json.Marshal(sampleResponse)
	if err != nil {
		t.Fatalf("failed to marshal sample response: %v", err)
	}
	sampleRespStr := string(respBytes)

	tests := []struct {
		name              string
		providerName      string
		operation         string
		expectedModel     string
		serverHandler     http.HandlerFunc
		execute           func(t *testing.T, baseURL string, logger *slog.Logger)
		expectError       bool
		expectedStatus    int
		validateRawResp   func(t *testing.T, rawResp string)
	}{
		{
			name:          "gemini_tailored_resume_success",
			providerName:  ProviderGemini,
			operation:     "GenerateTailoredResume",
			expectedModel: "test-model-override",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				geminiResp := map[string]any{
					"candidates": []map[string]any{
						{
							"content": map[string]any{
								"parts": []map[string]any{
									{"text": sampleRespStr},
								},
							},
						},
					},
				}
				_ = json.NewEncoder(w).Encode(geminiResp)
			},
			execute: func(t *testing.T, baseURL string, logger *slog.Logger) {
				provider := NewGeminiProvider(secretAPIKey,
					WithBaseURL(baseURL),
					WithLogger(logger),
				)
				res, err := provider.GenerateTailoredResume(context.Background(), sampleRequest())
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if res == nil {
					t.Fatal("expected non-nil response")
				}
			},
			expectError:    false,
			expectedStatus: http.StatusOK,
			validateRawResp: func(t *testing.T, rawResp string) {
				if !strings.Contains(rawResp, "candidates") {
					t.Errorf("expected raw response to contain candidates, got: %s", rawResp)
				}
			},
		},
		{
			name:          "gemini_complete_text_success",
			providerName:  ProviderGemini,
			operation:     "CompleteText",
			expectedModel: defaultGeminiModel,
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				geminiResp := map[string]any{
					"candidates": []map[string]any{
						{
							"content": map[string]any{
								"parts": []map[string]any{
									{"text": "Gemini extracted resume content"},
								},
							},
						},
					},
				}
				_ = json.NewEncoder(w).Encode(geminiResp)
			},
			execute: func(t *testing.T, baseURL string, logger *slog.Logger) {
				provider := NewGeminiProvider(secretAPIKey,
					WithBaseURL(baseURL),
					WithLogger(logger),
				)
				text, err := provider.CompleteText(context.Background(), "System instructions", "User prompt content", "")
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if text != "Gemini extracted resume content" {
					t.Errorf("unexpected completed text: %q", text)
				}
			},
			expectError:    false,
			expectedStatus: http.StatusOK,
			validateRawResp: func(t *testing.T, rawResp string) {
				if !strings.Contains(rawResp, "Gemini extracted resume content") {
					t.Errorf("expected raw response to contain generated text, got: %s", rawResp)
				}
			},
		},
		{
			name:          "openai_tailored_resume_success",
			providerName:  ProviderOpenAI,
			operation:     "GenerateTailoredResume",
			expectedModel: "test-model-override",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				openAIResp := map[string]any{
					"choices": []map[string]any{
						{
							"message": map[string]any{
								"content": sampleRespStr,
							},
						},
					},
				}
				_ = json.NewEncoder(w).Encode(openAIResp)
			},
			execute: func(t *testing.T, baseURL string, logger *slog.Logger) {
				provider := NewOpenAIProvider(secretAPIKey,
					WithBaseURL(baseURL),
					WithLogger(logger),
				)
				res, err := provider.GenerateTailoredResume(context.Background(), sampleRequest())
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if res == nil {
					t.Fatal("expected non-nil response")
				}
			},
			expectError:    false,
			expectedStatus: http.StatusOK,
			validateRawResp: func(t *testing.T, rawResp string) {
				if !strings.Contains(rawResp, "choices") {
					t.Errorf("expected raw response to contain choices, got: %s", rawResp)
				}
			},
		},
		{
			name:          "openai_complete_text_success",
			providerName:  ProviderOpenAI,
			operation:     "CompleteText",
			expectedModel: defaultOpenAIModel,
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				openAIResp := map[string]any{
					"choices": []map[string]any{
						{
							"message": map[string]any{
								"content": "OpenAI completion text",
							},
						},
					},
				}
				_ = json.NewEncoder(w).Encode(openAIResp)
			},
			execute: func(t *testing.T, baseURL string, logger *slog.Logger) {
				provider := NewOpenAIProvider(secretAPIKey,
					WithBaseURL(baseURL),
					WithLogger(logger),
				)
				text, err := provider.CompleteText(context.Background(), "System instructions", "User prompt content", "")
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if text != "OpenAI completion text" {
					t.Errorf("unexpected completed text: %q", text)
				}
			},
			expectError:    false,
			expectedStatus: http.StatusOK,
			validateRawResp: func(t *testing.T, rawResp string) {
				if !strings.Contains(rawResp, "OpenAI completion text") {
					t.Errorf("expected raw response to contain completion text, got: %s", rawResp)
				}
			},
		},
		{
			name:          "anthropic_tailored_resume_success",
			providerName:  ProviderAnthropic,
			operation:     "GenerateTailoredResume",
			expectedModel: "test-model-override",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				anthropicResp := map[string]any{
					"content": []map[string]any{
						{
							"type":  "tool_use",
							"name":  anthropicToolName,
							"input": sampleResponse,
						},
					},
				}
				_ = json.NewEncoder(w).Encode(anthropicResp)
			},
			execute: func(t *testing.T, baseURL string, logger *slog.Logger) {
				provider := NewAnthropicProvider(secretAPIKey,
					WithBaseURL(baseURL),
					WithLogger(logger),
				)
				res, err := provider.GenerateTailoredResume(context.Background(), sampleRequest())
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if res == nil {
					t.Fatal("expected non-nil response")
				}
			},
			expectError:    false,
			expectedStatus: http.StatusOK,
			validateRawResp: func(t *testing.T, rawResp string) {
				if !strings.Contains(rawResp, anthropicToolName) {
					t.Errorf("expected raw response to contain tool name, got: %s", rawResp)
				}
			},
		},
		{
			name:          "anthropic_complete_text_success",
			providerName:  ProviderAnthropic,
			operation:     "CompleteText",
			expectedModel: defaultAnthropicModel,
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				anthropicResp := map[string]any{
					"content": []map[string]any{
						{
							"type": "text",
							"text": "Anthropic message reply text",
						},
					},
				}
				_ = json.NewEncoder(w).Encode(anthropicResp)
			},
			execute: func(t *testing.T, baseURL string, logger *slog.Logger) {
				provider := NewAnthropicProvider(secretAPIKey,
					WithBaseURL(baseURL),
					WithLogger(logger),
				)
				text, err := provider.CompleteText(context.Background(), "System instructions", "User prompt content", "")
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if text != "Anthropic message reply text" {
					t.Errorf("unexpected completed text: %q", text)
				}
			},
			expectError:    false,
			expectedStatus: http.StatusOK,
			validateRawResp: func(t *testing.T, rawResp string) {
				if !strings.Contains(rawResp, "Anthropic message reply text") {
					t.Errorf("expected raw response to contain reply text, got: %s", rawResp)
				}
			},
		},
		{
			name:          "compat_tailored_resume_success",
			providerName:  ProviderCompat,
			operation:     "GenerateTailoredResume",
			expectedModel: "test-model-override",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				compatResp := map[string]any{
					"choices": []map[string]any{
						{
							"message": map[string]any{
								"content": "```json\n" + sampleRespStr + "\n```",
							},
						},
					},
				}
				_ = json.NewEncoder(w).Encode(compatResp)
			},
			execute: func(t *testing.T, baseURL string, logger *slog.Logger) {
				provider := NewCompatProvider(baseURL, secretAPIKey,
					WithLogger(logger),
				)
				res, err := provider.GenerateTailoredResume(context.Background(), sampleRequest())
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if res == nil {
					t.Fatal("expected non-nil response")
				}
			},
			expectError:    false,
			expectedStatus: http.StatusOK,
			validateRawResp: func(t *testing.T, rawResp string) {
				if !strings.Contains(rawResp, "choices") {
					t.Errorf("expected raw response to contain choices, got: %s", rawResp)
				}
			},
		},
		{
			name:          "compat_complete_text_success",
			providerName:  ProviderCompat,
			operation:     "CompleteText",
			expectedModel: defaultCompatModel,
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				compatResp := map[string]any{
					"choices": []map[string]any{
						{
							"message": map[string]any{
								"content": "OpenAI-compatible text response",
							},
						},
					},
				}
				_ = json.NewEncoder(w).Encode(compatResp)
			},
			execute: func(t *testing.T, baseURL string, logger *slog.Logger) {
				provider := NewCompatProvider(baseURL, secretAPIKey,
					WithLogger(logger),
				)
				text, err := provider.CompleteText(context.Background(), "System instructions", "User prompt content", "")
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if text != "OpenAI-compatible text response" {
					t.Errorf("unexpected completed text: %q", text)
				}
			},
			expectError:    false,
			expectedStatus: http.StatusOK,
			validateRawResp: func(t *testing.T, rawResp string) {
				if !strings.Contains(rawResp, "OpenAI-compatible text response") {
					t.Errorf("expected raw response to contain text, got: %s", rawResp)
				}
			},
		},
		{
			name:          "provider_http_error_logging",
			providerName:  ProviderGemini,
			operation:     "GenerateTailoredResume",
			expectedModel: "test-model-override",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"code":400,"message":"Invalid argument provided to Gemini API"}}`))
			},
			execute: func(t *testing.T, baseURL string, logger *slog.Logger) {
				provider := NewGeminiProvider(secretAPIKey,
					WithBaseURL(baseURL),
					WithLogger(logger),
				)
				_, err := provider.GenerateTailoredResume(context.Background(), sampleRequest())
				if err == nil {
					t.Fatal("expected error, got nil")
				}
			},
			expectError:    true,
			expectedStatus: http.StatusBadRequest,
			validateRawResp: func(t *testing.T, rawResp string) {
				if !strings.Contains(rawResp, "Invalid argument provided") {
					t.Errorf("expected error response to contain message, got: %s", rawResp)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(tc.serverHandler)
			defer server.Close()

			var logBuf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))

			tc.execute(t, server.URL, logger)

			output := logBuf.String()

			// 1. Security Invariant: API Key or Authorization token must NEVER appear in the log output
			if strings.Contains(output, secretAPIKey) {
				t.Errorf("SECURITY VIOLATION: secret API key leaked into log output:\n%s", output)
			}
			if strings.Contains(output, "Bearer ") {
				t.Errorf("SECURITY VIOLATION: Bearer token leaked into log output:\n%s", output)
			}

			// 2. Structured log entries verification
			records := parseLogs(t, output)
			if len(records) != 2 {
				t.Fatalf("expected exactly 2 log records (1 request, 1 response), got %d:\n%s", len(records), output)
			}

			// Verify Request Record
			reqRec := records[0]
			if reqRec.Level != "INFO" {
				t.Errorf("expected request log level INFO, got %q", reqRec.Level)
			}
			if reqRec.Msg != "dispatching LLM request" {
				t.Errorf("expected request msg 'dispatching LLM request', got %q", reqRec.Msg)
			}
			if reqRec.Provider != tc.providerName {
				t.Errorf("expected provider %q, got %q", tc.providerName, reqRec.Provider)
			}
			if reqRec.Model != tc.expectedModel {
				t.Errorf("expected model %q, got %q", tc.expectedModel, reqRec.Model)
			}
			if reqRec.Operation != tc.operation {
				t.Errorf("expected operation %q, got %q", tc.operation, reqRec.Operation)
			}
			if strings.TrimSpace(reqRec.SystemPrompt) == "" {
				t.Error("expected non-empty system_prompt")
			}
			if strings.TrimSpace(reqRec.UserPrompt) == "" {
				t.Error("expected non-empty user_prompt")
			}

			// Verify Response Record
			respRec := records[1]
			if respRec.Level != "INFO" {
				t.Errorf("expected response log level INFO, got %q", respRec.Level)
			}
			if respRec.Msg != "received LLM response" {
				t.Errorf("expected response msg 'received LLM response', got %q", respRec.Msg)
			}
			if respRec.Provider != tc.providerName {
				t.Errorf("expected response provider %q, got %q", tc.providerName, respRec.Provider)
			}
			if respRec.Model != tc.expectedModel {
				t.Errorf("expected response model %q, got %q", tc.expectedModel, respRec.Model)
			}
			if respRec.Operation != tc.operation {
				t.Errorf("expected response operation %q, got %q", tc.operation, respRec.Operation)
			}
			if respRec.StatusCode == nil || *respRec.StatusCode != tc.expectedStatus {
				t.Errorf("expected status code %d, got %v", tc.expectedStatus, respRec.StatusCode)
			}
			if respRec.DurationMS == nil || *respRec.DurationMS < 0 {
				t.Errorf("expected non-negative duration_ms, got %v", respRec.DurationMS)
			}
			if tc.validateRawResp != nil {
				tc.validateRawResp(t, respRec.Response)
			}
			if tc.expectError {
				if strings.TrimSpace(respRec.Error) == "" {
					t.Error("expected non-empty error in response log record")
				}
			} else {
				if respRec.Error != "" {
					t.Errorf("expected empty error in response log record, got %q", respRec.Error)
				}
			}
		})
	}
}

func TestLLMLogging_NilLoggerFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "ok"}},
			},
		})
	}))
	defer server.Close()

	// Ensure calling with no WithLogger option does not panic and uses slog.Default() safely
	provider := NewOpenAIProvider("test-key", WithBaseURL(server.URL))
	text, err := provider.CompleteText(context.Background(), "sys", "user", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "ok" {
		t.Errorf("expected 'ok', got %q", text)
	}
}

func TestLLMLogging_RegistryOptionPropagation(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{
				{"content": map[string]any{"parts": []map[string]any{{"text": "done"}}}},
			},
		})
	}))
	defer server.Close()

	cfg := &config.Config{
		GeminiAPIKey: "gemini-test-key",
	}

	reg := NewRegistry(cfg,
		WithLogger(logger),
		WithBaseURL(server.URL),
	)

	prov, err := reg.Get(ProviderGemini)
	if err != nil {
		t.Fatalf("unexpected error getting gemini provider: %v", err)
	}

	raw, ok := prov.(RawCompleter)
	if !ok {
		t.Fatalf("provider does not implement RawCompleter")
	}

	text, err := raw.CompleteText(context.Background(), "sys", "user", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "done" {
		t.Errorf("expected 'done', got %q", text)
	}

	logs := parseLogs(t, logBuf.String())
	if len(logs) != 2 {
		t.Fatalf("expected 2 log records propagated via registry option, got %d", len(logs))
	}
}

func TestLLMPromptDebugging_Integration(t *testing.T) {
	respBytes, err := json.Marshal(sampleResponse)
	if err != nil {
		t.Fatalf("failed to marshal sample response: %v", err)
	}
	sampleRespStr := string(respBytes)

	t.Run("creates prompt and output markdown files for providers", func(t *testing.T) {
		providerCases := []struct {
			name          string
			serverHandler http.HandlerFunc
			makeProvider  func(baseURL, dir string) (domain.LLMProvider, RawCompleter)
		}{
			{
				name: "OpenAI",
				serverHandler: func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{
						"choices": []map[string]any{
							{"message": map[string]any{"content": sampleRespStr}},
						},
					})
				},
				makeProvider: func(baseURL, dir string) (domain.LLMProvider, RawCompleter) {
					p := NewOpenAIProvider("test-key", WithBaseURL(baseURL), WithDebugDir(dir))
					return p, p
				},
			},
			{
				name: "Gemini",
				serverHandler: func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{
						"candidates": []map[string]any{
							{
								"content": map[string]any{
									"parts": []map[string]any{{"text": sampleRespStr}},
								},
							},
						},
					})
				},
				makeProvider: func(baseURL, dir string) (domain.LLMProvider, RawCompleter) {
					p := NewGeminiProvider("test-key", WithBaseURL(baseURL), WithDebugDir(dir))
					return p, p
				},
			},
			{
				name: "Anthropic",
				serverHandler: func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{
						"id": "msg_test",
						"content": []map[string]any{
							{
								"type":  "tool_use",
								"name":  anthropicToolName,
								"input": sampleResponse,
							},
							{
								"type": "text",
								"text": sampleRespStr,
							},
						},
					})
				},
				makeProvider: func(baseURL, dir string) (domain.LLMProvider, RawCompleter) {
					p := NewAnthropicProvider("test-key", WithBaseURL(baseURL), WithDebugDir(dir))
					return p, p
				},
			},
			{
				name: "Compat",
				serverHandler: func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{
						"choices": []map[string]any{
							{"message": map[string]any{"content": sampleRespStr}},
						},
					})
				},
				makeProvider: func(baseURL, dir string) (domain.LLMProvider, RawCompleter) {
					p := NewCompatProvider(baseURL, "test-key", WithDebugDir(dir))
					return p, p
				},
			},
		}

		for _, tc := range providerCases {
			t.Run(tc.name, func(t *testing.T) {
				tmpDir := t.TempDir()
				server := httptest.NewServer(tc.serverHandler)
				defer server.Close()

				prov, raw := tc.makeProvider(server.URL, tmpDir)

				// 1. Verify GenerateTailoredResume creates tailor_*.md and tailor_*_output.md
				res, err := prov.GenerateTailoredResume(context.Background(), sampleRequest())
				if err != nil {
					t.Fatalf("unexpected error from GenerateTailoredResume: %v", err)
				}
				if res == nil {
					t.Fatal("expected non-nil generation response")
				}

				targetDir := filepath.Join(tmpDir, "default")
				entries, err := os.ReadDir(targetDir)
				if err != nil {
					t.Fatalf("failed to read debug dir: %v", err)
				}
				if len(entries) != 2 {
					t.Fatalf("expected 2 files after GenerateTailoredResume, got %d", len(entries))
				}

				var promptTailor, outputTailor string
				for _, e := range entries {
					if strings.HasSuffix(e.Name(), "_output.md") {
						outputTailor = e.Name()
					} else if strings.HasSuffix(e.Name(), ".md") {
						promptTailor = e.Name()
					}
				}

				if !strings.HasPrefix(promptTailor, "tailor_") || !strings.HasPrefix(outputTailor, "tailor_") {
					t.Errorf("expected tailor files, got prompt=%q, output=%q", promptTailor, outputTailor)
				}

				promptData, _ := os.ReadFile(filepath.Join(targetDir, promptTailor))
				outputData, _ := os.ReadFile(filepath.Join(targetDir, outputTailor))

				if !strings.Contains(string(promptData), "# LLM Prompt: tailor") {
					t.Errorf("prompt missing tailor header: %s", string(promptData))
				}
				if !strings.Contains(string(outputData), "# LLM Response: tailor") {
					t.Errorf("output missing tailor header: %s", string(outputData))
				}

				// 2. Verify CompleteText creates extract_*.md and extract_*_output.md
				text, err := raw.CompleteText(context.Background(), "sys extract", "user extract", "")
				if err != nil {
					t.Fatalf("unexpected error from CompleteText: %v", err)
				}
				if text == "" {
					t.Fatal("expected non-empty text response")
				}

				entriesAll, err := os.ReadDir(targetDir)
				if err != nil {
					t.Fatalf("failed to read debug dir: %v", err)
				}
				if len(entriesAll) != 4 {
					t.Fatalf("expected 4 files after CompleteText, got %d", len(entriesAll))
				}

				var foundExtractPrompt, foundExtractOutput bool
				for _, e := range entriesAll {
					if strings.HasPrefix(e.Name(), "extract_") && strings.HasSuffix(e.Name(), "_output.md") {
						foundExtractOutput = true
					} else if strings.HasPrefix(e.Name(), "extract_") && strings.HasSuffix(e.Name(), ".md") {
						foundExtractPrompt = true
					}
				}
				if !foundExtractPrompt || !foundExtractOutput {
					t.Errorf("expected extract prompt and output files in %s", targetDir)
				}
			})
		}
	})

	t.Run("zero files created when debugging is disabled", func(t *testing.T) {
		tmpDir := t.TempDir()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{
					{"message": map[string]any{"content": "done"}},
				},
			})
		}))
		defer server.Close()

		provider := NewOpenAIProvider("test-key", WithBaseURL(server.URL))
		_, err := provider.CompleteText(context.Background(), "sys", "user", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		entries, _ := os.ReadDir(tmpDir)
		if len(entries) != 0 {
			t.Errorf("expected 0 files, got %d", len(entries))
		}
	})

	t.Run("registry automatically enables debug dir from config", func(t *testing.T) {
		tmpDir := t.TempDir()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{
					{"message": map[string]any{"content": "extracted"}},
				},
			})
		}))
		defer server.Close()

		cfg := &config.Config{
			OpenAIAPIKey:    "openai-test-key",
			LLMDebugPrompts: true,
			LLMDebugDir:     tmpDir,
		}

		reg := NewRegistry(cfg, WithBaseURL(server.URL))
		prov, err := reg.Get(ProviderOpenAI)
		if err != nil {
			t.Fatalf("failed to get provider: %v", err)
		}

		raw, ok := prov.(RawCompleter)
		if !ok {
			t.Fatalf("expected RawCompleter")
		}

		_, err = raw.CompleteText(context.Background(), "system instruction", "user content", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		entries, err := os.ReadDir(filepath.Join(tmpDir, "default"))
		if err != nil {
			t.Fatalf("failed to read debug dir: %v", err)
		}
		if len(entries) != 2 {
			t.Fatalf("expected 2 files from registry-configured provider, got %d", len(entries))
		}
	})

	t.Run("graceful degradation when directory is not writeable", func(t *testing.T) {
		// Use a file path where MkdirAll fails because a file already exists at that path
		tmpFile := filepath.Join(t.TempDir(), "not-a-dir")
		if err := os.WriteFile(tmpFile, []byte("blocker"), 0600); err != nil {
			t.Fatalf("failed to create blocker file: %v", err)
		}
		invalidDir := filepath.Join(tmpFile, "cannot-create-dir")

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{
					{"message": map[string]any{"content": "ok"}},
				},
			})
		}))
		defer server.Close()

		provider := NewOpenAIProvider("test-key",
			WithBaseURL(server.URL),
			WithDebugDir(invalidDir),
		)

		// Call should still succeed even though debug file cannot be written
		text, err := provider.CompleteText(context.Background(), "sys", "user", "")
		if err != nil {
			t.Fatalf("expected CompleteText to succeed despite debug write failure, got: %v", err)
		}
		if text != "ok" {
			t.Errorf("expected 'ok', got %q", text)
		}
	})
}
