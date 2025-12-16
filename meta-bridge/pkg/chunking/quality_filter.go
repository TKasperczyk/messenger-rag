package chunking

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.mau.fi/mautrix-meta/pkg/ragconfig"
)

// NOTE: Keep this logic in sync with the web-side filtering in web/src/lib/server/milvus.ts.
// Both are query-time filters (index-time filtering is ComputeIndexability in quality.go).

const (
	maxChunkTextLength            = 3000
	longTextWhitespaceCheckLength = 2000
	minWhitespaceRatio            = 0.02
	attachmentOnlyMaxNonURLAlnum  = 100
)

var (
	dataURIImageBase64Pattern = regexp.MustCompile(`(?i)\bdata:image/[a-z0-9.+-]+;base64,`)
	base64RunPattern          = regexp.MustCompile(`[A-Za-z0-9+/]{500,}={0,2}`)
	senderPrefixPattern       = regexp.MustCompile(`(?m)^\[[^\]]+\]:\s*`)
	attachmentOnlyPattern     = regexp.MustCompile(`(?i)sent an attachment`)
)

// IsLowQualityChunkText returns true if the chunk text should be filtered out at query time.
// It uses thresholds from rag.yaml via ragconfig.Config.
func IsLowQualityChunkText(cfg *ragconfig.Config, text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return true
	}

	trimmedLen := utf8.RuneCountInString(trimmed)
	if trimmedLen > maxChunkTextLength {
		return true
	}

	if cfg != nil && cfg.Quality.Filters.SkipBase64Blobs {
		if dataURIImageBase64Pattern.MatchString(trimmed) {
			return true
		}
		if base64RunPattern.MatchString(trimmed) {
			return true
		}
	}

	withoutPrefixes := senderPrefixPattern.ReplaceAllString(trimmed, "")
	withoutPrefixes = strings.TrimSpace(withoutPrefixes)
	if withoutPrefixes == "" {
		return true
	}

	urlChars := countURLChars(withoutPrefixes)
	withoutPrefixesLen := utf8.RuneCountInString(withoutPrefixes)

	maxURLDensity := 0.5
	if cfg != nil {
		maxURLDensity = cfg.Quality.Filters.MaxURLDensity
	}
	if urlChars > 0 && withoutPrefixesLen > 0 && float64(urlChars)/float64(withoutPrefixesLen) > maxURLDensity {
		return true
	}

	if urlChars > 0 {
		withoutURLs := strings.TrimSpace(urlPattern.ReplaceAllString(withoutPrefixes, ""))
		nonURLAlnum := CountAlnumChars(withoutURLs)

		if cfg != nil && cfg.Quality.URLSpecialCase.Enabled && nonURLAlnum < cfg.Quality.URLSpecialCase.MinAlnumChars {
			return true
		}

		if cfg != nil && cfg.Quality.Filters.SkipAttachmentOnly && nonURLAlnum < attachmentOnlyMaxNonURLAlnum {
			if attachmentOnlyPattern.MatchString(withoutURLs) {
				return true
			}
		}
	}

	if trimmedLen > longTextWhitespaceCheckLength {
		whitespaceChars := countWhitespaceChars(trimmed)
		if float64(whitespaceChars)/float64(trimmedLen) < minWhitespaceRatio {
			return true
		}
	}

	return false
}

func countURLChars(text string) int {
	total := 0
	for _, loc := range urlPattern.FindAllStringIndex(text, -1) {
		if len(loc) != 2 || loc[0] < 0 || loc[1] < loc[0] {
			continue
		}
		total += utf8.RuneCountInString(text[loc[0]:loc[1]])
	}
	return total
}

func countWhitespaceChars(text string) int {
	count := 0
	for _, r := range text {
		if unicode.IsSpace(r) {
			count++
		}
	}
	return count
}
