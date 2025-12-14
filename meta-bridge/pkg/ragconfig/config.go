// Package ragconfig provides unified configuration for the RAG system.
// This is the SINGLE SOURCE OF TRUTH for all shared settings across
// Go, Python, and TypeScript components.
package ragconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config represents the unified RAG configuration
type Config struct {
	Milvus    MilvusConfig    `yaml:"milvus"`
	Embedding EmbeddingConfig `yaml:"embedding"`
	Chunking  ChunkingConfig  `yaml:"chunking"`
	Quality   QualityConfig   `yaml:"quality"`
	Hybrid    HybridConfig    `yaml:"hybrid"`
	Database  DatabaseConfig  `yaml:"database"`
	Metadata  MetadataConfig  `yaml:"metadata"`
}

type MilvusConfig struct {
	Address                   string            `yaml:"address"`
	ChunkCollection           string            `yaml:"chunk_collection"`
	LegacyMessageCollection   string            `yaml:"legacy_message_collection"`
	Index                     MilvusIndexConfig `yaml:"index"`
	Search                    MilvusSearchConfig `yaml:"search"`
}

type MilvusIndexConfig struct {
	Type           string `yaml:"type"`
	Metric         string `yaml:"metric"`
	M              int    `yaml:"m"`
	EfConstruction int    `yaml:"ef_construction"`
}

type MilvusSearchConfig struct {
	Ef              int `yaml:"ef"`
	FetchMultiplier int `yaml:"fetch_multiplier"`
}

type EmbeddingConfig struct {
	BaseURL   string `yaml:"base_url"`
	Model     string `yaml:"model"`
	Dimension int    `yaml:"dimension"`
	BatchSize int    `yaml:"batch_size"`
}

type ChunkingConfig struct {
	Version  int                    `yaml:"version"`
	Coalesce ChunkCoalesceConfig    `yaml:"coalesce"`
	Session  ChunkSessionConfig     `yaml:"session"`
	Size     ChunkSizeConfig        `yaml:"size"`
	Format   ChunkFormatConfig      `yaml:"format"`
}

type ChunkCoalesceConfig struct {
	MaxGapSeconds    int `yaml:"max_gap_seconds"`
	MaxCombinedChars int `yaml:"max_combined_chars"`
}

type ChunkSessionConfig struct {
	GapMinutes int `yaml:"gap_minutes"`
}

type ChunkSizeConfig struct {
	TargetChars int `yaml:"target_chars"`
	MaxChars    int `yaml:"max_chars"`
	MinChars    int `yaml:"min_chars"`
}

type ChunkFormatConfig struct {
	SenderPrefix    bool   `yaml:"sender_prefix"`
	TimestampFormat string `yaml:"timestamp_format"`
}

type QualityConfig struct {
	MinChars        int                    `yaml:"min_chars"`
	MinAlnumChars   int                    `yaml:"min_alnum_chars"`
	MinUniqueWords  int                    `yaml:"min_unique_words"`
	URLSpecialCase  URLSpecialCaseConfig   `yaml:"url_special_case"`
	Filters         QualityFiltersConfig   `yaml:"filters"`
}

type URLSpecialCaseConfig struct {
	Enabled       bool `yaml:"enabled"`
	MinAlnumChars int  `yaml:"min_alnum_chars"`
}

type QualityFiltersConfig struct {
	MaxURLDensity      float64 `yaml:"max_url_density"`
	SkipAttachmentOnly bool    `yaml:"skip_attachment_only"`
	SkipBase64Blobs    bool    `yaml:"skip_base64_blobs"`
}

type HybridConfig struct {
	Enabled bool              `yaml:"enabled"`
	RRF     RRFConfig         `yaml:"rrf"`
	Weights HybridWeights     `yaml:"weights"`
	BM25    BM25Config        `yaml:"bm25"`
}

type RRFConfig struct {
	K int `yaml:"k"`
}

type HybridWeights struct {
	Vector float64 `yaml:"vector"`
	BM25   float64 `yaml:"bm25"`
}

type BM25Config struct {
	Table string `yaml:"table"`
}

type DatabaseConfig struct {
	SQLite string `yaml:"sqlite"`
}

type MetadataConfig struct {
	Table string            `yaml:"table"`
	Keys  MetadataKeysConfig `yaml:"keys"`
}

type MetadataKeysConfig struct {
	EmbeddingModel   string `yaml:"embedding_model"`
	EmbeddingDim     string `yaml:"embedding_dim"`
	ChunkingVersion  string `yaml:"chunking_version"`
	ConfigHash       string `yaml:"config_hash"`
	IndexedAt        string `yaml:"indexed_at"`
}

// Default returns the default configuration
func Default() *Config {
	return &Config{
		Milvus: MilvusConfig{
			Address:                 "localhost:19530",
			ChunkCollection:         "messenger_message_chunks_v2",
			LegacyMessageCollection: "messenger_messages",
			Index: MilvusIndexConfig{
				Type:           "HNSW",
				Metric:         "COSINE",
				M:              16,
				EfConstruction: 256,
			},
			Search: MilvusSearchConfig{
				Ef:              128,
				FetchMultiplier: 3,
			},
		},
		Embedding: EmbeddingConfig{
			BaseURL:   "http://127.0.0.1:11434/v1",
			Model:     "qwen3-embedding:8b",
			Dimension: 4096,
			BatchSize: 50,
		},
		Chunking: ChunkingConfig{
			Version: 2,
			Coalesce: ChunkCoalesceConfig{
				MaxGapSeconds:    120,
				MaxCombinedChars: 900,
			},
			Session: ChunkSessionConfig{
				GapMinutes: 45,
			},
			Size: ChunkSizeConfig{
				TargetChars: 900,
				MaxChars:    1400,
				MinChars:    100,
			},
			Format: ChunkFormatConfig{
				SenderPrefix:    true,
				TimestampFormat: "",
			},
		},
		Quality: QualityConfig{
			MinChars:       250,
			MinAlnumChars:  140,
			MinUniqueWords: 8,
			URLSpecialCase: URLSpecialCaseConfig{
				Enabled:       true,
				MinAlnumChars: 60,
			},
			Filters: QualityFiltersConfig{
				MaxURLDensity:      0.5,
				SkipAttachmentOnly: true,
				SkipBase64Blobs:    true,
			},
		},
		Hybrid: HybridConfig{
			Enabled: true,
			RRF: RRFConfig{
				K: 60,
			},
			Weights: HybridWeights{
				Vector: 0.5,
				BM25:   0.5,
			},
			BM25: BM25Config{
				Table: "chunks_fts",
			},
		},
		Database: DatabaseConfig{
			SQLite: "messenger.db",
		},
		Metadata: MetadataConfig{
			Table: "rag_metadata",
			Keys: MetadataKeysConfig{
				EmbeddingModel:  "rag_embedding_model",
				EmbeddingDim:    "rag_embedding_dim",
				ChunkingVersion: "rag_chunking_version",
				ConfigHash:      "rag_config_hash",
				IndexedAt:       "rag_indexed_at",
			},
		},
	}
}

// Load reads configuration from a YAML file
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	cfg := Default() // Start with defaults
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return cfg, nil
}

// LoadFromDir looks for rag.yaml in the given directory or parent directories
func LoadFromDir(dir string) (*Config, error) {
	// Walk up the directory tree looking for rag.yaml
	current := dir
	for {
		path := filepath.Join(current, "rag.yaml")
		if _, err := os.Stat(path); err == nil {
			return Load(path)
		}

		parent := filepath.Dir(current)
		if parent == current {
			break // Reached root
		}
		current = parent
	}

	return nil, fmt.Errorf("rag.yaml not found in %s or parent directories", dir)
}

// LoadOrDefault tries to load from rag.yaml, falls back to defaults
func LoadOrDefault(dir string) *Config {
	cfg, err := LoadFromDir(dir)
	if err != nil {
		return Default()
	}
	return cfg
}

// Hash returns a SHA256 hash of the configuration for change detection
func (c *Config) Hash() string {
	data, _ := yaml.Marshal(c)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// EmbeddingIdentity returns a string identifying the embedding configuration
// Use this to detect mismatches between index and query embeddings
func (c *Config) EmbeddingIdentity() string {
	return fmt.Sprintf("%s:%s:%d", c.Embedding.BaseURL, c.Embedding.Model, c.Embedding.Dimension)
}
