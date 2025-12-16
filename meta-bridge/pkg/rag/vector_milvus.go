package rag

import (
	"context"
	"fmt"
	"strings"

	"github.com/milvus-io/milvus-sdk-go/v2/client"
	"github.com/milvus-io/milvus-sdk-go/v2/entity"

	"go.mau.fi/mautrix-meta/pkg/ragconfig"
)

// MilvusVectorSearcher implements VectorSearcher using Milvus
type MilvusVectorSearcher struct {
	client     client.Client
	collection string
	cfg        *ragconfig.Config
}

// NewMilvusVectorSearcher creates a new Milvus vector searcher
func NewMilvusVectorSearcher(ctx context.Context, cfg *ragconfig.Config) (*MilvusVectorSearcher, error) {
	c, err := client.NewClient(ctx, client.Config{
		Address: cfg.Milvus.Address,
	})
	if err != nil {
		return nil, fmt.Errorf("connecting to Milvus: %w", err)
	}
	needsClose := true
	defer func() {
		if needsClose {
			_ = c.Close()
		}
	}()

	collection := cfg.Milvus.ChunkCollection

	// Load collection if not already loaded
	loaded, err := c.GetLoadState(ctx, collection, nil)
	if err != nil {
		return nil, fmt.Errorf("checking collection load state: %w", err)
	}
	if loaded != entity.LoadStateLoaded {
		if err := c.LoadCollection(ctx, collection, false); err != nil {
			return nil, fmt.Errorf("loading collection: %w", err)
		}
	}

	needsClose = false
	return &MilvusVectorSearcher{
		client:     c,
		collection: collection,
		cfg:        cfg,
	}, nil
}

// Search performs a vector similarity search
func (m *MilvusVectorSearcher) Search(ctx context.Context, embedding []float64, limit int, ef int) ([]VectorHit, error) {
	// Convert float64 to float32 for Milvus
	vec := make([]float32, len(embedding))
	for i, v := range embedding {
		vec[i] = float32(v)
	}

	vectors := []entity.Vector{entity.FloatVector(vec)}

	// Output fields to retrieve
	outputFields := []string{
		"chunk_id",
		"thread_id",
		"thread_name",
		"participant_ids",
		"participant_names",
		"text",
		"message_ids",
		"start_timestamp_ms",
		"end_timestamp_ms",
		"message_count",
		"session_idx",
		"chunk_idx",
	}

	// Search parameters
	sp, err := entity.NewIndexHNSWSearchParam(ef)
	if err != nil {
		return nil, fmt.Errorf("creating search params: %w", err)
	}

	results, err := m.client.Search(
		ctx,
		m.collection,
		nil, // partitions
		"",  // expression filter
		outputFields,
		vectors,
		"embedding",
		milvusMetricFromConfig(m.cfg.Milvus.Index.Metric),
		limit,
		sp,
	)
	if err != nil {
		return nil, fmt.Errorf("Milvus search: %w", err)
	}

	if len(results) == 0 {
		return []VectorHit{}, nil
	}

	// Parse results
	hits := make([]VectorHit, 0, results[0].ResultCount)
	for i := 0; i < results[0].ResultCount; i++ {
		hit := VectorHit{
			Rank:  i + 1,
			Score: float64(results[0].Scores[i]),
		}

		// Extract fields
		for _, field := range results[0].Fields {
			switch field.Name() {
			case "chunk_id":
				if col, ok := field.(*entity.ColumnVarChar); ok {
					val, err := col.ValueByIdx(i)
					if err != nil {
						return nil, fmt.Errorf("extracting chunk_id at idx %d: %w", i, err)
					}
					hit.ChunkID = val
				}
			case "thread_id":
				if col, ok := field.(*entity.ColumnInt64); ok {
					val, err := col.ValueByIdx(i)
					if err != nil {
						return nil, fmt.Errorf("extracting thread_id at idx %d: %w", i, err)
					}
					hit.ThreadID = val
				}
			case "thread_name":
				if col, ok := field.(*entity.ColumnVarChar); ok {
					val, err := col.ValueByIdx(i)
					if err != nil {
						return nil, fmt.Errorf("extracting thread_name at idx %d: %w", i, err)
					}
					hit.ThreadName = val
				}
			case "text":
				if col, ok := field.(*entity.ColumnVarChar); ok {
					val, err := col.ValueByIdx(i)
					if err != nil {
						return nil, fmt.Errorf("extracting text at idx %d: %w", i, err)
					}
					hit.Text = val
				}
			case "participant_ids":
				if col, ok := field.(*entity.ColumnVarChar); ok {
					jsonStr, err := col.ValueByIdx(i)
					if err != nil {
						return nil, fmt.Errorf("extracting participant_ids at idx %d: %w", i, err)
					}
					hit.ParticipantIDs = parseIntArray(jsonStr)
				}
			case "participant_names":
				if col, ok := field.(*entity.ColumnVarChar); ok {
					jsonStr, err := col.ValueByIdx(i)
					if err != nil {
						return nil, fmt.Errorf("extracting participant_names at idx %d: %w", i, err)
					}
					hit.ParticipantNames = parseStringArray(jsonStr)
				}
			case "message_ids":
				if col, ok := field.(*entity.ColumnVarChar); ok {
					jsonStr, err := col.ValueByIdx(i)
					if err != nil {
						return nil, fmt.Errorf("extracting message_ids at idx %d: %w", i, err)
					}
					hit.MessageIDs = parseStringArray(jsonStr)
				}
			case "start_timestamp_ms":
				if col, ok := field.(*entity.ColumnInt64); ok {
					val, err := col.ValueByIdx(i)
					if err != nil {
						return nil, fmt.Errorf("extracting start_timestamp_ms at idx %d: %w", i, err)
					}
					hit.StartTimestampMs = val
				}
			case "end_timestamp_ms":
				if col, ok := field.(*entity.ColumnInt64); ok {
					val, err := col.ValueByIdx(i)
					if err != nil {
						return nil, fmt.Errorf("extracting end_timestamp_ms at idx %d: %w", i, err)
					}
					hit.EndTimestampMs = val
				}
			case "message_count":
				if col, ok := field.(*entity.ColumnInt16); ok {
					val, err := col.ValueByIdx(i)
					if err != nil {
						return nil, fmt.Errorf("extracting message_count at idx %d: %w", i, err)
					}
					hit.MessageCount = int(val)
				}
			case "session_idx":
				if col, ok := field.(*entity.ColumnInt16); ok {
					val, err := col.ValueByIdx(i)
					if err != nil {
						return nil, fmt.Errorf("extracting session_idx at idx %d: %w", i, err)
					}
					hit.SessionIdx = int(val)
				}
			case "chunk_idx":
				if col, ok := field.(*entity.ColumnInt16); ok {
					val, err := col.ValueByIdx(i)
					if err != nil {
						return nil, fmt.Errorf("extracting chunk_idx at idx %d: %w", i, err)
					}
					hit.ChunkIdx = int(val)
				}
			}
		}

		hits = append(hits, hit)
	}

	return hits, nil
}

func milvusMetricFromConfig(metric string) entity.MetricType {
	switch strings.ToUpper(strings.TrimSpace(metric)) {
	case "L2":
		return entity.L2
	case "IP", "INNER_PRODUCT":
		return entity.IP
	case "COSINE":
		return entity.COSINE
	default:
		return entity.COSINE
	}
}

// Stats returns Milvus collection statistics
func (m *MilvusVectorSearcher) Stats(ctx context.Context) (MilvusStats, error) {
	stats := MilvusStats{
		Connected:      true,
		Collection:     m.collection,
		EmbeddingModel: m.cfg.Embedding.Model,
		EmbeddingDim:   m.cfg.Embedding.Dimension,
		IndexType:      m.cfg.Milvus.Index.Type,
	}

	// Get collection statistics
	collStats, err := m.client.GetCollectionStatistics(ctx, m.collection)
	if err != nil {
		return stats, fmt.Errorf("getting collection stats: %w", err)
	}

	if rowCount, ok := collStats["row_count"]; ok {
		fmt.Sscanf(rowCount, "%d", &stats.RowCount)
	}

	return stats, nil
}

// Close closes the Milvus connection
func (m *MilvusVectorSearcher) Close() error {
	return m.client.Close()
}
