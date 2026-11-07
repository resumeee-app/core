package llm

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// PromptDebugMeta contains metadata and prompt content for an outbound LLM request.
type PromptDebugMeta struct {
	TenantID     string
	Provider     string
	Model        string
	Operation    string
	SystemPrompt string
	UserPrompt   string
	Timestamp    time.Time
}

// ResponseDebugMeta contains metadata and output payload for an inbound LLM response.
type ResponseDebugMeta struct {
	Provider    string
	Model       string
	Operation   string
	Duration    time.Duration
	StatusCode  int
	RawResponse string
	Err         error
	Timestamp   time.Time
}

// PromptDebugSession handles recording the response paired with an in-flight prompt.
type PromptDebugSession interface {
	// WriteResponse formats and writes the LLM response output to persistent storage
	// associated with the initiating prompt. It returns an error if writing fails.
	WriteResponse(meta ResponseDebugMeta) error
}

// PromptDebugger defines an interface for recording LLM prompts and responses to persistent storage.
type PromptDebugger interface {
	// StartSession writes the outbound prompt markdown file to persistent storage and
	// returns a PromptDebugSession configured to record the subsequent LLM response.
	StartSession(meta PromptDebugMeta) (PromptDebugSession, error)
}

// FilePromptDebuggerOption configures properties of a FilePromptDebugger.
type FilePromptDebuggerOption func(*FilePromptDebugger)

// WithDebuggerClock overrides the clock used for generating file timestamps (useful in testing).
func WithDebuggerClock(clock func() time.Time) FilePromptDebuggerOption {
	return func(d *FilePromptDebugger) {
		if clock != nil {
			d.clock = clock
		}
	}
}

// WithDebuggerLogger overrides the logger used for reporting file write errors.
func WithDebuggerLogger(logger *slog.Logger) FilePromptDebuggerOption {
	return func(d *FilePromptDebugger) {
		if logger != nil {
			d.logger = logger
		}
	}
}

// FilePromptDebugger persists LLM prompts and responses as Markdown documents on the local filesystem.
type FilePromptDebugger struct {
	dir    string
	logger *slog.Logger
	clock  func() time.Time
	mu     sync.Mutex
	seq    int
}

// NewFilePromptDebugger creates a new FilePromptDebugger targeting the specified directory.
func NewFilePromptDebugger(dir string, logger *slog.Logger, opts ...FilePromptDebuggerOption) *FilePromptDebugger {
	if strings.TrimSpace(dir) == "" {
		dir = "data/llm"
	}
	if logger == nil {
		logger = slog.Default()
	}
	d := &FilePromptDebugger{
		dir:    dir,
		logger: logger,
		clock:  time.Now,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(d)
		}
	}
	return d
}

// SanitizeTenantID normalizes tenant identifiers to prevent directory traversal and remove invalid path characters.
func SanitizeTenantID(tenantID string) string {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return "default"
	}
	var b strings.Builder
	for _, r := range tenantID {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	cleaned := strings.Trim(b.String(), "_-")
	if cleaned == "" {
		return "default"
	}
	return cleaned
}

// SanitizeOperation normalizes operation names to prevent path traversal and map canonical LLM operations.
func SanitizeOperation(op string) string {
	op = strings.TrimSpace(op)
	lower := strings.ToLower(op)
	switch lower {
	case "generatetailoredresume", "tailored_resume", "tailor":
		return "tailor"
	case "extractresume", "completetext", "extract", "resume_extraction":
		return "extract"
	}

	var b strings.Builder
	for _, r := range op {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else if r == '-' || r == ' ' || r == '.' || r == '/' || r == '\\' {
			b.WriteRune('_')
		}
	}
	cleaned := strings.Trim(b.String(), "_")
	if cleaned == "" {
		return "operation"
	}
	return strings.ToLower(cleaned)
}

// StartSession creates the prompt markdown file atomically and returns a session to record the response.
func (d *FilePromptDebugger) StartSession(meta PromptDebugMeta) (PromptDebugSession, error) {
	tenant := SanitizeTenantID(meta.TenantID)
	cleanBaseDir := filepath.Clean(d.dir)
	tenantDir := filepath.Join(cleanBaseDir, tenant)
	cleanTenantDir := filepath.Clean(tenantDir)

	rel, err := filepath.Rel(cleanBaseDir, cleanTenantDir)
	if err != nil || strings.HasPrefix(rel, "..") {
		cleanTenantDir = filepath.Join(cleanBaseDir, "default")
	}

	if err := os.MkdirAll(cleanTenantDir, 0700); err != nil {
		d.logger.Warn("failed to create llm debug directory", "dir", cleanTenantDir, "err", err)
		return nil, fmt.Errorf("creating debug directory %s: %w", cleanTenantDir, err)
	}

	sanitizedOp := SanitizeOperation(meta.Operation)
	now := meta.Timestamp
	if now.IsZero() {
		now = d.clock()
	}
	ts := now.Format("20060102150405")
	content := formatPromptMarkdown(meta, sanitizedOp, now)

	d.mu.Lock()
	defer d.mu.Unlock()

	var (
		f          *os.File
		promptPath string
		outputPath string
	)

	// Attempt atomic exclusive creation (O_EXCL) to prevent collisions and TOCTOU races under concurrency
	for attempt := 0; attempt < 1000; attempt++ {
		var filenameBase string
		if attempt == 0 {
			filenameBase = fmt.Sprintf("%s_%s", sanitizedOp, ts)
		} else {
			d.seq++
			filenameBase = fmt.Sprintf("%s_%s_%d", sanitizedOp, ts, d.seq)
		}

		pPath := filepath.Join(cleanTenantDir, filenameBase+".md")
		oPath := filepath.Join(cleanTenantDir, filenameBase+"_output.md")

		// Defense in depth: path traversal verification
		cleanPrompt := filepath.Clean(pPath)
		pRel, pErr := filepath.Rel(cleanTenantDir, cleanPrompt)
		if pErr != nil || strings.HasPrefix(pRel, "..") {
			return nil, fmt.Errorf("path traversal detected: %s", pPath)
		}

		file, err := os.OpenFile(pPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			if os.IsExist(err) {
				continue
			}
			d.logger.Warn("failed to open llm prompt debug file", "path", pPath, "err", err)
			return nil, fmt.Errorf("creating prompt debug file %s: %w", pPath, err)
		}

		f = file
		promptPath = pPath
		outputPath = oPath
		break
	}

	if f == nil {
		return nil, fmt.Errorf("failed to allocate unique prompt debug file in %s after retry attempts", cleanTenantDir)
	}

	if _, err := f.Write([]byte(content)); err != nil {
		_ = f.Close()
		d.logger.Warn("failed to write llm prompt debug content", "path", promptPath, "err", err)
		return nil, fmt.Errorf("writing prompt debug content %s: %w", promptPath, err)
	}
	if err := f.Close(); err != nil {
		d.logger.Warn("failed to close llm prompt debug file", "path", promptPath, "err", err)
		return nil, fmt.Errorf("closing prompt debug file %s: %w", promptPath, err)
	}

	return &fileDebugSession{
		outputPath:  outputPath,
		sanitizedOp: sanitizedOp,
		debugger:    d,
	}, nil
}

type fileDebugSession struct {
	outputPath  string
	sanitizedOp string
	debugger    *FilePromptDebugger
}

func (s *fileDebugSession) WriteResponse(meta ResponseDebugMeta) error {
	now := meta.Timestamp
	if now.IsZero() {
		now = s.debugger.clock()
	}

	content := formatResponseMarkdown(meta, s.sanitizedOp, now)

	f, err := os.OpenFile(s.outputPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		s.debugger.logger.Warn("failed to open llm response debug file", "path", s.outputPath, "err", err)
		return fmt.Errorf("creating response debug file %s: %w", s.outputPath, err)
	}

	if _, err := f.Write([]byte(content)); err != nil {
		_ = f.Close()
		s.debugger.logger.Warn("failed to write llm response debug content", "path", s.outputPath, "err", err)
		return fmt.Errorf("writing response debug content %s: %w", s.outputPath, err)
	}

	if err := f.Close(); err != nil {
		s.debugger.logger.Warn("failed to close llm response debug file", "path", s.outputPath, "err", err)
		return fmt.Errorf("closing response debug file %s: %w", s.outputPath, err)
	}

	return nil
}

func formatPromptMarkdown(meta PromptDebugMeta, sanitizedOp string, ts time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# LLM Prompt: %s\n\n", sanitizedOp)
	if meta.TenantID != "" {
		fmt.Fprintf(&b, "- **Tenant**: %s\n", meta.TenantID)
	}
	fmt.Fprintf(&b, "- **Provider**: %s\n", meta.Provider)
	fmt.Fprintf(&b, "- **Model**: %s\n", meta.Model)
	fmt.Fprintf(&b, "- **Operation**: %s\n", sanitizedOp)
	fmt.Fprintf(&b, "- **Timestamp**: %s\n\n", ts.Format(time.RFC3339))
	b.WriteString("## System Prompt\n\n")
	b.WriteString(meta.SystemPrompt)
	b.WriteString("\n\n## User Prompt\n\n")
	b.WriteString(meta.UserPrompt)
	b.WriteString("\n")
	return b.String()
}

func formatResponseMarkdown(meta ResponseDebugMeta, sanitizedOp string, ts time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# LLM Response: %s\n\n", sanitizedOp)
	fmt.Fprintf(&b, "- **Provider**: %s\n", meta.Provider)
	fmt.Fprintf(&b, "- **Model**: %s\n", meta.Model)
	fmt.Fprintf(&b, "- **Operation**: %s\n", sanitizedOp)
	fmt.Fprintf(&b, "- **Timestamp**: %s\n", ts.Format(time.RFC3339))
	fmt.Fprintf(&b, "- **Duration**: %s\n", meta.Duration)
	fmt.Fprintf(&b, "- **Status Code**: %d\n", meta.StatusCode)
	if meta.Err != nil {
		fmt.Fprintf(&b, "- **Error**: %s\n", meta.Err.Error())
	}
	b.WriteString("\n## Output\n\n")
	if meta.RawResponse != "" {
		b.WriteString(meta.RawResponse)
	} else if meta.Err != nil {
		b.WriteString("(error occurred, no response body received)")
	}
	b.WriteString("\n")
	return b.String()
}
