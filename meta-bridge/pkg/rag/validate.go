package rag

import (
	"fmt"
	"strings"
	"unicode"
)

// ValidateSearchRequest validates search request parameters
func ValidateSearchRequest(req *SearchRequest) error {
	if strings.TrimSpace(req.Query) == "" {
		return fmt.Errorf("query cannot be empty")
	}

	// Check query length (prevent very long queries)
	if len(req.Query) > 2000 {
		return fmt.Errorf("query too long (max 2000 characters)")
	}

	// Validate mode
	switch req.Mode {
	case ModeVector, ModeBM25, ModeHybrid, "":
		// Valid
	default:
		return fmt.Errorf("invalid mode: %s (must be vector, bm25, or hybrid)", req.Mode)
	}

	return nil
}

// SanitizeQuery cleans user input for safe use
func SanitizeQuery(query string) string {
	// Trim whitespace
	query = strings.TrimSpace(query)

	// Remove control characters
	var sb strings.Builder
	for _, r := range query {
		if !unicode.IsControl(r) || r == '\n' || r == '\t' {
			sb.WriteRune(r)
		}
	}

	return sb.String()
}
