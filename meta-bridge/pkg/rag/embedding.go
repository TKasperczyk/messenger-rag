package rag

import (
	"context"

	"go.mau.fi/mautrix-meta/pkg/ragconfig"
	"go.mau.fi/mautrix-meta/pkg/vectordb"
)

// EmbeddingClientAdapter wraps vectordb.EmbeddingClient to implement the Embedder interface.
// This unifies embedding logic - vectordb.EmbeddingClient is the canonical implementation
// with batch support, proper timeouts, and dimension validation.
type EmbeddingClientAdapter struct {
	client *vectordb.EmbeddingClient
}

// NewEmbeddingClientAdapter creates a new adapter wrapping vectordb.EmbeddingClient
func NewEmbeddingClientAdapter(cfg *ragconfig.Config) *EmbeddingClientAdapter {
	client := vectordb.NewEmbeddingClient(vectordb.EmbeddingConfig{
		BaseURL:   cfg.Embedding.BaseURL,
		Model:     cfg.Embedding.Model,
		Dimension: cfg.Embedding.Dimension,
	})
	return &EmbeddingClientAdapter{client: client}
}

// Embed generates an embedding for the given text, converting float32 to float64
func (a *EmbeddingClientAdapter) Embed(ctx context.Context, text string) ([]float64, error) {
	embedding32, err := a.client.Embed(ctx, text)
	if err != nil {
		return nil, err
	}

	// Convert []float32 to []float64 for the rag service interface
	embedding64 := make([]float64, len(embedding32))
	for i, v := range embedding32 {
		embedding64[i] = float64(v)
	}

	return embedding64, nil
}

// IsAvailable checks if the embedding service is available
func (a *EmbeddingClientAdapter) IsAvailable(ctx context.Context) bool {
	return a.client.IsAvailable(ctx)
}
