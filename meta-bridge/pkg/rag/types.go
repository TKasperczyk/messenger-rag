// Package rag provides unified RAG (Retrieval-Augmented Generation) search
// capabilities combining vector search (Milvus) and BM25 search (SQLite FTS5).
//
// This is the authoritative backend for all search operations. CLI, web UI,
// and future MCP server should all use this package.
package rag

import "time"

// SearchMode specifies the search strategy
type SearchMode string

const (
	ModeVector SearchMode = "vector" // Vector-only search (Milvus)
	ModeBM25   SearchMode = "bm25"   // BM25-only search (SQLite FTS5)
	ModeHybrid SearchMode = "hybrid" // Hybrid RRF fusion of both
)

// SearchRequest contains parameters for a search operation
type SearchRequest struct {
	Query   string     `json:"q"`
	Mode    SearchMode `json:"mode"`
	Limit   int        `json:"limit"`
	Context int        `json:"context"` // Adjacent chunk radius (0 = disabled)

	// Optional overrides (use config defaults if zero)
	RrfK       int     `json:"rrf_k,omitempty"`
	WeightVec  float64 `json:"w_vector,omitempty"`
	WeightBM25 float64 `json:"w_bm25,omitempty"`
	CandMult   int     `json:"candidate_mult,omitempty"` // Candidate multiplier for fusion
}

// SearchResponse contains the search results and metadata
type SearchResponse struct {
	Query   string     `json:"query"`
	Mode    SearchMode `json:"mode"`
	Limit   int        `json:"limit"`
	Context int        `json:"context"`

	// Config values used
	RrfK    int     `json:"rrf_k"`
	Weights Weights `json:"weights"`

	// Timing
	TookMs int64 `json:"took_ms"`

	// Results ordered by relevance (best first)
	Results []Hit `json:"results"`
}

// Weights contains the normalized weights used for hybrid search
type Weights struct {
	Vector float64 `json:"vector"`
	BM25   float64 `json:"bm25"`
}

// Hit represents a single search result
type Hit struct {
	Chunk

	// Scoring info
	VectorRank *int     `json:"vector_rank"` // nil if not in vector results
	VectorScore *float64 `json:"vector_score"`
	BM25Rank   *int     `json:"bm25_rank"`   // nil if not in BM25 results
	BM25Score  *float64 `json:"bm25_score"`
	RrfScore   *float64 `json:"rrf_score"`   // nil for single-mode searches

	// Context (only populated if context > 0)
	ContextBefore []ContextChunk `json:"context_before,omitempty"`
	ContextAfter  []ContextChunk `json:"context_after,omitempty"`
}

// Chunk represents a message chunk from the database
type Chunk struct {
	ChunkID          string   `json:"chunk_id"`
	ThreadID         int64    `json:"thread_id"`
	ThreadName       string   `json:"thread_name"`
	ParticipantIDs   []int64  `json:"participant_ids"`
	ParticipantNames []string `json:"participant_names"`
	Text             string   `json:"text"`
	MessageIDs       []string `json:"message_ids"`
	StartTimestampMs int64    `json:"start_timestamp_ms"`
	EndTimestampMs   int64    `json:"end_timestamp_ms"`
	MessageCount     int      `json:"message_count"`
	SessionIdx       int      `json:"session_idx"`
	ChunkIdx         int      `json:"chunk_idx"`
}

// ContextChunk is a simplified chunk for context display
type ContextChunk struct {
	ChunkID     string `json:"chunk_id"`
	ChunkIdx    int    `json:"chunk_idx"`
	Text        string `json:"text"`
	IsIndexable bool   `json:"is_indexable"`
}

// VectorHit is an intermediate result from vector search
type VectorHit struct {
	Chunk
	Rank  int
	Score float64
}

// BM25Hit is an intermediate result from BM25 search
type BM25Hit struct {
	Chunk
	Rank  int
	Score float64 // Raw BM25 score (negative, lower = better)
}

// StatsResponse contains collection/database statistics
type StatsResponse struct {
	Milvus    MilvusStats    `json:"milvus"`
	SQLite    SQLiteStats    `json:"sqlite"`
	Config    ConfigInfo     `json:"config"`
	Timestamp time.Time      `json:"timestamp"`
}

// MilvusStats contains Milvus collection statistics
type MilvusStats struct {
	Connected      bool   `json:"connected"`
	Collection     string `json:"collection"`
	RowCount       int64  `json:"row_count"`
	IndexType      string `json:"index_type"`
	EmbeddingModel string `json:"embedding_model"`
	EmbeddingDim   int    `json:"embedding_dim"`
}

// SQLiteStats contains SQLite database statistics
type SQLiteStats struct {
	Connected     bool   `json:"connected"`
	ChunksTotal   int64  `json:"chunks_total"`
	ChunksIndexed int64  `json:"chunks_indexed"` // is_indexable = 1
	FtsTable      string `json:"fts_table"`
	FtsAvailable  bool   `json:"fts_available"`
}

// ConfigInfo contains configuration metadata
type ConfigInfo struct {
	Hash       string `json:"hash"`        // Config hash for change detection
	Collection string `json:"collection"`
	Model      string `json:"model"`
	Dimension  int    `json:"dimension"`
}

// HealthResponse for /health endpoint
type HealthResponse struct {
	Status    string    `json:"status"` // "ok", "degraded", "unhealthy"
	Milvus    bool      `json:"milvus"`
	SQLite    bool      `json:"sqlite"`
	Embedding bool      `json:"embedding"`
	Timestamp time.Time `json:"timestamp"`
}
