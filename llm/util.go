package llm

// stripCodeFences removes surrounding markdown formatting (e.g. ```json ... ```) or conversational preambles from raw LLM output.
func stripCodeFences(s string) string {
	return ExtractJSON(s)
}

