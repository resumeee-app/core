package store

import "encoding/json"

// cloneValue produces an independent copy of v so transaction helpers never alias caller or backend state.
func cloneValue[T any](v T) T {
	data, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		return v
	}
	return out
}
