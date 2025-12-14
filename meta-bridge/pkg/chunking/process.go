package chunking

import (
	"context"
	"database/sql"
	"fmt"

	"go.mau.fi/mautrix-meta/pkg/ragconfig"
)

// ThreadData contains a thread's messages and metadata.
type ThreadData struct {
	ThreadID   int64
	ThreadName string
	Messages   []Message
}

// ProcessThread processes a single thread into chunks.
func ProcessThread(thread ThreadData, cfg *ragconfig.Config) []Chunk {
	// Step 1: Coalesce messages
	coalesced := CoalesceMessages(thread.Messages, cfg)

	// Step 2: Split into sessions
	sessions := SplitIntoSessions(coalesced, cfg)

	// Step 3: Create greedy chunks
	var allChunks []Chunk
	for sessionIdx, session := range sessions {
		chunks := CreateGreedyChunks(session, thread.ThreadID, thread.ThreadName, sessionIdx, cfg)
		allChunks = append(allChunks, chunks...)
	}

	return allChunks
}

// FetchThreads fetches all threads with messages from the database.
func FetchThreads(ctx context.Context, db *sql.DB) ([]ThreadData, error) {
	// Get all thread IDs with messages
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT thread_id FROM messages
		WHERE text IS NOT NULL AND text != ''
		ORDER BY thread_id
	`)
	if err != nil {
		return nil, fmt.Errorf("querying thread IDs: %w", err)
	}

	var threadIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning thread ID: %w", err)
		}
		threadIDs = append(threadIDs, id)
	}
	rows.Close()

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating thread IDs: %w", err)
	}

	// Fetch each thread's data
	var threads []ThreadData
	for _, threadID := range threadIDs {
		thread, err := fetchThread(ctx, db, threadID)
		if err != nil {
			return nil, err
		}
		if len(thread.Messages) > 0 {
			threads = append(threads, thread)
		}
	}

	return threads, nil
}

func fetchThread(ctx context.Context, db *sql.DB, threadID int64) (ThreadData, error) {
	thread := ThreadData{ThreadID: threadID}

	// Fetch thread name
	row := db.QueryRowContext(ctx, "SELECT name FROM threads WHERE id = ?", threadID)
	var threadName sql.NullString
	if err := row.Scan(&threadName); err != nil && err != sql.ErrNoRows {
		return thread, fmt.Errorf("fetching thread name: %w", err)
	}
	thread.ThreadName = threadName.String

	// Fetch messages
	rows, err := db.QueryContext(ctx, `
		SELECT
			m.id,
			m.thread_id,
			m.sender_id,
			m.text,
			m.timestamp_ms,
			c.name as sender_name
		FROM messages m
		LEFT JOIN contacts c ON m.sender_id = c.id
		WHERE m.thread_id = ? AND m.text IS NOT NULL AND m.text != ''
		ORDER BY m.timestamp_ms ASC
	`, threadID)
	if err != nil {
		return thread, fmt.Errorf("fetching messages: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var msg Message
		var senderName sql.NullString

		if err := rows.Scan(
			&msg.ID,
			&msg.ThreadID,
			&msg.SenderID,
			&msg.Text,
			&msg.TimestampMs,
			&senderName,
		); err != nil {
			return thread, fmt.Errorf("scanning message: %w", err)
		}

		msg.SenderName = senderName.String
		thread.Messages = append(thread.Messages, msg)
	}

	if err := rows.Err(); err != nil {
		return thread, fmt.Errorf("iterating messages: %w", err)
	}

	return thread, nil
}

// ChunkCallback is called for each chunk produced.
type ChunkCallback func(chunk Chunk) error

// ProcessAllThreads processes all threads and calls the callback for each chunk.
// Returns statistics about the processing.
func ProcessAllThreads(
	ctx context.Context,
	db *sql.DB,
	cfg *ragconfig.Config,
	callback ChunkCallback,
	progressFn func(threadsProcessed, totalChunks int),
) (*Stats, error) {
	threads, err := FetchThreads(ctx, db)
	if err != nil {
		return nil, err
	}

	stats := NewStats()

	for _, thread := range threads {
		chunks := ProcessThread(thread, cfg)

		stats.TotalMessages += len(thread.Messages)
		stats.TotalChunks += len(chunks)
		stats.ThreadsProcessed++

		for _, chunk := range chunks {
			if chunk.IsIndexable {
				stats.IndexableChunks++
			} else {
				stats.NonIndexableChunks++
			}
			stats.UpdateCharRange(chunk.CharCount)

			if callback != nil {
				if err := callback(chunk); err != nil {
					return stats, fmt.Errorf("callback error: %w", err)
				}
			}
		}

		if progressFn != nil && stats.ThreadsProcessed%100 == 0 {
			progressFn(stats.ThreadsProcessed, stats.TotalChunks)
		}
	}

	return stats, nil
}
