// fts5-setup sets up SQLite FTS5 tables for hybrid BM25 search.
//
// This is the Go equivalent of the Python setup_fts5.py script.
// It creates chunks and FTS5 tables, then loads chunks from JSONL.
//
// Usage:
//
//	fts5-setup --db messenger.db --chunks chunks.jsonl
//	fts5-setup --db messenger.db --from-db  # Generate chunks from messages table
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"go.mau.fi/mautrix-meta/pkg/chunking"
	"go.mau.fi/mautrix-meta/pkg/ragconfig"
)

var (
	dbPath     = flag.String("db", "", "Path to SQLite database (defaults to database.sqlite from config)")
	chunksPath = flag.String("chunks", "", "Path to chunks JSONL file (required unless --from-db)")
	fromDB     = flag.Bool("from-db", false, "Generate chunks directly from messages table")
	cfgPath    = flag.String("config", "", "Path to rag.yaml (auto-detected if not specified)")
	debug      = flag.Bool("debug", false, "Enable debug logging")
)

var validIdentRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

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

	// Validate FTS table name
	ftsTable := cfg.Hybrid.BM25.Table
	if ftsTable == "" {
		ftsTable = "chunks_fts"
	}
	if !validIdentRe.MatchString(ftsTable) {
		log.Warn().Str("table", ftsTable).Msg("Invalid FTS table name, falling back to 'chunks_fts'")
		ftsTable = "chunks_fts"
	}

	fmt.Printf("Setting up FTS5 in: %s\n", sqlitePath)
	fmt.Printf("FTS table name: %s\n", ftsTable)
	fmt.Println()

	// Open database (read-write mode with WAL and busy timeout for concurrent access)
	db, err := sql.Open("sqlite3", sqlitePath+"?_busy_timeout=30000&_journal_mode=WAL")
	if err != nil {
		log.Fatal().Err(err).Str("path", sqlitePath).Msg("Failed to open database")
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatal().Err(err).Msg("Database not accessible")
	}

	ctx := context.Background()

	// Create tables
	if err := createTables(ctx, db, ftsTable); err != nil {
		log.Fatal().Err(err).Msg("Failed to create tables")
	}

	// Load chunks
	var total, indexable int
	if *fromDB {
		total, indexable, err = loadChunksFromDB(ctx, db, cfg)
	} else if *chunksPath != "" {
		total, indexable, err = loadChunksFromJSONL(ctx, db, *chunksPath)
	} else {
		log.Fatal().Msg("Either --chunks or --from-db must be specified")
	}
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load chunks")
	}

	// Verify FTS
	if err := verifyFTS(ctx, db, ftsTable); err != nil {
		log.Warn().Err(err).Msg("FTS verification failed")
	}

	fmt.Println()
	fmt.Println("============================================================")
	fmt.Println("FTS5 SETUP COMPLETE")
	fmt.Println("============================================================")
	fmt.Printf("Total chunks in SQLite: %d\n", total)
	fmt.Printf("Indexable chunks: %d\n", indexable)
	fmt.Println()
	fmt.Println("Tables created:")
	fmt.Println("  - chunks: Full chunk data")
	fmt.Printf("  - %s: FTS5 virtual table for BM25 search\n", ftsTable)
}

func loadConfig() (*ragconfig.Config, error) {
	if *cfgPath != "" {
		return ragconfig.Load(*cfgPath)
	}
	return ragconfig.LoadFromDir(".")
}

func createTables(ctx context.Context, db *sql.DB, ftsTable string) error {
	// Check if chunks table exists
	var tableExists int
	err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='chunks'").Scan(&tableExists)
	if err != nil {
		return fmt.Errorf("checking table existence: %w", err)
	}

	if tableExists == 0 {
		// Create chunks table with new schema (includes content_hash and milvus_synced)
		_, err := db.ExecContext(ctx, `
			CREATE TABLE chunks (
				chunk_id TEXT PRIMARY KEY,
				thread_id INTEGER NOT NULL,
				thread_name TEXT,
				session_idx INTEGER NOT NULL,
				chunk_idx INTEGER NOT NULL,
				message_ids TEXT NOT NULL,
				participant_ids TEXT NOT NULL,
				participant_names TEXT NOT NULL,
				text TEXT NOT NULL,
				start_timestamp_ms INTEGER NOT NULL,
				end_timestamp_ms INTEGER NOT NULL,
				message_count INTEGER NOT NULL,
				is_indexable INTEGER NOT NULL,
				char_count INTEGER NOT NULL,
				alnum_count INTEGER NOT NULL,
				unique_word_count INTEGER NOT NULL,
				content_hash TEXT,
				milvus_synced INTEGER DEFAULT 0
			)
		`)
		if err != nil {
			return fmt.Errorf("creating chunks table: %w", err)
		}

		// Create FTS5 virtual table
		_, err = db.ExecContext(ctx, fmt.Sprintf(`
			CREATE VIRTUAL TABLE %s USING fts5(
				chunk_id UNINDEXED,
				text,
				content='chunks',
				content_rowid='rowid'
			)
		`, ftsTable))
		if err != nil {
			return fmt.Errorf("creating FTS5 table: %w", err)
		}

		// Create triggers to keep FTS in sync
		_, err = db.ExecContext(ctx, fmt.Sprintf(`
			CREATE TRIGGER chunks_ai AFTER INSERT ON chunks BEGIN
				INSERT INTO %s(rowid, chunk_id, text)
				VALUES (new.rowid, new.chunk_id, new.text);
			END
		`, ftsTable))
		if err != nil {
			return fmt.Errorf("creating insert trigger: %w", err)
		}

		_, err = db.ExecContext(ctx, fmt.Sprintf(`
			CREATE TRIGGER chunks_ad AFTER DELETE ON chunks BEGIN
				INSERT INTO %s(%s, rowid, chunk_id, text)
				VALUES('delete', old.rowid, old.chunk_id, old.text);
			END
		`, ftsTable, ftsTable))
		if err != nil {
			return fmt.Errorf("creating delete trigger: %w", err)
		}

		_, err = db.ExecContext(ctx, fmt.Sprintf(`
			CREATE TRIGGER chunks_au AFTER UPDATE ON chunks BEGIN
				INSERT INTO %s(%s, rowid, chunk_id, text)
				VALUES('delete', old.rowid, old.chunk_id, old.text);
				INSERT INTO %s(rowid, chunk_id, text)
				VALUES (new.rowid, new.chunk_id, new.text);
			END
		`, ftsTable, ftsTable, ftsTable))
		if err != nil {
			return fmt.Errorf("creating update trigger: %w", err)
		}

		// Create indexes
		indexes := []string{
			"CREATE INDEX idx_chunks_thread_session ON chunks(thread_id, session_idx, chunk_idx)",
			"CREATE INDEX idx_chunks_indexable ON chunks(is_indexable)",
			"CREATE INDEX idx_chunks_timestamp ON chunks(start_timestamp_ms)",
			"CREATE INDEX idx_chunks_milvus_synced ON chunks(milvus_synced)",
		}
		for _, idx := range indexes {
			if _, err := db.ExecContext(ctx, idx); err != nil {
				return fmt.Errorf("creating index: %w", err)
			}
		}

		fmt.Printf("Created chunks and %s tables\n", ftsTable)
	} else {
		// Table exists - check if we need to add new columns
		var hasContentHash, hasMilvusSynced int
		err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM pragma_table_info('chunks') WHERE name='content_hash'").Scan(&hasContentHash)
		if err != nil {
			return fmt.Errorf("checking content_hash column: %w", err)
		}
		err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM pragma_table_info('chunks') WHERE name='milvus_synced'").Scan(&hasMilvusSynced)
		if err != nil {
			return fmt.Errorf("checking milvus_synced column: %w", err)
		}

		if hasContentHash == 0 || hasMilvusSynced == 0 {
			fmt.Println("Migrating chunks table...")
			if hasContentHash == 0 {
				fmt.Println("  Adding content_hash column...")
				_, err = db.ExecContext(ctx, "ALTER TABLE chunks ADD COLUMN content_hash TEXT")
				if err != nil {
					return fmt.Errorf("adding content_hash column: %w", err)
				}
			}
			if hasMilvusSynced == 0 {
				fmt.Println("  Adding milvus_synced column...")
				_, err = db.ExecContext(ctx, "ALTER TABLE chunks ADD COLUMN milvus_synced INTEGER DEFAULT 0")
				if err != nil {
					return fmt.Errorf("adding milvus_synced column: %w", err)
				}
			}
			fmt.Println("Migration complete")
		}

		// Always ensure index exists
		_, err = db.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS idx_chunks_milvus_synced ON chunks(milvus_synced)")
		if err != nil {
			return fmt.Errorf("creating milvus_synced index: %w", err)
		}

		fmt.Printf("Using existing chunks table (incremental mode)\n")
	}

	return nil
}

// computeContentHash generates a hash of all Milvus-stored fields for change detection
// Includes all fields that get stored in Milvus to detect any staleness
// Also includes is_indexable so that indexability changes trigger re-sync
func computeContentHash(text, messageIDs, threadName, participantIDs, participantNames string, isIndexable bool) string {
	h := sha256.New()
	h.Write([]byte(text))
	h.Write([]byte{0}) // separator
	h.Write([]byte(messageIDs))
	h.Write([]byte{0})
	h.Write([]byte(threadName))
	h.Write([]byte{0})
	h.Write([]byte(participantIDs))
	h.Write([]byte{0})
	h.Write([]byte(participantNames))
	h.Write([]byte{0})
	if isIndexable {
		h.Write([]byte("1"))
	} else {
		h.Write([]byte("0"))
	}
	return hex.EncodeToString(h.Sum(nil))[:16] // First 16 chars is enough
}

func loadChunksFromJSONL(ctx context.Context, db *sql.DB, jsonlPath string) (int, int, error) {
	file, err := os.Open(jsonlPath)
	if err != nil {
		return 0, 0, fmt.Errorf("opening file: %w", err)
	}
	defer file.Close()

	fmt.Printf("Loading chunks from: %s\n", jsonlPath)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("starting transaction: %w", err)
	}
	defer tx.Rollback()

	// Use INSERT OR REPLACE with content_hash tracking
	// When content_hash changes (or was NULL), milvus_synced is reset to 0
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO chunks (
			chunk_id, thread_id, thread_name, session_idx, chunk_idx,
			message_ids, participant_ids, participant_names, text,
			start_timestamp_ms, end_timestamp_ms, message_count,
			is_indexable, char_count, alnum_count, unique_word_count,
			content_hash, milvus_synced
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)
		ON CONFLICT(chunk_id) DO UPDATE SET
			thread_id = excluded.thread_id,
			thread_name = excluded.thread_name,
			session_idx = excluded.session_idx,
			chunk_idx = excluded.chunk_idx,
			message_ids = excluded.message_ids,
			participant_ids = excluded.participant_ids,
			participant_names = excluded.participant_names,
			text = excluded.text,
			start_timestamp_ms = excluded.start_timestamp_ms,
			end_timestamp_ms = excluded.end_timestamp_ms,
			message_count = excluded.message_count,
			is_indexable = excluded.is_indexable,
			char_count = excluded.char_count,
			alnum_count = excluded.alnum_count,
			unique_word_count = excluded.unique_word_count,
			content_hash = excluded.content_hash,
			milvus_synced = CASE
				WHEN chunks.content_hash IS NULL OR chunks.content_hash IS NOT excluded.content_hash THEN 0
				ELSE chunks.milvus_synced
			END
	`)
	if err != nil {
		return 0, 0, fmt.Errorf("preparing statement: %w", err)
	}
	defer stmt.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024) // 10MB max line

	total := 0
	indexable := 0
	batchSize := 1000

	for scanner.Scan() {
		var chunk chunking.Chunk
		if err := json.Unmarshal(scanner.Bytes(), &chunk); err != nil {
			return total, indexable, fmt.Errorf("parsing line %d: %w", total+1, err)
		}

		messageIDsJSON, _ := json.Marshal(chunk.MessageIDs)
		participantIDsJSON, _ := json.Marshal(chunk.ParticipantIDs)
		participantNamesJSON, _ := json.Marshal(chunk.ParticipantNames)

		isIndexable := 0
		if chunk.IsIndexable {
			isIndexable = 1
			indexable++
		}

		contentHash := computeContentHash(chunk.Text, string(messageIDsJSON), chunk.ThreadName, string(participantIDsJSON), string(participantNamesJSON), chunk.IsIndexable)

		_, err := stmt.ExecContext(ctx,
			chunk.ChunkID,
			chunk.ThreadID,
			chunk.ThreadName,
			chunk.SessionIdx,
			chunk.ChunkIdx,
			string(messageIDsJSON),
			string(participantIDsJSON),
			string(participantNamesJSON),
			chunk.Text,
			chunk.StartTimestampMs,
			chunk.EndTimestampMs,
			chunk.MessageCount,
			isIndexable,
			chunk.CharCount,
			chunk.AlnumCount,
			chunk.UniqueWordCount,
			contentHash,
		)
		if err != nil {
			return total, indexable, fmt.Errorf("inserting chunk %s: %w", chunk.ChunkID, err)
		}

		total++
		if total%batchSize == 0 {
			fmt.Printf("  Processed %d chunks...\n", total)
		}
	}

	if err := scanner.Err(); err != nil {
		return total, indexable, fmt.Errorf("reading file: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return total, indexable, fmt.Errorf("committing transaction: %w", err)
	}

	fmt.Printf("Loaded %d chunks (%d indexable)\n", total, indexable)
	return total, indexable, nil
}

func loadChunksFromDB(ctx context.Context, db *sql.DB, cfg *ragconfig.Config) (int, int, error) {
	fmt.Println("Generating chunks from messages table...")

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("starting transaction: %w", err)
	}
	defer tx.Rollback()

	// Use INSERT OR REPLACE with content_hash tracking
	// When content_hash changes (or was NULL), milvus_synced is reset to 0
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO chunks (
			chunk_id, thread_id, thread_name, session_idx, chunk_idx,
			message_ids, participant_ids, participant_names, text,
			start_timestamp_ms, end_timestamp_ms, message_count,
			is_indexable, char_count, alnum_count, unique_word_count,
			content_hash, milvus_synced
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)
		ON CONFLICT(chunk_id) DO UPDATE SET
			thread_id = excluded.thread_id,
			thread_name = excluded.thread_name,
			session_idx = excluded.session_idx,
			chunk_idx = excluded.chunk_idx,
			message_ids = excluded.message_ids,
			participant_ids = excluded.participant_ids,
			participant_names = excluded.participant_names,
			text = excluded.text,
			start_timestamp_ms = excluded.start_timestamp_ms,
			end_timestamp_ms = excluded.end_timestamp_ms,
			message_count = excluded.message_count,
			is_indexable = excluded.is_indexable,
			char_count = excluded.char_count,
			alnum_count = excluded.alnum_count,
			unique_word_count = excluded.unique_word_count,
			content_hash = excluded.content_hash,
			milvus_synced = CASE
				WHEN chunks.content_hash IS NULL OR chunks.content_hash IS NOT excluded.content_hash THEN 0
				ELSE chunks.milvus_synced
			END
	`)
	if err != nil {
		return 0, 0, fmt.Errorf("preparing statement: %w", err)
	}
	defer stmt.Close()

	total := 0
	indexable := 0

	callback := func(chunk chunking.Chunk) error {
		messageIDsJSON, _ := json.Marshal(chunk.MessageIDs)
		participantIDsJSON, _ := json.Marshal(chunk.ParticipantIDs)
		participantNamesJSON, _ := json.Marshal(chunk.ParticipantNames)

		isIndexable := 0
		if chunk.IsIndexable {
			isIndexable = 1
			indexable++
		}

		contentHash := computeContentHash(chunk.Text, string(messageIDsJSON), chunk.ThreadName, string(participantIDsJSON), string(participantNamesJSON), chunk.IsIndexable)

		_, err := stmt.ExecContext(ctx,
			chunk.ChunkID,
			chunk.ThreadID,
			chunk.ThreadName,
			chunk.SessionIdx,
			chunk.ChunkIdx,
			string(messageIDsJSON),
			string(participantIDsJSON),
			string(participantNamesJSON),
			chunk.Text,
			chunk.StartTimestampMs,
			chunk.EndTimestampMs,
			chunk.MessageCount,
			isIndexable,
			chunk.CharCount,
			chunk.AlnumCount,
			chunk.UniqueWordCount,
			contentHash,
		)
		if err != nil {
			return fmt.Errorf("inserting chunk %s: %w", chunk.ChunkID, err)
		}
		total++
		return nil
	}

	progressFn := func(threadsProcessed, totalChunks int) {
		fmt.Printf("  Processed %d threads, %d chunks...\n", threadsProcessed, totalChunks)
	}

	_, err = chunking.ProcessAllThreads(ctx, db, cfg, callback, progressFn)
	if err != nil {
		return total, indexable, fmt.Errorf("processing threads: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return total, indexable, fmt.Errorf("committing transaction: %w", err)
	}

	fmt.Printf("Generated %d chunks (%d indexable)\n", total, indexable)
	return total, indexable, nil
}

func verifyFTS(ctx context.Context, db *sql.DB, ftsTable string) error {
	// Test basic search
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`
		SELECT c.chunk_id, c.thread_name, c.char_count, c.is_indexable
		FROM %s fts
		JOIN chunks c ON c.chunk_id = fts.chunk_id
		WHERE %s MATCH 'test OR hello'
		ORDER BY rank
		LIMIT 5
	`, ftsTable, ftsTable))
	if err != nil {
		return fmt.Errorf("test query: %w", err)
	}
	defer rows.Close()

	fmt.Printf("\nFTS5 test query 'test OR hello':\n")
	for rows.Next() {
		var chunkID, threadName sql.NullString
		var charCount, isIndexable int
		if err := rows.Scan(&chunkID, &threadName, &charCount, &isIndexable); err != nil {
			return fmt.Errorf("scanning result: %w", err)
		}
		fmt.Printf("  %s: %s (%d chars, indexable=%d)\n",
			chunkID.String, threadName.String, charCount, isIndexable)
	}

	return rows.Err()
}
