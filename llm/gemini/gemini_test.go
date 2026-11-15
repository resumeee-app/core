package gemini_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/resumeee-app/resumeee-core/llm"
	"github.com/resumeee-app/resumeee-core/llm/gemini"
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
