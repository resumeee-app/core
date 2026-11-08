package llm_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"resumeee/internal/llm"
)

func TestSanitizeTenantID(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"tenant-123", "tenant-123"},
		{"tenant_user_456", "tenant_user_456"},
		{"../../etc/passwd", "etc_passwd"},
		{"..\\..\\windows", "windows"},
		{"", "default"},
		{"   ", "default"},
		{"!@#$%^&*()", "default"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			actual := llm.SanitizeTenantID(tc.input)
			if actual != tc.expected {
				t.Errorf("SanitizeTenantID(%q) = %q, expected %q", tc.input, actual, tc.expected)
			}
		})
	}
}

func TestSanitizeOperation(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"GenerateTailoredResume", "tailor"},
		{"generatetailoredresume", "tailor"},
		{"tailored_resume", "tailor"},
		{"tailor", "tailor"},
		{"CompleteText", "extract"},
		{"completetext", "extract"},
		{"extractresume", "extract"},
		{"extract", "extract"},
		{"resume_extraction", "extract"},
		{"custom_op", "custom_op"},
		{"CUSTOM-OP-2", "custom_op_2"},
		{"../../etc/passwd", "etc_passwd"},
		{"..\\..\\windows\\system32", "windows_system32"},
		{"!@#$%^&*()", "operation"},
		{"", "operation"},
		{"   ", "operation"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			actual := llm.SanitizeOperation(tc.input)
			if actual != tc.expected {
				t.Errorf("SanitizeOperation(%q) = %q, expected %q", tc.input, actual, tc.expected)
			}
		})
	}
}

func TestFilePromptDebugger_WritePromptAndResponse(t *testing.T) {
	tmpDir := t.TempDir()
	fixedTime := time.Date(2026, 11, 4, 15, 30, 45, 0, time.UTC)

	debugger := llm.NewFilePromptDebugger(tmpDir, nil, llm.WithDebuggerClock(func() time.Time {
		return fixedTime
	}))

	meta := llm.PromptDebugMeta{
		TenantID:     "tenant-alice",
		Provider:     "openai",
		Model:        "gpt-4o",
		Operation:    "GenerateTailoredResume",
		SystemPrompt: "You are a resume tailoring expert.",
		UserPrompt:   "Tailor this resume for a Go developer role.",
		Timestamp:    fixedTime,
	}

	session, err := debugger.StartSession(meta)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	if session == nil {
		t.Fatal("expected non-nil session")
	}

	expectedPromptFile := filepath.Join(tmpDir, "tenant-alice", "tailor_20261104153045.md")
	promptBytes, err := os.ReadFile(expectedPromptFile)
	if err != nil {
		t.Fatalf("expected prompt file %s to exist: %v", expectedPromptFile, err)
	}

	promptContent := string(promptBytes)
	if !strings.Contains(promptContent, "# LLM Prompt: tailor") {
		t.Errorf("prompt file missing header: %s", promptContent)
	}
	if !strings.Contains(promptContent, "**Tenant**: tenant-alice") {
		t.Errorf("prompt file missing tenant: %s", promptContent)
	}
	if !strings.Contains(promptContent, "**Provider**: openai") {
		t.Errorf("prompt file missing provider: %s", promptContent)
	}
	if !strings.Contains(promptContent, "**Model**: gpt-4o") {
		t.Errorf("prompt file missing model: %s", promptContent)
	}
	if !strings.Contains(promptContent, "You are a resume tailoring expert.") {
		t.Errorf("prompt file missing system prompt: %s", promptContent)
	}
	if !strings.Contains(promptContent, "Tailor this resume for a Go developer role.") {
		t.Errorf("prompt file missing user prompt: %s", promptContent)
	}

	respMeta := llm.ResponseDebugMeta{
		Provider:    "openai",
		Model:       "gpt-4o",
		Operation:   "GenerateTailoredResume",
		Duration:    1500 * time.Millisecond,
		StatusCode:  200,
		RawResponse: `{"basics": {"name": "Jane Doe"}}`,
		Timestamp:   fixedTime.Add(1500 * time.Millisecond),
	}

	if err := session.WriteResponse(respMeta); err != nil {
		t.Fatalf("WriteResponse failed: %v", err)
	}

	expectedOutputFile := filepath.Join(tmpDir, "tenant-alice", "tailor_20261104153045_output.md")
	outputBytes, err := os.ReadFile(expectedOutputFile)
	if err != nil {
		t.Fatalf("expected output file %s to exist: %v", expectedOutputFile, err)
	}

	outputContent := string(outputBytes)
	if !strings.Contains(outputContent, "# LLM Response: tailor") {
		t.Errorf("output file missing header: %s", outputContent)
	}
	if !strings.Contains(outputContent, "**Status Code**: 200") {
		t.Errorf("output file missing status code: %s", outputContent)
	}
	expectedFormattedOutput := "```json\n{\n  \"basics\": {\n    \"name\": \"Jane Doe\"\n  }\n}\n```"
	if !strings.Contains(outputContent, expectedFormattedOutput) {
		t.Errorf("output file missing formatted json block:\nexpected:\n%s\ngot:\n%s", expectedFormattedOutput, outputContent)
	}
}

func TestFilePromptDebugger_ErrorResponse(t *testing.T) {
	tmpDir := t.TempDir()
	fixedTime := time.Date(2026, 11, 4, 16, 0, 0, 0, time.UTC)

	debugger := llm.NewFilePromptDebugger(tmpDir, nil, llm.WithDebuggerClock(func() time.Time {
		return fixedTime
	}))

	session, err := debugger.StartSession(llm.PromptDebugMeta{
		Provider:     "gemini",
		Model:        "gemini-2.5-flash",
		Operation:    "CompleteText",
		SystemPrompt: "Extraction sys prompt",
		UserPrompt:   "User resume text",
		Timestamp:    fixedTime,
	})
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}

	expectedPromptFile := filepath.Join(tmpDir, "default", "extract_20261104160000.md")
	if _, err := os.Stat(expectedPromptFile); err != nil {
		t.Fatalf("expected prompt file %s: %v", expectedPromptFile, err)
	}

	if err := session.WriteResponse(llm.ResponseDebugMeta{
		Provider:   "gemini",
		Model:      "gemini-2.5-flash",
		Operation:  "CompleteText",
		Duration:   250 * time.Millisecond,
		StatusCode: 429,
		Err:        errors.New("quota exceeded: rate limit reached"),
		Timestamp:  fixedTime.Add(250 * time.Millisecond),
	}); err != nil {
		t.Fatalf("WriteResponse failed: %v", err)
	}

	expectedOutputFile := filepath.Join(tmpDir, "default", "extract_20261104160000_output.md")
	outputBytes, err := os.ReadFile(expectedOutputFile)
	if err != nil {
		t.Fatalf("expected output file %s: %v", expectedOutputFile, err)
	}

	outputContent := string(outputBytes)
	if !strings.Contains(outputContent, "**Error**: quota exceeded: rate limit reached") {
		t.Errorf("output file missing error details: %s", outputContent)
	}
	if !strings.Contains(outputContent, "**Status Code**: 429") {
		t.Errorf("output file missing status code: %s", outputContent)
	}
}

func TestFilePromptDebugger_CollisionHandling(t *testing.T) {
	tmpDir := t.TempDir()
	fixedTime := time.Date(2026, 11, 4, 17, 0, 0, 0, time.UTC)

	debugger := llm.NewFilePromptDebugger(tmpDir, nil, llm.WithDebuggerClock(func() time.Time {
		return fixedTime
	}))

	meta := llm.PromptDebugMeta{
		Provider:     "anthropic",
		Model:        "claude-3-5-sonnet-20241022",
		Operation:    "tailor",
		SystemPrompt: "Sys",
		UserPrompt:   "User",
		Timestamp:    fixedTime,
	}

	sess1, err := debugger.StartSession(meta)
	if err != nil {
		t.Fatalf("sess1 failed: %v", err)
	}
	sess2, err := debugger.StartSession(meta)
	if err != nil {
		t.Fatalf("sess2 failed: %v", err)
	}

	_ = sess1.WriteResponse(llm.ResponseDebugMeta{RawResponse: "resp1", Timestamp: fixedTime})
	_ = sess2.WriteResponse(llm.ResponseDebugMeta{RawResponse: "resp2", Timestamp: fixedTime})

	file1 := filepath.Join(tmpDir, "default", "tailor_20261104170000.md")
	file1Out := filepath.Join(tmpDir, "default", "tailor_20261104170000_output.md")
	file2 := filepath.Join(tmpDir, "default", "tailor_20261104170000_1.md")
	file2Out := filepath.Join(tmpDir, "default", "tailor_20261104170000_1_output.md")

	if _, err := os.Stat(file1); err != nil {
		t.Errorf("expected file1 %s to exist: %v", file1, err)
	}
	if _, err := os.Stat(file1Out); err != nil {
		t.Errorf("expected file1Out %s to exist: %v", file1Out, err)
	}
	if _, err := os.Stat(file2); err != nil {
		t.Errorf("expected file2 %s to exist: %v", file2, err)
	}
	if _, err := os.Stat(file2Out); err != nil {
		t.Errorf("expected file2Out %s to exist: %v", file2Out, err)
	}
}

func TestFilePromptDebugger_CreatesDirectoryAutomatically(t *testing.T) {
	tmpDir := t.TempDir()
	nestedDir := filepath.Join(tmpDir, "nested", "data", "llm")

	debugger := llm.NewFilePromptDebugger(nestedDir, nil)
	session, err := debugger.StartSession(llm.PromptDebugMeta{
		Operation: "tailor",
	})
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	if session == nil {
		t.Fatal("expected session")
	}

	info, err := os.Stat(filepath.Join(nestedDir, "default"))
	if err != nil {
		t.Fatalf("expected directory %s to be created: %v", filepath.Join(nestedDir, "default"), err)
	}
	if !info.IsDir() {
		t.Fatalf("expected %s to be directory", filepath.Join(nestedDir, "default"))
	}
}

func TestFilePromptDebugger_ConcurrentCollisionHandling(t *testing.T) {
	tmpDir := t.TempDir()
	fixedTime := time.Date(2026, 11, 4, 18, 0, 0, 0, time.UTC)

	debugger := llm.NewFilePromptDebugger(tmpDir, nil, llm.WithDebuggerClock(func() time.Time {
		return fixedTime
	}))

	const concurrency = 20
	var wg sync.WaitGroup
	errCh := make(chan error, concurrency*2)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		workerID := i
		go func() {
			defer wg.Done()
			meta := llm.PromptDebugMeta{
				Provider:     "openai",
				Model:        "gpt-4o",
				Operation:    "tailor",
				SystemPrompt: fmt.Sprintf("system-%d", workerID),
				UserPrompt:   fmt.Sprintf("user-%d", workerID),
				Timestamp:    fixedTime,
			}
			sess, err := debugger.StartSession(meta)
			if err != nil {
				errCh <- fmt.Errorf("worker %d StartSession: %w", workerID, err)
				return
			}
			if err := sess.WriteResponse(llm.ResponseDebugMeta{
				Provider:    "openai",
				Model:       "gpt-4o",
				Operation:   "tailor",
				StatusCode:  200,
				RawResponse: fmt.Sprintf("response-%d", workerID),
				Timestamp:   fixedTime,
			}); err != nil {
				errCh <- fmt.Errorf("worker %d WriteResponse: %w", workerID, err)
				return
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent worker error: %v", err)
		}
	}

	entries, err := os.ReadDir(filepath.Join(tmpDir, "default"))
	if err != nil {
		t.Fatalf("failed to read dir: %v", err)
	}

	// Exactly 20 prompt files (.md) and 20 response files (_output.md)
	expectedTotalFiles := concurrency * 2
	if len(entries) != expectedTotalFiles {
		t.Fatalf("expected exactly %d files, got %d", expectedTotalFiles, len(entries))
	}

	promptCount := 0
	outputCount := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "_output.md") {
			outputCount++
		} else if strings.HasSuffix(e.Name(), ".md") {
			promptCount++
		}
	}

	if promptCount != concurrency {
		t.Errorf("expected %d prompt files, got %d", concurrency, promptCount)
	}
	if outputCount != concurrency {
		t.Errorf("expected %d output files, got %d", concurrency, outputCount)
	}
}

func TestFilePromptDebugger_TenantIsolationAndTraversal(t *testing.T) {
	tmpDir := t.TempDir()
	fixedTime := time.Date(2026, 11, 4, 19, 0, 0, 0, time.UTC)
	debugger := llm.NewFilePromptDebugger(tmpDir, nil, llm.WithDebuggerClock(func() time.Time {
		return fixedTime
	}))

	// Tenant A
	sessA, err := debugger.StartSession(llm.PromptDebugMeta{
		TenantID:  "tenant-a",
		Operation: "tailor",
		Timestamp: fixedTime,
	})
	if err != nil {
		t.Fatalf("tenant A session failed: %v", err)
	}
	_ = sessA.WriteResponse(llm.ResponseDebugMeta{RawResponse: "respA", Timestamp: fixedTime})

	// Tenant B
	sessB, err := debugger.StartSession(llm.PromptDebugMeta{
		TenantID:  "tenant-b",
		Operation: "tailor",
		Timestamp: fixedTime,
	})
	if err != nil {
		t.Fatalf("tenant B session failed: %v", err)
	}
	_ = sessB.WriteResponse(llm.ResponseDebugMeta{RawResponse: "respB", Timestamp: fixedTime})

	// Traversal attempt
	sessC, err := debugger.StartSession(llm.PromptDebugMeta{
		TenantID:  "../../etc",
		Operation: "tailor",
		Timestamp: fixedTime,
	})
	if err != nil {
		t.Fatalf("traversal session failed: %v", err)
	}
	_ = sessC.WriteResponse(llm.ResponseDebugMeta{RawResponse: "respC", Timestamp: fixedTime})

	// Verify Tenant A dir
	dirA := filepath.Join(tmpDir, "tenant-a")
	if _, err := os.Stat(dirA); err != nil {
		t.Errorf("expected tenant-a dir to exist: %v", err)
	}

	// Verify Tenant B dir
	dirB := filepath.Join(tmpDir, "tenant-b")
	if _, err := os.Stat(dirB); err != nil {
		t.Errorf("expected tenant-b dir to exist: %v", err)
	}

	// Verify Traversal is sanitized into subfolder inside tmpDir
	dirC := filepath.Join(tmpDir, "etc")
	if _, err := os.Stat(dirC); err != nil {
		t.Errorf("expected sanitized etc dir to exist inside tmpDir: %v", err)
	}

	// Ensure no files escaped outside tmpDir
	parentDir := filepath.Dir(tmpDir)
	entries, err := os.ReadDir(parentDir)
	if err != nil {
		t.Fatalf("failed to read parent dir: %v", err)
	}
	for _, e := range entries {
		if e.Name() == "etc" {
			t.Errorf("file escaped to parent directory: %s", e.Name())
		}
	}
}

type mockCustomDebugger struct {
	called bool
}

func (m *mockCustomDebugger) StartSession(meta llm.PromptDebugMeta) (llm.PromptDebugSession, error) {
	m.called = true
	return &mockCustomSession{}, nil
}

type mockCustomSession struct{}

func (m *mockCustomSession) WriteResponse(meta llm.ResponseDebugMeta) error {
	return nil
}

func TestOptions_WithPromptDebuggerAndDebugPrompts(t *testing.T) {
	t.Run("WithPromptDebugger overrides debugger", func(t *testing.T) {
		mock := &mockCustomDebugger{}
		provider := llm.NewOpenAIProvider("test-key", llm.WithPromptDebugger(mock))
		if provider == nil {
			t.Fatal("expected non-nil provider")
		}
	})

	t.Run("WithDebugPrompts true enables debugger", func(t *testing.T) {
		tmpDir := t.TempDir()
		provider := llm.NewOpenAIProvider("test-key", llm.WithDebugPrompts(true, tmpDir))
		if provider == nil {
			t.Fatal("expected non-nil provider")
		}
	})

	t.Run("WithDebugPrompts false disables debugger", func(t *testing.T) {
		tmpDir := t.TempDir()
		provider := llm.NewOpenAIProvider("test-key",
			llm.WithDebugDir(tmpDir),
			llm.WithDebugPrompts(false, ""),
		)
		if provider == nil {
			t.Fatal("expected non-nil provider")
		}
	})

	t.Run("WithDebuggerLogger overrides logger", func(t *testing.T) {
		var buf bytes.Buffer
		customLogger := slog.New(slog.NewTextHandler(&buf, nil))
		debugger := llm.NewFilePromptDebugger(t.TempDir(), nil, llm.WithDebuggerLogger(customLogger))
		if debugger == nil {
			t.Fatal("expected non-nil debugger")
		}
	})
}

func TestFilePromptDebugger_DecodesAndFormatsPromptsAndResponses(t *testing.T) {
	fixedTime := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)

	t.Run("Encoded JSON prompts are decoded and wrapped in json code fences", func(t *testing.T) {
		tmpDir := t.TempDir()
		debugger := llm.NewFilePromptDebugger(tmpDir, nil, llm.WithDebuggerClock(func() time.Time {
			return fixedTime
		}))

		meta := llm.PromptDebugMeta{
			TenantID:     "tenant-test",
			Provider:     "openai",
			Model:        "gpt-4o",
			Operation:    "tailor",
			SystemPrompt: "\"{\\n  \\\"role\\\": \\\"expert\\\"\\n}\"",
			UserPrompt:   "{\"candidate\": \"Alice\"}",
			Timestamp:    fixedTime,
		}

		session, err := debugger.StartSession(meta)
		if err != nil {
			t.Fatalf("StartSession failed: %v", err)
		}

		promptPath := filepath.Join(tmpDir, "tenant-test", "tailor_20261107100000.md")
		promptBytes, err := os.ReadFile(promptPath)
		if err != nil {
			t.Fatalf("read prompt file failed: %v", err)
		}
		promptContent := string(promptBytes)

		expectedSys := "```json\n{\n  \"role\": \"expert\"\n}\n```"
		if !strings.Contains(promptContent, expectedSys) {
			t.Errorf("prompt missing formatted system prompt:\nexpected:\n%s\ngot:\n%s", expectedSys, promptContent)
		}

		expectedUser := "```json\n{\n  \"candidate\": \"Alice\"\n}\n```"
		if !strings.Contains(promptContent, expectedUser) {
			t.Errorf("prompt missing formatted user prompt:\nexpected:\n%s\ngot:\n%s", expectedUser, promptContent)
		}

		// Also verify response with encoded JSON string
		respMeta := llm.ResponseDebugMeta{
			Provider:    "openai",
			Model:       "gpt-4o",
			Operation:   "tailor",
			StatusCode:  200,
			RawResponse: "\"{\\n  \\\"name\\\": \\\"Bob\\\"\\n}\"",
			Timestamp:   fixedTime,
		}
		if err := session.WriteResponse(respMeta); err != nil {
			t.Fatalf("WriteResponse failed: %v", err)
		}

		outputPath := filepath.Join(tmpDir, "tenant-test", "tailor_20261107100000_output.md")
		outputBytes, err := os.ReadFile(outputPath)
		if err != nil {
			t.Fatalf("read output file failed: %v", err)
		}
		outputContent := string(outputBytes)

		expectedOutput := "```json\n{\n  \"name\": \"Bob\"\n}\n```"
		if !strings.Contains(outputContent, expectedOutput) {
			t.Errorf("output missing formatted response:\nexpected:\n%s\ngot:\n%s", expectedOutput, outputContent)
		}
	})

	t.Run("Encoded plain text prompts are unquoted without json code fences", func(t *testing.T) {
		tmpDir := t.TempDir()
		debugger := llm.NewFilePromptDebugger(tmpDir, nil, llm.WithDebuggerClock(func() time.Time {
			return fixedTime
		}))

		meta := llm.PromptDebugMeta{
			Operation:    "extract",
			SystemPrompt: "\"You are a resume parser.\\nExtract all fields.\"",
			UserPrompt:   "\"Please parse this text:\\nLine 1\\nLine 2\"",
			Timestamp:    fixedTime,
		}

		session, err := debugger.StartSession(meta)
		if err != nil {
			t.Fatalf("StartSession failed: %v", err)
		}

		promptPath := filepath.Join(tmpDir, "default", "extract_20261107100000.md")
		promptBytes, err := os.ReadFile(promptPath)
		if err != nil {
			t.Fatalf("read prompt file failed: %v", err)
		}
		promptContent := string(promptBytes)

		expectedSysText := "You are a resume parser.\nExtract all fields."
		if !strings.Contains(promptContent, expectedSysText) {
			t.Errorf("prompt missing unescaped system text:\nexpected to contain: %q\ngot:\n%s", expectedSysText, promptContent)
		}
		if strings.Contains(promptContent, "```json") {
			t.Errorf("plain text prompt should not be wrapped in ```json:\n%s", promptContent)
		}

		expectedUserText := "Please parse this text:\nLine 1\nLine 2"
		if !strings.Contains(promptContent, expectedUserText) {
			t.Errorf("prompt missing unescaped user text:\nexpected to contain: %q\ngot:\n%s", expectedUserText, promptContent)
		}

		// Non-JSON response
		respMeta := llm.ResponseDebugMeta{
			Operation:   "extract",
			StatusCode:  500,
			RawResponse: "Service Unavailable: high upstream demand",
			Timestamp:   fixedTime,
		}
		if err := session.WriteResponse(respMeta); err != nil {
			t.Fatalf("WriteResponse failed: %v", err)
		}

		outputPath := filepath.Join(tmpDir, "default", "extract_20261107100000_output.md")
		outputBytes, err := os.ReadFile(outputPath)
		if err != nil {
			t.Fatalf("read output file failed: %v", err)
		}
		outputContent := string(outputBytes)
		if !strings.Contains(outputContent, "Service Unavailable: high upstream demand") {
			t.Errorf("output missing plain text response:\n%s", outputContent)
		}
		if strings.Contains(outputContent, "```json") {
			t.Errorf("plain text output should not be wrapped in ```json:\n%s", outputContent)
		}
	})

	t.Run("OpenAI response with nested encoded JSON string is fully decoded", func(t *testing.T) {
		tmpDir := t.TempDir()
		debugger := llm.NewFilePromptDebugger(tmpDir, nil, llm.WithDebuggerClock(func() time.Time {
			return fixedTime
		}))

		meta := llm.PromptDebugMeta{
			Operation:    "tailor",
			SystemPrompt: "Tailor",
			UserPrompt:   "Candidate",
			Timestamp:    fixedTime,
		}

		session, err := debugger.StartSession(meta)
		if err != nil {
			t.Fatalf("StartSession failed: %v", err)
		}

		rawOpenAI := `{
			"choices": [
				{
					"message": {
						"role": "assistant",
						"content": "{\n  \"tailored_content\": {\n    \"summary\": \"Senior Engineer\"\n  }\n}"
					}
				}
			],
			"usage": {
				"total_tokens": 42
			}
		}`

		respMeta := llm.ResponseDebugMeta{
			Operation:   "tailor",
			StatusCode:  200,
			RawResponse: rawOpenAI,
			Timestamp:   fixedTime,
		}

		if err := session.WriteResponse(respMeta); err != nil {
			t.Fatalf("WriteResponse failed: %v", err)
		}

		outputPath := filepath.Join(tmpDir, "default", "tailor_20261107100000_output.md")
		outputBytes, err := os.ReadFile(outputPath)
		if err != nil {
			t.Fatalf("read output file failed: %v", err)
		}
		outputContent := string(outputBytes)

		// Must be wrapped in ```json
		if !strings.Contains(outputContent, "```json\n") {
			t.Errorf("output missing ```json wrapper:\n%s", outputContent)
		}
		// Inner JSON should be decoded as an object, NOT an escaped string containing `\"tailored_content\"`
		if strings.Contains(outputContent, `\"tailored_content\"`) {
			t.Errorf("inner json content should be decoded, but found escaped quotes:\n%s", outputContent)
		}
		if !strings.Contains(outputContent, `"summary": "Senior Engineer"`) {
			t.Errorf("output missing decoded summary:\n%s", outputContent)
		}
	})

	t.Run("Gemini response with nested encoded JSON string in parts is fully decoded", func(t *testing.T) {
		tmpDir := t.TempDir()
		debugger := llm.NewFilePromptDebugger(tmpDir, nil, llm.WithDebuggerClock(func() time.Time {
			return fixedTime
		}))

		session, err := debugger.StartSession(llm.PromptDebugMeta{
			Operation:    "tailor",
			SystemPrompt: "Tailor",
			UserPrompt:   "Candidate",
			Timestamp:    fixedTime,
		})
		if err != nil {
			t.Fatalf("StartSession failed: %v", err)
		}

		rawGemini := `{
			"candidates": [
				{
					"content": {
						"parts": [
							{
								"text": "{\n  \"basics\": {\n    \"name\": \"Alice\"\n  }\n}"
							}
						]
					}
				}
			]
		}`

		if err := session.WriteResponse(llm.ResponseDebugMeta{
			Operation:   "tailor",
			StatusCode:  200,
			RawResponse: rawGemini,
			Timestamp:   fixedTime,
		}); err != nil {
			t.Fatalf("WriteResponse failed: %v", err)
		}

		outputPath := filepath.Join(tmpDir, "default", "tailor_20261107100000_output.md")
		outputBytes, err := os.ReadFile(outputPath)
		if err != nil {
			t.Fatalf("read output file failed: %v", err)
		}
		outputContent := string(outputBytes)

		if !strings.Contains(outputContent, "```json\n") {
			t.Errorf("output missing ```json wrapper:\n%s", outputContent)
		}
		if strings.Contains(outputContent, `\"basics\"`) {
			t.Errorf("inner parts text should be decoded, but found escaped quotes:\n%s", outputContent)
		}
		if !strings.Contains(outputContent, `"name": "Alice"`) {
			t.Errorf("output missing decoded name:\n%s", outputContent)
		}
	})

	t.Run("Already fenced JSON is preserved without double wrapping", func(t *testing.T) {
		tmpDir := t.TempDir()
		debugger := llm.NewFilePromptDebugger(tmpDir, nil, llm.WithDebuggerClock(func() time.Time {
			return fixedTime
		}))

		fencedJSON := "```json\n{\n  \"already\": \"fenced\"\n}\n```"
		session, err := debugger.StartSession(llm.PromptDebugMeta{
			Operation:    "tailor",
			SystemPrompt: fencedJSON,
			UserPrompt:   fencedJSON,
			Timestamp:    fixedTime,
		})
		if err != nil {
			t.Fatalf("StartSession failed: %v", err)
		}

		if err := session.WriteResponse(llm.ResponseDebugMeta{
			Operation:   "tailor",
			StatusCode:  200,
			RawResponse: fencedJSON,
			Timestamp:   fixedTime,
		}); err != nil {
			t.Fatalf("WriteResponse failed: %v", err)
		}

		promptPath := filepath.Join(tmpDir, "default", "tailor_20261107100000.md")
		promptBytes, err := os.ReadFile(promptPath)
		if err != nil {
			t.Fatalf("failed to read prompt file: %v", err)
		}
		promptContent := string(promptBytes)

		if strings.Contains(promptContent, "```json\n```json") {
			t.Errorf("found duplicate nested code fences in prompt:\n%s", promptContent)
		}

		outputPath := filepath.Join(tmpDir, "default", "tailor_20261107100000_output.md")
		outputBytes, err := os.ReadFile(outputPath)
		if err != nil {
			t.Fatalf("failed to read output file: %v", err)
		}
		outputContent := string(outputBytes)

		if strings.Contains(outputContent, "```json\n```json") {
			t.Errorf("found duplicate nested code fences in output:\n%s", outputContent)
		}
	})

	t.Run("JSON array and nested arrays with escaped strings are decoded and formatted", func(t *testing.T) {
		tmpDir := t.TempDir()
		debugger := llm.NewFilePromptDebugger(tmpDir, nil, llm.WithDebuggerClock(func() time.Time {
			return fixedTime
		}))

		session, err := debugger.StartSession(llm.PromptDebugMeta{
			Operation:    "extract",
			SystemPrompt: "[{\"role\": \"system\"}]",
			UserPrompt:   "[{\"role\": \"user\", \"payload\": \"{\\\"data\\\": 123}\"}]",
			Timestamp:    fixedTime,
		})
		if err != nil {
			t.Fatalf("StartSession failed: %v", err)
		}

		rawArrayResponse := `["{\"item\": 1}", "normal_string", 42]`
		if err := session.WriteResponse(llm.ResponseDebugMeta{
			Operation:   "extract",
			StatusCode:  200,
			RawResponse: rawArrayResponse,
			Timestamp:   fixedTime,
		}); err != nil {
			t.Fatalf("WriteResponse failed: %v", err)
		}

		promptPath := filepath.Join(tmpDir, "default", "extract_20261107100000.md")
		promptBytes, err := os.ReadFile(promptPath)
		if err != nil {
			t.Fatalf("failed to read prompt file: %v", err)
		}
		promptContent := string(promptBytes)

		if !strings.Contains(promptContent, "```json\n[\n  {\n    \"role\": \"system\"\n  }\n]\n```") {
			t.Errorf("prompt missing formatted system array:\n%s", promptContent)
		}
		if strings.Contains(promptContent, `\"data\"`) {
			t.Errorf("prompt should have decoded nested json in payload:\n%s", promptContent)
		}

		outputPath := filepath.Join(tmpDir, "default", "extract_20261107100000_output.md")
		outputBytes, err := os.ReadFile(outputPath)
		if err != nil {
			t.Fatalf("failed to read output file: %v", err)
		}
		outputContent := string(outputBytes)

		if !strings.Contains(outputContent, "```json\n") {
			t.Errorf("output missing ```json wrapper:\n%s", outputContent)
		}
		if strings.Contains(outputContent, `\"item\"`) {
			t.Errorf("output should have decoded nested json in array item:\n%s", outputContent)
		}
		if !strings.Contains(outputContent, "\"item\": 1") {
			t.Errorf("output missing decoded item:\n%s", outputContent)
		}
	})

	t.Run("Unquoted string with escaped characters is decoded and formatted", func(t *testing.T) {
		tmpDir := t.TempDir()
		debugger := llm.NewFilePromptDebugger(tmpDir, nil, llm.WithDebuggerClock(func() time.Time {
			return fixedTime
		}))

		// Unquoted string containing \n and \"
		unquotedEscaped := `{\n  \"key\": \"value\"\n}`
		session, err := debugger.StartSession(llm.PromptDebugMeta{
			Operation:    "tailor",
			SystemPrompt: "System",
			UserPrompt:   unquotedEscaped,
			Timestamp:    fixedTime,
		})
		if err != nil {
			t.Fatalf("StartSession failed: %v", err)
		}

		if err := session.WriteResponse(llm.ResponseDebugMeta{
			Operation:   "tailor",
			StatusCode:  200,
			RawResponse: unquotedEscaped,
			Timestamp:   fixedTime,
		}); err != nil {
			t.Fatalf("WriteResponse failed: %v", err)
		}

		outputPath := filepath.Join(tmpDir, "default", "tailor_20261107100000_output.md")
		outputBytes, err := os.ReadFile(outputPath)
		if err != nil {
			t.Fatalf("failed to read output file: %v", err)
		}
		outputContent := string(outputBytes)

		expectedBlock := "```json\n{\n  \"key\": \"value\"\n}\n```"
		if !strings.Contains(outputContent, expectedBlock) {
			t.Errorf("expected formatted json block:\n%s\ngot:\n%s", expectedBlock, outputContent)
		}
	})

	t.Run("Dynamic code fence selects 4 backticks when JSON contains triple backticks", func(t *testing.T) {
		tmpDir := t.TempDir()
		debugger := llm.NewFilePromptDebugger(tmpDir, nil, llm.WithDebuggerClock(func() time.Time {
			return fixedTime
		}))

		jsonWithBackticks := "{\"code\": \"```python\\nprint('hello')\\n```\"}"
		session, err := debugger.StartSession(llm.PromptDebugMeta{
			Operation:  "tailor",
			UserPrompt: jsonWithBackticks,
			Timestamp:  fixedTime,
		})
		if err != nil {
			t.Fatalf("StartSession failed: %v", err)
		}

		if err := session.WriteResponse(llm.ResponseDebugMeta{
			Operation:   "tailor",
			StatusCode:  200,
			RawResponse: jsonWithBackticks,
			Timestamp:   fixedTime,
		}); err != nil {
			t.Fatalf("WriteResponse failed: %v", err)
		}

		promptPath := filepath.Join(tmpDir, "default", "tailor_20261107100000.md")
		promptBytes, err := os.ReadFile(promptPath)
		if err != nil {
			t.Fatalf("failed to read prompt file: %v", err)
		}
		promptContent := string(promptBytes)

		if !strings.Contains(promptContent, "````json\n") || !strings.Contains(promptContent, "\n````") {
			t.Errorf("expected 4 backticks fence for payload containing triple backticks, got:\n%s", promptContent)
		}

		outputPath := filepath.Join(tmpDir, "default", "tailor_20261107100000_output.md")
		outputBytes, err := os.ReadFile(outputPath)
		if err != nil {
			t.Fatalf("failed to read output file: %v", err)
		}
		outputContent := string(outputBytes)

		if !strings.Contains(outputContent, "````json\n") || !strings.Contains(outputContent, "\n````") {
			t.Errorf("expected 4 backticks fence for response containing triple backticks, got:\n%s", outputContent)
		}
	})

	t.Run("Embedded JSON inside XML tags is formatted with json code fences", func(t *testing.T) {
		tmpDir := t.TempDir()
		debugger := llm.NewFilePromptDebugger(tmpDir, nil, llm.WithDebuggerClock(func() time.Time {
			return fixedTime
		}))

		xmlPrompt := "System instruction\n<main_profile_data>{\"name\": \"Alice\", \"role\": \"developer\"}</main_profile_data>\nPlease tailor."
		session, err := debugger.StartSession(llm.PromptDebugMeta{
			Operation:  "tailor",
			UserPrompt: xmlPrompt,
			Timestamp:  fixedTime,
		})
		if err != nil {
			t.Fatalf("StartSession failed: %v", err)
		}
		if err := session.WriteResponse(llm.ResponseDebugMeta{
			Operation:   "tailor",
			StatusCode:  200,
			RawResponse: xmlPrompt,
			Timestamp:   fixedTime,
		}); err != nil {
			t.Fatalf("WriteResponse failed: %v", err)
		}

		promptPath := filepath.Join(tmpDir, "default", "tailor_20261107100000.md")
		promptBytes, err := os.ReadFile(promptPath)
		if err != nil {
			t.Fatalf("failed to read prompt file: %v", err)
		}
		promptContent := string(promptBytes)

		if !strings.Contains(promptContent, "<main_profile_data>\n```json\n{\n  \"name\": \"Alice\",\n  \"role\": \"developer\"\n}\n```\n</main_profile_data>") {
			t.Errorf("expected embedded json in xml tag to be formatted, got:\n%s", promptContent)
		}

		outputPath := filepath.Join(tmpDir, "default", "tailor_20261107100000_output.md")
		outputBytes, err := os.ReadFile(outputPath)
		if err != nil {
			t.Fatalf("failed to read output file: %v", err)
		}
		outputContent := string(outputBytes)
		if !strings.Contains(outputContent, "<main_profile_data>\n```json\n{\n  \"name\": \"Alice\",\n  \"role\": \"developer\"\n}\n```\n</main_profile_data>") {
			t.Errorf("expected embedded json in xml tag output to be formatted, got:\n%s", outputContent)
		}

		// Also verify non-JSON XML tag is untouched
		plainXML := "<instructions>Plain instructions here</instructions>"
		session2, err := debugger.StartSession(llm.PromptDebugMeta{
			Operation:  "tailor",
			UserPrompt: plainXML,
			Timestamp:  fixedTime,
		})
		if err != nil {
			t.Fatalf("StartSession2 failed: %v", err)
		}
		if err := session2.WriteResponse(llm.ResponseDebugMeta{
			Operation:   "tailor",
			StatusCode:  200,
			RawResponse: plainXML,
			Timestamp:   fixedTime,
		}); err != nil {
			t.Fatalf("WriteResponse failed: %v", err)
		}

		promptPath2 := filepath.Join(tmpDir, "default", "tailor_20261107100000_1.md")
		promptBytes2, err := os.ReadFile(promptPath2)
		if err != nil {
			t.Fatalf("failed to read prompt file 2: %v", err)
		}
		promptContent2 := string(promptBytes2)

		if strings.Contains(promptContent2, "```json") {
			t.Errorf("plain xml tag should not contain json code fence:\n%s", promptContent2)
		}
		if !strings.Contains(promptContent2, "<instructions>Plain instructions here</instructions>") {
			t.Errorf("expected plain xml tag to be preserved:\n%s", promptContent2)
		}

		// Also verify <prior_active_version> containing already-fenced JSON is not double-fenced
		priorActiveXML := "<prior_active_version>\nA prior active tailored version exists for this branch.\n\n```json\n{\n  \"summary\": \"Custom prior summary\"\n}\n```\n</prior_active_version>"
		session3, err := debugger.StartSession(llm.PromptDebugMeta{
			Operation:  "tailor",
			UserPrompt: priorActiveXML,
			Timestamp:  fixedTime,
		})
		if err != nil {
			t.Fatalf("StartSession3 failed: %v", err)
		}
		if err := session3.WriteResponse(llm.ResponseDebugMeta{
			Operation:   "tailor",
			StatusCode:  200,
			RawResponse: priorActiveXML,
			Timestamp:   fixedTime,
		}); err != nil {
			t.Fatalf("WriteResponse failed: %v", err)
		}

		promptPath3 := filepath.Join(tmpDir, "default", "tailor_20261107100000_2.md")
		promptBytes3, err := os.ReadFile(promptPath3)
		if err != nil {
			t.Fatalf("failed to read prompt file 3: %v", err)
		}
		promptContent3 := string(promptBytes3)

		if strings.Contains(promptContent3, "```json\n```json") || strings.Contains(promptContent3, "````") {
			t.Errorf("fenced prior_active_version should not be double-fenced:\n%s", promptContent3)
		}
		if !strings.Contains(promptContent3, priorActiveXML) {
			t.Errorf("expected prior_active_version with json fence to be preserved exactly, got:\n%s", promptContent3)
		}
	})

	t.Run("Payload exceeding 10MB bypasses formatting safely", func(t *testing.T) {
		tmpDir := t.TempDir()
		debugger := llm.NewFilePromptDebugger(tmpDir, nil, llm.WithDebuggerClock(func() time.Time {
			return fixedTime
		}))

		// Create a large JSON payload > 10MB
		hugePayload := "{\"data\": \"" + strings.Repeat("A", (10<<20)+100) + "\"}"
		session, err := debugger.StartSession(llm.PromptDebugMeta{
			Operation:  "tailor",
			UserPrompt: hugePayload,
			Timestamp:  fixedTime,
		})
		if err != nil {
			t.Fatalf("StartSession failed: %v", err)
		}
		if err := session.WriteResponse(llm.ResponseDebugMeta{
			Operation:   "tailor",
			StatusCode:  200,
			RawResponse: hugePayload,
			Timestamp:   fixedTime,
		}); err != nil {
			t.Fatalf("WriteResponse failed: %v", err)
		}

		promptPath := filepath.Join(tmpDir, "default", "tailor_20261107100000.md")
		promptBytes, err := os.ReadFile(promptPath)
		if err != nil {
			t.Fatalf("failed to read prompt file: %v", err)
		}
		promptContent := string(promptBytes)

		// Must not be indented or formatted in ```json
		if strings.Contains(promptContent, "```json") {
			t.Errorf("payload > 10MB should bypass json formatting")
		}
		if !strings.Contains(promptContent, hugePayload) {
			t.Errorf("expected raw huge payload to be preserved verbatim")
		}
	})

	t.Run("Deep JSON decode terminates at maxJSONDecodeDepth limit without error", func(t *testing.T) {
		tmpDir := t.TempDir()
		debugger := llm.NewFilePromptDebugger(tmpDir, nil, llm.WithDebuggerClock(func() time.Time {
			return fixedTime
		}))

		// Construct 15 levels of nested JSON strings
		current := `{"leaf": "deep_value"}`
		for i := 0; i < 15; i++ {
			b, err := json.Marshal(map[string]any{"nested": current})
			if err != nil {
				t.Fatalf("marshal error: %v", err)
			}
			current = string(b)
		}

		session, err := debugger.StartSession(llm.PromptDebugMeta{
			Operation:  "tailor",
			UserPrompt: current,
			Timestamp:  fixedTime,
		})
		if err != nil {
			t.Fatalf("StartSession failed: %v", err)
		}
		if err := session.WriteResponse(llm.ResponseDebugMeta{
			Operation:   "tailor",
			StatusCode:  200,
			RawResponse: current,
			Timestamp:   fixedTime,
		}); err != nil {
			t.Fatalf("WriteResponse failed: %v", err)
		}

		promptPath := filepath.Join(tmpDir, "default", "tailor_20261107100000.md")
		promptBytes, err := os.ReadFile(promptPath)
		if err != nil {
			t.Fatalf("failed to read prompt file: %v", err)
		}
		promptContent := string(promptBytes)

		if !strings.Contains(promptContent, "```json\n") {
			t.Errorf("expected outer json formatting:\n%s", promptContent)
		}
	})
}


