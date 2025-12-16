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

	_ "github.com/mattn/go-sqlite3"
	"github.com/milvus-io/milvus-sdk-go/v2/client"
	"github.com/milvus-io/milvus-sdk-go/v2/entity"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"go.mau.fi/mautrix-meta/pkg/ragconfig"
	"go.mau.fi/mautrix-meta/pkg/vectordb"
)

var (
	dbPath    = flag.String("db", "", "Path to SQLite database (defaults to database.sqlite from config)")
	cfgPath   = flag.String("config", "", "Path to rag.yaml (auto-detected if not specified)")
	dropFirst = flag.Bool("drop", false, "Drop existing collection before creating")
	cleanup   = flag.Bool("cleanup", false, "Delete stale chunks from Milvus (non-indexable or deleted from SQLite)")
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
	cfg, err := ragconfig.LoadFromFlagOrDir(*cfgPath, ".")
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

	// Open SQLite database (read-write for updating milvus_synced flag, with WAL and busy timeout)
	db, err := sql.Open("sqlite3", sqlitePath+"?_busy_timeout=30000&_journal_mode=WAL")
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

	// Create embedding client (availability checked later, only if needed)
	embClient := vectordb.NewEmbeddingClient(vectordb.EmbeddingConfig{
		BaseURL:   cfg.Embedding.BaseURL,
		Model:     cfg.Embedding.Model,
		Dimension: cfg.Embedding.Dimension,
	})

	// Handle collection creation
	collection := cfg.Milvus.ChunkCollection
	needsFullReindex := false

	if *dropFirst {
		if err := dropCollection(ctx, milvusClient, collection); err != nil {
			log.Fatal().Err(err).Msg("Failed to drop collection")
		}
		needsFullReindex = true
	}

	exists, err := milvusClient.HasCollection(ctx, collection)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to check collection existence")
	}

	if !exists {
		if err := createCollection(ctx, milvusClient, cfg); err != nil {
			log.Fatal().Err(err).Msg("Failed to create collection")
		}
		needsFullReindex = true
	} else {
		fmt.Printf("Collection %s already exists, using existing\n", collection)
		// Load collection for insertion
		if err := milvusClient.LoadCollection(ctx, collection, false); err != nil {
			log.Warn().Err(err).Msg("Failed to load collection (may already be loaded)")
		}
	}

	// Reset milvus_synced if collection was dropped or newly created
	// Reset ALL chunks (not just indexable) so that if is_indexable changes later, they get re-evaluated
	if needsFullReindex {
		fmt.Println("Resetting sync status for full reindex...")
		_, err := db.ExecContext(ctx, "UPDATE chunks SET milvus_synced = 0")
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to reset sync status")
		}
	}

	// Count unsynced indexable chunks
	var unsyncedChunks int
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM chunks WHERE is_indexable = 1 AND (milvus_synced = 0 OR milvus_synced IS NULL)").Scan(&unsyncedChunks)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to count chunks")
	}

	var totalChunks int
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM chunks WHERE is_indexable = 1").Scan(&totalChunks)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to count total chunks")
	}

	fmt.Printf("Unsynced chunks: %d (of %d total indexable)\n\n", unsyncedChunks, totalChunks)

	// Process chunks in batches (skip if nothing to do)
	start := time.Now()
	inserted := 0
	if unsyncedChunks > 0 {
		// Check embedding service only when we have chunks to embed
		if !embClient.IsAvailable(ctx) {
			log.Fatal().Msg("Embedding service not available at " + cfg.Embedding.BaseURL)
		}
		fmt.Printf("Embedding service available at %s\n", cfg.Embedding.BaseURL)

		inserted, err = indexChunks(ctx, db, milvusClient, embClient, cfg, *batchSize, unsyncedChunks)
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to index chunks")
		}
	} else {
		fmt.Println("All chunks already synced to Milvus.")
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

	// Cleanup stale chunks if requested
	if *cleanup {
		deleted, err := cleanupStaleChunks(ctx, db, milvusClient, collection)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to cleanup stale chunks")
		} else if deleted > 0 {
			fmt.Printf("\nCleaned up %d stale chunks from Milvus\n", deleted)
		}
	}
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
				Name:       "thread_name",
				DataType:   entity.FieldTypeVarChar,
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
				Name:       "participant_ids",
				DataType:   entity.FieldTypeVarChar,
				TypeParams: map[string]string{"max_length": "1024"},
			},
			{
				Name:       "participant_names",
				DataType:   entity.FieldTypeVarChar,
				TypeParams: map[string]string{"max_length": "2048"},
			},
			{
				Name:       "text",
				DataType:   entity.FieldTypeVarChar,
				TypeParams: map[string]string{"max_length": "8192"},
			},
			{
				Name:       "message_ids",
				DataType:   entity.FieldTypeVarChar,
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
				Name:       "embedding",
				DataType:   entity.FieldTypeFloatVector,
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
	ContentHash      string // Used for race-condition-safe UPDATE
}

func indexChunks(ctx context.Context, db *sql.DB, milvus client.Client, embClient *vectordb.EmbeddingClient, cfg *ragconfig.Config, batchSize, total int) (int, error) {
	collection := cfg.Milvus.ChunkCollection

	// Only select unsynced chunks, include content_hash for race-safe UPDATE
	rows, err := db.QueryContext(ctx, `
		SELECT
			chunk_id, thread_id, thread_name, session_idx, chunk_idx,
			participant_ids, participant_names, text, message_ids,
			start_timestamp_ms, end_timestamp_ms, message_count,
			COALESCE(content_hash, '') as content_hash
		FROM chunks
		WHERE is_indexable = 1 AND (milvus_synced = 0 OR milvus_synced IS NULL)
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
			&chunk.ContentHash,
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

			// Mark batch as synced with content_hash guard (prevents race condition)
			if err := markBatchSynced(ctx, db, batch); err != nil {
				log.Warn().Err(err).Msg("Failed to mark batch as synced")
			}

			inserted += n
			batchNum++

			// Small delay between batches
			time.Sleep(50 * time.Millisecond)

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

		// Mark final batch as synced
		if err := markBatchSynced(ctx, db, batch); err != nil {
			log.Warn().Err(err).Msg("Failed to mark final batch as synced")
		}

		inserted += n
	}

	return inserted, nil
}

// markBatchSynced marks chunks as synced only if their content_hash hasn't changed
// This prevents race conditions where fts5-setup updates content while we're indexing
func markBatchSynced(ctx context.Context, db *sql.DB, batch []chunkRow) error {
	if len(batch) == 0 {
		return nil
	}

	// Build batched UPDATE with content_hash guard
	// Only mark as synced if content_hash matches what we indexed
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		UPDATE chunks SET milvus_synced = 1
		WHERE chunk_id = ? AND (content_hash = ? OR (content_hash IS NULL AND ? = ''))
	`)
	if err != nil {
		return fmt.Errorf("preparing statement: %w", err)
	}
	defer stmt.Close()

	for _, c := range batch {
		if _, err := stmt.ExecContext(ctx, c.ChunkID, c.ContentHash, c.ContentHash); err != nil {
			log.Warn().Err(err).Str("chunk_id", c.ChunkID).Msg("Failed to mark chunk as synced")
		}
	}

	return tx.Commit()
}

func insertBatch(ctx context.Context, milvus client.Client, embClient *vectordb.EmbeddingClient, collection string, chunks []chunkRow, dim int) (int, error) {
	if len(chunks) == 0 {
		return 0, nil
	}

	// Log chunk IDs for debugging crashes (only build slice when debug enabled)
	if log.Debug().Enabled() {
		chunkIDsForLog := make([]string, len(chunks))
		for i, c := range chunks {
			chunkIDsForLog[i] = c.ChunkID
		}
		log.Debug().Strs("chunk_ids", chunkIDsForLog).Msg("Processing batch")
	}

	// Generate embeddings in batch for better GPU utilization
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Text
	}
	embeddings, err := embClient.EmbedBatch(ctx, texts)
	if err != nil {
		// Log the failing batch for debugging
		failedIDs := make([]string, len(chunks))
		for i, c := range chunks {
			failedIDs[i] = c.ChunkID
		}
		log.Error().Strs("chunk_ids", failedIDs).Err(err).Msg("Batch failed - these chunks caused crash")
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
	// UTF-8 safe truncation: don't cut in the middle of a multi-byte character
	// Walk backwards from maxLen to find a valid UTF-8 boundary
	for maxLen > 0 && !isUTF8Start(s[maxLen]) {
		maxLen--
	}
	return s[:maxLen]
}

// isUTF8Start returns true if byte is a valid UTF-8 start byte (not a continuation)
func isUTF8Start(b byte) bool {
	// UTF-8 continuation bytes are 10xxxxxx (0x80-0xBF)
	return (b & 0xC0) != 0x80
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

// cleanupStaleChunks removes chunks from Milvus that are no longer valid in SQLite
// (either deleted or marked as non-indexable)
// Note: For very large collections (>100k chunks), this may not catch all stale entries
// due to Milvus query limits. Use --drop for complete rebuild in such cases.
func cleanupStaleChunks(ctx context.Context, db *sql.DB, milvus client.Client, collection string) (int, error) {
	fmt.Println("\nChecking for stale chunks in Milvus...")

	// Get all valid indexable chunk_ids from SQLite
	validIDs := make(map[string]struct{})
	rows, err := db.QueryContext(ctx, "SELECT chunk_id FROM chunks WHERE is_indexable = 1")
	if err != nil {
		return 0, fmt.Errorf("querying valid chunks: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, fmt.Errorf("scanning chunk_id: %w", err)
		}
		validIDs[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterating rows: %w", err)
	}

	fmt.Printf("  Valid indexable chunks in SQLite: %d\n", len(validIDs))

	// Query chunk_ids from Milvus in batches using hex prefix ranges
	// This works around Milvus's default result limits
	var staleIDs []string
	milvusTotal := 0
	hexPrefixes := []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "a", "b", "c", "d", "e", "f"}

	for _, prefix := range hexPrefixes {
		// Query chunks with this hex prefix (chunk_ids are hex hashes)
		expr := fmt.Sprintf("chunk_id like \"%s%%\"", prefix)
		results, err := milvus.Query(ctx, collection, []string{}, expr, []string{"chunk_id"})
		if err != nil {
			log.Warn().Err(err).Str("prefix", prefix).Msg("Failed to query Milvus partition")
			continue
		}

		// Extract chunk_ids from results and find stale ones
		for _, col := range results {
			if col.Name() == "chunk_id" {
				if strCol, ok := col.(*entity.ColumnVarChar); ok {
					for i := 0; i < strCol.Len(); i++ {
						val, err := strCol.ValueByIdx(i)
						if err != nil {
							continue
						}
						milvusTotal++
						if _, valid := validIDs[val]; !valid {
							staleIDs = append(staleIDs, val)
						}
					}
				}
			}
		}
	}

	fmt.Printf("  Chunks scanned in Milvus: %d\n", milvusTotal)

	if len(staleIDs) == 0 {
		fmt.Println("  No stale chunks found")
		return 0, nil
	}

	fmt.Printf("  Found %d stale chunks, deleting...\n", len(staleIDs))

	// Delete stale chunks in batches
	deleteBatchSize := 1000
	deleted := 0

	for i := 0; i < len(staleIDs); i += deleteBatchSize {
		end := i + deleteBatchSize
		if end > len(staleIDs) {
			end = len(staleIDs)
		}
		batch := staleIDs[i:end]

		// Build expression for deletion
		expr := fmt.Sprintf("chunk_id in [\"%s\"]", strings.Join(batch, "\",\""))
		if err := milvus.Delete(ctx, collection, "", expr); err != nil {
			log.Warn().Err(err).Int("batch_start", i).Msg("Failed to delete batch")
			continue
		}
		deleted += len(batch)
	}

	// Also reset milvus_synced for non-indexable chunks in SQLite (for consistency)
	_, _ = db.ExecContext(ctx, "UPDATE chunks SET milvus_synced = 0 WHERE is_indexable = 0 AND milvus_synced = 1")

	return deleted, nil
}
