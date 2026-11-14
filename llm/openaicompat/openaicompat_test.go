package openaicompat_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/resumeee-app/resumeee-core/llm"
	"github.com/resumeee-app/resumeee-core/llm/openaicompat"
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
		},
		Required:             []string{"summary"},
		AdditionalProperties: boolPtr(false),
	}
}

func TestOpenAICompatClient_Complete_FallbackAndNoDoubleSchema(t *testing.T) {
	schema := sampleTestSchema()
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

	client := openaicompat.New(srv.URL+"/v1", "optional-key", openaicompat.WithDefaultModel("llama3.3"))
	if client.Name() != llm.ProviderCompat {
		t.Errorf("expected %q, got %q", llm.ProviderCompat, client.Name())
	}
	if client.SupportsStructuredOutput("") {
		t.Errorf("expected openaicompat.Client.SupportsStructuredOutput to return false")
	}

	// 1. Direct call without <response_schema> embeds <json_output_schema>
	resp, err := client.Complete(context.Background(), llm.Request{
		SystemPrompt:   "sys",
		UserPrompt:     "user",
		ResponseSchema: schema,
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
	_, err = client.Complete(context.Background(), llm.Request{
		SystemPrompt:   preCompiledSys,
		UserPrompt:     "user",
		ResponseSchema: schema,
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

func TestOpenAICompatClient_NilGuard(t *testing.T) {
	if _, err := (*openaicompat.Client)(nil).Complete(context.Background(), llm.Request{UserPrompt: "ping"}); !errors.Is(err, llm.ErrNoProviders) {
		t.Errorf("expected ErrNoProviders for nil openaicompat.Client, got %v", err)
	}
}
