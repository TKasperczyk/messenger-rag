# Re-import and Re-index Required

## Issue Fixed: Facebook Share Message Bug

**Date**: 2025-12-14

### Problem
The Go import-export CLI was not parsing Facebook's `share` field in messages. When users shared links (Instagram, YouTube, etc.) with accompanying text, the structure was:

```json
{
  "sender_name": "...",
  "timestamp_ms": 1234567890,
  "share": {
    "link": "https://www.instagram.com/reel/...",
    "share_text": "user's comment about the shared link"
  }
}
```

But only `content` was being read, so:
- `share_text` (user's comment) was **silently dropped**
- `share.link` was often lost (content was just "You sent a link.")

### Fix Applied
File: `meta-bridge/cmd/import-export/main.go`

1. Added `FBShare` struct with `Link` and `ShareText` fields
2. Added `Share *FBShare` to `FBMessage` struct
3. Created `fbMessageText()` helper that combines content + share_text + link
4. Created `isSharePlaceholder()` to filter out "You sent a link." placeholders
5. Updated both FB import paths to use the new helper

### Re-import Steps Required

1. **Re-import from Facebook export** (updates SQLite with corrected message text):
   ```bash
   cd /home/luthriel/Programming/messenger-rag/meta-bridge
   ./import-export -input /path/to/facebook/export -drop-db -skip-vector
   ```

2. **Re-generate chunks** (Python script reads from SQLite):
   ```bash
   cd /home/luthriel/Programming/messenger-rag/embeddings_runpod
   python generate_chunks.py  # outputs chunks_v2.jsonl
   ```

3. **Re-generate embeddings** (on RunPod or locally):
   ```bash
   # On RunPod with GPU:
   python embed_chunks_v2.py
   ```

4. **Re-insert into Milvus**:
   ```bash
   python insert_chunks_milvus_v2.py
   ```

## Architecture Problem: Logic Duplication

The codebase currently has severe logic duplication that needs to be addressed:

### Current State (Messy)

| Component | Language | Collection | Embedding API | Issues |
|-----------|----------|------------|---------------|--------|
| Go CLI (`import-export`) | Go | `messenger_messages` | LMStudio | Old per-message approach, unused |
| Python chunker | Python | N/A | N/A | Separate chunking logic |
| Python embedder | Python | N/A | Ollama/RunPod | Different embedding setup |
| Python inserter | Python | `messenger_message_chunks_v2` | N/A | Different collection |
| Web app | TypeScript | `messenger_message_chunks_v2` | Ollama | Has quality filters not in Python |

### Files Involved

**Go (meta-bridge/):**
- `cmd/import-export/main.go` - FB import + (unused) vector indexing
- `cmd/messenger-cli/main.go` - CLI search tool
- `pkg/vectordb/milvus.go` - Milvus client, uses `messenger_messages`
- `pkg/vectordb/embeddings.go` - Embedding via LMStudio API
- `pkg/storage/storage.go` - SQLite operations

**Python (embeddings_runpod/):**
- `generate_chunks.py` - Chunking logic (session-based)
- `embed_chunks_v2.py` - Embedding via Ollama
- `insert_chunks_milvus_v2.py` - Insert to `messenger_message_chunks_v2`

**TypeScript (web/src/lib/server/):**
- `milvus.ts` - Queries `messenger_message_chunks_v2`, has quality filters
- `hybrid.ts` - BM25 + vector hybrid search
- `sqlite.ts` - SQLite queries

### What Needs Unification

1. **Collection name** - Should be single source of truth
2. **Chunking logic** - Currently only in Python
3. **Quality filters** - Currently only in TypeScript web app
4. **Embedding API** - Different between Go (LMStudio), Python (Ollama), TypeScript (Ollama)
5. **Search logic** - Duplicated between Go CLI and TypeScript web app

---

## Unified Architecture Plan (from Codex analysis 2025-12-14)

### Key Discrepancies Found

| Issue | Go | Python | TypeScript |
|-------|-----|--------|------------|
| Collection | `messenger_messages` | `messenger_message_chunks_v2` | `messenger_message_chunks_v2` |
| Embedding API | LMStudio | Ollama | LMStudio |
| Index Type | IVF_FLAT | HNSW | HNSW |
| Unit | per-message | chunks | chunks |
| Quality Filter | none | `is_indexable` (250 chars, URL special-case) | query-time (250 chars, no URL special-case) |

### Solution: Go as Authoritative Backend

1. **Single config file** (`rag.yaml`): collection names, embedding config, chunking params, quality thresholds
2. **Go backend owns**: chunking, quality gates, embedding, Milvus schema, search
3. **Web app**: calls Go backend for search (stops doing embedding/Milvus directly)
4. **Python**: deprecated or reduced to GPU embedding worker only

### Implementation Phases

- **Phase 1**: Stop divergence - disable Go message indexing, create unified config
- **Phase 2**: Unify search - Go backend serves all search requests
- **Phase 3**: Port chunking to Go
- **Phase 4**: Retire Python indexer
