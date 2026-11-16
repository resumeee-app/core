package gemini_test

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
	"github.com/resumeee-app/core/llm/gemini"
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

func TestGeminiClient_Complete_InteractionsSDK(t *testing.T) {
	t.Run("structured response via genai Interactions SDK", func(t *testing.T) {
		schema := sampleTestSchema()
		beforeSnapshot := cloneJSONSnapshot(t, schema)

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
				t.Errorf("expected $schema meta-keyword to be absent from schema")
			}
			if _, hasDefs := jsonSchema["definitions"]; hasDefs {
				t.Errorf("expected definitions meta-keyword to be absent from schema")
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

		client := gemini.New("test-gemini-key", gemini.WithBaseURL(srv.URL), gemini.WithHTTPClient(srv.Client()))
		if client.Name() != llm.ProviderGemini {
			t.Errorf("expected provider %q, got %q", llm.ProviderGemini, client.Name())
		}
		if !client.SupportsStructuredOutput("") {
			t.Errorf("expected gemini.Client.SupportsStructuredOutput to return true")
		}
		if client.DefaultModel() != "gemini-3.8-flash" {
			t.Errorf("expected DefaultModel gemini-3.8-flash, got %q", client.DefaultModel())
		}
		if len(client.Models()) != 1 || client.Models()[0].Model != "gemini-3.8-flash" {
			t.Errorf("expected single default model gemini-3.8-flash, got %+v", client.Models())
		}

		resp, err := client.Complete(context.Background(), llm.Request{
			SystemPrompt:   "sys",
			UserPrompt:     "user",
			ReasoningLevel: "high",
			ResponseSchema: schema,
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
		if after := cloneJSONSnapshot(t, schema); after != beforeSnapshot {
			t.Errorf("gemini.Client mutated caller schema!\nbefore: %s\nafter:  %s", beforeSnapshot, after)
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

		client := gemini.New("test-gemini-key", gemini.WithBaseURL(srv.URL), gemini.WithDefaultModel("gemini-custom"))
		if len(client.Models()) != 1 || client.Models()[0].Model != "gemini-custom" {
			t.Errorf("expected single custom default model 'gemini-custom', got %+v", client.Models())
		}
		resp, err := client.Complete(context.Background(), llm.Request{
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

	t.Run("nil client and empty api key return ErrUnauthorized without ambient env fallback", func(t *testing.T) {
		if _, err := (*gemini.Client)(nil).Complete(context.Background(), llm.Request{UserPrompt: "ping"}); !errors.Is(err, llm.ErrUnauthorized) {
			t.Errorf("expected ErrUnauthorized for nil gemini.Client, got %v", err)
		}
		client := gemini.New("")
		_, err := client.Complete(context.Background(), llm.Request{
			UserPrompt: "hello",
		})
		if !errors.Is(err, llm.ErrUnauthorized) {
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
			{"400 client error schema", http.StatusBadRequest, "application/json", `{"error":{"code":"invalid_argument","message":"bad req\nbad req"}}`, llm.ErrBadRequest},
			{"400 legacy int code error", http.StatusBadRequest, "application/json", `{"error":{"code":400,"message":"bad req\nbad req"}}`, llm.ErrBadRequest},
			{"401 unauthorized", http.StatusUnauthorized, "application/json", `{"error":{"code":"unauthenticated","message":"invalid api key"}}`, llm.ErrUnauthorized},
			{"429 rate limited", http.StatusTooManyRequests, "text/plain", `rate limit exceeded`, llm.ErrRateLimited},
			{"429 billing quota exhausted", http.StatusTooManyRequests, "application/json", `{"error":{"code":"resource_exhausted","message":"Billing account is not active; check billing quota."}}`, llm.ErrUpstreamQuota},
			{"429 quota indicator only in error.code", http.StatusTooManyRequests, "application/json", `{"error":{"code":"insufficient_quota","message":"Quota exhausted"}}`, llm.ErrUpstreamQuota},
			{"400 quota indicator only in error.code", http.StatusBadRequest, "application/json", `{"error":{"code":"billing_not_active","message":"Request failed"}}`, llm.ErrUpstreamQuota},
			{"402 payment required", http.StatusPaymentRequired, "text/plain", `payment required`, llm.ErrUpstreamQuota},
			{"503 upstream", http.StatusServiceUnavailable, "application/json", `{"error":{"code":"unavailable","message":"overloaded"}}`, llm.ErrUpstream},
			{"200 malformed json", http.StatusOK, "application/json", `{not-json`, llm.ErrInvalidResponse},
			{"200 empty steps", http.StatusOK, "application/json", `{"id":"i1","status":"completed","steps":[]}`, llm.ErrInvalidResponse},
			{"200 failed status", http.StatusOK, "application/json", `{"id":"i2","status":"failed","errors":[{"code":"safety","message":"blocked"}]}`, llm.ErrInvalidResponse},
			{"200 budget_exceeded with partial output", http.StatusOK, "application/json", `{"id":"i3","status":"budget_exceeded","steps":[{"type":"model_output","content":[{"type":"text","text":"{\"partial\":"}]}]}`, llm.ErrInvalidResponse},
			{"200 requires_continuation with partial output", http.StatusOK, "application/json", `{"id":"i4","status":"requires_continuation","steps":[{"type":"model_output","content":[{"type":"text","text":"partial text"}]}]}`, llm.ErrInvalidResponse},
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

				client := gemini.New("key", gemini.WithBaseURL(srv.URL), gemini.WithHTTPClient(srv.Client()))
				resp, err := client.Complete(context.Background(), llm.Request{
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

func TestGeminiClient_Complete_Telemetry(t *testing.T) {
	t.Run("pre-flight nil receiver and empty key return nil response", func(t *testing.T) {
		if resp, err := (*gemini.Client)(nil).Complete(context.Background(), llm.Request{UserPrompt: "ping"}); !errors.Is(err, llm.ErrUnauthorized) || resp != nil {
			t.Errorf("expected (nil, ErrUnauthorized) for nil gemini.Client, got (%+v, %v)", resp, err)
		}
		if resp, err := gemini.New("   ").Complete(context.Background(), llm.Request{UserPrompt: "ping"}); !errors.Is(err, llm.ErrUnauthorized) || resp != nil {
			t.Errorf("expected (nil, ErrUnauthorized) for blank gemini key, got (%+v, %v)", resp, err)
		}
	})

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
				body:      `{"id":"g1","status":"completed","steps":[{"type":"model_output","content":[{"type":"text","text":"ok"}]}],"usage":{"total_input_tokens":100,"total_output_tokens":50,"total_tokens":175}}`,
				wantModel: llm.DefaultModelForProvider(llm.ProviderGemini),
				wantUsage: core.TokenUsage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 175},
			},
			{
				name:      "omitted total tokens backfilled with explicit request model",
				reqModel:  "  gemini-2.5-pro  ",
				body:      `{"id":"g2","status":"completed","steps":[{"type":"model_output","content":[{"type":"text","text":"ok"}]}],"usage":{"total_input_tokens":40,"total_output_tokens":15}}`,
				wantModel: "gemini-2.5-pro",
				wantUsage: core.TokenUsage{PromptTokens: 40, CompletionTokens: 15, TotalTokens: 55},
			},
			{
				name:         "prompt-only usage backfilled with custom default model",
				defaultModel: "gemini-custom",
				body:         `{"id":"g3","status":"completed","steps":[{"type":"model_output","content":[{"type":"text","text":"ok"}]}],"usage":{"total_input_tokens":22}}`,
				wantModel:    "gemini-custom",
				wantUsage:    core.TokenUsage{PromptTokens: 22, TotalTokens: 22},
			},
			{
				name:      "omitted usage stays zero",
				body:      `{"id":"g4","status":"completed","steps":[{"type":"model_output","content":[{"type":"text","text":"ok"}]}]}`,
				wantModel: llm.DefaultModelForProvider(llm.ProviderGemini),
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

				opts := []gemini.Option{gemini.WithBaseURL(srv.URL), gemini.WithHTTPClient(srv.Client())}
				if tc.defaultModel != "" {
					opts = append(opts, gemini.WithDefaultModel(tc.defaultModel))
				}
				client := gemini.New("test-key", opts...)

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
				if resp.Provider != llm.ProviderGemini || resp.Model != tc.wantModel || resp.StatusCode != http.StatusOK {
					t.Errorf("metadata = (provider=%q, model=%q, status=%d), want (%q, %q, 200)", resp.Provider, resp.Model, resp.StatusCode, llm.ProviderGemini, tc.wantModel)
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
			name        string
			reqModel    string
			statusCode  int
			contentType string
			body        string
			wantErr     error
			wantModel   string
			wantUsage   core.TokenUsage
		}{
			{
				name:        "400 bad request",
				statusCode:  http.StatusBadRequest,
				contentType: "application/json",
				body:        `{"error":{"code":"invalid_argument","message":"bad req"}}`,
				wantErr:     llm.ErrBadRequest,
				wantModel:   llm.DefaultModelForProvider(llm.ProviderGemini),
			},
			{
				name:        "401 unauthorized",
				reqModel:    "gemini-2.5-pro",
				statusCode:  http.StatusUnauthorized,
				contentType: "application/json",
				body:        `{"error":{"code":"unauthenticated","message":"invalid key"}}`,
				wantErr:     llm.ErrUnauthorized,
				wantModel:   "gemini-2.5-pro",
			},
			{
				name:        "429 rate limited json",
				statusCode:  http.StatusTooManyRequests,
				contentType: "application/json",
				body:        `{"error":{"code":"resource_exhausted","message":"quota exceeded"}}`,
				wantErr:     llm.ErrRateLimited,
				wantModel:   llm.DefaultModelForProvider(llm.ProviderGemini),
			},
			{
				name:        "500 upstream error",
				statusCode:  http.StatusInternalServerError,
				contentType: "application/json",
				body:        `{"error":{"code":"internal","message":"boom"}}`,
				wantErr:     llm.ErrUpstream,
				wantModel:   llm.DefaultModelForProvider(llm.ProviderGemini),
			},
			{
				name:        "200 malformed json",
				statusCode:  http.StatusOK,
				contentType: "application/json",
				body:        `{broken`,
				wantErr:     llm.ErrInvalidResponse,
				wantModel:   llm.DefaultModelForProvider(llm.ProviderGemini),
			},
			{
				name:        "200 unexpected text content-type",
				statusCode:  http.StatusOK,
				contentType: "text/plain",
				body:        `plain text`,
				wantErr:     llm.ErrInvalidResponse,
				wantModel:   llm.DefaultModelForProvider(llm.ProviderGemini),
			},
			{
				name:        "200 failed status preserves partial usage with backfill",
				reqModel:    "gemini-2.5-flash",
				statusCode:  http.StatusOK,
				contentType: "application/json",
				body:        `{"id":"gf","status":"failed","errors":[{"code":"safety","message":"blocked"}],"usage":{"total_input_tokens":33,"total_output_tokens":7}}`,
				wantErr:     llm.ErrInvalidResponse,
				wantModel:   "gemini-2.5-flash",
				wantUsage:   core.TokenUsage{PromptTokens: 33, CompletionTokens: 7, TotalTokens: 40},
			},
			{
				name:        "200 incomplete status preserves explicit total usage",
				statusCode:  http.StatusOK,
				contentType: "application/json",
				body:        `{"id":"gi","status":"incomplete","usage":{"total_input_tokens":50,"total_output_tokens":10,"total_tokens":65}}`,
				wantErr:     llm.ErrInvalidResponse,
				wantModel:   llm.DefaultModelForProvider(llm.ProviderGemini),
				wantUsage:   core.TokenUsage{PromptTokens: 50, CompletionTokens: 10, TotalTokens: 65},
			},
			{
				name:        "200 empty output preserves partial usage",
				statusCode:  http.StatusOK,
				contentType: "application/json",
				body:        `{"id":"ge","status":"completed","steps":[],"usage":{"total_input_tokens":19}}`,
				wantErr:     llm.ErrInvalidResponse,
				wantModel:   llm.DefaultModelForProvider(llm.ProviderGemini),
				wantUsage:   core.TokenUsage{PromptTokens: 19, TotalTokens: 19},
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", tc.contentType)
					w.WriteHeader(tc.statusCode)
					_, _ = w.Write([]byte(tc.body))
				}))
				defer srv.Close()

				client := gemini.New("test-key", gemini.WithBaseURL(srv.URL), gemini.WithHTTPClient(srv.Client()))
				resp, err := client.Complete(context.Background(), llm.Request{
					Model:      tc.reqModel,
					UserPrompt: "hello",
				})
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("error = %v, want %v", err, tc.wantErr)
				}
				if resp == nil {
					t.Fatalf("expected non-nil response on error")
				}
				if resp.Provider != llm.ProviderGemini || resp.Model != tc.wantModel || resp.StatusCode != tc.statusCode {
					t.Errorf("metadata = (provider=%q, model=%q, status=%d), want (%q, %q, %d)", resp.Provider, resp.Model, resp.StatusCode, llm.ProviderGemini, tc.wantModel, tc.statusCode)
				}
				if resp.Latency <= 0 {
					t.Errorf("expected Latency > 0, got %v", resp.Latency)
				}
				if resp.Usage != tc.wantUsage {
					t.Errorf("Usage = %+v, want %+v", resp.Usage, tc.wantUsage)
				}
				if strings.Contains(err.Error(), "invalid key") || strings.Contains(err.Error(), "quota exceeded") || strings.Contains(err.Error(), "boom") || strings.Contains(err.Error(), "safety") {
					t.Errorf("expected gemini error string not to echo raw upstream body/message, got %q", err.Error())
				}
			})
		}

		t.Run("upstream error bodies containing PII never leak into error string", func(t *testing.T) {
			const secretPII = "SENSITIVE_GEMINI_ECHO_PII_5599"
			for _, tc := range []struct {
				name        string
				statusCode  int
				contentType string
				body        string
				wantErr     error
			}{
				{
					name:        "json error message redacted",
					statusCode:  http.StatusBadRequest,
					contentType: "application/json",
					body:        `{"error":{"code":"invalid_argument","message":"` + secretPII + `"}}`,
					wantErr:     llm.ErrBadRequest,
				},
				{
					name:        "plain text 502 error body redacted",
					statusCode:  http.StatusBadGateway,
					contentType: "text/plain",
					body:        "upstream proxy failed with " + secretPII,
					wantErr:     llm.ErrUpstream,
				},
			} {
				t.Run(tc.name, func(t *testing.T) {
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.Header().Set("Content-Type", tc.contentType)
						w.WriteHeader(tc.statusCode)
						_, _ = w.Write([]byte(tc.body))
					}))
					defer srv.Close()

					client := gemini.New("test-key", gemini.WithBaseURL(srv.URL), gemini.WithHTTPClient(srv.Client()))
					resp, err := client.Complete(context.Background(), llm.Request{UserPrompt: "hello"})
					if !errors.Is(err, tc.wantErr) {
						t.Fatalf("error = %v, want %v", err, tc.wantErr)
					}
					if strings.Contains(err.Error(), secretPII) {
						t.Errorf("error string leaked upstream PII: %q", err.Error())
					}
					if resp == nil || !strings.Contains(resp.RawBody, secretPII) {
						t.Errorf("expected RawBody to retain raw response for in-memory diagnostics, got %+v", resp)
					}
				})
			}
		})
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

			client := gemini.New("test-key", gemini.WithBaseURL(srv.URL), gemini.WithHTTPClient(srv.Client()))
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
			defer cancel()

			resp, err := client.Complete(ctx, llm.Request{
				Model:      "gemini-2.5-pro",
				UserPrompt: "ping",
			})
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expected context.DeadlineExceeded, got %v", err)
			}
			if resp == nil {
				t.Fatalf("expected non-nil response on timeout")
			}
			if resp.Provider != llm.ProviderGemini || resp.Model != "gemini-2.5-pro" || resp.StatusCode != 0 {
				t.Errorf("metadata = (provider=%q, model=%q, status=%d), want (%q, %q, 0)", resp.Provider, resp.Model, resp.StatusCode, llm.ProviderGemini, "gemini-2.5-pro")
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

			client := gemini.New(
				"test-key",
				gemini.WithBaseURL(srv.URL),
				gemini.WithHTTPClient(srv.Client()),
				gemini.WithTimeout(15*time.Millisecond),
			)
			resp, err := client.Complete(context.Background(), llm.Request{
				Model:      "gemini-2.5-flash",
				UserPrompt: "ping",
			})
			if err == nil {
				t.Fatalf("expected timeout error from WithTimeout")
			}
			if resp == nil {
				t.Fatalf("expected non-nil response on client timeout")
			}
			if resp.Provider != llm.ProviderGemini || resp.Model != "gemini-2.5-flash" || resp.StatusCode != 0 {
				t.Errorf("metadata = (provider=%q, model=%q, status=%d), want (%q, %q, 0)", resp.Provider, resp.Model, resp.StatusCode, llm.ProviderGemini, "gemini-2.5-flash")
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

			client := gemini.New("test-key", gemini.WithBaseURL(srvURL), gemini.WithHTTPClient(httpClient))
			resp, err := client.Complete(context.Background(), llm.Request{UserPrompt: "ping"})
			if err == nil {
				t.Fatalf("expected transport error against closed server")
			}
			if resp == nil {
				t.Fatalf("expected non-nil response on transport error")
			}
			wantModel := llm.DefaultModelForProvider(llm.ProviderGemini)
			if resp.Provider != llm.ProviderGemini || resp.Model != wantModel || resp.StatusCode != 0 {
				t.Errorf("metadata = (provider=%q, model=%q, status=%d), want (%q, %q, 0)", resp.Provider, resp.Model, resp.StatusCode, llm.ProviderGemini, wantModel)
			}
			if resp.Latency <= 0 {
				t.Errorf("expected Latency > 0, got %v", resp.Latency)
			}
		})
	})
}
