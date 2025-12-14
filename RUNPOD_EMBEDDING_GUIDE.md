# RunPod Embedding Guide for Messenger RAG

This guide documents the process and quirks learned from embedding 520k+ messages using RunPod GPU instances.

## Data Export Locations

### Facebook Exports (Dec 2025)
Location: `/mnt/fb_bkp/`
```
facebook-tkasperczyk-11_12_2025-OVRdqVbG.zip  (2.4 GB)
facebook-tkasperczyk-11_12_2025-OiHhmt3I.zip  (2.5 GB)
facebook-tkasperczyk-11_12_2025-TQiT23Oq.zip  (1.9 GB)
facebook-tkasperczyk-11_12_2025-Tq2TWSCy.zip  (2.5 GB)
facebook-tkasperczyk-11_12_2025-ZD5MyIkS.zip  (2.4 GB)
```
Total: ~12 GB across 5 split archives

### Messenger Export
Location: `/home/luthriel/Important/emulate/fb/messages.zip` (861 MB)

### Older Facebook Export (Oct 2025)
Location: `/home/luthriel/Important/emulate/fb/facebook-tkasperczyk-11_10_2025-7mIMqThf.zip` (559 MB)

## Overview

Embedding locally on a consumer GPU (RTX 3070) was too slow (~14+ hours estimated). Using RunPod with RTX 5090s reduced this to ~1-2 hours across multiple parallel pods.

## Architecture

```
SQLite DB (messenger.db)
    |
    v
[Split messages across N pods]
    |
    v
RunPod Pods (each running Ollama + qwen3-embedding:8b)
    |
    v
JSON batch files (embeddings saved locally per pod)
    |
    v
[Download & merge all batch files]
    |
    v
Insert into Milvus (with deduplication)
```

## Critical Configuration

### RunPod Pod Setup (PROVEN WORKING)

```bash
# GPU: RTX 5090 (Blackwell architecture)
# Image: PyTorch base with CUDA 11.8 (TEI/Infinity images DON'T work on 5090!)
# Cost: ~$0.69/hr per pod

runpodctl create pod \
  --name "embed-5090" \
  --gpuType "NVIDIA GeForce RTX 5090" \
  --containerDiskSize 50 \
  --imageName "runpod/pytorch:2.1.0-py3.10-cuda11.8.0-devel-ubuntu22.04" \
  --ports "8080/http" --ports "22/tcp" \
  --startSSH
```

**IMPORTANT**: TEI (Text Embeddings Inference) and Infinity images crash on RTX 5090 due to Blackwell architecture (compute_120) not being supported. Use the PyTorch base image and install Ollama manually.

### Embedding Model: qwen3-embedding:8b (Q4_K_M quantization)
- **Dimension: 4096** (CRITICAL - must match Milvus collection)
- **Model file**: Qwen3-Embedding-8B-Q4_K_M.gguf (4.68 GB)
- Uses Ollama API at `/api/embed` endpoint
- Batch size: 50 texts per API call for optimal GPU utilization

### Ollama Setup on RunPod Pod

```bash
# 1. Install Ollama
curl -fsSL https://ollama.com/install.sh | sh
ollama serve &

# 2. Download the GGUF model from HuggingFace
pip install huggingface_hub
python3 -c "from huggingface_hub import hf_hub_download; hf_hub_download(repo_id='Qwen/Qwen3-Embedding-8B-GGUF', filename='Qwen3-Embedding-8B-Q4_K_M.gguf', local_dir='/workspace')"

# 3. Create Ollama model from GGUF
cat > /workspace/Modelfile << 'EOF'
FROM /workspace/Qwen3-Embedding-8B-Q4_K_M.gguf
EOF
ollama create qwen3-embedding:8b -f /workspace/Modelfile

# 4. Verify model works (should return 4096-dim embedding)
curl http://localhost:11434/api/embed -d '{"model":"qwen3-embedding:8b","input":"test"}' | python3 -c "import sys,json; d=json.load(sys.stdin); print('Dimension:', len(d['embeddings'][0]))"
```

### Milvus Collection Schema
```python
fields = [
    FieldSchema(name="id", dtype=DataType.VARCHAR, max_length=128, is_primary=True),
    FieldSchema(name="thread_id", dtype=DataType.INT64),
    FieldSchema(name="sender_id", dtype=DataType.INT64),
    FieldSchema(name="sender_name", dtype=DataType.VARCHAR, max_length=256),
    FieldSchema(name="thread_name", dtype=DataType.VARCHAR, max_length=256),
    FieldSchema(name="text", dtype=DataType.VARCHAR, max_length=8192),
    FieldSchema(name="timestamp_ms", dtype=DataType.INT64),
    FieldSchema(name="embedding", dtype=DataType.FLOAT_VECTOR, dim=4096),  # MUST MATCH MODEL
]
```

## Known Quirks & Issues

### 1. Dimension Mismatch
**Problem:** If you change embedding models, the vector dimensions will differ.
**Solution:** Must drop and recreate the Milvus collection:
```bash
# In Go CLI
./messenger-cli -index-existing -drop-collection

# Or via pymilvus
utility.drop_collection("messenger_messages")
```

### 2. Model Name Consistency
**Problem:** Different systems use different model name formats:
- Ollama: `qwen3-embedding:8b`
- LMStudio: `text-embedding-qwen3-embedding-8b`

**Solution:** The Go code defaults to LMStudio format. When using Ollama on RunPod, update the model name in the Python scripts.

### 3. Empty/Short Messages
**Problem:** Single-word messages like "ok", "haha" don't carry semantic meaning.
**Solution:** Consider chunking messages (see "Future Improvements" section).

### 4. Duplicate Message IDs
**Problem:** Re-running embedding can create duplicates in Milvus.
**Solution:** The `insert_milvus.py` script fetches all existing IDs first and skips duplicates:
```python
existing_ids = get_existing_ids(collection)  # Paginated query
new_messages = [m for m in messages if m["id"] not in existing_ids]
```

### 5. Timeout on Large Batches
**Problem:** Large embedding batches can timeout.
**Solution:**
- Set appropriate timeouts: `timeout=300` for embedding requests
- Process in smaller chunks (50 texts per API call, 100 messages per output file)

### 6. Missing Embeddings (The 87k "Missing" Messages)
**Problem:** After initial embedding run, comparison showed ~87k "missing" messages.
**Investigation revealed:** These weren't actually missing!
- Initial run: 520,257 messages (close to SQLite's 520,314)
- "Missing" batch: 87,075 additional embeddings
- Total would be 607k, exceeding SQLite count

**Root cause:** The comparison logic had issues:
1. **Duplicate message IDs** - Same ID appearing in different thread contexts
2. **Empty text filtering** - Different `WHERE text != ''` behavior between queries
3. **Counting methodology** - Python script counted JSON records, not unique IDs

**Solution:** The `embed_missing.py` script was created but the "missing" messages were mostly duplicates. After insertion with deduplication, Milvus correctly rejected the duplicates.

**Lesson learned:** Always verify counts BEFORE re-embedding:
```python
# Check unique IDs, not just row counts
milvus_ids = set(query_all_ids())
sqlite_ids = set(query_sqlite_ids())
actually_missing = sqlite_ids - milvus_ids
```

## Step-by-Step Process

### 1. Prepare RunPod Pod

```bash
# Create pod with RTX 4090
runpodctl create pod \
  --name "embeddings-pod" \
  --gpuType "NVIDIA GeForce RTX 4090" \
  --gpuCount 1 \
  --imageName "ollama/ollama" \
  --ports "11434/http" \
  --containerDiskSize 30 \
  --volumeSize 20

# SSH into pod
runpodctl ssh <pod-id>

# Pull embedding model
ollama pull qwen3-embedding:8b
```

### 2. Upload Database and Scripts

```bash
# From local machine
scp messenger.db root@<pod-ip>:/workspace/
scp parallel_index.py root@<pod-ip>:/workspace/
```

### 3. Split Work Across Pods

For N pods, split the message range:
```bash
# Get total message count
sqlite3 messenger.db "SELECT COUNT(*) FROM messages WHERE text IS NOT NULL AND text != ''"

# Example: 520,000 messages across 7 pods
# Pod 0: offset=0, count=74286
# Pod 1: offset=74286, count=74286
# ...
```

### 4. Run Embedding on Each Pod

```bash
python3 parallel_index.py \
  --offset 0 \
  --count 74286 \
  --pod-id 0 \
  --db /workspace/messenger.db \
  --output /workspace/embeddings
```

### 5. Download Results

```bash
# From local machine
mkdir -p embeddings_runpod/pod0
scp -r root@<pod0-ip>:/workspace/embeddings/* embeddings_runpod/pod0/
# Repeat for each pod
```

### 6. Insert into Milvus

```bash
cd embeddings_runpod
python3 insert_milvus.py
```

## Scripts Reference

### parallel_index.py
- Fetches messages from SQLite with offset/count
- Generates embeddings via Ollama API in batches
- Saves to JSON files (100 messages per file)
- Handles errors gracefully with fallback to single-message processing

### insert_milvus.py
- Connects to local Milvus instance
- Creates collection if doesn't exist
- Fetches existing IDs to prevent duplicates
- Inserts from all pod directories
- Reports inserted vs skipped counts

### embed_missing.py
- Re-embeds messages that failed in initial run
- Reads from `/workspace/missing_messages.json`
- Outputs to `/workspace/embeddings/`

## Verification

After insertion, verify counts:
```python
from pymilvus import Collection, connections

connections.connect("default", host="localhost", port="19530")
collection = Collection("messenger_messages")
print(f"Total vectors: {collection.num_entities}")
```

Compare against SQLite:
```sql
SELECT COUNT(*) FROM messages WHERE text IS NOT NULL AND text != '';
```

## Cost Estimation

- RTX 4090 on RunPod: ~$0.44/hr (community cloud)
- 520k messages: ~1-2 hours total across 7 pods
- Estimated cost: $3-6

## Message Chunking (Recommended)

The per-message embedding approach has poor semantic search results because individual messages are often too short (single words, emojis). The chunking approach groups messages into contextual conversation snippets.

### Chunking Strategy

1. **Message Coalescing**: Merge consecutive messages from the same sender within 120 seconds (max 500 chars)
2. **Session Splitting**: Create new session when conversation gap > 2 hours
3. **Sliding Window**: 30 messages per chunk, 10 message overlap, max 6000 chars

### Results

- Original: 520,314 individual messages
- Chunked: 28,272 conversation chunks
- Compression ratio: **18.4x**

### Chunk Generation Workflow

```bash
# 1. Generate chunks locally (no GPU needed)
cd embeddings_runpod
python3 generate_chunks.py --db ../meta-bridge/messenger.db --output chunks.jsonl

# 2. View statistics
python3 generate_chunks.py --db ../meta-bridge/messenger.db --stats

# 3. Upload to RunPod
scp chunks.jsonl root@<pod-ip>:/workspace/
scp embed_chunks.py root@<pod-ip>:/workspace/

# 4. Generate embeddings on RunPod
python3 embed_chunks.py --input chunks.jsonl --output chunk_embeddings/

# 5. For parallel processing, use --stats to see split recommendations
python3 embed_chunks.py --input chunks.jsonl --stats

# 6. Download embeddings
scp -r root@<pod-ip>:/workspace/chunk_embeddings/* chunk_embeddings/

# 7. Insert into Milvus (new collection: messenger_message_chunks_v1)
python3 insert_chunks_milvus.py
```

### Chunk Collection Schema

```python
fields = [
    FieldSchema(name="chunk_id", dtype=DataType.VARCHAR, max_length=32, is_primary=True),
    FieldSchema(name="thread_id", dtype=DataType.INT64),
    FieldSchema(name="thread_name", dtype=DataType.VARCHAR, max_length=512),
    FieldSchema(name="session_idx", dtype=DataType.INT16),
    FieldSchema(name="chunk_idx", dtype=DataType.INT16),
    FieldSchema(name="participant_ids", dtype=DataType.VARCHAR, max_length=1024),  # JSON array
    FieldSchema(name="participant_names", dtype=DataType.VARCHAR, max_length=2048),  # JSON array
    FieldSchema(name="text", dtype=DataType.VARCHAR, max_length=8192),
    FieldSchema(name="message_ids", dtype=DataType.VARCHAR, max_length=8192),  # JSON array
    FieldSchema(name="start_timestamp_ms", dtype=DataType.INT64),
    FieldSchema(name="end_timestamp_ms", dtype=DataType.INT64),
    FieldSchema(name="message_count", dtype=DataType.INT16),
    FieldSchema(name="embedding", dtype=DataType.FLOAT_VECTOR, dim=4096),
]
```

### Chunk Text Format

Each chunk contains conversation in this format:
```
[Sender1]: First message
[Sender1]: Second message (coalesced if within 120s)
[Sender2]: Reply message
[Sender1]: Another message
...
```

### Frontend Integration

The SvelteKit app automatically uses chunks if the `messenger_message_chunks_v1` collection exists and is populated. To force legacy per-message search, use `?mode=messages` query param.

## Legacy Per-Message Approach

### Pre-filtering (if using per-message)
Skip messages that are:
- Single emoji
- Less than 3 characters
- Pure URLs
- System messages ("You sent a photo")

## Troubleshooting

### "dimension mismatch" error
You're using a different model than what the collection was created with. Either:
1. Drop collection and re-embed with new model
2. Switch back to original model

### Ollama not responding
```bash
# Check if running
curl http://localhost:11434/api/tags

# Restart
systemctl restart ollama
# or
ollama serve
```

### Milvus connection refused
```bash
# Check if running
docker ps | grep milvus

# Start standalone Milvus
docker-compose up -d
```

### Out of GPU memory
Reduce `EMBED_BATCH_SIZE` in Python scripts (default: 50).
