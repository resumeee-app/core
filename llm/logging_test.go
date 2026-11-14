package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/resumeee-app/resumeee-core"
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

func TestLLMLogging_ClientProvider(t *testing.T) {
	respBytes, err := json.Marshal(sampleResponse)
	if err != nil {
		t.Fatalf("failed to marshal sample response: %v", err)
	}
	sampleRespStr := string(respBytes)

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	debugDir := t.TempDir()

	client := &fakeTransportClient{
		name:         ProviderGemini,
		defaultModel: defaultGeminiModel,
		completeFn: func(_ context.Context, _ core.LLMCompletionRequest) (*core.LLMCompletionResponse, error) {
			return &core.LLMCompletionResponse{
				Text:       sampleRespStr,
				RawBody:    sampleRespStr,
				StatusCode: http.StatusOK,
				Usage:      core.TokenUsage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
			}, nil
		},
	}

	provider := NewClientProvider(client,
		WithLogger(logger),
		WithDebugPrompts(true, debugDir),
	)

	res, err := provider.GenerateTailoredResume(context.Background(), sampleRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil response")
	}

	records := parseLogs(t, logBuf.String())
	if len(records) < 2 {
		t.Fatalf("expected at least 2 log records (dispatch + receive), got %d", len(records))
	}
	if records[0].Provider != ProviderGemini || records[0].Operation != "GenerateTailoredResume" {
		t.Errorf("unexpected dispatch log record: %+v", records[0])
	}
	if records[1].StatusCode == nil || *records[1].StatusCode != http.StatusOK {
		t.Errorf("expected status_code 200 in response log, got %+v", records[1])
	}

	tenantDir := filepath.Join(debugDir, "default")
	entries, err := os.ReadDir(tenantDir)
	if err != nil {
		t.Fatalf("failed to read tenant debugDir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected debug markdown files to be written to tenantDir")
	}
	content, err := os.ReadFile(filepath.Join(tenantDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("failed to read debug file: %v", err)
	}
	if !strings.Contains(string(content), "tailor") {
		t.Errorf("expected debug file to contain sanitized operation name 'tailor'")
	}
}
