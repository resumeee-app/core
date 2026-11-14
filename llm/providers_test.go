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
			if _, hasDeprecated := genCfg["responseSchema"]; hasDeprecated {
				t.Errorf("did not expect deprecated responseSchema alongside responseJsonSchema")
			}
			jsonSchema, hasJSONSchema := genCfg["responseJsonSchema"].(map[string]any)
			if !hasJSONSchema || jsonSchema["additionalProperties"] != false {
				t.Errorf("expected responseJsonSchema preserving additionalProperties: false, got %v", genCfg["responseJsonSchema"])
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

func TestProviders_SupportsStructuredOutput(t *testing.T) {
	cases := []struct {
		name string
		prov core.LLMClient
		want bool
	}{
		{"gemini", NewGeminiProvider("k"), true},
		{"openai", NewOpenAIProvider("k"), true},
		{"anthropic", NewAnthropicProvider("k"), true},
		{"ollama", NewOllamaProvider("", ""), true},
		{"compat", NewCompatProvider("http://localhost:8080/v1", ""), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.prov.SupportsStructuredOutput(); got != tc.want {
				t.Errorf("SupportsStructuredOutput() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOpenAIProvider_Complete_StructuredAndText(t *testing.T) {
	t.Run("structured response via Responses API", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/responses" {
				t.Errorf("expected /v1/responses, got %q", r.URL.Path)
			}
			if r.Header.Get("Authorization") != "Bearer test-openai-key" {
				t.Errorf("unexpected Authorization header: %q", r.Header.Get("Authorization"))
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("failed to decode body: %v", err)
			}
			if body["instructions"] != "sys" {
				t.Errorf("expected instructions 'sys', got %v", body["instructions"])
			}
			if body["input"] != "user" {
				t.Errorf("expected input 'user', got %v", body["input"])
			}
			reasoning, ok := body["reasoning"].(map[string]any)
			if !ok || reasoning["effort"] != "high" {
				t.Errorf("expected reasoning effort high, got %v", body["reasoning"])
			}
			textObj, ok := body["text"].(map[string]any)
			if !ok {
				t.Fatalf("expected text object in Responses API body, got %v", body)
			}
			formatObj, ok := textObj["format"].(map[string]any)
			if !ok || formatObj["type"] != "json_schema" || formatObj["strict"] != true || formatObj["name"] != "gap_analysis_response" {
				t.Errorf("unexpected text.format payload: %v", textObj["format"])
			}
			schemaObj, ok := formatObj["schema"].(map[string]any)
			if !ok || schemaObj["additionalProperties"] != false {
				t.Errorf("expected normalized strict schema in text.format.schema, got %v", formatObj["schema"])
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"status": "completed",
				"output": [{
					"type": "message",
					"role": "assistant",
					"content": [{"type": "output_text", "text": "{\"summary\":\"OpenAI Tailored\"}"}]
				}],
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
			ResponseSchema: sampleTestSchema,
			SchemaName:     "gap_analysis_response",
			ReasoningLevel: "high",
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
	})

	t.Run("plain text without schema omits text.format and reasoning when none", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if _, hasText := body["text"]; hasText {
				t.Errorf("did not expect text format when ResponseSchema is nil, got %v", body["text"])
			}
			if _, hasReasoning := body["reasoning"]; hasReasoning {
				t.Errorf("did not expect reasoning when level is 'none', got %v", body["reasoning"])
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"output_text": "Plain Responses Text",
				"usage": {"input_tokens": 12, "output_tokens": 8}
			}`))
		}))
		defer srv.Close()

		prov := NewOpenAIProvider("key", WithBaseURL(srv.URL))
		resp, err := prov.Complete(context.Background(), core.LLMCompletionRequest{
			UserPrompt:     "hi",
			ReasoningLevel: "none",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Text != "Plain Responses Text" || resp.Usage.TotalTokens != 20 {
			t.Errorf("unexpected resp: %+v", resp)
		}
	})

	t.Run("refusal and empty output return ErrInvalidResponse", func(t *testing.T) {
		cases := []struct {
			name string
			body string
		}{
			{"empty output", `{"output":[]}`},
			{"refusal block", `{"output":[{"type":"message","content":[{"type":"refusal","refusal":"unsafe"}]}]}`},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(tc.body))
				}))
				defer srv.Close()

				prov := NewOpenAIProvider("key", WithBaseURL(srv.URL))
				_, err := prov.Complete(context.Background(), core.LLMCompletionRequest{UserPrompt: "hi"})
				if !errors.Is(err, ErrInvalidResponse) {
					t.Errorf("expected ErrInvalidResponse, got %v", err)
				}
			})
		}
	})
}

func TestAnthropicProvider_Complete_StructuredAndText(t *testing.T) {
	t.Run("structured response via output_config.format", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("x-api-key") != "test-anthropic-key" {
				t.Errorf("unexpected x-api-key: %q", r.Header.Get("x-api-key"))
			}
			if r.Header.Get("anthropic-version") != anthropicAPIVersion {
				t.Errorf("unexpected anthropic-version: %q", r.Header.Get("anthropic-version"))
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("failed to decode body: %v", err)
			}
			if _, hasTools := body["tools"]; hasTools {
				t.Errorf("did not expect legacy tools workaround when using output_config.format")
			}
			if _, hasTC := body["tool_choice"]; hasTC {
				t.Errorf("did not expect legacy tool_choice workaround when using output_config.format")
			}
			outCfg, ok := body["output_config"].(map[string]any)
			if !ok {
				t.Fatalf("expected output_config in anthropic request, got %v", body)
			}
			fmtCfg, ok := outCfg["format"].(map[string]any)
			if !ok || fmtCfg["type"] != "json_schema" {
				t.Errorf("expected output_config.format.type == json_schema, got %v", outCfg["format"])
			}
			schemaObj, ok := fmtCfg["schema"].(map[string]any)
			if !ok || schemaObj["additionalProperties"] != false {
				t.Errorf("expected normalized schema with additionalProperties: false, got %v", fmtCfg["schema"])
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"content": [{"type": "text", "text": "{\"summary\":\"Anthropic Tailored\"}"}],
				"stop_reason": "end_turn",
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
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if _, hasOutCfg := body["output_config"]; hasOutCfg {
				t.Errorf("did not expect output_config for plain text request")
			}
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

	t.Run("refusal and empty content return ErrInvalidResponse", func(t *testing.T) {
		cases := []struct {
			name string
			body string
		}{
			{"empty content", `{"content": []}`},
			{"refusal stop reason", `{"stop_reason": "refusal", "content": [{"type": "text", "text": "cannot comply"}]}`},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(tc.body))
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
	})
}

func TestOllamaProvider_Complete_StructuredAndText(t *testing.T) {
	t.Run("structured response sets stream false and format schema", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/chat" {
				t.Errorf("expected /api/chat, got %q", r.URL.Path)
			}
			if r.Header.Get("Authorization") != "Bearer ollama-secret" {
				t.Errorf("expected optional bearer auth header, got %q", r.Header.Get("Authorization"))
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("failed to decode body: %v", err)
			}
			streamVal, hasStream := body["stream"]
			if !hasStream || streamVal != false {
				t.Errorf("expected explicit stream: false in Ollama /api/chat request, got present=%v val=%v", hasStream, streamVal)
			}
			fmtObj, ok := body["format"].(map[string]any)
			if !ok || fmtObj["type"] != "object" {
				t.Errorf("expected JSON Schema object in Ollama format field, got %v", body["format"])
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"model": "llama3.2",
				"message": {"role": "assistant", "content": "{\"summary\":\"Ollama Tailored\"}"},
				"done": true,
				"prompt_eval_count": 45,
				"eval_count": 15
			}`))
		}))
		defer srv.Close()

		prov := NewOllamaProvider(srv.URL, "ollama-secret", WithDefaultModel("llama3.2"))
		if prov.Name() != ProviderOllama {
			t.Errorf("expected %q, got %q", ProviderOllama, prov.Name())
		}
		if len(prov.Models()) != 1 || prov.Models()[0].Model != "llama3.2" {
			t.Errorf("unexpected ollama models: %+v", prov.Models())
		}

		resp, err := prov.Complete(context.Background(), core.LLMCompletionRequest{
			SystemPrompt:   "sys",
			UserPrompt:     "user",
			ResponseSchema: sampleTestSchema,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Text != `{"summary":"Ollama Tailored"}` {
			t.Errorf("unexpected text: %q", resp.Text)
		}
		if resp.Usage.PromptTokens != 45 || resp.Usage.CompletionTokens != 15 || resp.Usage.TotalTokens != 60 {
			t.Errorf("unexpected ollama token usage: %+v", resp.Usage)
		}
	})

	t.Run("plain text omits format and handles errors", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if _, hasFormat := body["format"]; hasFormat {
				t.Errorf("did not expect format for plain text Ollama request, got %v", body["format"])
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"message": {"role": "assistant", "content": "  Plain Ollama Output  "},
				"prompt_eval_count": 10,
				"eval_count": 5
			}`))
		}))
		defer srv.Close()

		prov := NewOllamaProvider(srv.URL, "")
		resp, err := prov.Complete(context.Background(), core.LLMCompletionRequest{UserPrompt: "hi"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Text != "Plain Ollama Output" || resp.Usage.TotalTokens != 15 {
			t.Errorf("unexpected response: %+v", resp)
		}
	})

	t.Run("empty content and error status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":""}}`))
		}))
		defer srv.Close()

		prov := NewOllamaProvider(srv.URL, "")
		if _, err := prov.Complete(context.Background(), core.LLMCompletionRequest{UserPrompt: "hi"}); !errors.Is(err, ErrInvalidResponse) {
			t.Errorf("expected ErrInvalidResponse, got %v", err)
		}
	})
}

func TestCompatProvider_Complete_StructuredAndText(t *testing.T) {
	t.Run("direct schema without prompt directive appends fallback", func(t *testing.T) {
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
	})

	t.Run("does not duplicate schema when <response_schema> already compiled", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"messages"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if strings.Contains(body.Messages[0].Content, "<json_output_schema>") {
				t.Errorf("expected CompatProvider not to duplicate schema when <response_schema> already present")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices": [{"message": {"content": "{\"summary\":\"ok\"}"}}]}`))
		}))
		defer srv.Close()

		prov := NewCompatProvider(srv.URL+"/v1", "")
		_, err := prov.Complete(context.Background(), core.LLMCompletionRequest{
			SystemPrompt:   "rules\n<response_schema>{}</response_schema>",
			UserPrompt:     "user",
			ResponseSchema: sampleTestSchema,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
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
		OllamaModel:     "llama3.1",
		CompatBaseURL:   "http://localhost:1234/v1",
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
