# Sample Data

This directory contains sample conversation data for demo purposes.

## Quick Demo

```bash
# From the repo root:
./demo.sh
```

This will:
1. Import sample conversations into `demo.db`
2. Start the embedding server, RAG server, and web UI
3. Open http://localhost:5173

## Sample Queries to Try

The sample data is designed to showcase semantic search. Try these queries:

| Query | What it finds |
|-------|---------------|
| "camping trip with rain" | Weekend hiking trip where the roof leaked |
| "book about AI" | Klara and the Sun recommendation |
| "grandma's dessert recipe" | Cheesecake recipe from Sam |
| "deployment problems" | Work chat about OOM killed container |
| "birthday celebration" | 30th birthday party planning |
| "sci-fi movie we watched" | Dune Part 2 movie night |
| "budget tracking app" | YNAB recommendation |
| "back pain from sitting" | Standing desk conversation |
| "trip to asia" | Japan travel planning |
| "cute dog photos" | Pet Photos group chat |

## Manual Setup

If you want to run each step manually:

```bash
# 1. Build the import tool
cd meta-bridge
go build -o ../bin/import-sample ./cmd/import-sample

# 2. Import sample data
cd ..
./bin/import-sample -json sample_data/conversations.json -db demo.db

# 3. Generate chunks
./bin/fts5-setup -db demo.db --from-db

# 4. Index to Milvus (requires embedding server running)
./bin/milvus-index -db demo.db --drop

# 5. Start servers with demo database
DB_PATH=demo.db ./start.sh
```

## Customizing

Edit `conversations.json` to add your own sample conversations. The format is:

```json
{
  "contacts": [
    {"id": 1001, "name": "Person Name"}
  ],
  "threads": [
    {
      "id": 9001,
      "name": "Thread Name",
      "type": 1,  // 1 = DM, 2 = group
      "participants": [1001, 1002],
      "messages": [
        {"sender": 1001, "text": "message text", "ts": "2024-01-01T12:00:00Z"}
      ]
    }
  ]
}
```
