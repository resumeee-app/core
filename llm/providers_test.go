package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

var sampleTestSchema = map[string]any{
	"$schema": "http://json-schema.org/draft-07/schema#",
	"definitions": map[string]any{
		"unused_def": map[string]any{"type": "string"},
	},
	"type": "object",
	"properties": map[string]any{
		"summary": map[string]any{
			"type":        "string",
			"description": "Tailored summary",
		},
		"notes": map[string]any{
			"type":        "string",
			"description": "Optional notes",
		},
		"severity": map[string]any{
			"type":        "string",
			"enum":        []string{"low", "high"},
			"description": "Optional severity",
		},
		"meta": map[string]any{
			"type":        "object",
			"description": "Optional nested object",
			"properties": map[string]any{
				"tag": map[string]any{"type": "string"},
			},
		},
	},
	"required":             []string{"summary"},
	"additionalProperties": false,
}

func cloneJSONSnapshot(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("failed to snapshot JSON: %v", err)
	}
	return string(b)
}

func TestGeminiProvider_Complete_InteractionsSDK(t *testing.T) {
	t.Run("structured response via genai Interactions SDK", func(t *testing.T) {
		beforeSnapshot := cloneJSONSnapshot(t, sampleTestSchema)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			if r.URL.Path != "/v1beta/interactions" {
				t.Errorf("expected /v1beta/interactions path, got %q", r.URL.Path)
			}
			if r.Header.Get("x-goog-api-key") != "test-gemini-key" {
				t.Errorf("expected x-goog-api-key header, got %q", r.Header.Get("x-goog-api-key"))
			}
			ua := r.Header.Get("User-Agent")
			if !strings.Contains(ua, "google-genai-sdk/") {
				t.Errorf("expected google-genai-sdk User-Agent from official Go library, got %q", ua)
			}

			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("failed to decode request body: %v", err)
			}
			if body["model"] != "gemini-3.8-flash" {
				t.Errorf("expected default model gemini-3.8-flash, got %v", body["model"])
			}
			if body["system_instruction"] != "sys" {
				t.Errorf("expected system_instruction 'sys', got %v", body["system_instruction"])
			}
			if body["input"] != "user" {
				t.Errorf("expected input 'user', got %v", body["input"])
			}
			genCfg, ok := body["generation_config"].(map[string]any)
			if !ok || genCfg["thinking_level"] != "high" {
				t.Errorf("expected generation_config.thinking_level 'high', got %v", body["generation_config"])
			}

			rf, ok := body["response_format"].(map[string]any)
			if !ok {
				t.Fatalf("expected response_format object in interaction request, got %v", body["response_format"])
			}
			if rf["type"] != "text" || rf["mime_type"] != "application/json" {
				t.Errorf("expected response_format type=text, mime_type=application/json, got %v", rf)
			}
			jsonSchema, ok := rf["schema"].(map[string]any)
			if !ok {
				t.Fatalf("expected schema object inside response_format, got %v", rf)
			}
			if jsonSchema["additionalProperties"] != false {
				t.Errorf("expected schema to preserve additionalProperties=false, got %v", jsonSchema["additionalProperties"])
			}
			if _, hasMeta := jsonSchema["$schema"]; hasMeta {
				t.Errorf("expected $schema meta-keyword to be stripped from schema")
			}
			if _, hasDefs := jsonSchema["definitions"]; hasDefs {
				t.Errorf("expected definitions meta-keyword to be stripped from schema")
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"id": "inter_struct_1",
				"status": "completed",
				"steps": [
					{
						"type": "thought",
						"signature": "sig_123",
						"summary": [{"type": "text", "text": "thinking..."}]
					},
					{
						"type": "model_output",
						"content": [{"type": "text", "text": "{\"summary\":\"Tailored\"}"}]
					}
				],
				"usage": {
					"total_input_tokens": 100,
					"total_output_tokens": 50,
					"total_tokens": 150
				}
			}`))
		}))
		defer srv.Close()

		prov := NewGeminiProvider("test-gemini-key", WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
		if prov.Name() != ProviderGemini {
			t.Errorf("expected provider %q, got %q", ProviderGemini, prov.Name())
		}
		if !prov.SupportsStructuredOutput("") {
			t.Errorf("expected GeminiProvider.SupportsStructuredOutput to return true")
		}
		if prov.DefaultModel() != "gemini-3.8-flash" {
			t.Errorf("expected DefaultModel gemini-3.8-flash, got %q", prov.DefaultModel())
		}
		if len(prov.Models()) != 1 || prov.Models()[0].Model != "gemini-3.8-flash" {
			t.Errorf("expected single default model gemini-3.8-flash, got %+v", prov.Models())
		}

		resp, err := prov.Complete(context.Background(), core.LLMCompletionRequest{
			SystemPrompt:   "sys",
			UserPrompt:     "user",
			ReasoningLevel: "high",
			ResponseSchema: sampleTestSchema,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Text != `{"summary":"Tailored"}` {
			t.Errorf("unexpected text: %q", resp.Text)
		}
		if resp.Usage.PromptTokens != 100 || resp.Usage.CompletionTokens != 50 || resp.Usage.TotalTokens != 150 {
			t.Errorf("unexpected usage: %+v", resp.Usage)
		}
		if after := cloneJSONSnapshot(t, sampleTestSchema); after != beforeSnapshot {
			t.Errorf("GeminiProvider mutated caller schema!\nbefore: %s\nafter:  %s", beforeSnapshot, after)
		}
	})

	t.Run("plain text completion without schema omits response_format", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if _, hasRF := body["response_format"]; hasRF {
				t.Errorf("did not expect response_format for plain text completion, got %v", body["response_format"])
			}
			if _, hasGenCfg := body["generation_config"]; hasGenCfg {
				t.Errorf("did not expect generation_config when ReasoningLevel is empty, got %v", body["generation_config"])
			}
			if body["model"] != "gemini-custom" {
				t.Errorf("expected custom default model gemini-custom, got %v", body["model"])
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"id": "inter_text_1",
				"status": "completed",
				"steps": [
					{
						"type": "model_output",
						"content": [{"type": "text", "text": "  extracted text  "}]
					}
				],
				"usage": {"total_input_tokens": 10, "total_output_tokens": 5}
			}`))
		}))
		defer srv.Close()

		prov := NewGeminiProvider("test-gemini-key", WithBaseURL(srv.URL), WithDefaultModel("gemini-custom"))
		if len(prov.Models()) != 1 || prov.Models()[0].Model != "gemini-custom" {
			t.Errorf("expected single custom default model 'gemini-custom', got %+v", prov.Models())
		}
		resp, err := prov.Complete(context.Background(), core.LLMCompletionRequest{
			SystemPrompt: "sys",
			UserPrompt:   "user",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Text != "extracted text" {
			t.Errorf("expected 'extracted text', got %q", resp.Text)
		}
		if resp.Usage.TotalTokens != 15 {
			t.Errorf("expected computed total tokens 15, got %d", resp.Usage.TotalTokens)
		}
	})

	t.Run("empty api key returns ErrUnauthorized without ambient env fallback", func(t *testing.T) {
		prov := NewGeminiProvider("")
		_, err := prov.Complete(context.Background(), core.LLMCompletionRequest{
			UserPrompt: "hello",
		})
		if !errors.Is(err, ErrUnauthorized) {
			t.Errorf("expected ErrUnauthorized for empty API key, got %v", err)
		}
	})

	t.Run("error statuses, failed interactions, and malformed responses", func(t *testing.T) {
		cases := []struct {
			name        string
			statusCode  int
			contentType string
			body        string
			wantErr     error
		}{
			{"400 client error schema", http.StatusBadRequest, "application/json", `{"error":{"code":"invalid_argument","message":"bad req\nbad req"}}`, ErrBadRequest},
			{"400 legacy int code error", http.StatusBadRequest, "application/json", `{"error":{"code":400,"message":"bad req\nbad req"}}`, ErrBadRequest},
			{"401 unauthorized", http.StatusUnauthorized, "application/json", `{"error":{"code":"unauthenticated","message":"invalid api key"}}`, ErrUnauthorized},
			{"429 rate limited", http.StatusTooManyRequests, "text/plain", `rate limit exceeded`, ErrRateLimited},
			{"503 upstream", http.StatusServiceUnavailable, "application/json", `{"error":{"code":"unavailable","message":"overloaded"}}`, ErrUpstream},
			{"200 malformed json", http.StatusOK, "application/json", `{not-json`, ErrInvalidResponse},
			{"200 empty steps", http.StatusOK, "application/json", `{"id":"i1","status":"completed","steps":[]}`, ErrInvalidResponse},
			{"200 failed status", http.StatusOK, "application/json", `{"id":"i2","status":"failed","errors":[{"code":"safety","message":"blocked"}]}`, ErrInvalidResponse},
			{"200 budget_exceeded with partial output", http.StatusOK, "application/json", `{"id":"i3","status":"budget_exceeded","steps":[{"type":"model_output","content":[{"type":"text","text":"{\"partial\":"}]}]}`, ErrInvalidResponse},
			{"200 requires_continuation with partial output", http.StatusOK, "application/json", `{"id":"i4","status":"requires_continuation","steps":[{"type":"model_output","content":[{"type":"text","text":"partial text"}]}]}`, ErrInvalidResponse},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if tc.contentType != "" {
						w.Header().Set("Content-Type", tc.contentType)
					}
					w.WriteHeader(tc.statusCode)
					_, _ = w.Write([]byte(tc.body))
				}))
				defer srv.Close()

				prov := NewGeminiProvider("key", WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
				resp, err := prov.Complete(context.Background(), core.LLMCompletionRequest{
					SystemPrompt: "sys",
					UserPrompt:   "usr",
				})
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("expected error %v, got %v", tc.wantErr, err)
				}
				if resp == nil || resp.StatusCode != tc.statusCode {
					t.Errorf("expected response metadata with status %d, got %+v", tc.statusCode, resp)
				}
			})
		}
	})
}

func TestOpenAIProvider_Complete_ResponsesAPI(t *testing.T) {
	t.Run("model structured output capability gating", func(t *testing.T) {
		prov := NewOpenAIProvider("test-openai-key")
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
			if got := prov.SupportsStructuredOutput(tc.model); got != tc.want {
				t.Errorf("SupportsStructuredOutput(%q) = %v, want %v", tc.model, got, tc.want)
			}
		}
	})

	t.Run("structured response via /v1/responses with strict json_schema", func(t *testing.T) {
		beforeSnapshot := cloneJSONSnapshot(t, sampleTestSchema)

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

		prov := NewOpenAIProvider("test-openai-key", WithBaseURL(srv.URL))
		if prov.Name() != ProviderOpenAI {
			t.Errorf("expected %q, got %q", ProviderOpenAI, prov.Name())
		}
		if len(prov.Models()) != 1 || prov.Models()[0].Model != defaultOpenAIModel {
			t.Errorf("expected single default model %q, got %+v", defaultOpenAIModel, prov.Models())
		}

		resp, err := prov.Complete(context.Background(), core.LLMCompletionRequest{
			SystemPrompt:   "sys",
			UserPrompt:     "user",
			SchemaName:     "tailored_resume_response",
			ResponseSchema: sampleTestSchema,
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
		if after := cloneJSONSnapshot(t, sampleTestSchema); after != beforeSnapshot {
			t.Errorf("OpenAIProvider mutated caller schema!\nbefore: %s\nafter:  %s", beforeSnapshot, after)
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

				prov := NewOpenAIProvider("key", WithBaseURL(srv.URL))
				_, err := prov.Complete(context.Background(), core.LLMCompletionRequest{
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

		prov := NewOpenAIProvider("key", WithBaseURL(srv.URL))
		_, err := prov.Complete(context.Background(), core.LLMCompletionRequest{
			Model:          "gpt-4-turbo",
			SystemPrompt:   "Follow instructions.",
			UserPrompt:     "user",
			ResponseSchema: sampleTestSchema,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		_, err = prov.Complete(context.Background(), core.LLMCompletionRequest{
			Model:          "gpt-4-turbo",
			SystemPrompt:   "Follow instructions.\n\n<response_schema>\n{\"type\":\"object\"}\n</response_schema>",
			UserPrompt:     "user",
			ResponseSchema: sampleTestSchema,
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
				wantErr:    ErrInvalidResponse,
			},
			{
				name:       "incomplete response",
				statusCode: http.StatusOK,
				body:       `{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[]}`,
				wantErr:    ErrInvalidResponse,
			},
			{
				name:       "empty output",
				statusCode: http.StatusOK,
				body:       `{"status":"completed","output":[]}`,
				wantErr:    ErrInvalidResponse,
			},
			{
				name:       "malformed json",
				statusCode: http.StatusOK,
				body:       `{broken`,
				wantErr:    ErrInvalidResponse,
			},
			{
				name:       "400 bad request",
				statusCode: http.StatusBadRequest,
				body:       `{"error":{"message":"invalid schema"}}`,
				wantErr:    ErrBadRequest,
			},
			{
				name:       "429 rate limited",
				statusCode: http.StatusTooManyRequests,
				body:       `{"error":{"message":"slow down"}}`,
				wantErr:    ErrRateLimited,
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tc.statusCode)
					_, _ = w.Write([]byte(tc.body))
				}))
				defer srv.Close()

				prov := NewOpenAIProvider("key", WithBaseURL(srv.URL))
				_, err := prov.Complete(context.Background(), core.LLMCompletionRequest{UserPrompt: "hi"})
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("expected error %v, got %v", tc.wantErr, err)
				}
			})
		}
	})
}

func TestAnthropicProvider_Complete_OutputConfigAndModelGating(t *testing.T) {
	t.Run("model structured output capability gating", func(t *testing.T) {
		prov := NewAnthropicProvider("test-anthropic-key")
		if prov.DefaultModel() != "claude-sonnet-4-5" {
			t.Errorf("expected default Anthropic model claude-sonnet-4-5, got %q", prov.DefaultModel())
		}
		cases := []struct {
			model string
			want  bool
		}{
			{"", true},
			{"claude-sonnet-4-5", true},
			{"claude-sonnet-4-5-20250929", true},
			{"claude-haiku-4-5", true},
			{"claude-haiku-4-5-20251001", true},
			{"claude-opus-4-1", true},
			{"claude-opus-4-1-20250805", true},
			{"claude-opus-4-5", true},
			{"claude-sonnet-4-6", true},
			{"claude-opus-4-6", true},
			{"claude-sonnet-5-0", true},
			{"claude-3-5-haiku-20241022", false},
			{"claude-3-5-sonnet-20241022", false},
			{"claude-3-7-sonnet-20250219", false},
			{"claude-3-opus-20240229", false},
			{"claude-sonnet-4-20250514", false},
			{"claude-opus-4-20250514", false},
			{"claude-2.1", false},
			{"claude-sonnet-4-18446744073709551621", false},
		}
		for _, tc := range cases {
			if got := prov.SupportsStructuredOutput(tc.model); got != tc.want {
				t.Errorf("AnthropicProvider.SupportsStructuredOutput(%q) = %v, want %v", tc.model, got, tc.want)
			}
		}
	})

	t.Run("structured response via output_config.format on Claude 4.5+", func(t *testing.T) {
		beforeSnapshot := cloneJSONSnapshot(t, sampleTestSchema)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/messages" {
				t.Errorf("expected /v1/messages, got %q", r.URL.Path)
			}
			if r.Header.Get("x-api-key") != "test-anthropic-key" {
				t.Errorf("unexpected x-api-key: %q", r.Header.Get("x-api-key"))
			}
			if r.Header.Get("anthropic-version") != anthropicAPIVersion {
				t.Errorf("unexpected anthropic-version: %q", r.Header.Get("anthropic-version"))
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("failed to decode request body: %v", err)
			}
			if _, hasTools := body["tools"]; hasTools {
				t.Errorf("did not expect legacy tools workaround in Anthropic payload")
			}
			if _, hasToolChoice := body["tool_choice"]; hasToolChoice {
				t.Errorf("did not expect legacy tool_choice workaround in Anthropic payload")
			}
			outCfg, ok := body["output_config"].(map[string]any)
			if !ok {
				t.Fatalf("expected output_config object, got %v", body["output_config"])
			}
			fmtCfg, ok := outCfg["format"].(map[string]any)
			if !ok || fmtCfg["type"] != "json_schema" {
				t.Fatalf("expected output_config.format.type='json_schema', got %v", outCfg["format"])
			}
			schemaMap, ok := fmtCfg["schema"].(map[string]any)
			if !ok || schemaMap["additionalProperties"] != false {
				t.Fatalf("expected strict-normalized schema with additionalProperties=false, got %v", fmtCfg["schema"])
			}
			reqKeys, _ := schemaMap["required"].([]any)
			if len(reqKeys) != 4 {
				t.Errorf("expected all 4 properties in required to stay within Anthropic grammar limits, got %v", schemaMap["required"])
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"stop_reason": "end_turn",
				"content": [{"type": "text", "text": "{\"summary\":\"Anthropic Tailored\"}"}],
				"usage": {"input_tokens": 40, "output_tokens": 20}
			}`))
		}))
		defer srv.Close()

		prov := NewAnthropicProvider("test-anthropic-key", WithBaseURL(srv.URL))
		resp, err := prov.Complete(context.Background(), core.LLMCompletionRequest{
			SystemPrompt:   "sys",
			UserPrompt:     "user",
			ResponseSchema: sampleTestSchema,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Text != `{"summary":"Anthropic Tailored"}` {
			t.Errorf("unexpected text: %q", resp.Text)
		}
		if resp.Usage.TotalTokens != 60 {
			t.Errorf("expected 60 total tokens, got %d", resp.Usage.TotalTokens)
		}
		if after := cloneJSONSnapshot(t, sampleTestSchema); after != beforeSnapshot {
			t.Errorf("AnthropicProvider mutated caller schema!\nbefore: %s\nafter:  %s", beforeSnapshot, after)
		}
	})

	t.Run("legacy Claude 3.5 Haiku falls back without output_config or double schema append", func(t *testing.T) {
		var capturedSystem string
		var hasOutputConfig bool

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			capturedSystem, _ = body["system"].(string)
			_, hasOutputConfig = body["output_config"]
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"stop_reason": "end_turn",
				"content": [{"type": "text", "text": "{\"summary\":\"Legacy Haiku\"}"}],
				"usage": {"input_tokens": 12, "output_tokens": 8}
			}`))
		}))
		defer srv.Close()

		prov := NewAnthropicProvider("key", WithBaseURL(srv.URL))
		resp, err := prov.Complete(context.Background(), core.LLMCompletionRequest{
			Model:          "claude-3-5-haiku-20241022",
			SystemPrompt:   "Base instructions",
			UserPrompt:     "user",
			ResponseSchema: sampleTestSchema,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if hasOutputConfig {
			t.Errorf("expected output_config to be omitted for unsupported legacy model")
		}
		if strings.Count(capturedSystem, "<response_schema>") != 1 {
			t.Errorf("expected single <response_schema> fallback in system prompt, got:\n%s", capturedSystem)
		}
		if resp.Text != `{"summary":"Legacy Haiku"}` {
			t.Errorf("unexpected text: %q", resp.Text)
		}

		// Calling again when SystemPrompt already contains <response_schema> must not double-append
		_, err = prov.Complete(context.Background(), core.LLMCompletionRequest{
			Model:          "claude-3-5-haiku-20241022",
			SystemPrompt:   capturedSystem,
			UserPrompt:     "user",
			ResponseSchema: sampleTestSchema,
		})
		if err != nil {
			t.Fatalf("unexpected error on pre-appended prompt: %v", err)
		}
		if strings.Count(capturedSystem, "<response_schema>") != 1 {
			t.Errorf("expected zero double-appending of <response_schema>, got count=%d", strings.Count(capturedSystem, "<response_schema>"))
		}
	})

	t.Run("plain text, refusal, and error handling", func(t *testing.T) {
		cases := []struct {
			name       string
			statusCode int
			body       string
			withSchema bool
			wantErr    error
		}{
			{
				name:       "stop_reason refusal",
				statusCode: http.StatusOK,
				body:       `{"stop_reason":"refusal","content":[{"type":"text","text":"I cannot assist with that."}]}`,
				withSchema: true,
				wantErr:    ErrInvalidResponse,
			},
			{
				name:       "stop_reason max_tokens on structured output",
				statusCode: http.StatusOK,
				body:       `{"stop_reason":"max_tokens","content":[{"type":"text","text":"{\"summary\":\"trunc"}]}`,
				withSchema: true,
				wantErr:    ErrInvalidResponse,
			},
			{
				name:       "empty content",
				statusCode: http.StatusOK,
				body:       `{"stop_reason":"end_turn","content":[]}`,
				withSchema: false,
				wantErr:    ErrInvalidResponse,
			},
			{
				name:       "malformed json",
				statusCode: http.StatusOK,
				body:       `{invalid`,
				withSchema: false,
				wantErr:    ErrInvalidResponse,
			},
			{
				name:       "401 unauthorized",
				statusCode: http.StatusUnauthorized,
				body:       `{"error":{"message":"invalid x-api-key"}}`,
				withSchema: false,
				wantErr:    ErrUnauthorized,
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tc.statusCode)
					_, _ = w.Write([]byte(tc.body))
				}))
				defer srv.Close()

				prov := NewAnthropicProvider("key", WithBaseURL(srv.URL))
				var schema map[string]any
				if tc.withSchema {
					schema = sampleTestSchema
				}
				_, err := prov.Complete(context.Background(), core.LLMCompletionRequest{
					UserPrompt:     "user",
					ResponseSchema: schema,
				})
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("expected %v, got %v", tc.wantErr, err)
				}
			})
		}
	})
}

func TestOllamaProvider_Complete_StructuredAndPlainText(t *testing.T) {
	t.Run("structured response with stream=false, format schema, and Bearer auth", func(t *testing.T) {
		beforeSnapshot := cloneJSONSnapshot(t, sampleTestSchema)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/chat" {
				t.Errorf("expected /api/chat, got %q", r.URL.Path)
			}
			if r.Header.Get("Authorization") != "Bearer ollama-token" {
				t.Errorf("expected Bearer ollama-token, got %q", r.Header.Get("Authorization"))
			}
			rawReqBytes, err := readBoundedBody(r.Body)
			if err != nil {
				t.Fatalf("failed to read request body: %v", err)
			}
			var body map[string]any
			if err := json.Unmarshal(rawReqBytes, &body); err != nil {
				t.Fatalf("failed to unmarshal ollama request: %v", err)
			}
			streamVal, hasStream := body["stream"]
			if !hasStream || streamVal != false {
				t.Errorf("expected explicit \"stream\": false in ollama payload, got has=%v val=%v (%s)", hasStream, streamVal, string(rawReqBytes))
			}
			fmtMap, ok := body["format"].(map[string]any)
			if !ok || fmtMap["type"] != "object" {
				t.Fatalf("expected format JSON schema object, got %v", body["format"])
			}
			if _, hasMeta := fmtMap["$schema"]; hasMeta {
				t.Errorf("expected $schema meta-keyword stripped from ollama format")
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"model": "llama3.1",
				"message": {"role": "assistant", "content": "{\"summary\":\"Ollama Structured\"}"},
				"done": true,
				"prompt_eval_count": 64,
				"eval_count": 36
			}`))
		}))
		defer srv.Close()

		prov := NewOllamaProvider(srv.URL, "ollama-token")
		if prov.Name() != ProviderOllama {
			t.Errorf("expected %q, got %q", ProviderOllama, prov.Name())
		}
		if !prov.SupportsStructuredOutput("") {
			t.Errorf("expected OllamaProvider.SupportsStructuredOutput to be true")
		}
		if len(prov.Models()) != 1 || prov.Models()[0].Model != defaultOllamaModel {
			t.Errorf("expected default model %q, got %+v", defaultOllamaModel, prov.Models())
		}

		resp, err := prov.Complete(context.Background(), core.LLMCompletionRequest{
			SystemPrompt:   "sys",
			UserPrompt:     "user",
			ResponseSchema: sampleTestSchema,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Text != `{"summary":"Ollama Structured"}` {
			t.Errorf("unexpected text: %q", resp.Text)
		}
		if resp.Usage.PromptTokens != 64 || resp.Usage.CompletionTokens != 36 || resp.Usage.TotalTokens != 100 {
			t.Errorf("unexpected usage: %+v", resp.Usage)
		}
		if after := cloneJSONSnapshot(t, sampleTestSchema); after != beforeSnapshot {
			t.Errorf("OllamaProvider mutated caller schema!\nbefore: %s\nafter:  %s", beforeSnapshot, after)
		}
	})

	t.Run("plain text omits format, keeps stream=false, omits Authorization when key empty, normalizes URLs", func(t *testing.T) {
		suffixes := []string{"", "/", "/api", "/api/chat"}
		for _, suffix := range suffixes {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/chat" {
					t.Errorf("suffix %q: expected /api/chat, got %q", suffix, r.URL.Path)
				}
				if auth := r.Header.Get("Authorization"); auth != "" {
					t.Errorf("expected empty Authorization header when no apiKey set, got %q", auth)
				}
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				if streamVal, ok := body["stream"]; !ok || streamVal != false {
					t.Errorf("expected explicit stream=false, got %v", body["stream"])
				}
				if _, hasFormat := body["format"]; hasFormat {
					t.Errorf("did not expect format on plain-text ollama request")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"  plain ollama  "},"prompt_eval_count":10,"eval_count":5}`))
			}))

			prov := NewOllamaProvider(srv.URL+suffix, "", WithDefaultModel("qwen2.5:14b"))
			resp, err := prov.Complete(context.Background(), core.LLMCompletionRequest{
				UserPrompt: "hello",
			})
			srv.Close()
			if err != nil {
				t.Fatalf("suffix %q: unexpected error: %v", suffix, err)
			}
			if resp.Text != "plain ollama" || resp.Usage.TotalTokens != 15 {
				t.Errorf("suffix %q: unexpected response %+v", suffix, resp)
			}
		}
	})

	t.Run("non-200 HTTP errors, malformed JSON, and empty content", func(t *testing.T) {
		cases := []struct {
			name       string
			statusCode int
			body       string
			wantErr    error
		}{
			{"400 bad request", http.StatusBadRequest, `{"error":"invalid format schema"}`, ErrBadRequest},
			{"401 unauthorized", http.StatusUnauthorized, `unauthorized`, ErrUnauthorized},
			{"429 rate limited", http.StatusTooManyRequests, `too many requests`, ErrRateLimited},
			{"500 internal error", http.StatusInternalServerError, `model runner crashed`, ErrUpstream},
			{"200 malformed json", http.StatusOK, `{not-json`, ErrInvalidResponse},
			{"200 empty message content", http.StatusOK, `{"message":{"role":"assistant","content":"   "},"prompt_eval_count":11,"eval_count":4}`, ErrInvalidResponse},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tc.statusCode)
					_, _ = w.Write([]byte(tc.body))
				}))
				defer srv.Close()

				prov := NewOllamaProvider(srv.URL, "")
				resp, err := prov.Complete(context.Background(), core.LLMCompletionRequest{UserPrompt: "hi"})
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("expected %v, got %v", tc.wantErr, err)
				}
				if resp == nil || resp.StatusCode != tc.statusCode {
					t.Errorf("expected status %d in response, got %+v", tc.statusCode, resp)
				}
				if tc.name == "200 empty message content" && resp != nil && resp.Usage.TotalTokens != 15 {
					t.Errorf("expected empty-content error response to preserve usage TotalTokens=15, got %+v", resp.Usage)
				}
			})
		}
	})
}

func TestCompatProvider_Complete_FallbackAndNoDoubleSchema(t *testing.T) {
	var capturedMessages []map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("expected /v1/chat/completions, got %q", r.URL.Path)
		}
		var body struct {
			Messages []map[string]any `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		capturedMessages = body.Messages
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices": [{"message": {"content": "{\"summary\":\"Compat Tailored\"}"}}],
			"usage": {"prompt_tokens": 30, "completion_tokens": 10}
		}`))
	}))
	defer srv.Close()

	prov := NewCompatProvider(srv.URL+"/v1", "optional-key", WithDefaultModel("llama3.3"))
	if prov.Name() != ProviderCompat {
		t.Errorf("expected %q, got %q", ProviderCompat, prov.Name())
	}
	if prov.SupportsStructuredOutput("") {
		t.Errorf("expected CompatProvider.SupportsStructuredOutput to return false")
	}

	// 1. Direct call without <response_schema> embeds <json_output_schema>
	resp, err := prov.Complete(context.Background(), core.LLMCompletionRequest{
		SystemPrompt:   "sys",
		UserPrompt:     "user",
		ResponseSchema: sampleTestSchema,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sysMsg, _ := capturedMessages[0]["content"].(string)
	if !strings.Contains(sysMsg, "<json_output_schema>") {
		t.Errorf("expected <json_output_schema> fallback on direct call, got %q", sysMsg)
	}
	if resp.Text != `{"summary":"Compat Tailored"}` || resp.Usage.TotalTokens != 40 {
		t.Errorf("unexpected compat response: %+v", resp)
	}

	// 2. Call when <response_schema> is already present in SystemPrompt does NOT double-append
	preCompiledSys := "sys\n\n<response_schema>\n{\"type\":\"object\"}\n</response_schema>"
	_, err = prov.Complete(context.Background(), core.LLMCompletionRequest{
		SystemPrompt:   preCompiledSys,
		UserPrompt:     "user",
		ResponseSchema: sampleTestSchema,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sysMsgAfter, _ := capturedMessages[0]["content"].(string)
	if strings.Contains(sysMsgAfter, "<json_output_schema>") {
		t.Errorf("expected no duplicate <json_output_schema> when <response_schema> already present, got %q", sysMsgAfter)
	}
	if !reflect.DeepEqual(sysMsgAfter, preCompiledSys) {
		t.Errorf("expected system prompt unchanged, got %q", sysMsgAfter)
	}
}

func TestRegisterClients_DefaultModelPerProvider(t *testing.T) {
	reg := NewRegistry()
	RegisterClients(reg, ProviderConfig{
		DefaultProvider: ProviderOpenAI,
		GeminiAPIKey:    "gem-key",
		GeminiModel:     "gemini-3.8-pro",
		OpenAIAPIKey:    "oai-key",
		OpenAIModel:     "gpt-4o-mini",
		AnthropicAPIKey: "ant-key",
		AnthropicModel:  "claude-sonnet-4-5",
		OllamaBaseURL:   "http://localhost:11434",
		OllamaModel:     "llama3.2",
		CompatBaseURL:   "http://localhost:8000/v1",
		CompatModel:     "qwen2.5",
	}, WithTimeout(30*time.Second))

	for _, name := range []string{ProviderGemini, ProviderOpenAI, ProviderAnthropic, ProviderOllama, ProviderCompat, ProviderCompatAlt} {
		if _, err := reg.Get(name); err != nil {
			t.Errorf("expected provider %q to be registered: %v", name, err)
		}
	}

	defProv, _, err := reg.Resolve(core.GenerationRequest{}, nil, nil)
	if err != nil || defProv.Name() != ProviderOpenAI {
		t.Errorf("expected default provider %q, got %v (err=%v)", ProviderOpenAI, defProv, err)
	}

	models := reg.AvailableModels()
	if len(models) != 5 {
		t.Errorf("expected exactly 5 default models (one per registered provider), got %d: %+v", len(models), models)
	}
}

func TestTransportUtilities_BoundsAndStatusError(t *testing.T) {
	b, err := readBoundedBody(nil)
	if err != nil || b != nil {
		t.Errorf("expected nil for nil reader, got %v, %v", b, err)
	}

	b, err = readBoundedBody(bytes.NewReader([]byte("hello")))
	if err != nil || string(b) != "hello" {
		t.Errorf("unexpected readBoundedBody output: %q, %v", string(b), err)
	}

	if err := checkStatusError("openai", http.StatusBadRequest, []byte("bad")); !errors.Is(err, ErrBadRequest) {
		t.Errorf("expected ErrBadRequest, got %v", err)
	}
	if err := checkStatusError("openai", http.StatusUnauthorized, []byte("unauth")); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
	if err := checkStatusError("openai", http.StatusTooManyRequests, []byte("rate")); !errors.Is(err, ErrRateLimited) {
		t.Errorf("expected ErrRateLimited, got %v", err)
	}
	if err := checkStatusError("openai", http.StatusBadGateway, []byte("502")); !errors.Is(err, ErrUpstream) {
		t.Errorf("expected ErrUpstream, got %v", err)
	}
	if err := checkStatusError("openai", http.StatusTeapot, []byte("418")); err == nil {
		t.Error("expected error for status 418")
	}

	oversizedBody := strings.Repeat("A", 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(oversizedBody))
	}))
	defer srv.Close()

	wrappedClient := wrapBoundedHTTPClientWithLimit(srv.Client(), 16)
	resp, err := wrappedClient.Get(srv.URL)
	if err != nil {
		t.Fatalf("unexpected wrapped client error: %v", err)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("unexpected io.ReadAll error: %v", err)
	}
	if len(got) != 16 || string(got) != strings.Repeat("A", 16) {
		t.Fatalf("expected bounded transport to truncate at 16 bytes, got len=%d (%q)", len(got), string(got))
	}
	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("unexpected Close error: %v", closeErr)
	}
}

func TestProviders_DefensiveNilAndEmptyCredentialGuards(t *testing.T) {
	ctx := context.Background()
	req := core.LLMCompletionRequest{UserPrompt: "ping"}

	if _, err := (*GeminiProvider)(nil).Complete(ctx, req); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized for nil GeminiProvider, got %v", err)
	}
	if _, err := (*OpenAIProvider)(nil).Complete(ctx, req); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized for nil OpenAIProvider, got %v", err)
	}
	if _, err := NewOpenAIProvider("   ").Complete(ctx, req); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized for empty OpenAI key, got %v", err)
	}
	if _, err := (*AnthropicProvider)(nil).Complete(ctx, req); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized for nil AnthropicProvider, got %v", err)
	}
	if _, err := NewAnthropicProvider("").Complete(ctx, req); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized for empty Anthropic key, got %v", err)
	}
	if _, err := (*OllamaProvider)(nil).Complete(ctx, req); !errors.Is(err, ErrNoProviders) {
		t.Errorf("expected ErrNoProviders for nil OllamaProvider, got %v", err)
	}
	if _, err := (*CompatProvider)(nil).Complete(ctx, req); !errors.Is(err, ErrNoProviders) {
		t.Errorf("expected ErrNoProviders for nil CompatProvider, got %v", err)
	}
}
