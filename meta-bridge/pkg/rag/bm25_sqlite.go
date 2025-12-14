package rag

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"

	"go.mau.fi/mautrix-meta/pkg/ragconfig"
)

// SQLiteBM25Searcher implements BM25Searcher using SQLite FTS5
type SQLiteBM25Searcher struct {
	db       *sql.DB
	ftsTable string
}

// NewSQLiteBM25Searcher creates a new SQLite BM25 searcher
func NewSQLiteBM25Searcher(db *sql.DB, cfg *ragconfig.Config) (*SQLiteBM25Searcher, error) {
	ftsTable := cfg.Hybrid.BM25.Table
	if ftsTable == "" {
		ftsTable = "chunks_fts"
	}

	// Validate table name to prevent SQL injection
	if !isValidIdentifier(ftsTable) {
		return nil, fmt.Errorf("invalid FTS table name: %s", ftsTable)
	}

	return &SQLiteBM25Searcher{
		db:       db,
		ftsTable: ftsTable,
	}, nil
}

// isValidIdentifier checks if a string is a valid SQL identifier
func isValidIdentifier(s string) bool {
	matched, _ := regexp.MatchString(`^[A-Za-z_][A-Za-z0-9_]*$`, s)
	return matched
}

// Search performs a BM25 full-text search
func (s *SQLiteBM25Searcher) Search(ctx context.Context, query string, limit int) ([]BM25Hit, error) {
	// Build FTS5 query from user input
	ftsQuery := buildFTSQuery(query)
	if ftsQuery == "" {
		return []BM25Hit{}, nil
	}

	// Query with FTS5 MATCH
	// Note: bm25() returns negative scores where more negative = better match
	sqlQuery := fmt.Sprintf(`
		SELECT
			c.chunk_id,
			c.thread_id,
			c.thread_name,
			c.session_idx,
			c.chunk_idx,
			c.participant_ids,
			c.participant_names,
			c.text,
			c.message_ids,
			c.start_timestamp_ms,
			c.end_timestamp_ms,
			c.message_count,
			bm25(%s) as bm25_score
		FROM %s fts
		JOIN chunks c ON c.chunk_id = fts.chunk_id
		WHERE %s MATCH ?
		AND c.is_indexable = 1
		ORDER BY bm25(%s)
		LIMIT ?
	`, s.ftsTable, s.ftsTable, s.ftsTable, s.ftsTable)

	rows, err := s.db.QueryContext(ctx, sqlQuery, ftsQuery, limit)
	if err != nil {
		return nil, fmt.Errorf("BM25 search query: %w", err)
	}
	defer rows.Close()

	var results []BM25Hit
	rank := 0
	for rows.Next() {
		rank++
		var hit BM25Hit
		var participantIDsJSON, participantNamesJSON, messageIDsJSON string
		var threadName sql.NullString

		err := rows.Scan(
			&hit.ChunkID,
			&hit.ThreadID,
			&threadName,
			&hit.SessionIdx,
			&hit.ChunkIdx,
			&participantIDsJSON,
			&participantNamesJSON,
			&hit.Text,
			&messageIDsJSON,
			&hit.StartTimestampMs,
			&hit.EndTimestampMs,
			&hit.MessageCount,
			&hit.Score,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning BM25 result: %w", err)
		}

		hit.ThreadName = threadName.String
		hit.Rank = rank
		// BM25 scores are negative (lower = better match), convert to positive
		hit.Score = math.Abs(hit.Score)

		// Parse JSON arrays
		hit.ParticipantIDs = parseIntArray(participantIDsJSON)
		hit.ParticipantNames = parseStringArray(participantNamesJSON)
		hit.MessageIDs = parseStringArray(messageIDsJSON)

		results = append(results, hit)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating BM25 results: %w", err)
	}

	return results, nil
}

// Stats returns SQLite statistics
func (s *SQLiteBM25Searcher) Stats(ctx context.Context) (SQLiteStats, error) {
	stats := SQLiteStats{
		Connected: true,
		FtsTable:  s.ftsTable,
	}

	// Check if chunks table exists and get counts
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chunks`)
	if err := row.Scan(&stats.ChunksTotal); err != nil {
		stats.Connected = false
		return stats, err
	}

	row = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chunks WHERE is_indexable = 1`)
	if err := row.Scan(&stats.ChunksIndexed); err != nil {
		return stats, err
	}

	// Check if FTS table exists
	var name string
	row = s.db.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, s.ftsTable)
	if err := row.Scan(&name); err == nil {
		stats.FtsAvailable = true
	}

	return stats, nil
}

// buildFTSQuery converts user input to FTS5 query syntax.
// Uses AND by default for better precision. Use "|" to explicitly request OR.
// Examples:
//   - "cat dog"       -> "cat" AND "dog"
//   - "cat | dog"     -> "cat" OR "dog"
//   - "cat dog | fish" -> "cat" AND "dog" OR "fish"
func buildFTSQuery(query string) string {
	// Remove quotes (we'll add our own)
	query = strings.ReplaceAll(query, `"`, "")
	query = strings.ReplaceAll(query, `'`, "")

	// Split by "|" to find explicit OR groups
	orParts := strings.Split(query, "|")

	var orGroups []string
	for _, part := range orParts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		// Split into words within each OR group
		words := strings.Fields(part)

		// Filter and quote words
		var quoted []string
		for _, w := range words {
			// Skip very short words
			if len(w) <= 1 {
				continue
			}
			// Escape any remaining special characters
			w = escapeFTSWord(w)
			if w != "" {
				quoted = append(quoted, fmt.Sprintf(`"%s"`, w))
			}
		}

		if len(quoted) > 0 {
			// AND within each group
			orGroups = append(orGroups, strings.Join(quoted, " AND "))
		}
	}

	if len(orGroups) == 0 {
		return ""
	}

	// OR between groups (if user explicitly used "|")
	return strings.Join(orGroups, " OR ")
}

// escapeFTSWord escapes special FTS5 characters in a word
func escapeFTSWord(word string) string {
	// FTS5 special characters: " ' ( ) * : ^
	replacer := strings.NewReplacer(
		`"`, ``,
		`'`, ``,
		`(`, ``,
		`)`, ``,
		`*`, ``,
		`:`, ``,
		`^`, ``,
	)
	return replacer.Replace(word)
}

// parseIntArray parses a JSON array of integers
func parseIntArray(s string) []int64 {
	if s == "" {
		return nil
	}

	var arr []interface{}
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		return nil
	}

	result := make([]int64, 0, len(arr))
	for _, v := range arr {
		switch n := v.(type) {
		case float64:
			result = append(result, int64(n))
		case int64:
			result = append(result, n)
		case int:
			result = append(result, int64(n))
		}
	}
	return result
}

// parseStringArray parses a JSON array of strings
func parseStringArray(s string) []string {
	if s == "" {
		return nil
	}

	var arr []string
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		// Try as array of interfaces (mixed types)
		var mixed []interface{}
		if err := json.Unmarshal([]byte(s), &mixed); err != nil {
			return nil
		}
		result := make([]string, 0, len(mixed))
		for _, v := range mixed {
			if str, ok := v.(string); ok && str != "" {
				result = append(result, str)
			}
		}
		return result
	}

	// Filter empty strings
	result := make([]string, 0, len(arr))
	for _, str := range arr {
		if str != "" {
			result = append(result, str)
		}
	}
	return result
}
