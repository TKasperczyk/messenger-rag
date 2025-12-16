package rag

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/rs/zerolog/log"

	"go.mau.fi/mautrix-meta/pkg/ragconfig"
)

// Service is the main RAG service that coordinates search operations
type Service struct {
	cfg     *ragconfig.Config
	vectors VectorSearcher
	bm25    BM25Searcher
	chunks  ChunkStore
	embed   Embedder
}

// VectorSearcher provides vector similarity search
type VectorSearcher interface {
	Search(ctx context.Context, embedding []float64, limit int, ef int) ([]VectorHit, error)
	Stats(ctx context.Context) (MilvusStats, error)
	Close() error
}

// BM25Searcher provides BM25 full-text search
type BM25Searcher interface {
	Search(ctx context.Context, query string, limit int) ([]BM25Hit, error)
	Stats(ctx context.Context) (SQLiteStats, error)
}

// ChunkStore provides chunk retrieval and context expansion
type ChunkStore interface {
	GetContext(ctx context.Context, threadID int64, sessionIdx, chunkIdx, radius int) ([]ContextChunk, error)
	GetByID(ctx context.Context, chunkID string) (*Chunk, error)
}

// Embedder generates embeddings for text
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float64, error)
	IsAvailable(ctx context.Context) bool
}

// NewService creates a new RAG service with the given dependencies
func NewService(cfg *ragconfig.Config, vectors VectorSearcher, bm25 BM25Searcher, chunks ChunkStore, embed Embedder) *Service {
	return &Service{
		cfg:     cfg,
		vectors: vectors,
		bm25:    bm25,
		chunks:  chunks,
		embed:   embed,
	}
}

// Search performs a search based on the request parameters
func (s *Service) Search(ctx context.Context, req SearchRequest) (*SearchResponse, error) {
	start := time.Now()

	// Apply defaults and clamp values
	req = s.normalizeRequest(req)

	var results []Hit
	var err error

	switch req.Mode {
	case ModeVector:
		results, err = s.vectorSearch(ctx, req)
	case ModeBM25:
		results, err = s.bm25Search(ctx, req)
	case ModeHybrid:
		results, err = s.hybridSearch(ctx, req)
	default:
		return nil, fmt.Errorf("invalid search mode: %s", req.Mode)
	}

	if err != nil {
		return nil, err
	}

	// Add context if requested
	if req.Context > 0 {
		results, err = s.addContext(ctx, results, req.Context)
		if err != nil {
			// Log but don't fail - context is optional
			log.Warn().Err(err).Msg("context expansion failed")
		}
	}

	weights := s.getWeights(req)

	return &SearchResponse{
		Query:   req.Query,
		Mode:    req.Mode,
		Limit:   req.Limit,
		Context: req.Context,
		RrfK:    s.getRrfK(req),
		Weights: weights,
		TookMs:  time.Since(start).Milliseconds(),
		Results: results,
	}, nil
}

// normalizeRequest applies defaults and clamps values
func (s *Service) normalizeRequest(req SearchRequest) SearchRequest {
	if req.Mode == "" {
		req.Mode = ModeHybrid
	}

	if req.Limit <= 0 {
		req.Limit = 20
	} else if req.Limit > 100 {
		req.Limit = 100
	}

	if req.Context < 0 {
		req.Context = 0
	} else if req.Context > 5 {
		req.Context = 5
	}

	if req.CandMult <= 0 {
		req.CandMult = 2
	} else if req.CandMult > 10 {
		req.CandMult = 10 // Cap to prevent excessive candidate fetching
	}

	return req
}

// getRrfK returns the RRF k parameter
func (s *Service) getRrfK(req SearchRequest) int {
	if req.RrfK > 0 {
		return req.RrfK
	}
	if s.cfg.Hybrid.RRF.K > 0 {
		return s.cfg.Hybrid.RRF.K
	}
	return 60
}

// getWeights returns normalized weights
func (s *Service) getWeights(req SearchRequest) Weights {
	wv := req.WeightVec
	wb := req.WeightBM25

	if (wv <= 0 && wb <= 0) || !isFinite(wv) || !isFinite(wb) {
		wv = s.cfg.Hybrid.Weights.Vector
		wb = s.cfg.Hybrid.Weights.BM25
	}

	sum := wv + wb
	if sum <= 0 || !isFinite(sum) {
		return Weights{Vector: 0.5, BM25: 0.5}
	}

	vector := wv / sum
	bm25 := wb / sum
	if !isFinite(vector) || !isFinite(bm25) {
		return Weights{Vector: 0.5, BM25: 0.5}
	}

	return Weights{Vector: vector, BM25: bm25}
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func (s *Service) vectorCandidates(ctx context.Context, embedding []float64, want int) ([]VectorHit, error) {
	if want <= 0 {
		return []VectorHit{}, nil
	}

	fetchMult := s.cfg.Milvus.Search.FetchMultiplier
	if fetchMult < 1 {
		fetchMult = 1
	}

	fetchLimit := want * fetchMult
	ef := s.cfg.Milvus.Search.Ef
	if fetchLimit > ef {
		ef = fetchLimit
	}

	vectorHits, err := s.vectors.Search(ctx, embedding, fetchLimit, ef)
	if err != nil {
		return nil, err
	}

	vectorHits = filterVectorHits(s.cfg, vectorHits)
	if len(vectorHits) > want {
		vectorHits = vectorHits[:want]
	}

	return vectorHits, nil
}

// vectorSearch performs vector-only search
func (s *Service) vectorSearch(ctx context.Context, req SearchRequest) ([]Hit, error) {
	// Get embedding for query
	embedding, err := s.embed.Embed(ctx, req.Query)
	if err != nil {
		return nil, fmt.Errorf("embedding query: %w", err)
	}

	vectorHits, err := s.vectorCandidates(ctx, embedding, req.Limit)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}

	// Convert to hits
	results := make([]Hit, 0, len(vectorHits))
	for i, vh := range vectorHits {
		rank := i + 1
		score := vh.Score

		results = append(results, Hit{
			Chunk:       vh.Chunk,
			VectorRank:  &rank,
			VectorScore: &score,
		})
	}

	return results, nil
}

// bm25Search performs BM25-only search
func (s *Service) bm25Search(ctx context.Context, req SearchRequest) ([]Hit, error) {
	bm25Hits, err := s.bm25.Search(ctx, req.Query, req.Limit)
	if err != nil {
		return nil, fmt.Errorf("bm25 search: %w", err)
	}

	results := make([]Hit, 0, len(bm25Hits))
	for i, bh := range bm25Hits {
		rank := i + 1
		score := bh.Score

		results = append(results, Hit{
			Chunk:     bh.Chunk,
			BM25Rank:  &rank,
			BM25Score: &score,
		})
	}

	return results, nil
}

// hybridSearch performs hybrid RRF fusion search with graceful degradation.
// If one search fails, it falls back to single-mode search rather than failing entirely.
func (s *Service) hybridSearch(ctx context.Context, req SearchRequest) ([]Hit, error) {
	// Get embedding for query
	embedding, err := s.embed.Embed(ctx, req.Query)
	if err != nil {
		// If embedding fails, fall back to BM25-only search
		return s.bm25Search(ctx, req)
	}

	// Match TypeScript behavior: if hybrid is disabled, do vector-only fallback
	// but keep RRF scoring/ranks.
	if !s.cfg.Hybrid.Enabled {
		vectorHits, err := s.vectorCandidates(ctx, embedding, req.Limit)
		if err != nil {
			return nil, fmt.Errorf("vector search: %w", err)
		}

		k := s.getRrfK(req)
		weights := s.getWeights(req)

		results := make([]Hit, 0, len(vectorHits))
		for i, vh := range vectorHits {
			rank := i + 1
			score := vh.Score
			rrfScore := weights.Vector / float64(k+rank)

			results = append(results, Hit{
				Chunk:       vh.Chunk,
				VectorRank:  &rank,
				VectorScore: &score,
				RrfScore:    &rrfScore,
			})
		}

		return results, nil
	}

	candidates := req.Limit * req.CandMult

	// Run both searches in parallel
	type vectorResult struct {
		hits []VectorHit
		err  error
	}
	type bm25Result struct {
		hits []BM25Hit
		err  error
	}

	vectorCh := make(chan vectorResult, 1)
	bm25Ch := make(chan bm25Result, 1)

	go func() {
		hits, err := s.vectorCandidates(ctx, embedding, candidates)
		vectorCh <- vectorResult{hits, err}
	}()

	go func() {
		hits, err := s.bm25.Search(ctx, req.Query, candidates)
		bm25Ch <- bm25Result{hits, err}
	}()

	vr := <-vectorCh
	br := <-bm25Ch

	// Graceful degradation: if one search fails, fall back to the other
	vectorOK := vr.err == nil
	bm25OK := br.err == nil

	if !vectorOK && !bm25OK {
		// Both failed - return error with both reasons
		return nil, fmt.Errorf("both searches failed: vector=%v, bm25=%v", vr.err, br.err)
	}

	if !vectorOK {
		// Vector failed, fall back to BM25-only
		// Convert BM25 hits to Hit slice with RRF-style scoring
		k := s.getRrfK(req)
		weights := s.getWeights(req)

		results := make([]Hit, 0, len(br.hits))
		for i, bh := range br.hits {
			if i >= req.Limit {
				break
			}
			rank := i + 1
			score := bh.Score
			rrfScore := weights.BM25 / float64(k+rank)

			results = append(results, Hit{
				Chunk:     bh.Chunk,
				BM25Rank:  &rank,
				BM25Score: &score,
				RrfScore:  &rrfScore,
			})
		}
		return results, nil
	}

	if !bm25OK {
		// BM25 failed, fall back to vector-only
		k := s.getRrfK(req)
		weights := s.getWeights(req)

		results := make([]Hit, 0, len(vr.hits))
		for i, vh := range vr.hits {
			if i >= req.Limit {
				break
			}
			rank := i + 1
			score := vh.Score
			rrfScore := weights.Vector / float64(k+rank)

			results = append(results, Hit{
				Chunk:       vh.Chunk,
				VectorRank:  &rank,
				VectorScore: &score,
				RrfScore:    &rrfScore,
			})
		}
		return results, nil
	}

	// Both succeeded - fuse results using RRF
	return s.fuseRRF(vr.hits, br.hits, req), nil
}

// fuseRRF combines vector and BM25 results using Reciprocal Rank Fusion
func (s *Service) fuseRRF(vectorHits []VectorHit, bm25Hits []BM25Hit, req SearchRequest) []Hit {
	k := s.getRrfK(req)
	weights := s.getWeights(req)

	// Build rank maps
	vectorRanks := make(map[string]int)
	vectorScores := make(map[string]float64)
	for i, vh := range vectorHits {
		vectorRanks[vh.ChunkID] = i + 1
		vectorScores[vh.ChunkID] = vh.Score
	}

	bm25Ranks := make(map[string]int)
	bm25Scores := make(map[string]float64)
	for i, bh := range bm25Hits {
		bm25Ranks[bh.ChunkID] = i + 1
		bm25Scores[bh.ChunkID] = bh.Score
	}

	// Collect all unique chunks
	chunkMap := make(map[string]Chunk)
	for _, vh := range vectorHits {
		chunkMap[vh.ChunkID] = vh.Chunk
	}
	for _, bh := range bm25Hits {
		if _, exists := chunkMap[bh.ChunkID]; !exists {
			chunkMap[bh.ChunkID] = bh.Chunk
		}
	}

	// Calculate RRF scores
	results := make([]Hit, 0, len(chunkMap))
	for chunkID, chunk := range chunkMap {
		var rrfScore float64
		var vectorRank, bm25Rank *int
		var vectorScore, bm25Score *float64

		if vr, ok := vectorRanks[chunkID]; ok {
			vectorRank = &vr
			vs := vectorScores[chunkID]
			vectorScore = &vs
			rrfScore += weights.Vector / float64(k+vr)
		}

		if br, ok := bm25Ranks[chunkID]; ok {
			bm25Rank = &br
			bs := bm25Scores[chunkID]
			bm25Score = &bs
			rrfScore += weights.BM25 / float64(k+br)
		}

		results = append(results, Hit{
			Chunk:       chunk,
			VectorRank:  vectorRank,
			VectorScore: vectorScore,
			BM25Rank:    bm25Rank,
			BM25Score:   bm25Score,
			RrfScore:    &rrfScore,
		})
	}

	// Sort by RRF score with tiebreakers
	sortHits(results)

	// Limit results
	if len(results) > req.Limit {
		results = results[:req.Limit]
	}

	return results
}

// addContext adds surrounding chunks to each hit
func (s *Service) addContext(ctx context.Context, hits []Hit, radius int) ([]Hit, error) {
	failures := 0
	var lastErr error

	for i := range hits {
		hit := &hits[i]

		contextChunks, err := s.chunks.GetContext(ctx, hit.ThreadID, hit.SessionIdx, hit.ChunkIdx, radius)
		if err != nil {
			failures++
			lastErr = err
			continue // Skip on error
		}

		for _, cc := range contextChunks {
			if cc.ChunkID == hit.ChunkID {
				continue
			}
			if cc.ChunkIdx < hit.ChunkIdx {
				hit.ContextBefore = append(hit.ContextBefore, cc)
			} else {
				hit.ContextAfter = append(hit.ContextAfter, cc)
			}
		}
	}

	if failures > 0 && lastErr != nil {
		return hits, fmt.Errorf("context expansion failed for %d/%d hits: %w", failures, len(hits), lastErr)
	}

	return hits, nil
}

// Stats returns statistics about the RAG system
func (s *Service) Stats(ctx context.Context) (*StatsResponse, error) {
	milvusStats, err := s.vectors.Stats(ctx)
	if err != nil {
		milvusStats = MilvusStats{Connected: false}
	}

	sqliteStats, err := s.bm25.Stats(ctx)
	if err != nil {
		sqliteStats = SQLiteStats{Connected: false}
	}

	return &StatsResponse{
		Milvus: milvusStats,
		SQLite: sqliteStats,
		Config: ConfigInfo{
			Hash:       s.cfg.Hash(),
			Collection: s.cfg.Milvus.ChunkCollection,
			Model:      s.cfg.Embedding.Model,
			Dimension:  s.cfg.Embedding.Dimension,
		},
		Timestamp: time.Now(),
	}, nil
}

// Health returns the health status
func (s *Service) Health(ctx context.Context) *HealthResponse {
	milvusOK := false
	sqliteOK := false
	embeddingOK := false

	// Check Milvus
	if stats, err := s.vectors.Stats(ctx); err == nil && stats.Connected {
		milvusOK = true
	}

	// Check SQLite
	if stats, err := s.bm25.Stats(ctx); err == nil && stats.Connected {
		sqliteOK = true
	}

	// Check embedding service availability (lightweight /models probe via IsAvailable)
	if s.embed != nil && s.embed.IsAvailable(ctx) {
		embeddingOK = true
	}

	status := "ok"
	if !milvusOK || !sqliteOK || !embeddingOK {
		status = "degraded"
	}
	if !milvusOK && !sqliteOK {
		status = "unhealthy"
	}

	return &HealthResponse{
		Status:    status,
		Milvus:    milvusOK,
		SQLite:    sqliteOK,
		Embedding: embeddingOK,
		Timestamp: time.Now(),
	}
}

// Close closes all connections
func (s *Service) Close() error {
	if s.vectors != nil {
		return s.vectors.Close()
	}
	return nil
}
