# Messenger RAG

A local, privacy-focused semantic search system for your Facebook Messenger history. Captures messages in real-time via WebSocket, stores them in SQLite with full-text search, and provides powerful vector-based semantic search via Milvus.

## Features

- **Real-time message capture** - Connects to Messenger via WebSocket protocol
- **E2EE decryption** - Decrypts end-to-end encrypted messages using Signal protocol
- **Hybrid search** - Combines BM25 keyword search with semantic vector search
- **Modern web UI** - SvelteKit frontend for browsing and searching your archive
- **Incremental sync** - Only re-indexes changed content, not the entire archive
- **Fully local** - All data stays on your machine, no cloud services required

## Privacy Warning

This tool accesses your Facebook Messenger data using your browser session cookies. Please be aware:

- **Your `cookies.json` contains your Facebook session** - Treat it like a password
- **The SQLite database contains all your messages** - Keep it secure
- **Never expose the servers to the internet** - They bind to `127.0.0.1` by default for a reason
- **This may violate Facebook's Terms of Service** - Use at your own risk

## Architecture

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│   Web Frontend  │────▶│   RAG Server    │────▶│     Milvus      │
│   (SvelteKit)   │     │      (Go)       │     │  (Vector Store) │
└─────────────────┘     └────────┬────────┘     └─────────────────┘
                                 │
                                 ▼
┌─────────────────┐     ┌─────────────────┐
│ Embedding Server│     │     SQLite      │
│    (Python)     │     │  (FTS5 + Data)  │
└─────────────────┘     └─────────────────┘
```

## Quick Start

### Prerequisites

- **Go 1.21+** with CGO enabled
- **Node.js 20+** and pnpm
- **Python 3.10+** with venv
- **Docker** (for Milvus)
- **4GB+ RAM** for the embedding model

### 1. Start Milvus

```bash
docker run -d --name milvus-standalone \
  -p 19530:19530 -p 9091:9091 \
  -v milvus-data:/var/lib/milvus \
  milvusdb/milvus:v2.4.0 standalone
```

### 2. Setup Python environment

```bash
python -m venv .venv
source .venv/bin/activate
pip install sentence-transformers flask
```

### 3. Get your cookies

1. Log into [messenger.com](https://messenger.com)
2. Export cookies using a browser extension (Netscape/cookies.txt format)
3. Convert to JSON:
   ```bash
   cd meta-bridge
   go build ./cmd/cookie-converter
   ./cookie-converter ../cookies.txt ../cookies.json
   ```

### 4. Import your history (optional)

Download your data from Facebook ([Settings > Your Information > Download Your Information](https://www.facebook.com/dyi)) and import:

```bash
cd meta-bridge
go build ./cmd/import-export
./import-export -zip ~/Downloads/facebook-export.zip -db ../messenger.db
```

### 5. Start everything

```bash
./start.sh           # Basic: web UI + search
./start.sh --sync    # With live message capture
```

Open http://localhost:5173

## Components

| Component | Description | Port |
|-----------|-------------|------|
| `web/` | SvelteKit frontend | 5173 |
| `meta-bridge/cmd/rag-server` | Search API server | 8090 |
| `scripts/embed_server.py` | Sentence-transformers embeddings | 1235 |
| `meta-bridge/cmd/messenger-cli` | Real-time message capture | - |
| `meta-bridge/cmd/fts5-setup` | SQLite chunking + FTS5 indexing | - |
| `meta-bridge/cmd/milvus-index` | Vector embedding + Milvus indexing | - |

## Configuration

Edit `rag.yaml` to customize:

```yaml
database:
  sqlite: messenger.db

milvus:
  address: localhost:19530
  chunk_collection: messenger_message_chunks_v2

embedding:
  base_url: http://127.0.0.1:1235/v1
  model: mmlw-roberta-large
  dimension: 1024

search:
  default_limit: 20
  context_radius: 2
```

## Manual Operations

### Re-index everything

```bash
# Regenerate chunks and FTS5 index
./bin/fts5-setup -db messenger.db --from-db

# Re-embed all chunks (drops Milvus collection)
./bin/milvus-index -db messenger.db --drop
```

### Incremental sync

```bash
# Only processes new/changed chunks
./bin/fts5-setup -db messenger.db --from-db
./bin/milvus-index -db messenger.db
```

## Building from source

```bash
# Go binaries (with FTS5 support)
cd meta-bridge
CGO_CFLAGS="-DSQLITE_ENABLE_FTS5" CGO_LDFLAGS="-lm" \
  go build -tags "fts5" -o ../bin/rag-server ./cmd/rag-server
# Repeat for other commands...

# Web frontend
cd web
pnpm install
pnpm build
```

## Credits

This project builds upon the excellent work of others:

- **[mautrix-meta](https://github.com/mautrix/meta)** by [Tulir Asokan](https://github.com/tulir) and the [mautrix](https://github.com/mautrix) team - The Facebook Messenger protocol implementation (`pkg/messagix/`) is derived from their Matrix-Facebook bridge. Licensed under AGPL-3.0.
  - Documentation: [docs.mau.fi](https://docs.mau.fi/bridges/go/meta/)
  - Matrix room: [#meta:maunium.net](https://matrix.to/#/#meta:maunium.net)
- **[sdadas/mmlw-roberta-large](https://huggingface.co/sdadas/mmlw-roberta-large)** - Polish embedding model used for semantic search
- **[Milvus](https://milvus.io/)** - Open-source vector database

## License

AGPL-3.0 - See [LICENSE](LICENSE)

This license is inherited from mautrix-meta and applies to the entire project.
