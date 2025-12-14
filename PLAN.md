# Messenger RAG with MCP Integration

## Project Goal

Turn all Messenger conversations into a RAG (Retrieval-Augmented Generation) system and expose it as an MCP server for Claude Code. Support real-time updates when new messages arrive.

## Background & Research (Dec 6, 2025)

### The Problem

Facebook/Meta doesn't provide a public API for accessing personal Messenger data:
- **Messenger Platform API** - Only for business chatbots
- **Graph API** - Personal message access removed post-2018 (Cambridge Analytica)
- **Official path** - Manual data export via "Download Your Information"

### The Solution

Use the `mautrix-meta` bridge's core Facebook protocol library (`pkg/messagix/`) which:
- Handles Facebook authentication via cookies
- Connects to Messenger's WebSocket for real-time messages
- Parses the internal message protocol
- **Has zero Matrix dependencies** in the core messagix package

We can extract just the Facebook parts and replace the Matrix output with our own RAG pipeline.

## Architecture

```
┌──────────────────────────────────────────────────────────────────┐
│                    messenger-rag                                  │
│                                                                   │
│  ┌─────────────────────────────────┐   ┌───────────────────────┐ │
│  │      pkg/messagix/              │   │   Your Code           │ │
│  │  (From mautrix-meta - KEEP)     │   │                       │ │
│  │                                 │   │  ┌─────────────────┐  │ │
│  │  • socket.go   → WebSocket      │   │  │ SQLite/Vector   │  │ │
│  │  • client.go   → Session mgmt   │──→│  │ DB Storage      │  │ │
│  │  • events.go   → Event types    │   │  └─────────────────┘  │ │
│  │  • table/*.go  → Message structs│   │           │           │ │
│  │  • cookies/    → Auth           │   │  ┌─────────────────┐  │ │
│  │  • e2ee-*.go   → Encryption     │   │  │ MCP Server      │  │ │
│  └─────────────────────────────────┘   │  │ (search tools)  │  │ │
│                                        │  └─────────────────┘  │ │
│                                        └───────────────────────┘ │
└──────────────────────────────────────────────────────────────────┘
```

## Current Status

### What's Done (PoC in `meta-bridge/`)

1. **Cloned mautrix-meta** - Full repo in `meta-bridge/`

2. **Created standalone CLI** (`meta-bridge/cmd/messenger-cli/main.go`)
   - Loads cookies from JSON file
   - Connects to Messenger via the messagix library
   - Receives real-time messages via WebSocket
   - Handles: new messages, edits, reactions, typing indicators, read receipts, thread info
   - **Now stores data to SQLite database** (Dec 10, 2025)

3. **Code Analysis Complete**
   - Traced message flow: socket.go → client.go → event handler
   - Identified Matrix API call points (in `pkg/connector/`)
   - Confirmed `pkg/messagix/` has no Matrix dependencies
   - Identified E2EE complexity (uses whatsmeow/Signal protocol)

4. **SQLite Storage Implemented** (Dec 10, 2025)
   - Created `pkg/storage/` package with schema and operations
   - Schema includes: contacts, threads, thread_participants, messages, attachments, reactions
   - Full-text search using FTS4 virtual table
   - CLI now supports:
     - `-db path` - Specify database file (default: messenger.db)
     - `-stats` - Show database statistics
     - `-search "query"` - Search messages
     - `-v` - Verbose logging
     - `-e2ee` - Toggle E2EE (default: enabled)

5. **E2EE Support Implemented** (Dec 10, 2025)
   - Uses whatsmeow's Signal protocol implementation
   - Automatic device registration with Facebook's ICDC
   - E2EE keys stored in same SQLite database via sqlstore
   - Receives and decrypts E2EE messages in real-time
   - Successfully tested: 72/93 messages now have decrypted text

6. **Cookie Converter Tool** (Dec 10, 2025)
   - Created `cmd/cookie-converter/` to convert Netscape cookies.txt to JSON
   - Usage: `./cookie-converter ~/Downloads/cookies.txt cookies.json`

7. **Vector Database & Semantic Search** (Dec 10, 2025)
   - Created `pkg/vectordb/` package with Milvus client and LMStudio embeddings
   - Collection: `messenger_messages` (separate from other Milvus data)
   - Uses `text-embedding-nomic-embed-text-v1.5` model (768 dimensions)
   - IVF_FLAT index with COSINE similarity
   - CLI options:
     - `-vector` - Enable real-time vector indexing
     - `-semantic "query"` - Semantic search
     - `-index-existing` - Batch index existing messages
     - `-milvus` - Milvus server address
     - `-lmstudio` - LMStudio API URL
   - Successfully indexed 72 messages

### Key Files

```
meta-bridge/
├── cmd/
│   ├── messenger-cli/
│   │   └── main.go          # CLI with SQLite + vector storage
│   └── cookie-converter/
│       └── main.go          # Convert browser cookies to JSON
├── pkg/
│   ├── messagix/            # KEEP - Pure FB protocol, no Matrix deps
│   │   ├── client.go        # Main client, session management
│   │   ├── socket.go        # WebSocket handling
│   │   ├── events.go        # Event types (Event_Ready, Event_PublishResponse, etc.)
│   │   ├── e2ee-client.go   # E2EE via whatsmeow (needed for encrypted chats)
│   │   ├── e2ee-register.go # E2EE device registration
│   │   ├── cookies/         # Auth cookie handling
│   │   └── table/           # Message data structures (LSInsertMessage, etc.)
│   ├── storage/             # SQLite storage layer
│   │   ├── schema.go        # Database schema (contacts, threads, messages, etc.)
│   │   ├── storage.go       # CRUD operations for all entities
│   │   └── e2ee.go          # E2EE metadata storage
│   ├── vectordb/            # Vector database integration
│   │   ├── milvus.go        # Milvus client and collection schema
│   │   └── embeddings.go    # LMStudio embedding generation
│   ├── connector/           # UNUSED - Matrix-specific glue code
│   └── msgconv/             # PARTIAL - Message conversion utilities
└── go.mod                   # Dependencies
```

## What Needs To Be Done

### Phase 1: Basic Message Capture

1. **~~Add SQLite Storage~~** ✅ DONE
   - ~~Create schema for messages, threads, contacts~~
   - ~~Replace console logging with DB writes in `handleTable()`~~
   - ~~Store: message_id, thread_id, sender_id, text, timestamp, attachments~~

2. **Cookie Extraction Helper** (TODO)
   - Script/guide to export cookies from browser (Firefox/Chrome)
   - Format conversion to the expected JSON structure

3. **Historical Import** (TODO)
   - Parse Facebook's "Download Your Information" export
   - Import existing messages to bootstrap the RAG

### Phase 2: E2EE Support ✅ DONE

~~**Your messages are encrypted**, which means:~~

1. ~~**Understand the E2EE flow**~~
   ~~- Uses `go.mau.fi/whatsmeow` (WhatsApp's Signal protocol implementation)~~
   ~~- Need to handle: device registration, key exchange, decryption~~
   ~~- State must be persisted (encryption keys, sessions)~~

2. ~~**Required components from mautrix-meta**~~
   ~~- `e2ee-client.go` - Client setup~~
   ~~- `e2ee-register.go` - Device registration with Facebook~~
   ~~- Database for storing encryption state (keys, sessions)~~

**Implementation completed Dec 10, 2025:**
- Device auto-registers with Facebook on first run
- Keys persisted in SQLite via whatsmeow's sqlstore
- E2EE messages decrypted in real-time

### Phase 3: Vector Database & Embeddings ✅ DONE

~~1. **Choose embedding approach**~~
   ~~- Local: sentence-transformers~~
   ~~- API: OpenAI embeddings~~

~~2. **Choose vector DB**~~
   ~~- Local options: ChromaDB, SQLite with vector extension, FAISS~~
   ~~- Consider: Qdrant (also local-first)~~

~~3. **Implement chunking strategy**~~
   ~~- Conversation-based chunks (preserve context)~~
   ~~- Include metadata: sender, timestamp, thread~~

**Implementation completed Dec 10, 2025:**
- Using Milvus (local instance) with separate `messenger_messages` collection
- LMStudio with `text-embedding-nomic-embed-text-v1.5` (768 dim)
- Each message indexed individually with metadata (sender, thread, timestamp)
- IVF_FLAT index with COSINE similarity for semantic search

### Phase 4: MCP Server

1. **Expose search tools**
   - `search_messages(query, limit)` → Semantic search
   - `get_conversation(person, date_range)` → Retrieve specific chats
   - `list_contacts()` → Who you've messaged

2. **Integration with Claude Code**
   - Configure as MCP server in Claude Code settings
   - Test retrieval quality

## Technical Details

### Message Flow (from code analysis)

```
1. socket.go:196    readLoop() receives WebSocket data
2. socket.go:249    Response.Read() parses binary → event struct
3. client.go:268    HandleEvent(ctx, evt) calls registered handler
4. YOUR CODE        handleMetaEvent() receives:
                    - Event_PublishResponse → contains table.LSTable with:
                      - LSInsertMessage (new messages)
                      - LSUpsertMessage (edits)
                      - LSUpsertReaction (reactions)
                      - LSDeleteThenInsertThread (thread info)
                    - Event_Ready (connection established)
                    - Event_SocketError / Event_PermanentError
```

### Key Data Structure: `table.LSInsertMessage`

```go
type LSInsertMessage struct {
    ThreadKey       int64
    SenderId        int64
    MessageId       string
    TimestampMs     int64
    Text            string
    // ... attachments, replies, etc.
}
```

### Cookie Format

```json
{
  "c_user": "your_fb_user_id",
  "xs": "session_token",
  "datr": "...",
  "fr": "...",
  // ... other FB cookies
  "Platform": "messenger"
}
```

## Running the CLI

```bash
cd meta-bridge

# Build
go build ./cmd/messenger-cli

# Export cookies from browser and save to cookies.json
# Then run:
./messenger-cli cookies.json

# Other options:
./messenger-cli -db mydata.db cookies.json    # Custom database path
./messenger-cli -stats                         # Show database statistics
./messenger-cli -search "keyword"              # Search messages
./messenger-cli -v cookies.json                # Verbose logging
```

## Risks & Considerations

1. **ToS**: Using unofficial APIs may violate Facebook's Terms of Service
2. **Fragility**: Facebook can change their internal protocol at any time
3. **E2EE Complexity**: Signal protocol is non-trivial to implement/maintain
4. **Session Management**: Cookies expire, need refresh mechanism
5. **Rate Limiting**: Facebook may rate-limit or block aggressive usage

## Next Immediate Steps

1. [x] Test the CLI with your actual cookies
2. [x] Verify E2EE messages can be decrypted
3. [x] Design SQLite schema for messages
4. [x] Implement basic storage (replace logging with DB writes)
5. [x] Create cookie extraction guide (cookie-converter tool)
6. [ ] Implement MCP server for Claude Code integration
7. [ ] Add vector embeddings for semantic search
8. [ ] Historical message import from Facebook data export

## References

- **mautrix-meta**: https://github.com/mautrix/meta
- **whatsmeow**: https://github.com/tulir/whatsmeow (Signal protocol for E2EE)
- **Original conversation**: Dec 6, 2025 on archer@10.11.12.60

---

*Last updated: Dec 10, 2025 - Vector DB & semantic search completed*
