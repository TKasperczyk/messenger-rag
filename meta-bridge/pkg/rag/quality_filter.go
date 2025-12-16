package rag

import (
	"strings"
	"unicode/utf8"

	"go.mau.fi/mautrix-meta/pkg/chunking"
	"go.mau.fi/mautrix-meta/pkg/ragconfig"
)

func filterVectorHits(cfg *ragconfig.Config, hits []VectorHit) []VectorHit {
	if cfg == nil || len(hits) == 0 {
		return hits
	}

	minChars := cfg.Quality.MinChars
	filtered := make([]VectorHit, 0, len(hits))
	for _, hit := range hits {
		text := hit.Text
		if minChars > 0 && utf8.RuneCountInString(strings.TrimSpace(text)) < minChars {
			continue
		}
		if chunking.IsLowQualityChunkText(cfg, text) {
			continue
		}
		filtered = append(filtered, hit)
	}

	return filtered
}
