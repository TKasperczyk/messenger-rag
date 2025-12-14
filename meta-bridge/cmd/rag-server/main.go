// rag-server is the HTTP API server for RAG search operations.
//
// This is the authoritative backend for all search operations. Web UI,
// CLI, and future MCP server should all use this API.
//
// Endpoints:
//   - GET  /search   - Semantic/BM25/hybrid search
//   - GET  /stats    - Collection statistics
//   - GET  /health   - Health check
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"go.mau.fi/mautrix-meta/pkg/rag"
	"go.mau.fi/mautrix-meta/pkg/ragconfig"
)

var (
	addr    = flag.String("addr", ":8090", "HTTP listen address")
	dbPath  = flag.String("db", "", "Path to SQLite database (defaults to database.sqlite from config)")
	cfgPath = flag.String("config", "", "Path to rag.yaml (auto-detected if not specified)")
	debug   = flag.Bool("debug", false, "Enable debug logging")
	corsAny = flag.Bool("cors-any", false, "Allow CORS from any origin (for development)")
)

func main() {
	flag.Parse()

	// Setup logging
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	if *debug {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	} else {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}

	// Load configuration
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load configuration")
	}
	log.Info().Str("collection", cfg.Milvus.ChunkCollection).Msg("Loaded configuration")

	sqlitePath := *dbPath
	if sqlitePath == "" {
		sqlitePath = cfg.Database.SQLite
	}
	if sqlitePath == "" {
		log.Fatal().Msg("SQLite database path is empty (set -db or database.sqlite in rag.yaml)")
	}

	// Open SQLite database
	db, err := sql.Open("sqlite3", sqlitePath+"?mode=ro")
	if err != nil {
		log.Fatal().Err(err).Str("path", sqlitePath).Msg("Failed to open SQLite database")
	}
	defer db.Close()

	// Verify SQLite connection
	if err := db.Ping(); err != nil {
		log.Fatal().Err(err).Msg("SQLite database not accessible")
	}
	log.Info().Str("path", sqlitePath).Msg("Connected to SQLite")

	// Create service components
	ctx := context.Background()

	vectors, err := rag.NewMilvusVectorSearcher(ctx, cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to Milvus")
	}
	// Note: vectors.Close() is called by service.Close(), don't defer here
	log.Info().Str("address", cfg.Milvus.Address).Msg("Connected to Milvus")

	bm25, err := rag.NewSQLiteBM25Searcher(db, cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create BM25 searcher")
	}

	chunks := rag.NewSQLiteChunkStore(db)
	embedder := rag.NewEmbeddingClientAdapter(cfg)

	service := rag.NewService(cfg, vectors, bm25, chunks, embedder)
	defer service.Close()

	// Create HTTP server
	mux := http.NewServeMux()

	// Wrap handlers with CORS if enabled
	wrap := func(h http.HandlerFunc) http.HandlerFunc {
		if *corsAny {
			return corsMiddleware(h)
		}
		return h
	}

	mux.HandleFunc("GET /search", wrap(searchHandler(service)))
	mux.HandleFunc("GET /stats", wrap(statsHandler(service)))
	mux.HandleFunc("GET /health", wrap(healthHandler(service)))

	// Also support POST for search (for larger queries)
	mux.HandleFunc("POST /search", wrap(searchPostHandler(service)))

	// Handle OPTIONS for CORS preflight (needed for browser POST requests)
	if *corsAny {
		mux.HandleFunc("OPTIONS /search", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {}))
	}

	server := &http.Server{
		Addr:         *addr,
		Handler:      loggingMiddleware(mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh

		log.Info().Msg("Shutting down server...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			log.Error().Err(err).Msg("Server shutdown error")
		}
	}()

	log.Info().Str("addr", *addr).Msg("Starting RAG server")
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal().Err(err).Msg("Server error")
	}

	log.Info().Msg("Server stopped")
}

func loadConfig() (*ragconfig.Config, error) {
	if *cfgPath != "" {
		return ragconfig.Load(*cfgPath)
	}
	return ragconfig.LoadFromDir(".")
}

// searchHandler handles GET /search requests
func searchHandler(svc *rag.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()

		req := rag.SearchRequest{
			Query:    query.Get("q"),
			Mode:     rag.SearchMode(query.Get("mode")),
			Limit:    parseIntDefault(query.Get("limit"), 20),
			Context:  parseIntDefault(query.Get("context"), 0),
			RrfK:     parseIntDefault(query.Get("rrf_k"), 0),
			CandMult: parseIntDefault(query.Get("candidate_mult"), 0),
		}

		// Parse weights
		if wv := query.Get("w_vector"); wv != "" {
			if f, err := strconv.ParseFloat(wv, 64); err == nil {
				req.WeightVec = f
			}
		}
		if wb := query.Get("w_bm25"); wb != "" {
			if f, err := strconv.ParseFloat(wb, 64); err == nil {
				req.WeightBM25 = f
			}
		}

		// Sanitize and validate
		req.Query = rag.SanitizeQuery(req.Query)
		if err := rag.ValidateSearchRequest(&req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		resp, err := svc.Search(r.Context(), req)
		if err != nil {
			log.Error().Err(err).Str("query", req.Query).Msg("Search failed")
			writeError(w, http.StatusInternalServerError, "search failed")
			return
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

// searchPostHandler handles POST /search requests
func searchPostHandler(svc *rag.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req rag.SearchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}

		// Sanitize and validate
		req.Query = rag.SanitizeQuery(req.Query)
		if err := rag.ValidateSearchRequest(&req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		resp, err := svc.Search(r.Context(), req)
		if err != nil {
			log.Error().Err(err).Str("query", req.Query).Msg("Search failed")
			writeError(w, http.StatusInternalServerError, "search failed")
			return
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

// statsHandler handles GET /stats requests
func statsHandler(svc *rag.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stats, err := svc.Stats(r.Context())
		if err != nil {
			log.Error().Err(err).Msg("Stats failed")
			writeError(w, http.StatusInternalServerError, "stats failed")
			return
		}

		writeJSON(w, http.StatusOK, stats)
	}
}

// healthHandler handles GET /health requests
func healthHandler(svc *rag.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		health := svc.Health(r.Context())

		status := http.StatusOK
		if health.Status == "degraded" {
			status = http.StatusOK // Still return 200 for degraded
		} else if health.Status == "unhealthy" {
			status = http.StatusServiceUnavailable
		}

		writeJSON(w, status, health)
	}
}

// loggingMiddleware logs HTTP requests
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap response writer to capture status code
		wrapped := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(wrapped, r)

		log.Info().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Str("query", r.URL.RawQuery).
			Int("status", wrapped.status).
			Dur("duration", time.Since(start)).
			Msg("HTTP request")
	})
}

// corsMiddleware adds CORS headers for development
func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next(w, r)
	}
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func parseIntDefault(s string, def int) int {
	if s == "" {
		return def
	}
	if i, err := strconv.Atoi(s); err == nil {
		return i
	}
	return def
}
