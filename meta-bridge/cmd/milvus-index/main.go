// milvus-index creates a Milvus collection and indexes chunks with embeddings.
//
// This is the Go equivalent of the Python insert_chunks_milvus_v2.py script.
// It reads indexable chunks from SQLite, generates embeddings, and inserts into Milvus.
//
// Usage:
//
//	milvus-index --db messenger.db
//	milvus-index --db messenger.db --drop  # Drop and recreate collection
//	milvus-index --db messenger.db --batch-size 50
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/milvus-io/milvus-sdk-go/v2/client"
	"github.com/milvus-io/milvus-sdk-go/v2/entity"
	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"go.mau.fi/mautrix-meta/pkg/ragconfig"
	"go.mau.fi/mautrix-meta/pkg/vectordb"
)

var (
	dbPath    = flag.String("db", "", "Path to SQLite database (defaults to database.sqlite from config)")
	cfgPath   = flag.String("config", "", "Path to rag.yaml (auto-detected if not specified)")
	dropFirst = flag.Bool("drop", false, "Drop existing collection before creating")
	batchSize = flag.Int("batch-size", 50, "Number of chunks to embed and insert per batch")
	debug     = flag.Bool("debug", false, "Enable debug logging")
)

func main() {
	flag.Parse()

	// Setup logging
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	if *debug {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	} else {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	// Load configuration
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load configuration")
	}

	sqlitePath := *dbPath
	if sqlitePath == "" {
		sqlitePath = cfg.Database.SQLite
	}
	if sqlitePath == "" {
		log.Fatal().Msg("SQLite database path is empty (set -db or database.sqlite in rag.yaml)")
	}

	fmt.Printf("Configuration:\n")
	fmt.Printf("  SQLite: %s\n", sqlitePath)
	fmt.Printf("  Milvus: %s\n", cfg.Milvus.Address)
	fmt.Printf("  Collection: %s\n", cfg.Milvus.ChunkCollection)
	fmt.Printf("  Embedding: %s (%d dim)\n", cfg.Embedding.Model, cfg.Embedding.Dimension)
	fmt.Printf("  Batch size: %d\n", *batchSize)
	fmt.Println()

	ctx := context.Background()

	// Open SQLite database
	db, err := sql.Open("sqlite3", sqlitePath+"?mode=ro")
	if err != nil {
		log.Fatal().Err(err).Str("path", sqlitePath).Msg("Failed to open database")
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatal().Err(err).Msg("Database not accessible")
	}

	// Connect to Milvus
	milvusClient, err := client.NewClient(ctx, client.Config{
		Address: cfg.Milvus.Address,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to Milvus")
	}
	defer milvusClient.Close()
	fmt.Printf("Connected to Milvus at %s\n", cfg.Milvus.Address)

	// Create embedding client
	embClient := vectordb.NewEmbeddingClient(vectordb.EmbeddingConfig{
		BaseURL:   cfg.Embedding.BaseURL,
		Model:     cfg.Embedding.Model,
		Dimension: cfg.Embedding.Dimension,
	})

	// Check embedding service
	if !embClient.IsAvailable(ctx) {
		log.Fatal().Msg("Embedding service not available at " + cfg.Embedding.BaseURL)
	}
	fmt.Printf("Embedding service available at %s\n", cfg.Embedding.BaseURL)

	// Handle collection creation
	collection := cfg.Milvus.ChunkCollection
	if *dropFirst {
		if err := dropCollection(ctx, milvusClient, collection); err != nil {
			log.Fatal().Err(err).Msg("Failed to drop collection")
		}
	}

	exists, err := milvusClient.HasCollection(ctx, collection)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to check collection existence")
	}

	if !exists {
		if err := createCollection(ctx, milvusClient, cfg); err != nil {
			log.Fatal().Err(err).Msg("Failed to create collection")
		}
	} else {
		fmt.Printf("Collection %s already exists, using existing\n", collection)
		// Load collection for insertion
		if err := milvusClient.LoadCollection(ctx, collection, false); err != nil {
			log.Warn().Err(err).Msg("Failed to load collection (may already be loaded)")
		}
	}

	// Count indexable chunks
	var totalChunks int
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM chunks WHERE is_indexable = 1").Scan(&totalChunks)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to count chunks")
	}
	fmt.Printf("Total indexable chunks: %d\n\n", totalChunks)

	if totalChunks == 0 {
		fmt.Println("No indexable chunks found. Run fts5-setup first.")
		return
	}

	// Process chunks in batches
	start := time.Now()
	inserted, err := indexChunks(ctx, db, milvusClient, embClient, cfg, *batchSize, totalChunks)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to index chunks")
	}

	// Flush
	fmt.Println("Flushing...")
	if err := milvusClient.Flush(ctx, collection, false); err != nil {
		log.Warn().Err(err).Msg("Failed to flush")
	}

	// Get final count
	stats, err := milvusClient.GetCollectionStatistics(ctx, collection)
	finalCount := int64(0)
	if err == nil {
		if rowCount, ok := stats["row_count"]; ok {
			fmt.Sscanf(rowCount, "%d", &finalCount)
		}
	}

	fmt.Println()
	fmt.Println("============================================================")
	fmt.Println("INDEXING COMPLETE")
	fmt.Println("============================================================")
	fmt.Printf("Total inserted: %d\n", inserted)
	fmt.Printf("Final collection size: %d\n", finalCount)
	fmt.Printf("Duration: %s\n", time.Since(start).Round(time.Second))
}

func loadConfig() (*ragconfig.Config, error) {
	if *cfgPath != "" {
		return ragconfig.Load(*cfgPath)
	}
	return ragconfig.LoadFromDir(".")
}

func dropCollection(ctx context.Context, c client.Client, collection string) error {
	exists, err := c.HasCollection(ctx, collection)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}

	fmt.Printf("Dropping existing collection %s...\n", collection)
	return c.DropCollection(ctx, collection)
}

func createCollection(ctx context.Context, c client.Client, cfg *ragconfig.Config) error {
	collection := cfg.Milvus.ChunkCollection
	dim := cfg.Embedding.Dimension

	fmt.Printf("Creating collection %s...\n", collection)

	schema := &entity.Schema{
		CollectionName: collection,
		Description:    "Messenger message chunks v2 - improved coherence",
		Fields: []*entity.Field{
			{
				Name:       "chunk_id",
				DataType:   entity.FieldTypeVarChar,
				PrimaryKey: true,
				TypeParams: map[string]string{"max_length": "32"},
			},
			{
				Name:     "thread_id",
				DataType: entity.FieldTypeInt64,
			},
			{
				Name:     "thread_name",
				DataType: entity.FieldTypeVarChar,
				TypeParams: map[string]string{"max_length": "512"},
			},
			{
				Name:     "session_idx",
				DataType: entity.FieldTypeInt16,
			},
			{
				Name:     "chunk_idx",
				DataType: entity.FieldTypeInt16,
			},
			{
				Name:     "participant_ids",
				DataType: entity.FieldTypeVarChar,
				TypeParams: map[string]string{"max_length": "1024"},
			},
			{
				Name:     "participant_names",
				DataType: entity.FieldTypeVarChar,
				TypeParams: map[string]string{"max_length": "2048"},
			},
			{
				Name:     "text",
				DataType: entity.FieldTypeVarChar,
				TypeParams: map[string]string{"max_length": "8192"},
			},
			{
				Name:     "message_ids",
				DataType: entity.FieldTypeVarChar,
				TypeParams: map[string]string{"max_length": "8192"},
			},
			{
				Name:     "start_timestamp_ms",
				DataType: entity.FieldTypeInt64,
			},
			{
				Name:     "end_timestamp_ms",
				DataType: entity.FieldTypeInt64,
			},
			{
				Name:     "message_count",
				DataType: entity.FieldTypeInt16,
			},
			{
				Name:     "embedding",
				DataType: entity.FieldTypeFloatVector,
				TypeParams: map[string]string{"dim": fmt.Sprintf("%d", dim)},
			},
		},
	}

	if err := c.CreateCollection(ctx, schema, entity.DefaultShardNumber); err != nil {
		return fmt.Errorf("creating collection: %w", err)
	}

	// Create HNSW index
	idx, err := entity.NewIndexHNSW(
		milvusMetricFromConfig(cfg.Milvus.Index.Metric),
		cfg.Milvus.Index.M,
		cfg.Milvus.Index.EfConstruction,
	)
	if err != nil {
		return fmt.Errorf("creating index params: %w", err)
	}

	if err := c.CreateIndex(ctx, collection, "embedding", idx, false); err != nil {
		return fmt.Errorf("creating index: %w", err)
	}

	// Load collection
	if err := c.LoadCollection(ctx, collection, false); err != nil {
		return fmt.Errorf("loading collection: %w", err)
	}

	fmt.Printf("Collection created with HNSW index (M=%d, ef_construction=%d)\n",
		cfg.Milvus.Index.M, cfg.Milvus.Index.EfConstruction)

	return nil
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

type chunkRow struct {
	ChunkID          string
	ThreadID         int64
	ThreadName       string
	SessionIdx       int
	ChunkIdx         int
	ParticipantIDs   string
	ParticipantNames string
	Text             string
	MessageIDs       string
	StartTimestampMs int64
	EndTimestampMs   int64
	MessageCount     int
}

func indexChunks(ctx context.Context, db *sql.DB, milvus client.Client, embClient *vectordb.EmbeddingClient, cfg *ragconfig.Config, batchSize, total int) (int, error) {
	collection := cfg.Milvus.ChunkCollection

	rows, err := db.QueryContext(ctx, `
		SELECT
			chunk_id, thread_id, thread_name, session_idx, chunk_idx,
			participant_ids, participant_names, text, message_ids,
			start_timestamp_ms, end_timestamp_ms, message_count
		FROM chunks
		WHERE is_indexable = 1
		ORDER BY thread_id, session_idx, chunk_idx
	`)
	if err != nil {
		return 0, fmt.Errorf("querying chunks: %w", err)
	}
	defer rows.Close()

	var batch []chunkRow
	inserted := 0
	batchNum := 0

	for rows.Next() {
		var chunk chunkRow
		var threadName sql.NullString

		if err := rows.Scan(
			&chunk.ChunkID,
			&chunk.ThreadID,
			&threadName,
			&chunk.SessionIdx,
			&chunk.ChunkIdx,
			&chunk.ParticipantIDs,
			&chunk.ParticipantNames,
			&chunk.Text,
			&chunk.MessageIDs,
			&chunk.StartTimestampMs,
			&chunk.EndTimestampMs,
			&chunk.MessageCount,
		); err != nil {
			return inserted, fmt.Errorf("scanning chunk: %w", err)
		}
		chunk.ThreadName = threadName.String
		batch = append(batch, chunk)

		if len(batch) >= batchSize {
			n, err := insertBatch(ctx, milvus, embClient, collection, batch, cfg.Embedding.Dimension)
			if err != nil {
				return inserted, fmt.Errorf("inserting batch %d: %w", batchNum, err)
			}
			inserted += n
			batchNum++

			if batchNum%10 == 0 {
				pct := float64(inserted) / float64(total) * 100
				fmt.Printf("  [%d/%d] %.1f%% - inserted %d chunks\n", inserted, total, pct, inserted)
			}

			batch = batch[:0]
		}
	}

	if err := rows.Err(); err != nil {
		return inserted, fmt.Errorf("iterating rows: %w", err)
	}

	// Insert remaining
	if len(batch) > 0 {
		n, err := insertBatch(ctx, milvus, embClient, collection, batch, cfg.Embedding.Dimension)
		if err != nil {
			return inserted, fmt.Errorf("inserting final batch: %w", err)
		}
		inserted += n
	}

	return inserted, nil
}

func insertBatch(ctx context.Context, milvus client.Client, embClient *vectordb.EmbeddingClient, collection string, chunks []chunkRow, dim int) (int, error) {
	if len(chunks) == 0 {
		return 0, nil
	}

	// Collect texts for embedding
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Text
	}

	// Generate embeddings
	embeddings, err := embClient.EmbedBatch(ctx, texts)
	if err != nil {
		return 0, fmt.Errorf("generating embeddings: %w", err)
	}

	// Prepare columns
	chunkIDs := make([]string, len(chunks))
	threadIDs := make([]int64, len(chunks))
	threadNames := make([]string, len(chunks))
	sessionIdxs := make([]int16, len(chunks))
	chunkIdxs := make([]int16, len(chunks))
	participantIDsList := make([]string, len(chunks))
	participantNamesList := make([]string, len(chunks))
	textList := make([]string, len(chunks))
	messageIDsList := make([]string, len(chunks))
	startTimestamps := make([]int64, len(chunks))
	endTimestamps := make([]int64, len(chunks))
	messageCounts := make([]int16, len(chunks))
	embeddingsList := make([][]float32, len(chunks))

	for i, c := range chunks {
		chunkIDs[i] = c.ChunkID
		threadIDs[i] = c.ThreadID
		threadNames[i] = truncate(c.ThreadName, 511)
		sessionIdxs[i] = int16(c.SessionIdx)
		chunkIdxs[i] = int16(c.ChunkIdx)
		participantIDsList[i] = truncateJSON(c.ParticipantIDs, 1023)
		participantNamesList[i] = truncateJSON(c.ParticipantNames, 2047)
		textList[i] = truncate(c.Text, 8191)
		messageIDsList[i] = truncateJSON(c.MessageIDs, 8191)
		startTimestamps[i] = c.StartTimestampMs
		endTimestamps[i] = c.EndTimestampMs
		messageCounts[i] = int16(c.MessageCount)
		embeddingsList[i] = embeddings[i]
	}

	// Create columns
	cols := []entity.Column{
		entity.NewColumnVarChar("chunk_id", chunkIDs),
		entity.NewColumnInt64("thread_id", threadIDs),
		entity.NewColumnVarChar("thread_name", threadNames),
		entity.NewColumnInt16("session_idx", sessionIdxs),
		entity.NewColumnInt16("chunk_idx", chunkIdxs),
		entity.NewColumnVarChar("participant_ids", participantIDsList),
		entity.NewColumnVarChar("participant_names", participantNamesList),
		entity.NewColumnVarChar("text", textList),
		entity.NewColumnVarChar("message_ids", messageIDsList),
		entity.NewColumnInt64("start_timestamp_ms", startTimestamps),
		entity.NewColumnInt64("end_timestamp_ms", endTimestamps),
		entity.NewColumnInt16("message_count", messageCounts),
		entity.NewColumnFloatVector("embedding", dim, embeddingsList),
	}

	// Insert (use Upsert for idempotency)
	_, err = milvus.Upsert(ctx, collection, "", cols...)
	if err != nil {
		return 0, fmt.Errorf("upserting: %w", err)
	}

	return len(chunks), nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

func truncateJSON(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}

	// Try to parse and trim JSON array
	var arr []interface{}
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		return "[]"
	}

	for len(arr) > 0 {
		arr = arr[:len(arr)-1]
		trimmed, _ := json.Marshal(arr)
		if len(trimmed) <= maxLen {
			return string(trimmed)
		}
	}

	return "[]"
}
