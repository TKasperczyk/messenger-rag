package vectordb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// EmbeddingClient generates embeddings via LMStudio's OpenAI-compatible API
type EmbeddingClient struct {
	baseURL    string
	model      string
	httpClient *http.Client
	dimension  int
}

// EmbeddingConfig holds configuration for the embedding client
type EmbeddingConfig struct {
	BaseURL   string // LMStudio server URL (default: http://127.0.0.1:1234/v1)
	Model     string // Embedding model name (default: text-embedding-qwen3-embedding-8b)
	Dimension int    // Vector dimension (default: 4096 for qwen3)
}

// DefaultEmbeddingConfig returns sensible defaults.
// Note: If you change the embedding model, you must recreate the vector collection
// using -drop-collection flag, as dimensions will differ between models.
func DefaultEmbeddingConfig() EmbeddingConfig {
	return EmbeddingConfig{
		BaseURL:   "http://127.0.0.1:1234/v1",
		Model:     "text-embedding-qwen3-embedding-8b",
		Dimension: 4096,
	}
}

// NewEmbeddingClient creates a new embedding client
func NewEmbeddingClient(cfg EmbeddingConfig) *EmbeddingClient {
	defaults := DefaultEmbeddingConfig()
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaults.BaseURL
	}
	if cfg.Model == "" {
		cfg.Model = defaults.Model
	}
	if cfg.Dimension == 0 {
		cfg.Dimension = defaults.Dimension
	}

	return &EmbeddingClient{
		baseURL:   cfg.BaseURL,
		model:     cfg.Model,
		dimension: cfg.Dimension,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// embeddingRequest is the request body for the embeddings API
type embeddingRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

// embeddingResponse is the response from the embeddings API
type embeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Model string `json:"model"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

// Embed generates an embedding for a single text
func (c *EmbeddingClient) Embed(ctx context.Context, text string) ([]float32, error) {
	embeddings, err := c.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(embeddings) == 0 {
		return nil, fmt.Errorf("no embeddings returned")
	}
	return embeddings[0], nil
}

// EmbedBatch generates embeddings for multiple texts
func (c *EmbeddingClient) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	reqBody := embeddingRequest{
		Input: texts,
		Model: c.model,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/embeddings", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errBody bytes.Buffer
		errBody.ReadFrom(resp.Body)
		return nil, fmt.Errorf("embedding API returned status %d: %s", resp.StatusCode, errBody.String())
	}

	var embResp embeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&embResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Sort by index to ensure correct order
	result := make([][]float32, len(texts))
	for _, data := range embResp.Data {
		if c.dimension > 0 && len(data.Embedding) != c.dimension {
			return nil, fmt.Errorf("embedding dimension mismatch: expected %d, got %d", c.dimension, len(data.Embedding))
		}
		if data.Index < len(result) {
			result[data.Index] = data.Embedding
		}
	}

	for i, emb := range result {
		if emb == nil {
			return nil, fmt.Errorf("missing embedding for index %d", i)
		}
	}

	return result, nil
}

// Dimension returns the embedding dimension
func (c *EmbeddingClient) Dimension() int {
	return c.dimension
}

// IsAvailable checks if the embedding service is available
func (c *EmbeddingClient) IsAvailable(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/models", nil)
	if err != nil {
		return false
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}
