// Package renderer executes multi-format resume templates across HTML, Markdown, and Plain Text
// in a sandboxed, memory-efficient environment with built-in AST validation and custom func maps.
package renderer

import (
	"bytes"
	"context"
	"fmt"
	htmltemplate "html/template"
	"io"
	"regexp"
	"sync"
	texttemplate "text/template"

	"github.com/resumeee-app/core"
	"github.com/resumeee-app/core/branch"
	"github.com/resumeee-app/core/worker"
)

// Buffer pool sizing limits controlling preallocation and reuse retention.
const (
	// defaultBufferCapacity preallocates 16 KiB to cover typical rendered resume output without reallocation.
	defaultBufferCapacity = 16 * 1024
	// maxPooledBufferCapacity caps retained buffer capacity at 512 KiB to bound pool memory footprint.
	maxPooledBufferCapacity = 512 * 1024
)

// Compiled patterns for stripping script tags from rendered HTML previews.
var (
	// scriptBlockRegex matches full <script>...</script> blocks case-insensitively.
	scriptBlockRegex = regexp.MustCompile(`(?i)<script\b[^>]*>[\s\S]*?<\/script\b[^>]*>`)
	// scriptTagRegex matches stray opening, closing, or self-closing script tags case-insensitively.
	scriptTagRegex = regexp.MustCompile(`(?i)<\/?script\b[^>]*\/?>`)
)

// StripScriptTags removes all <script>...</script> blocks and lingering <script...> or </script> tags from the provided HTML string.
// This prevents sandboxed preview iframes (sandbox="") from encountering script execution errors or security blocks.
func StripScriptTags(html string) string {
	if html == "" {
		return ""
	}
	res := html
	for {
		stripped := scriptBlockRegex.ReplaceAllString(res, "")
		stripped = scriptTagRegex.ReplaceAllString(stripped, "")
		if stripped == res {
			break
		}
		res = stripped
	}
	return res
}

// bufferPool recycles bytes.Buffer instances to eliminate allocations during frequent template rendering passes.
var bufferPool = sync.Pool{
	New: func() any {
		return bytes.NewBuffer(make([]byte, 0, defaultBufferCapacity))
	},
}

// acquireBuffer borrows and resets a pooled bytes.Buffer, allocating a fresh one if empty or unexpected.
func acquireBuffer() *bytes.Buffer {
	buf, ok := bufferPool.Get().(*bytes.Buffer)
	if !ok || buf == nil {
		return bytes.NewBuffer(make([]byte, 0, defaultBufferCapacity))
	}
	buf.Reset()
	return buf
}

// releaseBuffer returns buf to bufferPool unless it grew past maxPooledBufferCapacity.
func releaseBuffer(buf *bytes.Buffer) {
	if buf != nil && buf.Cap() <= maxPooledBufferCapacity {
		buf.Reset()
		bufferPool.Put(buf)
	}
}

// Validate executes pre-persistence dry-run validation on HTML, Markdown, and Plain Text templates.
func Validate(tmpl *core.TemplateSet) error {
	if tmpl == nil {
		return fmt.Errorf("%w: template set cannot be nil", core.ErrValidation)
	}

	synth := SyntheticRenderContext()
	synth.CSSStyles = htmltemplate.CSS(tmpl.CSSStyles)

	if tmpl.HTMLTemplate != "" {
		ht, err := htmltemplate.New("html").Funcs(SafeFuncMap).Parse(tmpl.HTMLTemplate)
		if err != nil {
			return fmt.Errorf("%w in html_template: %v", core.ErrTemplateSyntax, err)
		}
		if err := ht.Execute(io.Discard, synth); err != nil {
			return fmt.Errorf("%w in html_template evaluation: %v", core.ErrTemplateSyntax, err)
		}
	}

	for _, textTmpl := range []struct {
		name string
		body string
	}{
		{"markdown_template", tmpl.MarkdownTemplate},
		{"text_template", tmpl.TextTemplate},
	} {
		if textTmpl.body == "" {
			continue
		}
		tt, err := texttemplate.New(textTmpl.name).Funcs(SafeFuncMap).Parse(textTmpl.body)
		if err != nil {
			return fmt.Errorf("%w in %s: %v", core.ErrTemplateSyntax, textTmpl.name, err)
		}
		if err := tt.Execute(io.Discard, synth); err != nil {
			return fmt.Errorf("%w in %s evaluation: %v", core.ErrTemplateSyntax, textTmpl.name, err)
		}
	}

	return nil
}

// RenderFormat compiles and executes a single template string targeting the designated OutputFormat.
func RenderFormat(format core.OutputFormat, tmplStr string, data any) (string, error) {
	if tmplStr == "" {
		return "", nil
	}

	buf := acquireBuffer()
	defer releaseBuffer(buf)

	switch format {
	case core.OutputFormatHTML:
		t, err := htmltemplate.New("html").Funcs(SafeFuncMap).Parse(tmplStr)
		if err != nil {
			return "", fmt.Errorf("%w in html template: %v", core.ErrTemplateSyntax, err)
		}
		if err := t.Execute(buf, data); err != nil {
			return "", fmt.Errorf("%w in html template execution: %v", core.ErrTemplateSyntax, err)
		}
		return buf.String(), nil

	case core.OutputFormatMarkdown, core.OutputFormatPlainText:
		name := "markdown"
		if format == core.OutputFormatPlainText {
			name = "text"
		}
		t, err := texttemplate.New(name).Funcs(SafeFuncMap).Parse(tmplStr)
		if err != nil {
			return "", fmt.Errorf("%w in %s template: %v", core.ErrTemplateSyntax, name, err)
		}
		if err := t.Execute(buf, data); err != nil {
			return "", fmt.Errorf("%w in %s template execution: %v", core.ErrTemplateSyntax, name, err)
		}
		return buf.String(), nil

	default:
		return "", fmt.Errorf("unsupported output format: %s", format)
	}
}

// Render compiles and executes HTML, Markdown, and Plain Text formats for a given TemplateSet against candidate data.
func Render(_ context.Context, tmpl *core.TemplateSet, profile *core.Profile, content *core.TailoredContent) (*core.RenderOutput, error) {
	if tmpl == nil {
		return nil, fmt.Errorf("%w: template set is required", core.ErrValidation)
	}

	renderCtx := NewRenderContext(tmpl, profile, content)

	htmlOut, err := RenderFormat(core.OutputFormatHTML, tmpl.HTMLTemplate, renderCtx)
	if err != nil {
		return nil, err
	}

	mdOut, err := RenderFormat(core.OutputFormatMarkdown, tmpl.MarkdownTemplate, renderCtx)
	if err != nil {
		return nil, err
	}

	txtOut, err := RenderFormat(core.OutputFormatPlainText, tmpl.TextTemplate, renderCtx)
	if err != nil {
		return nil, err
	}

	return &core.RenderOutput{
		HTML:      htmlOut,
		Markdown:  mdOut,
		PlainText: txtOut,
	}, nil
}

// Renderer provides stateless, concurrent-safe multi-format template rendering and validation.
type Renderer struct{}

// New constructs a thread-safe, stateless Renderer.
func New() *Renderer {
	return &Renderer{}
}

// Render compiles and executes HTML, Markdown, and plain-text templates for tmpl against profile and content.
func (r *Renderer) Render(ctx context.Context, tmpl *core.TemplateSet, profile *core.Profile, content *core.TailoredContent) (*core.RenderOutput, error) {
	return Render(ctx, tmpl, profile, content)
}

// RenderFormat compiles and executes a single template string targeting format.
func (r *Renderer) RenderFormat(format core.OutputFormat, tmplStr string, data any) (string, error) {
	return RenderFormat(format, tmplStr, data)
}

// Validate runs syntax and dry-run evaluation checks across all formats in tmpl.
func (r *Renderer) Validate(tmpl *core.TemplateSet) error {
	return Validate(tmpl)
}

// Compile-time assertions verifying Renderer satisfies branch and worker template rendering contracts.
var (
	_ branch.TemplateRenderer = (*Renderer)(nil)
	_ worker.TemplateRenderer = (*Renderer)(nil)
)
