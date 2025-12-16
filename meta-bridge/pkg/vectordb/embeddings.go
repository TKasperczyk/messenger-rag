package vectordb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
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
			Transport: &http.Transport{
				DisableKeepAlives: true, // Fresh connection each request (fixes LMStudio crashes)
			},
		},
	}
}

// embeddingRequest is the request body for the embeddings API (batch)
type embeddingRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

// embeddingRequestSingle is for single text (avoids LMStudio batch code path bug)
type embeddingRequestSingle struct {
	Input string `json:"input"`
	Model string `json:"model"`
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
// Uses curl subprocess because Go's http.Client causes LMStudio crashes
// Includes retry logic to handle transient LMStudio crashes (model auto-reloads)
func (c *EmbeddingClient) Embed(ctx context.Context, text string) ([]float32, error) {
	// Trim trailing whitespace - trailing newlines crash EmbeddingGemma model
	text = strings.TrimSpace(text)

	reqBody := embeddingRequestSingle{
		Input: text,
		Model: c.model,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	const maxRetries = 3
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			// Wait for LMStudio to reload the model (takes ~5-10s)
			waitTime := 10 * time.Second
			log.Warn().
				Int("attempt", attempt+1).
				Int("max_retries", maxRetries).
				Dur("wait", waitTime).
				Msg("Retrying embedding request after model reload")
			time.Sleep(waitTime)
		}

		// Write JSON to temp file to avoid shell escaping issues
		tmpFile, err := os.CreateTemp("", "embed-*.json")
		if err != nil {
			return nil, fmt.Errorf("failed to create temp file: %w", err)
		}

		if _, err := tmpFile.Write(jsonBody); err != nil {
			tmpFile.Close()
			os.Remove(tmpFile.Name())
			return nil, fmt.Errorf("failed to write temp file: %w", err)
		}
		tmpFile.Close()

		// Use curl subprocess with @file and timeout
		cmd := exec.CommandContext(ctx, "curl", "-s", "-X", "POST",
			"--max-time", "30", // 30s timeout - model crashes can hang
			c.baseURL+"/embeddings",
			"-H", "Content-Type: application/json",
			"-d", "@"+tmpFile.Name())

		output, err := cmd.Output()
		os.Remove(tmpFile.Name())

		if err != nil {
			lastErr = fmt.Errorf("curl failed: %w", err)
			continue
		}

		var embResp embeddingResponse
		if err := json.Unmarshal(output, &embResp); err != nil {
			lastErr = fmt.Errorf("failed to decode embedding response: %w", err)
			continue
		}

		if len(embResp.Data) == 0 {
			// Model crashed - LMStudio returns empty data, will auto-reload
			lastErr = fmt.Errorf("model crashed, waiting for reload")
			log.Warn().
				Int("attempt", attempt+1).
				Msg("Embedding model crashed, will retry")
			continue
		}

		embedding := embResp.Data[0].Embedding
		if c.dimension > 0 && len(embedding) != c.dimension {
			return nil, fmt.Errorf("embedding dimension mismatch: expected %d, got %d", c.dimension, len(embedding))
		}

		// Small delay between requests
		time.Sleep(100 * time.Millisecond)

		return embedding, nil
	}

	log.Error().Int("max_retries", maxRetries).Err(lastErr).Msg("Embedding request failed after retries")
	return nil, fmt.Errorf("failed after %d retries: %w", maxRetries, lastErr)
}

// EmbedBatch generates embeddings for multiple texts
// Uses curl subprocess with retry logic to handle LMStudio crashes
func (c *EmbeddingClient) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	// Sanitize all texts - trim whitespace to avoid model crashes
	sanitized := make([]string, len(texts))
	for i, t := range texts {
		sanitized[i] = strings.TrimSpace(t)
	}

	reqBody := embeddingRequest{
		Input: sanitized,
		Model: c.model,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	const maxRetries = 3
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			// Wait for LMStudio to reload the model (takes ~10-15s for 8B model)
			waitTime := 15 * time.Second
			log.Warn().
				Int("attempt", attempt+1).
				Int("max_retries", maxRetries).
				Dur("wait", waitTime).
				Msg("Retrying batch embedding request after model reload")
			time.Sleep(waitTime)
		}

		// Write JSON to temp file to avoid shell escaping issues
		tmpFile, err := os.CreateTemp("", "embed-batch-*.json")
		if err != nil {
			return nil, fmt.Errorf("failed to create temp file: %w", err)
		}

		if _, err := tmpFile.Write(jsonBody); err != nil {
			tmpFile.Close()
			os.Remove(tmpFile.Name())
			return nil, fmt.Errorf("failed to write temp file: %w", err)
		}
		tmpFile.Close()

		// Use curl subprocess with timeout (batch can take longer)
		cmd := exec.CommandContext(ctx, "curl", "-s", "-X", "POST",
			"--max-time", "120", // 2 minute timeout for batch
			c.baseURL+"/embeddings",
			"-H", "Content-Type: application/json",
			"-d", "@"+tmpFile.Name())

		output, err := cmd.Output()
		os.Remove(tmpFile.Name())

		if err != nil {
			lastErr = fmt.Errorf("curl failed: %w", err)
			log.Warn().
				Int("attempt", attempt+1).
				Err(err).
				Msg("Batch embedding request failed")
			continue
		}

		// Check for error response (model crashed)
		if bytes.Contains(output, []byte("unloaded or crashed")) || bytes.Contains(output, []byte("\"error\"")) {
			lastErr = fmt.Errorf("model crashed (error response)")
			log.Warn().
				Int("attempt", attempt+1).
				Int("batch_size", len(texts)).
				Msg("Embedding model crashed during batch request, will retry")
			continue
		}

		var embResp embeddingResponse
		if err := json.Unmarshal(output, &embResp); err != nil {
			lastErr = fmt.Errorf("failed to decode embedding response: %w", err)
			continue
		}

		if len(embResp.Data) == 0 {
			lastErr = fmt.Errorf("empty response, model may have crashed")
			log.Warn().
				Int("attempt", attempt+1).
				Msg("Empty embedding batch response, will retry")
			continue
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

	log.Error().Int("max_retries", maxRetries).Err(lastErr).Msg("Batch embedding request failed after retries")
	return nil, fmt.Errorf("failed after %d retries: %w", maxRetries, lastErr)
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
