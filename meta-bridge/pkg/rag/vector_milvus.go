package rag

import (
	"context"
	"encoding/json"
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
					hit.ChunkID, _ = col.ValueByIdx(i)
				}
			case "thread_id":
				if col, ok := field.(*entity.ColumnInt64); ok {
					hit.ThreadID, _ = col.ValueByIdx(i)
				}
			case "thread_name":
				if col, ok := field.(*entity.ColumnVarChar); ok {
					hit.ThreadName, _ = col.ValueByIdx(i)
				}
			case "text":
				if col, ok := field.(*entity.ColumnVarChar); ok {
					hit.Text, _ = col.ValueByIdx(i)
				}
			case "participant_ids":
				if col, ok := field.(*entity.ColumnVarChar); ok {
					jsonStr, _ := col.ValueByIdx(i)
					hit.ParticipantIDs = parseJSONIntArray(jsonStr)
				}
			case "participant_names":
				if col, ok := field.(*entity.ColumnVarChar); ok {
					jsonStr, _ := col.ValueByIdx(i)
					hit.ParticipantNames = parseJSONStringArray(jsonStr)
				}
			case "message_ids":
				if col, ok := field.(*entity.ColumnVarChar); ok {
					jsonStr, _ := col.ValueByIdx(i)
					hit.MessageIDs = parseJSONStringArray(jsonStr)
				}
			case "start_timestamp_ms":
				if col, ok := field.(*entity.ColumnInt64); ok {
					hit.StartTimestampMs, _ = col.ValueByIdx(i)
				}
			case "end_timestamp_ms":
				if col, ok := field.(*entity.ColumnInt64); ok {
					hit.EndTimestampMs, _ = col.ValueByIdx(i)
				}
			case "message_count":
				if col, ok := field.(*entity.ColumnInt16); ok {
					val, _ := col.ValueByIdx(i)
					hit.MessageCount = int(val)
				}
			case "session_idx":
				if col, ok := field.(*entity.ColumnInt16); ok {
					val, _ := col.ValueByIdx(i)
					hit.SessionIdx = int(val)
				}
			case "chunk_idx":
				if col, ok := field.(*entity.ColumnInt16); ok {
					val, _ := col.ValueByIdx(i)
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

// parseJSONIntArray parses a JSON array string to []int64
func parseJSONIntArray(s string) []int64 {
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
		}
	}
	return result
}

// parseJSONStringArray parses a JSON array string to []string
func parseJSONStringArray(s string) []string {
	if s == "" {
		return nil
	}
	var arr []string
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		return nil
	}
	result := make([]string, 0, len(arr))
	for _, str := range arr {
		if str != "" {
			result = append(result, str)
		}
	}
	return result
}
