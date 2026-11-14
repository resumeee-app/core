package llm

import (
	"fmt"
	"regexp"
	"strings"
)

// codeFenceRegex matches fenced markdown code blocks containing optional JSON payloads.
var codeFenceRegex = regexp.MustCompile("(?s)```(?:json|JSON)?\\s*([\\s\\S]*?)\\s*```")

// ExtractJSON extracts raw JSON from conversational wrappers, markdown fences, or raw text.
func ExtractJSON(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return ""
	}

	if matches := codeFenceRegex.FindStringSubmatch(trimmed); len(matches) > 1 {
		if bounded := sliceOutermostJSON(strings.TrimSpace(matches[1])); bounded != "" {
			return bounded
		}
		return strings.TrimSpace(matches[1])
	}

	if bounded := sliceOutermostJSON(trimmed); bounded != "" {
		return bounded
	}
	return trimmed
}

// sliceOutermostJSON returns the substring spanning the outermost JSON object or array delimiters in s.
func sliceOutermostJSON(s string) string {
	if firstObj, lastObj := strings.Index(s, "{"), strings.LastIndex(s, "}"); firstObj != -1 && lastObj > firstObj {
		return s[firstObj : lastObj+1]
	}
	if firstArr, lastArr := strings.Index(s, "["), strings.LastIndex(s, "]"); firstArr != -1 && lastArr > firstArr {
		return s[firstArr : lastArr+1]
	}
	return ""
}

// ContainsResponseSchemaDirective reports whether prompt already embeds an inline response schema block.
func ContainsResponseSchemaDirective(prompt string) bool {
	lower := strings.ToLower(prompt)
	return strings.Contains(lower, "<response_schema>") || strings.Contains(lower, "<json_output_schema>")
}

// AppendRawSchemaBlock appends a formatted <response_schema> XML block containing schemaJSON to systemPrompt.
func AppendRawSchemaBlock(systemPrompt, schemaJSON string) string {
	trimmed := strings.TrimRight(systemPrompt, "\n")
	block := fmt.Sprintf(
		"<response_schema>\nThe expected response must be a valid JSON object conforming strictly to the following JSON Schema:\n```json\n%s\n```\n</response_schema>",
		schemaJSON,
	)
	if trimmed == "" {
		return block
	}
	return trimmed + "\n\n" + block
}

// AppendFallbackSchema appends a serialized JSON Schema block to systemPrompt unless already present.
func AppendFallbackSchema(systemPrompt string, schema *Schema) (string, error) {
	if schema == nil || ContainsResponseSchemaDirective(systemPrompt) {
		return systemPrompt, nil
	}
	schemaJSON, err := schema.JSON()
	if err != nil {
		return "", fmt.Errorf("failed to marshal fallback response schema: %w", err)
	}
	return AppendRawSchemaBlock(systemPrompt, schemaJSON), nil
}
