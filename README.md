# Messenger RAG

[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL%203.0-blue.svg)](LICENSE)
[![Go 1.21+](https://img.shields.io/badge/Go-1.21+-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![SvelteKit](https://img.shields.io/badge/SvelteKit-2-FF3E00?logo=svelte&logoColor=white)](https://kit.svelte.dev/)
[![Milvus](https://img.shields.io/badge/Milvus-2.4-00A1EA)](https://milvus.io/)

![Demo](docs/demo.gif)

I have 500k+ Messenger messages going back over a decade. Facebook's built-in search is practically useless - it only matches exact keywords and can't find anything unless you remember the exact words someone used.

So I built this. It's a local semantic search system that actually understands what you're looking for.

## The problem this solves

**Messenger search:**
```
"camping trip" → 0 results
"tent" → 47 results, none relevant
"that weekend in the mountains" → lol no
```

**This tool:**
```
"that time we went camping and it rained all night"
  → finds the conversation where you complained about the wet sleeping bag

"recipe someone sent me for that cake"
  → finds a 3-year-old message with a cheesecake recipe from your aunt

"who recommended that sci-fi book"
  → finds your friend mentioning "Solaris" in a thread about movies
```

It works because instead of matching keywords, it searches by *meaning*. The embedding model understands that "dog" and "furry friend" are related, that "angry" and "frustrated" are similar, even if you never used those exact words.

## Try it (with sample data)

Don't want to connect your real Messenger? Try the demo with fake conversations:

```bash
./demo.sh
```

This imports sample conversations and starts everything. Try searching for "camping trip with rain" or "grandma's dessert recipe".

## What's inside

- **Real-time sync** - Connects to Messenger via WebSocket protocol, captures new messages as they arrive
- **E2EE support** - Decrypts end-to-end encrypted conversations using Signal protocol
- **Hybrid search** - Combines semantic vectors (meaning) with BM25 (keywords) for best results
- **Web UI** - Browse threads, search everything, see context around results
- **Fully local** - Your data never leaves your machine. No cloud, no API keys, no subscriptions.

## How it works

```
You: "that argument about diet"
                ↓
        [Embedding Model]
        Converts to 1024-dim vector
                ↓
        [Milvus Vector DB]
        Finds semantically similar chunks
                ↓
        [SQLite FTS5]
        Also does keyword matching
                ↓
        [Hybrid Ranking]
        Combines both, returns best matches
                ↓
Found: Conversation from 2024 about veganism
       that you'd completely forgotten about
```

## Privacy & legal stuff

**Your data stays local.** Everything runs on your machine - SQLite database, Milvus vector store, embedding model. Nothing is sent anywhere.

**But be careful:**
- Your `cookies.json` is essentially your Facebook password. Treat it accordingly.
- The SQLite database contains all your messages in plaintext. Encrypt your disk.
- All servers bind to `127.0.0.1` by default. Don't expose them to the internet.
- This probably violates Facebook's ToS. Use at your own risk.

## Getting started

### You'll need

- Go 1.21+ (with CGO)
- Node.js 20+ and pnpm
- Python 3.10+
- Docker (for Milvus)
- ~4GB RAM for the embedding model

### Quick setup

**1. Start Milvus** (vector database)
```bash
docker run -d --name milvus-standalone \
  -p 19530:19530 -p 9091:9091 \
  milvusdb/milvus:v2.4.0 standalone
```

**2. Set up Python env** (for embeddings)
```bash
python -m venv .venv
source .venv/bin/activate
pip install sentence-transformers flask
```

**3. Get your cookies**

Log into messenger.com, export cookies with a browser extension (Netscape format), then:
```bash
cd meta-bridge && go build ./cmd/cookie-converter
./cookie-converter ../cookies.txt ../cookies.json
```

**4. Import your history** (optional but recommended)

Download your data from [Facebook](https://www.facebook.com/dyi) (Settings → Your Information → Download Your Information), then:
```bash
go build ./cmd/import-export
./import-export -zip ~/Downloads/facebook-export.zip -db ../messenger.db
```

**5. Run it**
```bash
./start.sh              # Just search
./start.sh --sync       # Search + live capture
```

Open http://localhost:5173 and start searching.

## Configuration

Everything is in `rag.yaml`. The defaults work fine, but you can tweak:

```yaml
embedding:
  model: mmlw-roberta-large  # Default: Polish. See below for other languages
  dimension: 1024

search:
  default_limit: 20
  context_radius: 2          # Messages before/after each result

quality:
  min_chars: 250             # Skip tiny chunks
  min_unique_words: 8        # Skip "haha ok" conversations
```

**Embedding models for other languages:**
- English: `sentence-transformers/all-mpnet-base-v2` (768 dim)
- Multilingual: `sentence-transformers/paraphrase-multilingual-mpnet-base-v2` (768 dim)
- German: `deutsche-telekom/gbert-large-paraphrase-cosine` (1024 dim)
- Chinese: `shibing624/text2vec-base-chinese` (768 dim)

Just change `model` and `dimension` in `rag.yaml` and reindex.

## Advanced usage

**Full reindex** (after config changes):
```bash
./bin/fts5-setup -db messenger.db --from-db
./bin/milvus-index -db messenger.db --drop
```

**Incremental sync** (daily use):
```bash
./bin/fts5-setup -db messenger.db --from-db
./bin/milvus-index -db messenger.db
```

Only new/changed chunks get re-embedded. A 500k message database takes ~10 minutes for full reindex, <1 second for incremental.

## Tech stack

| What | Why |
|------|-----|
| [mautrix-meta](https://github.com/mautrix/meta) | Facebook protocol (WebSocket, E2EE) |
| [Milvus](https://milvus.io/) | Vector similarity search |
| [sentence-transformers](https://www.sbert.net/) | Embedding models (swap for your language) |
| SQLite + FTS5 | Storage + keyword search |
| SvelteKit | Web UI |
| Go | Everything else |

## Credits

The Messenger protocol implementation is based on [mautrix-meta](https://github.com/mautrix/meta) by [Tulir Asokan](https://github.com/tulir). Seriously impressive reverse-engineering work. Check out their [Matrix bridge](https://docs.mau.fi/bridges/go/meta/) if you want Messenger in Matrix.

## License

AGPL-3.0 (inherited from mautrix-meta)
