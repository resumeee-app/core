package llm

import "strings"

// stripCodeFences removes surrounding markdown formatting (e.g. ```json ... ```) from raw LLM output.
func stripCodeFences(s string) string {
	trimmed := strings.TrimSpace(s)
	if strings.HasPrefix(trimmed, "```json") {
		trimmed = strings.TrimPrefix(trimmed, "```json")
	} else if strings.HasPrefix(trimmed, "```JSON") {
		trimmed = strings.TrimPrefix(trimmed, "```JSON")
	} else if strings.HasPrefix(trimmed, "```") {
		trimmed = strings.TrimPrefix(trimmed, "```")
	}

	trimmed = strings.TrimSuffix(trimmed, "```")

	return strings.TrimSpace(trimmed)
}
