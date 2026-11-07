package llm_test

import (
	"bytes"
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
	if !strings.Contains(outputContent, `{"basics": {"name": "Jane Doe"}}`) {
		t.Errorf("output file missing raw response: %s", outputContent)
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
	entries, _ := os.ReadDir(parentDir)
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

