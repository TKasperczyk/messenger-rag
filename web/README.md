# Messenger RAG Web UI

SvelteKit frontend for browsing and searching your Messenger archive.

## Features

- Thread browser with search
- Semantic search with context expansion
- Hybrid search (BM25 + vector)
- Message viewer with infinite scroll
- Dark mode support

## Development

```bash
pnpm install
pnpm dev
```

Open http://localhost:5173

## Configuration

The web server reads `../rag.yaml` at runtime. Key settings:

- `RAG_SERVER_URL` - Go backend URL (default: http://127.0.0.1:8090)
- `database.sqlite` - Path to SQLite database

## Building

```bash
pnpm build
pnpm preview
```

## Architecture

The frontend can operate in two modes:

1. **With Go backend** (recommended) - Calls the RAG server for all search operations
2. **Direct mode** - Falls back to direct SQLite/Milvus access if backend unavailable

## Tech Stack

- SvelteKit 2
- TypeScript
- TailwindCSS
- better-sqlite3 (for direct DB access)
