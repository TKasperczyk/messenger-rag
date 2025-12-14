// chunk-generator generates message chunks for RAG embedding.
//
// This is the Go equivalent of the Python generate_chunks.py script.
// It processes messages from SQLite into chunks ready for embedding.
//
// Usage:
//
//	chunk-generator --db messenger.db --output chunks.jsonl
//	chunk-generator --db messenger.db --stats  # Print statistics only
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"go.mau.fi/mautrix-meta/pkg/chunking"
	"go.mau.fi/mautrix-meta/pkg/ragconfig"
)

var (
	dbPath     = flag.String("db", "", "Path to SQLite database (defaults to database.sqlite from config)")
	outputPath = flag.String("output", "chunks.jsonl", "Output JSONL file")
	cfgPath    = flag.String("config", "", "Path to rag.yaml (auto-detected if not specified)")
	statsOnly  = flag.Bool("stats", false, "Print statistics only (don't write output)")
	debug      = flag.Bool("debug", false, "Enable debug logging")
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

	// Print configuration
	fmt.Printf("Processing database: %s\n", sqlitePath)
	fmt.Printf("Configuration (from rag.yaml):\n")
	fmt.Printf("  - Coalesce gap: %ds\n", cfg.Chunking.Coalesce.MaxGapSeconds)
	fmt.Printf("  - Coalesce max chars: %d\n", cfg.Chunking.Coalesce.MaxCombinedChars)
	fmt.Printf("  - Session gap: %dmin\n", cfg.Chunking.Session.GapMinutes)
	fmt.Printf("  - Intra-session boundary: %dmin\n", chunking.IntraSessionGapMs/60/1000)
	fmt.Printf("  - Chunk target chars: %d\n", cfg.Chunking.Size.TargetChars)
	fmt.Printf("  - Chunk max chars: %d\n", cfg.Chunking.Size.MaxChars)
	fmt.Printf("  - Min chars for index: %d\n", cfg.Quality.MinChars)
	fmt.Printf("  - Min alnum for index: %d\n", cfg.Quality.MinAlnumChars)
	fmt.Printf("  - Min unique words: %d\n", cfg.Quality.MinUniqueWords)
	fmt.Printf("  - Sender prefix: %v\n", cfg.Chunking.Format.SenderPrefix)
	fmt.Println()

	// Open database
	db, err := sql.Open("sqlite3", sqlitePath+"?mode=ro")
	if err != nil {
		log.Fatal().Err(err).Str("path", sqlitePath).Msg("Failed to open database")
	}
	defer db.Close()

	// Verify connection
	if err := db.Ping(); err != nil {
		log.Fatal().Err(err).Msg("Database not accessible")
	}

	ctx := context.Background()

	// Setup output file
	var outputFile *os.File
	if !*statsOnly {
		outputFile, err = os.Create(*outputPath)
		if err != nil {
			log.Fatal().Err(err).Str("path", *outputPath).Msg("Failed to create output file")
		}
		defer outputFile.Close()
	}

	// Process all threads
	callback := func(chunk chunking.Chunk) error {
		if outputFile != nil {
			data, err := json.Marshal(chunk)
			if err != nil {
				return fmt.Errorf("marshaling chunk: %w", err)
			}
			if _, err := outputFile.Write(append(data, '\n')); err != nil {
				return fmt.Errorf("writing chunk: %w", err)
			}
		}
		return nil
	}

	progressFn := func(threadsProcessed, totalChunks int) {
		log.Info().
			Int("threads", threadsProcessed).
			Int("chunks", totalChunks).
			Msg("Progress")
	}

	stats, err := chunking.ProcessAllThreads(ctx, db, cfg, callback, progressFn)
	if err != nil {
		log.Fatal().Err(err).Msg("Processing failed")
	}

	// Print statistics
	fmt.Println()
	fmt.Println("============================================================")
	fmt.Println("CHUNKING COMPLETE")
	fmt.Println("============================================================")
	fmt.Printf("Threads processed: %d\n", stats.ThreadsProcessed)
	fmt.Printf("Original messages: %d\n", stats.TotalMessages)
	fmt.Printf("Total chunks: %d\n", stats.TotalChunks)
	if stats.TotalChunks > 0 {
		fmt.Printf("  - Indexable: %d (%.1f%%)\n", stats.IndexableChunks, 100*float64(stats.IndexableChunks)/float64(stats.TotalChunks))
		fmt.Printf("  - Non-indexable: %d (%.1f%%)\n", stats.NonIndexableChunks, 100*float64(stats.NonIndexableChunks)/float64(stats.TotalChunks))
		fmt.Printf("Compression ratio: %.1fx\n", float64(stats.TotalMessages)/float64(stats.TotalChunks))
	}
	fmt.Println()
	fmt.Println("Chunk size distribution:")
	for _, rangeName := range []string{"<100", "100-250", "250-500", "500-900", "900-1400", ">1400"} {
		count := stats.CharRanges[rangeName]
		pct := float64(0)
		if stats.TotalChunks > 0 {
			pct = 100 * float64(count) / float64(stats.TotalChunks)
		}
		fmt.Printf("  %s: %d (%.1f%%)\n", rangeName, count, pct)
	}
	fmt.Println()

	if !*statsOnly {
		fmt.Printf("Output written to: %s\n", *outputPath)
	}
}

func loadConfig() (*ragconfig.Config, error) {
	if *cfgPath != "" {
		return ragconfig.Load(*cfgPath)
	}
	return ragconfig.LoadFromDir(".")
}
