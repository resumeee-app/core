package anthropic_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/resumeee-app/resumeee-core/llm"
	"github.com/resumeee-app/resumeee-core/llm/anthropic"
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

func TestAnthropicClient_Complete_OutputConfigAndModelGating(t *testing.T) {
	t.Run("model structured output capability gating", func(t *testing.T) {
		client := anthropic.New("test-anthropic-key")
		if client.DefaultModel() != "claude-sonnet-4-5" {
			t.Errorf("expected default Anthropic model claude-sonnet-4-5, got %q", client.DefaultModel())
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
			if got := client.SupportsStructuredOutput(tc.model); got != tc.want {
				t.Errorf("anthropic.Client.SupportsStructuredOutput(%q) = %v, want %v", tc.model, got, tc.want)
			}
		}
	})

	t.Run("structured response via output_config.format on Claude 4.5+", func(t *testing.T) {
		schema := sampleTestSchema()
		beforeSnapshot := cloneJSONSnapshot(t, schema)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/messages" {
				t.Errorf("expected /v1/messages, got %q", r.URL.Path)
			}
			if r.Header.Get("x-api-key") != "test-anthropic-key" {
				t.Errorf("unexpected x-api-key: %q", r.Header.Get("x-api-key"))
			}
			if r.Header.Get("anthropic-version") != "2023-06-01" {
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

		client := anthropic.New("test-anthropic-key", anthropic.WithBaseURL(srv.URL))
		resp, err := client.Complete(context.Background(), llm.Request{
			SystemPrompt:   "sys",
			UserPrompt:     "user",
			ResponseSchema: schema,
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
		if after := cloneJSONSnapshot(t, schema); after != beforeSnapshot {
			t.Errorf("anthropic.Client mutated caller schema!\nbefore: %s\nafter:  %s", beforeSnapshot, after)
		}
	})

	t.Run("legacy Claude 3.5 Haiku falls back without output_config or double schema append", func(t *testing.T) {
		schema := sampleTestSchema()
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

		client := anthropic.New("key", anthropic.WithBaseURL(srv.URL))
		resp, err := client.Complete(context.Background(), llm.Request{
			Model:          "claude-3-5-haiku-20241022",
			SystemPrompt:   "Base instructions",
			UserPrompt:     "user",
			ResponseSchema: schema,
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

		_, err = client.Complete(context.Background(), llm.Request{
			Model:          "claude-3-5-haiku-20241022",
			SystemPrompt:   capturedSystem,
			UserPrompt:     "user",
			ResponseSchema: schema,
		})
		if err != nil {
			t.Fatalf("unexpected error on pre-appended prompt: %v", err)
		}
		if strings.Count(capturedSystem, "<response_schema>") != 1 {
			t.Errorf("expected zero double-appending of <response_schema>, got count=%d", strings.Count(capturedSystem, "<response_schema>"))
		}
	})

	t.Run("nil client and empty api key guards", func(t *testing.T) {
		req := llm.Request{UserPrompt: "ping"}
		if _, err := (*anthropic.Client)(nil).Complete(context.Background(), req); !errors.Is(err, llm.ErrUnauthorized) {
			t.Errorf("expected ErrUnauthorized for nil anthropic.Client, got %v", err)
		}
		if _, err := anthropic.New("").Complete(context.Background(), req); !errors.Is(err, llm.ErrUnauthorized) {
			t.Errorf("expected ErrUnauthorized for empty Anthropic key, got %v", err)
		}
	})

	t.Run("plain text, refusal, and error handling", func(t *testing.T) {
		schema := sampleTestSchema()
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
				wantErr:    llm.ErrInvalidResponse,
			},
			{
				name:       "stop_reason max_tokens on structured output",
				statusCode: http.StatusOK,
				body:       `{"stop_reason":"max_tokens","content":[{"type":"text","text":"{\"summary\":\"trunc"}]}`,
				withSchema: true,
				wantErr:    llm.ErrInvalidResponse,
			},
			{
				name:       "empty content",
				statusCode: http.StatusOK,
				body:       `{"stop_reason":"end_turn","content":[]}`,
				withSchema: false,
				wantErr:    llm.ErrInvalidResponse,
			},
			{
				name:       "malformed json",
				statusCode: http.StatusOK,
				body:       `{invalid`,
				withSchema: false,
				wantErr:    llm.ErrInvalidResponse,
			},
			{
				name:       "401 unauthorized",
				statusCode: http.StatusUnauthorized,
				body:       `{"error":{"message":"invalid x-api-key"}}`,
				withSchema: false,
				wantErr:    llm.ErrUnauthorized,
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tc.statusCode)
					_, _ = w.Write([]byte(tc.body))
				}))
				defer srv.Close()

				client := anthropic.New("key", anthropic.WithBaseURL(srv.URL))
				var reqSchema *llm.Schema
				if tc.withSchema {
					reqSchema = schema
				}
				_, err := client.Complete(context.Background(), llm.Request{
					UserPrompt:     "user",
					ResponseSchema: reqSchema,
				})
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("expected %v, got %v", tc.wantErr, err)
				}
			})
		}
	})
}
