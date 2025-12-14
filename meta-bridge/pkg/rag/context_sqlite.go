package rag

import (
	"context"
	"database/sql"
	"fmt"
)

// SQLiteChunkStore implements ChunkStore using SQLite
type SQLiteChunkStore struct {
	db *sql.DB
}

// NewSQLiteChunkStore creates a new SQLite chunk store
func NewSQLiteChunkStore(db *sql.DB) *SQLiteChunkStore {
	return &SQLiteChunkStore{db: db}
}

// GetContext retrieves chunks within a radius of the specified chunk
func (s *SQLiteChunkStore) GetContext(ctx context.Context, threadID int64, sessionIdx, chunkIdx, radius int) ([]ContextChunk, error) {
	query := `
		SELECT
			chunk_id,
			chunk_idx,
			text,
			is_indexable
		FROM chunks
		WHERE thread_id = ?
		AND session_idx = ?
		AND chunk_idx BETWEEN ? AND ?
		ORDER BY chunk_idx
	`

	minIdx := chunkIdx - radius
	maxIdx := chunkIdx + radius

	rows, err := s.db.QueryContext(ctx, query, threadID, sessionIdx, minIdx, maxIdx)
	if err != nil {
		return nil, fmt.Errorf("querying context: %w", err)
	}
	defer rows.Close()

	var results []ContextChunk
	for rows.Next() {
		var cc ContextChunk
		var isIndexable int

		err := rows.Scan(&cc.ChunkID, &cc.ChunkIdx, &cc.Text, &isIndexable)
		if err != nil {
			return nil, fmt.Errorf("scanning context chunk: %w", err)
		}

		cc.IsIndexable = isIndexable == 1
		results = append(results, cc)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating context results: %w", err)
	}

	return results, nil
}

// GetByID retrieves a single chunk by its ID
func (s *SQLiteChunkStore) GetByID(ctx context.Context, chunkID string) (*Chunk, error) {
	query := `
		SELECT
			chunk_id,
			thread_id,
			thread_name,
			session_idx,
			chunk_idx,
			participant_ids,
			participant_names,
			text,
			message_ids,
			start_timestamp_ms,
			end_timestamp_ms,
			message_count
		FROM chunks
		WHERE chunk_id = ?
	`

	row := s.db.QueryRowContext(ctx, query, chunkID)

	var chunk Chunk
	var threadName sql.NullString
	var participantIDsJSON, participantNamesJSON, messageIDsJSON string

	err := row.Scan(
		&chunk.ChunkID,
		&chunk.ThreadID,
		&threadName,
		&chunk.SessionIdx,
		&chunk.ChunkIdx,
		&participantIDsJSON,
		&participantNamesJSON,
		&chunk.Text,
		&messageIDsJSON,
		&chunk.StartTimestampMs,
		&chunk.EndTimestampMs,
		&chunk.MessageCount,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanning chunk: %w", err)
	}

	chunk.ThreadName = threadName.String
	chunk.ParticipantIDs = parseIntArray(participantIDsJSON)
	chunk.ParticipantNames = parseStringArray(participantNamesJSON)
	chunk.MessageIDs = parseStringArray(messageIDsJSON)

	return &chunk, nil
}
