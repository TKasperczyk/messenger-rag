// Package chunking implements message chunking for RAG embedding.
//
// This is a Go port of the Python generate_chunks.py script.
// It implements:
// 1. Message coalescing: Merge same-sender consecutive messages
// 2. Session splitting: New session when gap > 45 minutes
// 3. Greedy packing: Target 900 chars, max 1400 chars per chunk
// 4. Quality filters: Only index semantically meaningful chunks
// 5. Boundary detection: Time gaps, topic markers, URLs
package chunking

// Message represents a single message from the database.
type Message struct {
	ID          string
	ThreadID    int64
	SenderID    int64
	SenderName  string
	Text        string
	TimestampMs int64
}

// CoalescedMessage is a message composed of multiple original messages
// from the same sender within a short time window.
type CoalescedMessage struct {
	MessageIDs       []string
	ThreadID         int64
	SenderID         int64
	SenderName       string
	Text             string
	StartTimestampMs int64
	EndTimestampMs   int64
}

// Chunk is a chunk of conversation ready for embedding.
type Chunk struct {
	ChunkID          string   `json:"chunk_id"`
	ThreadID         int64    `json:"thread_id"`
	ThreadName       string   `json:"thread_name"`
	SessionIdx       int      `json:"session_idx"`
	ChunkIdx         int      `json:"chunk_idx"`
	MessageIDs       []string `json:"message_ids"`
	ParticipantIDs   []int64  `json:"participant_ids"`
	ParticipantNames []string `json:"participant_names"`
	Text             string   `json:"text"`
	StartTimestampMs int64    `json:"start_timestamp_ms"`
	EndTimestampMs   int64    `json:"end_timestamp_ms"`
	MessageCount     int      `json:"message_count"`
	IsIndexable      bool     `json:"is_indexable"`
	CharCount        int      `json:"char_count"`
	AlnumCount       int      `json:"alnum_count"`
	UniqueWordCount  int      `json:"unique_word_count"`
}

// Stats contains chunking statistics.
type Stats struct {
	ThreadsProcessed   int
	TotalMessages      int
	TotalChunks        int
	IndexableChunks    int
	NonIndexableChunks int
	CharRanges         map[string]int
}

// NewStats creates a new Stats with initialized CharRanges.
func NewStats() *Stats {
	return &Stats{
		CharRanges: map[string]int{
			"<100":      0,
			"100-250":   0,
			"250-500":   0,
			"500-900":   0,
			"900-1400":  0,
			">1400":     0,
		},
	}
}

// UpdateCharRange updates the character range statistics for a chunk.
func (s *Stats) UpdateCharRange(charCount int) {
	switch {
	case charCount < 100:
		s.CharRanges["<100"]++
	case charCount < 250:
		s.CharRanges["100-250"]++
	case charCount < 500:
		s.CharRanges["250-500"]++
	case charCount < 900:
		s.CharRanges["500-900"]++
	case charCount < 1400:
		s.CharRanges["900-1400"]++
	default:
		s.CharRanges[">1400"]++
	}
}
