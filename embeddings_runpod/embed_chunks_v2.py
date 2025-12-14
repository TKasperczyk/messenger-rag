#!/usr/bin/env python3
"""
Chunk Embedding Generator for RunPod (v2)

This script generates embeddings for INDEXABLE message chunks only.
Non-indexable chunks are stored in SQLite for context expansion but not embedded.

Usage on RunPod:
    # Upload chunks_v2.jsonl and this script to /workspace/
    ollama pull qwen3-embedding:8b
    python3 embed_chunks_v2.py --input chunks_v2.jsonl --output chunk_embeddings_v2/

Expected: ~42k indexable chunks (vs 28k in v1)
"""

import argparse
import json
import requests
import os
import sys
from typing import List, Dict, Any

OLLAMA_URL = "http://localhost:11434/api/embed"
MODEL = "qwen3-embedding:8b"
EMBED_BATCH_SIZE = 25  # Chunks per embedding API call
FILE_BATCH_SIZE = 100  # Chunks per output file


def get_embeddings_batch(texts: List[str]) -> List[List[float]]:
    """Get embeddings for multiple texts in one API call."""
    response = requests.post(OLLAMA_URL, json={
        "model": MODEL,
        "input": texts
    }, timeout=300)
    response.raise_for_status()
    return response.json()["embeddings"]


def load_indexable_chunks(input_path: str, offset: int = 0, count: int = None) -> List[Dict[str, Any]]:
    """Load ONLY indexable chunks from JSONL file."""
    chunks = []
    skipped = 0
    with open(input_path, 'r') as f:
        for i, line in enumerate(f):
            chunk = json.loads(line.strip())

            # Skip non-indexable chunks
            if not chunk.get('is_indexable', True):
                skipped += 1
                continue

            # Apply offset (after filtering)
            if len(chunks) + skipped < offset:
                continue

            # Apply count limit
            if count is not None and len(chunks) >= count:
                break

            chunks.append(chunk)

    return chunks, skipped


def count_chunks(input_path: str) -> tuple:
    """Count total and indexable chunks in JSONL file."""
    total = 0
    indexable = 0
    with open(input_path, 'r') as f:
        for line in f:
            chunk = json.loads(line.strip())
            total += 1
            if chunk.get('is_indexable', True):
                indexable += 1
    return total, indexable


def process_chunks(chunks: List[Dict], pod_id: int, output_dir: str):
    """Process chunks with batched embedding calls."""
    os.makedirs(output_dir, exist_ok=True)

    total = len(chunks)
    processed = 0
    file_batch = []
    file_batch_num = 0

    # Process in embedding batches
    for i in range(0, total, EMBED_BATCH_SIZE):
        batch_chunks = chunks[i:i+EMBED_BATCH_SIZE]
        texts = [c["text"] for c in batch_chunks]

        try:
            embeddings = get_embeddings_batch(texts)

            for chunk, emb in zip(batch_chunks, embeddings):
                chunk["embedding"] = emb
                file_batch.append(chunk)
                processed += 1

                # Save file batch when full
                if len(file_batch) >= FILE_BATCH_SIZE:
                    save_batch(file_batch, pod_id, file_batch_num, output_dir)
                    file_batch_num += 1
                    file_batch = []

            # Progress update
            pct = processed * 100 / total
            print(f"[P{pod_id}] {processed}/{total} ({pct:.1f}%) - batch {file_batch_num}", flush=True)

        except Exception as e:
            print(f"Error at batch {i}: {e}", flush=True)
            # Fall back to individual processing for this batch
            for chunk in batch_chunks:
                try:
                    emb = get_embeddings_batch([chunk["text"]])[0]
                    chunk["embedding"] = emb
                    file_batch.append(chunk)
                    processed += 1
                    if len(file_batch) >= FILE_BATCH_SIZE:
                        save_batch(file_batch, pod_id, file_batch_num, output_dir)
                        file_batch_num += 1
                        file_batch = []
                except Exception as e2:
                    print(f"Skip chunk {chunk['chunk_id']}: {e2}", flush=True)

    # Save remaining
    if file_batch:
        save_batch(file_batch, pod_id, file_batch_num, output_dir)
        file_batch_num += 1

    print(f"[P{pod_id}] DONE! {processed} chunks embedded, {file_batch_num} files", flush=True)


def save_batch(batch: List[Dict], pod_id: int, batch_num: int, output_dir: str):
    """Save a batch of embedded chunks to JSON."""
    filename = f"chunks_v2_pod{pod_id}_batch_{batch_num:06d}.json"
    filepath = os.path.join(output_dir, filename)
    with open(filepath, 'w') as f:
        json.dump(batch, f)


def main():
    parser = argparse.ArgumentParser(description="Generate embeddings for indexable chunks (v2)")
    parser.add_argument('--input', type=str, default='chunks_v2.jsonl', help='Input JSONL file with chunks')
    parser.add_argument('--output', type=str, default='chunk_embeddings_v2', help='Output directory for embeddings')
    parser.add_argument('--pod-id', type=int, default=0, help='Pod identifier for parallel processing')
    parser.add_argument('--offset', type=int, default=0, help='Start from this chunk index (among indexable only)')
    parser.add_argument('--count', type=int, default=None, help='Process this many chunks (default: all)')
    parser.add_argument('--stats', action='store_true', help='Print stats only without processing')
    args = parser.parse_args()

    # Count chunks
    total_chunks, indexable_chunks = count_chunks(args.input)
    print(f"Input file: {args.input}")
    print(f"Total chunks: {total_chunks}")
    print(f"Indexable chunks: {indexable_chunks} ({100*indexable_chunks/total_chunks:.1f}%)")
    print(f"Non-indexable (skipped): {total_chunks - indexable_chunks}")
    print()

    if args.stats:
        # Calculate split recommendations
        print("Recommended splits for parallel processing:")
        for n_pods in [2, 3, 4]:
            per_pod = indexable_chunks // n_pods
            print(f"  {n_pods} pods: {per_pod} chunks each")

        # Estimate time
        rate = 25  # chunks per second roughly
        est_time = indexable_chunks / rate / 60
        print(f"\nEstimated time (single GPU): {est_time:.1f} minutes")
        return

    # Load only indexable chunks
    print(f"Loading indexable chunks (offset={args.offset}, count={args.count or 'all'})...")
    chunks, skipped = load_indexable_chunks(args.input, args.offset, args.count)
    print(f"Loaded {len(chunks)} indexable chunks (skipped {skipped} non-indexable)")

    if not chunks:
        print("No chunks to process!")
        return

    # Process
    print(f"Processing with pod_id={args.pod_id}")
    print(f"Output directory: {args.output}")
    print()

    process_chunks(chunks, args.pod_id, args.output)


if __name__ == "__main__":
    main()
