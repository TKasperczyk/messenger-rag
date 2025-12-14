# Messenger RAG

A local Messenger data capture and semantic search system. Connects to Facebook Messenger via WebSocket, stores messages in SQLite with full-text search, and provides vector-based semantic search via Milvus.

Built on the protocol library from [mautrix-meta](https://github.com/mautrix/meta), with Matrix dependencies removed.

## Features

- **Real-time message capture** via Facebook's WebSocket protocol
- **E2EE support** - Decrypts end-to-end encrypted messages using Signal protocol
- **SQLite storage** with FTS4 full-text search
- **Vector search** via Milvus + LMStudio embeddings
- **Historical import** from Facebook's "Download Your Information" export

## Tools

### messenger-cli

Main application for real-time message capture and search.

```bash
# Build
go build ./cmd/messenger-cli

# Real-time capture (requires cookies.json)
./messenger-cli cookies.json

# With vector indexing
./messenger-cli -vector cookies.json

# Search modes (no cookies needed)
./messenger-cli -stats                    # Database statistics
./messenger-cli -search "keyword"         # Full-text search
./messenger-cli -semantic "meaning"       # Semantic/vector search
./messenger-cli -contacts                 # List all contacts
./messenger-cli -from "Alice"             # Messages from person

# Batch index existing messages
./messenger-cli -index-existing -vector
```

### cookie-converter

Convert browser-exported cookies to the required JSON format.

```bash
go build ./cmd/cookie-converter
./cookie-converter cookies.txt cookies.json
```

### import-export

Import historical messages from Facebook's data export.

```bash
go build ./cmd/import-export
./import-export -zip ~/Downloads/messages.zip -db messenger.db
```

## Cookie Setup

1. Log into [messenger.com](https://messenger.com) in your browser
2. Export cookies using a browser extension (Netscape format)
3. Convert to JSON:
   ```bash
   ./cookie-converter cookies.txt cookies.json
   ```

Required cookies: `c_user`, `xs`, `datr`, `fr`

## Dependencies

- **Milvus** (optional) - For semantic search: `docker run -d --name milvus -p 19530:19530 milvusdb/milvus:latest`
- **LMStudio** (optional) - For embeddings: Run with an embedding model loaded

## Project Structure

```
cmd/
├── messenger-cli/      # Main CLI application
├── cookie-converter/   # Cookie format converter
└── import-export/      # Historical data importer

pkg/
├── messagix/           # Facebook protocol library (from mautrix-meta)
├── storage/            # SQLite storage layer
└── vectordb/           # Milvus + embeddings client
```

## License

AGPL-3.0 (inherited from mautrix-meta)
