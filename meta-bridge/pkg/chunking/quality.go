package chunking

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.mau.fi/mautrix-meta/pkg/ragconfig"
)

var (
	// URL pattern for detecting links
	urlPattern = regexp.MustCompile(`(?i)https?://\S+`)
)

// CountAlnumChars counts alphanumeric characters in text.
func CountAlnumChars(text string) int {
	count := 0
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			count++
		}
	}
	return count
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || unicode.IsMark(r)
}

// CountUniqueWords counts unique words (3+ chars) in text.
func CountUniqueWords(text string) int {
	unique := make(map[string]struct{})
	word := make([]rune, 0, 32)

	flush := func() {
		if len(word) >= 3 {
			unique[strings.ToLower(string(word))] = struct{}{}
		}
		word = word[:0]
	}

	for _, r := range text {
		if isWordRune(r) {
			word = append(word, r)
			continue
		}
		flush()
	}
	flush()

	return len(unique)
}

// HasURL checks if text contains a URL.
func HasURL(text string) bool {
	return urlPattern.MatchString(text)
}

// ComputeIndexability determines if a chunk should be indexed (embedded).
// Returns (isIndexable, charCount, alnumCount, uniqueWordCount)
// charCount is in Unicode runes (not bytes) to match Python's len() behavior.
//
// Query-time filtering uses IsLowQualityChunkText (see quality_filter.go). Keep any
// shared heuristics consistent with the web-side filtering in web/src/lib/server/milvus.ts.
func ComputeIndexability(text string, cfg *ragconfig.Config) (bool, int, int, int) {
	charCount := utf8.RuneCountInString(text) // Unicode chars, not bytes
	alnumCount := CountAlnumChars(text)
	uniqueWords := CountUniqueWords(text)

	// Standard indexability criteria
	if charCount >= cfg.Quality.MinChars &&
		alnumCount >= cfg.Quality.MinAlnumChars &&
		uniqueWords >= cfg.Quality.MinUniqueWords {
		return true, charCount, alnumCount, uniqueWords
	}

	// Special case: URL with meaningful context
	if cfg.Quality.URLSpecialCase.Enabled &&
		HasURL(text) &&
		alnumCount >= cfg.Quality.URLSpecialCase.MinAlnumChars {
		return true, charCount, alnumCount, uniqueWords
	}

	return false, charCount, alnumCount, uniqueWords
}
