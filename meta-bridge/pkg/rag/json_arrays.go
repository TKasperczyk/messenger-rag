package rag

import "encoding/json"

// parseIntArray parses a JSON array of integers that may be encoded as JSON
// numbers or strings (to preserve JavaScript precision).
func parseIntArray(s string) Int64Strings {
	if s == "" {
		return nil
	}

	var ids Int64Strings
	if err := json.Unmarshal([]byte(s), &ids); err != nil {
		return nil
	}
	return ids
}

// parseStringArray parses a JSON array and keeps only non-empty string elements.
// Non-string elements are ignored to be tolerant of mixed-type arrays.
func parseStringArray(s string) []string {
	if s == "" {
		return nil
	}

	var raw []json.RawMessage
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return nil
	}

	out := make([]string, 0, len(raw))
	for _, item := range raw {
		var str string
		if err := json.Unmarshal(item, &str); err == nil && str != "" {
			out = append(out, str)
		}
	}
	return out
}
