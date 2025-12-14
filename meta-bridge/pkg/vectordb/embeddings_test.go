package vectordb

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEmbeddingClient_DimensionMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{
			"data": [{"embedding": [0.1, 0.2], "index": 0}],
			"model": "test",
			"usage": {"prompt_tokens": 0, "total_tokens": 0}
		}`)
	}))
	defer srv.Close()

	c := NewEmbeddingClient(EmbeddingConfig{
		BaseURL:   srv.URL,
		Model:     "test",
		Dimension: 3,
	})

	_, err := c.EmbedBatch(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatalf("expected dimension mismatch error")
	}
}
