#!/usr/bin/env python3
"""
Chunk Embedding Generator for RunPod

This script generates embeddings for message chunks created by generate_chunks.py.
Designed to run on RunPod with Ollama and qwen3-embedding:8b model.

Usage on RunPod:
    # Upload chunks.jsonl and this script to /workspace/
    ollama pull qwen3-embedding:8b
    python3 embed_chunks.py --input chunks.jsonl --output chunk_embeddings/ --pod-id 0

For parallel processing across multiple pods:
    # Split the JSONL first:
    split -l 5000 chunks.jsonl chunk_part_

    # Then run on each pod with different input files
"""

import argparse
import json
import requests
import os
import sys
from typing import List, Dict, Any

OLLAMA_URL = "http://localhost:11434/api/embed"
MODEL = "qwen3-embedding:8b"
EMBED_BATCH_SIZE = 25  # Chunks are larger than messages, use smaller batch
FILE_BATCH_SIZE = 100  # Chunks per output file


def get_embeddings_batch(texts: List[str]) -> List[List[float]]:
    """Get embeddings for multiple texts in one API call."""
    response = requests.post(OLLAMA_URL, json={
        "model": MODEL,
        "input": texts
    }, timeout=300)
    response.raise_for_status()
    return response.json()["embeddings"]


def load_chunks(input_path: str, offset: int = 0, count: int = None) -> List[Dict[str, Any]]:
    """Load chunks from JSONL file with optional offset and count."""
    chunks = []
    with open(input_path, 'r') as f:
        for i, line in enumerate(f):
            if i < offset:
                continue
            if count is not None and len(chunks) >= count:
                break
            chunks.append(json.loads(line.strip()))
    return chunks


def count_chunks(input_path: str) -> int:
    """Count total chunks in JSONL file."""
    count = 0
    with open(input_path, 'r') as f:
        for _ in f:
            count += 1
    return count


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

    print(f"[P{pod_id}] DONE! {processed} chunks, {file_batch_num} files", flush=True)


def save_batch(batch: List[Dict], pod_id: int, batch_num: int, output_dir: str):
    """Save a batch of embedded chunks to JSON."""
    filename = f"chunks_pod{pod_id}_batch_{batch_num:06d}.json"
    filepath = os.path.join(output_dir, filename)
    with open(filepath, 'w') as f:
        json.dump(batch, f)


def main():
    parser = argparse.ArgumentParser(description="Generate embeddings for message chunks")
    parser.add_argument('--input', type=str, default='chunks.jsonl', help='Input JSONL file with chunks')
    parser.add_argument('--output', type=str, default='chunk_embeddings', help='Output directory for embeddings')
    parser.add_argument('--pod-id', type=int, default=0, help='Pod identifier for parallel processing')
    parser.add_argument('--offset', type=int, default=0, help='Start from this chunk index')
    parser.add_argument('--count', type=int, default=None, help='Process this many chunks (default: all)')
    parser.add_argument('--stats', action='store_true', help='Print stats only without processing')
    args = parser.parse_args()

    # Count total chunks
    total_chunks = count_chunks(args.input)
    print(f"Input file: {args.input}")
    print(f"Total chunks in file: {total_chunks}")

    if args.stats:
        # Calculate split recommendations
        print()
        print("Recommended splits for parallel processing:")
        for n_pods in [2, 3, 4, 5, 7]:
            per_pod = total_chunks // n_pods
            print(f"  {n_pods} pods: {per_pod} chunks each")
            for p in range(n_pods):
                offset = p * per_pod
                count = per_pod if p < n_pods - 1 else total_chunks - offset
                print(f"    Pod {p}: --offset {offset} --count {count}")
        return

    # Load chunks
    print(f"Loading chunks (offset={args.offset}, count={args.count or 'all'})...")
    chunks = load_chunks(args.input, args.offset, args.count)
    print(f"Loaded {len(chunks)} chunks")

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
