package openai_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/resumeee-app/core"
	"github.com/resumeee-app/core/llm"
	"github.com/resumeee-app/core/llm/openai"
)

func boolPtr(v bool) *bool { return &v }

func sampleTestSchema() *llm.Schema {
	return &llm.Schema{
		Type: llm.SchemaTypes{llm.TypeObject},
		Properties: map[string]*llm.Schema{
			"summary": {
				Type:        llm.SchemaTypes{llm.TypeString},
				Description: "Tailored summary",
			},
			"notes": {
				Type:        llm.SchemaTypes{llm.TypeString},
				Description: "Optional notes",
			},
			"severity": {
				Type:        llm.SchemaTypes{llm.TypeString},
				Enum:        []string{"low", "high"},
				Description: "Optional severity",
			},
			"meta": {
				Type:        llm.SchemaTypes{llm.TypeObject},
				Description: "Optional nested object",
				Properties: map[string]*llm.Schema{
					"tag": {Type: llm.SchemaTypes{llm.TypeString}},
				},
			},
		},
		Required:             []string{"summary"},
		AdditionalProperties: boolPtr(false),
	}
}

func cloneJSONSnapshot(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("failed to snapshot JSON: %v", err)
	}
	return string(b)
}

func TestOpenAIClient_Complete_ResponsesAPI(t *testing.T) {
	t.Run("model structured output capability gating", func(t *testing.T) {
		client := openai.New("test-openai-key")
		cases := []struct {
			model string
			want  bool
		}{
			{"", true},
			{"gpt-4o", true},
			{"gpt-4o-mini", true},
			{"gpt-4.1", true},
			{"gpt-5", true},
			{"o1", true},
			{"o3-mini", true},
			{"o4-mini", true},
			{"gpt-4-turbo", false},
			{"gpt-4-turbo-2024-04-09", false},
			{"gpt-3.5-turbo", false},
			{"gpt-4o-2024-05-13", false},
			{"o1-mini", false},
			{"o1-preview", false},
		}
		for _, tc := range cases {
			if got := client.SupportsStructuredOutput(tc.model); got != tc.want {
				t.Errorf("SupportsStructuredOutput(%q) = %v, want %v", tc.model, got, tc.want)
			}
		}
	})

	t.Run("structured response via /v1/responses with strict json_schema", func(t *testing.T) {
		schema := sampleTestSchema()
		beforeSnapshot := cloneJSONSnapshot(t, schema)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/responses" {
				t.Errorf("expected /v1/responses, got %q", r.URL.Path)
			}
			if r.Header.Get("Authorization") != "Bearer test-openai-key" {
				t.Errorf("unexpected Authorization header: %q", r.Header.Get("Authorization"))
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("failed to decode request body: %v", err)
			}
			if body["instructions"] != "sys" {
				t.Errorf("expected instructions='sys', got %v", body["instructions"])
			}
			if body["input"] != "user" {
				t.Errorf("expected input='user', got %v", body["input"])
			}
			if _, hasLegacyMessages := body["messages"]; hasLegacyMessages {
				t.Errorf("did not expect legacy chat completions messages field")
			}
			if _, hasLegacyRF := body["response_format"]; hasLegacyRF {
				t.Errorf("did not expect legacy response_format field")
			}

			textCfg, ok := body["text"].(map[string]any)
			if !ok {
				t.Fatalf("expected text config object, got %v", body["text"])
			}
			formatCfg, ok := textCfg["format"].(map[string]any)
			if !ok {
				t.Fatalf("expected text.format object, got %v", textCfg["format"])
			}
			if formatCfg["type"] != "json_schema" || formatCfg["strict"] != true || formatCfg["name"] != "tailored_resume_response" {
				t.Errorf("unexpected text.format metadata: %+v", formatCfg)
			}

			schemaMap, ok := formatCfg["schema"].(map[string]any)
			if !ok {
				t.Fatalf("expected text.format.schema object, got %v", formatCfg["schema"])
			}
			if schemaMap["additionalProperties"] != false {
				t.Errorf("expected additionalProperties=false, got %v", schemaMap["additionalProperties"])
			}
			reqKeys, _ := schemaMap["required"].([]any)
			if len(reqKeys) != 4 {
				t.Errorf("expected all 4 properties in required for strict mode, got %v", schemaMap["required"])
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"id": "resp_123",
				"status": "completed",
				"output": [
					{"type": "reasoning", "summary": []},
					{
						"type": "message",
						"role": "assistant",
						"content": [{"type": "output_text", "text": "{\"summary\":\"OpenAI Tailored\"}"}]
					}
				],
				"usage": {"input_tokens": 50, "output_tokens": 25, "total_tokens": 75}
			}`))
		}))
		defer srv.Close()

		client := openai.New("test-openai-key", openai.WithBaseURL(srv.URL))
		if client.Name() != llm.ProviderOpenAI {
			t.Errorf("expected %q, got %q", llm.ProviderOpenAI, client.Name())
		}
		defaultOpenAIModel := llm.DefaultModelForProvider(llm.ProviderOpenAI)
		if len(client.Models()) != 1 || client.Models()[0].Model != defaultOpenAIModel {
			t.Errorf("expected single default model %q, got %+v", defaultOpenAIModel, client.Models())
		}

		resp, err := client.Complete(context.Background(), llm.Request{
			SystemPrompt:   "sys",
			UserPrompt:     "user",
			SchemaName:     "tailored_resume_response",
			ResponseSchema: schema,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Text != `{"summary":"OpenAI Tailored"}` {
			t.Errorf("unexpected text: %q", resp.Text)
		}
		if resp.Usage.PromptTokens != 50 || resp.Usage.CompletionTokens != 25 || resp.Usage.TotalTokens != 75 {
			t.Errorf("unexpected usage: %+v", resp.Usage)
		}
		if after := cloneJSONSnapshot(t, schema); after != beforeSnapshot {
			t.Errorf("openai.Client mutated caller schema!\nbefore: %s\nafter:  %s", beforeSnapshot, after)
		}
	})

	t.Run("reasoning.effort is gated strictly to reasoning models", func(t *testing.T) {
		cases := []struct {
			name            string
			model           string
			reasoningLevel  string
			wantEffortField string
		}{
			{"gpt-4o omits reasoning even when high", "gpt-4o", "high", ""},
			{"gpt-4o-mini omits reasoning even when medium", "gpt-4o-mini", "medium", ""},
			{"gpt-4-turbo omits reasoning even when low", "gpt-4-turbo", "low", ""},
			{"o3-mini serializes reasoning.effort=high", "o3-mini", "high", "high"},
			{"o1 serializes reasoning.effort=low", "o1", "low", "low"},
			{"gpt-5 serializes reasoning.effort=medium", "gpt-5", "medium", "medium"},
			{"o4-mini omits reasoning when level is none", "o4-mini", "none", ""},
			{"gpt-5 omits reasoning when level is empty", "gpt-5", "", ""},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				var gotReasoning map[string]any
				var hasReasoningKey bool

				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					_ = json.NewDecoder(r.Body).Decode(&body)
					rawR, exists := body["reasoning"]
					hasReasoningKey = exists
					gotReasoning, _ = rawR.(map[string]any)
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{
						"status": "completed",
						"output": [{"type": "message", "content": [{"type": "output_text", "text": "ok"}]}],
						"usage": {"input_tokens": 5, "output_tokens": 2}
					}`))
				}))
				defer srv.Close()

				client := openai.New("key", openai.WithBaseURL(srv.URL))
				_, err := client.Complete(context.Background(), llm.Request{
					Model:          tc.model,
					UserPrompt:     "hello",
					ReasoningLevel: tc.reasoningLevel,
				})
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if tc.wantEffortField == "" {
					if hasReasoningKey {
						t.Errorf("expected reasoning key to be omitted for model %q (level %q), got %v", tc.model, tc.reasoningLevel, gotReasoning)
					}
				} else {
					if !hasReasoningKey || gotReasoning["effort"] != tc.wantEffortField {
						t.Errorf("expected reasoning.effort=%q for model %q, got %v", tc.wantEffortField, tc.model, gotReasoning)
					}
				}
			})
		}
	})

	t.Run("legacy OpenAI model falls back to prompt schema without text.format or double schema", func(t *testing.T) {
		schema := sampleTestSchema()
		var capturedBodies []map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			capturedBodies = append(capturedBodies, body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"status": "completed",
				"output": [{"type":"message","role":"assistant","content":[{"type":"output_text","text":"{\"summary\":\"legacy ok\"}"}]}],
				"usage": {"input_tokens": 12, "output_tokens": 6, "total_tokens": 18}
			}`))
		}))
		defer srv.Close()

		client := openai.New("key", openai.WithBaseURL(srv.URL))
		_, err := client.Complete(context.Background(), llm.Request{
			Model:          "gpt-4-turbo",
			SystemPrompt:   "Follow instructions.",
			UserPrompt:     "user",
			ResponseSchema: schema,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		_, err = client.Complete(context.Background(), llm.Request{
			Model:          "gpt-4-turbo",
			SystemPrompt:   "Follow instructions.\n\n<response_schema>\n{\"type\":\"object\"}\n</response_schema>",
			UserPrompt:     "user",
			ResponseSchema: schema,
		})
		if err != nil {
			t.Fatalf("unexpected error on pre-appended call: %v", err)
		}
		if len(capturedBodies) != 2 {
			t.Fatalf("expected 2 requests, got %d", len(capturedBodies))
		}
		for i, b := range capturedBodies {
			if _, hasText := b["text"]; hasText {
				t.Errorf("call %d: did not expect text.format for legacy gpt-4-turbo, got %v", i, b["text"])
			}
			inst, _ := b["instructions"].(string)
			if strings.Count(inst, "<response_schema>") != 1 {
				t.Errorf("call %d: expected exactly 1 <response_schema> in instructions, got %d (%q)", i, strings.Count(inst, "<response_schema>"), inst)
			}
		}
	})

	t.Run("nil client and empty api key guards", func(t *testing.T) {
		req := llm.Request{UserPrompt: "ping"}
		if resp, err := (*openai.Client)(nil).Complete(context.Background(), req); !errors.Is(err, llm.ErrUnauthorized) || resp != nil {
			t.Errorf("expected (nil, ErrUnauthorized) for nil openai.Client, got (%+v, %v)", resp, err)
		}
		if resp, err := openai.New("   ").Complete(context.Background(), req); !errors.Is(err, llm.ErrUnauthorized) || resp != nil {
			t.Errorf("expected (nil, ErrUnauthorized) for empty OpenAI key, got (%+v, %v)", resp, err)
		}
	})

	t.Run("refusals, incomplete responses, and HTTP/JSON errors", func(t *testing.T) {
		cases := []struct {
			name       string
			statusCode int
			body       string
			wantErr    error
		}{
			{
				name:       "model refusal block",
				statusCode: http.StatusOK,
				body:       `{"status":"completed","output":[{"type":"message","content":[{"type":"refusal","refusal":"Cannot fulfill request"}]}]}`,
				wantErr:    llm.ErrInvalidResponse,
			},
			{
				name:       "incomplete response",
				statusCode: http.StatusOK,
				body:       `{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[]}`,
				wantErr:    llm.ErrInvalidResponse,
			},
			{
				name:       "empty output",
				statusCode: http.StatusOK,
				body:       `{"status":"completed","output":[]}`,
				wantErr:    llm.ErrInvalidResponse,
			},
			{
				name:       "malformed json",
				statusCode: http.StatusOK,
				body:       `{broken`,
				wantErr:    llm.ErrInvalidResponse,
			},
			{
				name:       "400 bad request",
				statusCode: http.StatusBadRequest,
				body:       `{"error":{"message":"invalid schema"}}`,
				wantErr:    llm.ErrBadRequest,
			},
			{
				name:       "429 rate limited",
				statusCode: http.StatusTooManyRequests,
				body:       `{"error":{"message":"slow down"}}`,
				wantErr:    llm.ErrRateLimited,
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tc.statusCode)
					_, _ = w.Write([]byte(tc.body))
				}))
				defer srv.Close()

				client := openai.New("key", openai.WithBaseURL(srv.URL))
				_, err := client.Complete(context.Background(), llm.Request{UserPrompt: "hi"})
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("expected error %v, got %v", tc.wantErr, err)
				}
			})
		}
	})
}

func TestOpenAIClient_Complete_Telemetry(t *testing.T) {
	t.Run("usage extraction and model resolution", func(t *testing.T) {
		cases := []struct {
			name         string
			defaultModel string
			reqModel     string
			body         string
			wantModel    string
			wantUsage    core.TokenUsage
		}{
			{
				name:      "explicit total tokens preserved with provider default model",
				body:      `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":50,"output_tokens":25,"total_tokens":90}}`,
				wantModel: llm.DefaultModelForProvider(llm.ProviderOpenAI),
				wantUsage: core.TokenUsage{PromptTokens: 50, CompletionTokens: 25, TotalTokens: 90},
			},
			{
				name:      "omitted total tokens backfilled with explicit request model",
				reqModel:  "  gpt-4.1  ",
				body:      `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":18,"output_tokens":7}}`,
				wantModel: "gpt-4.1",
				wantUsage: core.TokenUsage{PromptTokens: 18, CompletionTokens: 7, TotalTokens: 25},
			},
			{
				name:         "prompt-only usage backfilled with custom default model",
				defaultModel: "gpt-5",
				body:         `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":14}}`,
				wantModel:    "gpt-5",
				wantUsage:    core.TokenUsage{PromptTokens: 14, TotalTokens: 14},
			},
			{
				name:      "omitted usage stays zero",
				body:      `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`,
				wantModel: llm.DefaultModelForProvider(llm.ProviderOpenAI),
				wantUsage: core.TokenUsage{},
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(tc.body))
				}))
				defer srv.Close()

				opts := []openai.Option{openai.WithBaseURL(srv.URL), openai.WithHTTPClient(srv.Client())}
				if tc.defaultModel != "" {
					opts = append(opts, openai.WithDefaultModel(tc.defaultModel))
				}
				client := openai.New("key", opts...)

				resp, err := client.Complete(context.Background(), llm.Request{
					Model:      tc.reqModel,
					UserPrompt: "hello",
				})
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if resp == nil {
					t.Fatalf("expected non-nil response")
				}
				if resp.Provider != llm.ProviderOpenAI || resp.Model != tc.wantModel || resp.StatusCode != http.StatusOK {
					t.Errorf("metadata = (provider=%q, model=%q, status=%d), want (%q, %q, 200)", resp.Provider, resp.Model, resp.StatusCode, llm.ProviderOpenAI, tc.wantModel)
				}
				if resp.Latency <= 0 {
					t.Errorf("expected Latency > 0, got %v", resp.Latency)
				}
				if resp.Usage != tc.wantUsage {
					t.Errorf("Usage = %+v, want %+v", resp.Usage, tc.wantUsage)
				}
			})
		}
	})

	t.Run("HTTP errors and 200 invalid responses populate telemetry and partial usage", func(t *testing.T) {
		cases := []struct {
			name       string
			reqModel   string
			statusCode int
			body       string
			wantErr    error
			wantModel  string
			wantUsage  core.TokenUsage
		}{
			{
				name:       "400 bad request",
				statusCode: http.StatusBadRequest,
				body:       `{"error":{"message":"invalid schema"}}`,
				wantErr:    llm.ErrBadRequest,
				wantModel:  llm.DefaultModelForProvider(llm.ProviderOpenAI),
			},
			{
				name:       "401 unauthorized",
				reqModel:   "gpt-4.1",
				statusCode: http.StatusUnauthorized,
				body:       `{"error":{"message":"invalid key"}}`,
				wantErr:    llm.ErrUnauthorized,
				wantModel:  "gpt-4.1",
			},
			{
				name:       "429 rate limited",
				statusCode: http.StatusTooManyRequests,
				body:       `{"error":{"message":"rate limit"}}`,
				wantErr:    llm.ErrRateLimited,
				wantModel:  llm.DefaultModelForProvider(llm.ProviderOpenAI),
			},
			{
				name:       "500 upstream error",
				statusCode: http.StatusInternalServerError,
				body:       `{"error":{"message":"internal error"}}`,
				wantErr:    llm.ErrUpstream,
				wantModel:  llm.DefaultModelForProvider(llm.ProviderOpenAI),
			},
			{
				name:       "200 malformed json",
				statusCode: http.StatusOK,
				body:       `{broken`,
				wantErr:    llm.ErrInvalidResponse,
				wantModel:  llm.DefaultModelForProvider(llm.ProviderOpenAI),
			},
			{
				name:       "200 incomplete preserves partial usage with backfill",
				reqModel:   "gpt-4o-mini",
				statusCode: http.StatusOK,
				body:       `{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[],"usage":{"input_tokens":30,"output_tokens":12}}`,
				wantErr:    llm.ErrInvalidResponse,
				wantModel:  "gpt-4o-mini",
				wantUsage:  core.TokenUsage{PromptTokens: 30, CompletionTokens: 12, TotalTokens: 42},
			},
			{
				name:       "200 empty output preserves partial usage",
				statusCode: http.StatusOK,
				body:       `{"status":"completed","output":[],"usage":{"input_tokens":24}}`,
				wantErr:    llm.ErrInvalidResponse,
				wantModel:  llm.DefaultModelForProvider(llm.ProviderOpenAI),
				wantUsage:  core.TokenUsage{PromptTokens: 24, TotalTokens: 24},
			},
			{
				name:       "200 refusal preserves explicit usage",
				statusCode: http.StatusOK,
				body:       `{"status":"completed","output":[{"type":"message","content":[{"type":"refusal","refusal":"nope"}]}],"usage":{"input_tokens":11,"output_tokens":3,"total_tokens":16}}`,
				wantErr:    llm.ErrInvalidResponse,
				wantModel:  llm.DefaultModelForProvider(llm.ProviderOpenAI),
				wantUsage:  core.TokenUsage{PromptTokens: 11, CompletionTokens: 3, TotalTokens: 16},
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tc.statusCode)
					_, _ = w.Write([]byte(tc.body))
				}))
				defer srv.Close()

				client := openai.New("key", openai.WithBaseURL(srv.URL), openai.WithHTTPClient(srv.Client()))
				resp, err := client.Complete(context.Background(), llm.Request{
					Model:      tc.reqModel,
					UserPrompt: "hi",
				})
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("error = %v, want %v", err, tc.wantErr)
				}
				if resp == nil {
					t.Fatalf("expected non-nil response on error")
				}
				if resp.Provider != llm.ProviderOpenAI || resp.Model != tc.wantModel || resp.StatusCode != tc.statusCode {
					t.Errorf("metadata = (provider=%q, model=%q, status=%d), want (%q, %q, %d)", resp.Provider, resp.Model, resp.StatusCode, llm.ProviderOpenAI, tc.wantModel, tc.statusCode)
				}
				if resp.Latency <= 0 {
					t.Errorf("expected Latency > 0, got %v", resp.Latency)
				}
				if resp.Usage != tc.wantUsage {
					t.Errorf("Usage = %+v, want %+v", resp.Usage, tc.wantUsage)
				}
			})
		}
	})

	t.Run("timeout and transport errors return non-nil telemetry with zero status", func(t *testing.T) {
		t.Run("context deadline exceeded", func(t *testing.T) {
			done := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				select {
				case <-done:
				case <-r.Context().Done():
				case <-time.After(500 * time.Millisecond):
				}
			}))
			defer srv.Close()
			defer close(done)

			client := openai.New("key", openai.WithBaseURL(srv.URL), openai.WithHTTPClient(srv.Client()))
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
			defer cancel()

			resp, err := client.Complete(ctx, llm.Request{
				Model:      "gpt-4.1",
				UserPrompt: "ping",
			})
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expected context.DeadlineExceeded, got %v", err)
			}
			if resp == nil {
				t.Fatalf("expected non-nil response on timeout")
			}
			if resp.Provider != llm.ProviderOpenAI || resp.Model != "gpt-4.1" || resp.StatusCode != 0 {
				t.Errorf("metadata = (provider=%q, model=%q, status=%d), want (%q, %q, 0)", resp.Provider, resp.Model, resp.StatusCode, llm.ProviderOpenAI, "gpt-4.1")
			}
			if resp.Latency <= 0 {
				t.Errorf("expected Latency > 0, got %v", resp.Latency)
			}
		})

		t.Run("client WithTimeout exceeded", func(t *testing.T) {
			done := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				select {
				case <-done:
				case <-r.Context().Done():
				case <-time.After(500 * time.Millisecond):
				}
			}))
			defer srv.Close()
			defer close(done)

			client := openai.New(
				"key",
				openai.WithBaseURL(srv.URL),
				openai.WithHTTPClient(srv.Client()),
				openai.WithTimeout(15*time.Millisecond),
			)
			resp, err := client.Complete(context.Background(), llm.Request{
				Model:      "gpt-4o-mini",
				UserPrompt: "ping",
			})
			if err == nil {
				t.Fatalf("expected timeout error from WithTimeout")
			}
			if resp == nil {
				t.Fatalf("expected non-nil response on client timeout")
			}
			if resp.Provider != llm.ProviderOpenAI || resp.Model != "gpt-4o-mini" || resp.StatusCode != 0 {
				t.Errorf("metadata = (provider=%q, model=%q, status=%d), want (%q, %q, 0)", resp.Provider, resp.Model, resp.StatusCode, llm.ProviderOpenAI, "gpt-4o-mini")
			}
			if resp.Latency <= 0 {
				t.Errorf("expected Latency > 0, got %v", resp.Latency)
			}
		})

		t.Run("closed server transport failure", func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			srvURL := srv.URL
			httpClient := srv.Client()
			srv.Close()

			client := openai.New("key", openai.WithBaseURL(srvURL), openai.WithHTTPClient(httpClient))
			resp, err := client.Complete(context.Background(), llm.Request{UserPrompt: "ping"})
			if err == nil {
				t.Fatalf("expected transport error against closed server")
			}
			if resp == nil {
				t.Fatalf("expected non-nil response on transport error")
			}
			wantModel := llm.DefaultModelForProvider(llm.ProviderOpenAI)
			if resp.Provider != llm.ProviderOpenAI || resp.Model != wantModel || resp.StatusCode != 0 {
				t.Errorf("metadata = (provider=%q, model=%q, status=%d), want (%q, %q, 0)", resp.Provider, resp.Model, resp.StatusCode, llm.ProviderOpenAI, wantModel)
			}
			if resp.Latency <= 0 {
				t.Errorf("expected Latency > 0, got %v", resp.Latency)
			}
		})
	})
}
