package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

var sampleTestSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"summary": map[string]any{"type": "string"},
	},
	"required":             []string{"summary"},
	"additionalProperties": false,
}

func TestGeminiProvider_Complete_StructuredAndText(t *testing.T) {
	t.Run("structured response with schema", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("x-goog-api-key") != "test-gemini-key" {
				t.Errorf("expected x-goog-api-key header, got %q", r.Header.Get("x-goog-api-key"))
			}
			if !strings.Contains(r.URL.Path, "gemini-3.8-pro:generateContent") {
				t.Errorf("expected model in path, got %q", r.URL.Path)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("failed to decode request body: %v", err)
			}
			genCfg, ok := body["generationConfig"].(map[string]any)
			if !ok || genCfg["responseMimeType"] != "application/json" {
				t.Errorf("expected responseMimeType application/json, got %v", body["generationConfig"])
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"candidates": [{"content": {"parts": [{"text": "{\"summary\":\"Tailored\"}"}]}}],
				"usageMetadata": {"promptTokenCount": 100, "candidatesTokenCount": 50, "totalTokenCount": 150}
			}`))
		}))
		defer srv.Close()

		prov := NewGeminiProvider("test-gemini-key", WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
		if prov.Name() != ProviderGemini {
			t.Errorf("expected provider %q, got %q", ProviderGemini, prov.Name())
		}
		if len(prov.Models()) != 1 || prov.Models()[0].Model != defaultGeminiModel {
			t.Errorf("expected single default model %q, got %+v", defaultGeminiModel, prov.Models())
		}

		resp, err := prov.Complete(context.Background(), core.LLMCompletionRequest{
			Model:          "gemini-3.8-pro",
			SystemPrompt:   "sys",
			UserPrompt:     "user",
			ResponseSchema: sampleTestSchema,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Text != `{"summary":"Tailored"}` {
			t.Errorf("unexpected text: %q", resp.Text)
		}
		if resp.Usage.TotalTokens != 150 {
			t.Errorf("expected 150 total tokens, got %d", resp.Usage.TotalTokens)
		}
	})

	t.Run("plain text completion without schema", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if _, hasGenCfg := body["generationConfig"]; hasGenCfg {
				t.Error("did not expect generationConfig for plain text completion")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"candidates": [{"content": {"parts": [{"text": "  extracted text  "}]}}],
				"usageMetadata": {"promptTokenCount": 10, "candidatesTokenCount": 5}
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

	t.Run("error statuses and malformed responses", func(t *testing.T) {
		cases := []struct {
			name       string
			statusCode int
			body       string
			wantErr    error
		}{
			{"400 bad request", http.StatusBadRequest, `{"error":{"code":400,"message":"bad req\nbad req"}}`, ErrBadRequest},
			{"401 unauthorized", http.StatusUnauthorized, `{"error":"invalid api key"}`, ErrUnauthorized},
			{"429 rate limited", http.StatusTooManyRequests, `rate limit exceeded`, ErrRateLimited},
			{"503 upstream", http.StatusServiceUnavailable, ``, ErrUpstream},
			{"200 malformed json", http.StatusOK, `{not-json`, ErrInvalidResponse},
			{"200 empty candidates", http.StatusOK, `{"candidates":[]}`, ErrInvalidResponse},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tc.statusCode)
					_, _ = w.Write([]byte(tc.body))
				}))
				defer srv.Close()

				prov := NewGeminiProvider("key", WithBaseURL(srv.URL))
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

func TestOpenAIProvider_Complete_StructuredAndText(t *testing.T) {
	t.Run("structured response with json_schema", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer test-openai-key" {
				t.Errorf("unexpected Authorization header: %q", r.Header.Get("Authorization"))
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			rf, ok := body["response_format"].(map[string]any)
			if !ok || rf["type"] != "json_schema" {
				t.Errorf("expected response_format json_schema, got %v", body["response_format"])
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"choices": [{"message": {"content": "{\"summary\":\"OpenAI Tailored\"}"}}],
				"usage": {"prompt_tokens": 50, "completion_tokens": 25, "total_tokens": 75}
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
			ResponseSchema: sampleTestSchema,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Text != `{"summary":"OpenAI Tailored"}` {
			t.Errorf("unexpected text: %q", resp.Text)
		}
		if resp.Usage.TotalTokens != 75 {
			t.Errorf("expected 75 tokens, got %d", resp.Usage.TotalTokens)
		}
	})

	t.Run("error statuses and empty choices", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"choices":[]}`))
		}))
		defer srv.Close()

		prov := NewOpenAIProvider("key", WithBaseURL(srv.URL))
		_, err := prov.Complete(context.Background(), core.LLMCompletionRequest{UserPrompt: "hi"})
		if !errors.Is(err, ErrInvalidResponse) {
			t.Errorf("expected ErrInvalidResponse, got %v", err)
		}
	})
}

func TestAnthropicProvider_Complete_StructuredAndText(t *testing.T) {
	t.Run("structured response via tool_use", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("x-api-key") != "test-anthropic-key" {
				t.Errorf("unexpected x-api-key: %q", r.Header.Get("x-api-key"))
			}
			if r.Header.Get("anthropic-version") != anthropicAPIVersion {
				t.Errorf("unexpected anthropic-version: %q", r.Header.Get("anthropic-version"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"content": [{"type": "tool_use", "name": "generate_tailored_resume", "input": {"summary": "Anthropic Tailored"}}],
				"usage": {"input_tokens": 40, "output_tokens": 20}
			}`))
		}))
		defer srv.Close()

		prov := NewAnthropicProvider("test-anthropic-key", WithBaseURL(srv.URL))
		if prov.Name() != ProviderAnthropic {
			t.Errorf("expected %q, got %q", ProviderAnthropic, prov.Name())
		}
		if len(prov.Models()) != 1 || prov.Models()[0].Model != defaultAnthropicModel {
			t.Errorf("expected single default model %q, got %+v", defaultAnthropicModel, prov.Models())
		}

		resp, err := prov.Complete(context.Background(), core.LLMCompletionRequest{
			SystemPrompt:   "sys",
			UserPrompt:     "user",
			ResponseSchema: sampleTestSchema,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(resp.Text, "Anthropic Tailored") {
			t.Errorf("unexpected text: %q", resp.Text)
		}
		if resp.Usage.TotalTokens != 60 {
			t.Errorf("expected 60 total tokens, got %d", resp.Usage.TotalTokens)
		}
	})

	t.Run("plain text response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"content": [{"type": "text", "text": "Plain Claude Output"}],
				"usage": {"input_tokens": 10, "output_tokens": 5}
			}`))
		}))
		defer srv.Close()

		prov := NewAnthropicProvider("test-anthropic-key", WithBaseURL(srv.URL))
		resp, err := prov.Complete(context.Background(), core.LLMCompletionRequest{
			SystemPrompt: "sys",
			UserPrompt:   "user",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Text != "Plain Claude Output" {
			t.Errorf("unexpected text: %q", resp.Text)
		}
	})

	t.Run("missing tool call returns ErrInvalidResponse", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"content": [{"type": "text", "text": "no tool"}]}`))
		}))
		defer srv.Close()

		prov := NewAnthropicProvider("key", WithBaseURL(srv.URL))
		_, err := prov.Complete(context.Background(), core.LLMCompletionRequest{
			UserPrompt:     "user",
			ResponseSchema: sampleTestSchema,
		})
		if !errors.Is(err, ErrInvalidResponse) {
			t.Errorf("expected ErrInvalidResponse, got %v", err)
		}
	})
}

func TestCompatProvider_Complete_StructuredAndText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("expected /v1/chat/completions, got %q", r.URL.Path)
		}
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body.Messages) == 0 || !strings.Contains(body.Messages[0].Content, "<json_output_schema>") {
			t.Errorf("expected embedded <json_output_schema> in system message, got %+v", body.Messages)
		}
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
	if len(prov.Models()) != 1 || prov.Models()[0].Model != "llama3.3" {
		t.Errorf("unexpected compat models: %+v", prov.Models())
	}

	resp, err := prov.Complete(context.Background(), core.LLMCompletionRequest{
		SystemPrompt:   "sys",
		UserPrompt:     "user",
		ResponseSchema: sampleTestSchema,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != `{"summary":"Compat Tailored"}` {
		t.Errorf("unexpected text: %q", resp.Text)
	}
	if resp.Usage.TotalTokens != 40 {
		t.Errorf("expected 40 total tokens, got %d", resp.Usage.TotalTokens)
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
		AnthropicModel:  "claude-3-5-haiku-20241022",
		CompatBaseURL:   "http://localhost:11434/v1",
		CompatModel:     "qwen2.5",
	}, WithTimeout(30*time.Second))

	for _, name := range []string{ProviderGemini, ProviderOpenAI, ProviderAnthropic, ProviderCompat, ProviderCompatAlt} {
		if _, err := reg.Get(name); err != nil {
			t.Errorf("expected provider %q to be registered: %v", name, err)
		}
	}

	defProv, _, err := reg.Resolve(core.GenerationRequest{}, nil, nil)
	if err != nil || defProv.Name() != ProviderOpenAI {
		t.Errorf("expected default provider %q, got %v (err=%v)", ProviderOpenAI, defProv, err)
	}

	models := reg.AvailableModels()
	if len(models) != 4 {
		t.Errorf("expected exactly 4 default models (one per registered provider), got %d: %+v", len(models), models)
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
}
