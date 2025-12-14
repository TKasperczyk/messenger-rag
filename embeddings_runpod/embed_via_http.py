#!/usr/bin/env python3
"""
Generate embeddings via RunPod HTTP API.

Processes chunks locally and sends text to RunPod Ollama for embedding.
No file upload needed - just HTTP requests.
"""

import argparse
import json
import requests
import os
import sys
import time
from typing import List, Dict, Any

EMBED_BATCH_SIZE = 25  # Texts per API call
FILE_BATCH_SIZE = 100  # Chunks per output file


def get_embeddings_batch(texts: List[str], ollama_url: str) -> List[List[float]]:
    """Get embeddings for multiple texts in one API call."""
    response = requests.post(
        f"{ollama_url}/api/embed",
        json={"model": "qwen3-embedding:8b", "input": texts},
        timeout=300
    )
    response.raise_for_status()
    return response.json()["embeddings"]


def load_indexable_chunks(input_path: str) -> List[Dict[str, Any]]:
    """Load ONLY indexable chunks from JSONL file."""
    chunks = []
    skipped = 0
    with open(input_path, 'r') as f:
        for line in f:
            chunk = json.loads(line.strip())
            if chunk.get('is_indexable', True):
                chunks.append(chunk)
            else:
                skipped += 1
    return chunks, skipped


def process_chunks(chunks: List[Dict], ollama_url: str, output_dir: str):
    """Process chunks with batched embedding calls."""
    os.makedirs(output_dir, exist_ok=True)

    total = len(chunks)
    processed = 0
    failed = 0
    file_batch = []
    file_batch_num = 0
    start_time = time.time()

    # Process in embedding batches
    for i in range(0, total, EMBED_BATCH_SIZE):
        batch_chunks = chunks[i:i+EMBED_BATCH_SIZE]
        texts = [c["text"] for c in batch_chunks]

        try:
            embeddings = get_embeddings_batch(texts, ollama_url)

            for chunk, emb in zip(batch_chunks, embeddings):
                chunk["embedding"] = emb
                file_batch.append(chunk)
                processed += 1

                # Save file batch when full
                if len(file_batch) >= FILE_BATCH_SIZE:
                    save_batch(file_batch, file_batch_num, output_dir)
                    file_batch_num += 1
                    file_batch = []

            # Progress update
            elapsed = time.time() - start_time
            rate = processed / elapsed if elapsed > 0 else 0
            eta = (total - processed) / rate if rate > 0 else 0
            print(f"[{processed}/{total}] {processed*100/total:.1f}% | {rate:.1f} chunks/s | ETA: {eta/60:.1f}min", flush=True)

        except Exception as e:
            print(f"Error at batch {i}: {e}", flush=True)
            # Fall back to individual processing for this batch
            for chunk in batch_chunks:
                try:
                    emb = get_embeddings_batch([chunk["text"]], ollama_url)[0]
                    chunk["embedding"] = emb
                    file_batch.append(chunk)
                    processed += 1
                    if len(file_batch) >= FILE_BATCH_SIZE:
                        save_batch(file_batch, file_batch_num, output_dir)
                        file_batch_num += 1
                        file_batch = []
                except Exception as e2:
                    print(f"Skip chunk {chunk['chunk_id']}: {e2}", flush=True)
                    failed += 1

    # Save remaining
    if file_batch:
        save_batch(file_batch, file_batch_num, output_dir)
        file_batch_num += 1

    elapsed = time.time() - start_time
    print(f"\nDONE! {processed} chunks embedded in {elapsed/60:.1f} min ({file_batch_num} files)", flush=True)
    if failed > 0:
        print(f"WARNING: {failed} chunks failed", flush=True)


def save_batch(batch: List[Dict], batch_num: int, output_dir: str):
    """Save a batch of embedded chunks to JSON."""
    filename = f"chunks_v2_batch_{batch_num:06d}.json"
    filepath = os.path.join(output_dir, filename)
    with open(filepath, 'w') as f:
        json.dump(batch, f)


def main():
    parser = argparse.ArgumentParser(description="Generate embeddings via RunPod HTTP API")
    parser.add_argument('--input', type=str, default='chunks_v2.jsonl', help='Input JSONL file with chunks')
    parser.add_argument('--output', type=str, default='chunk_embeddings_v2', help='Output directory')
    parser.add_argument('--url', type=str, required=True, help='RunPod Ollama proxy URL (e.g., https://xxx-11434.proxy.runpod.net)')
    parser.add_argument('--test', action='store_true', help='Test connection only')
    args = parser.parse_args()

    # Test connection
    print(f"Testing connection to {args.url}...")
    try:
        resp = requests.get(f"{args.url}/api/tags", timeout=10)
        resp.raise_for_status()
        models = resp.json().get("models", [])
        print(f"Connected! Models available: {[m['name'] for m in models]}")
    except Exception as e:
        print(f"Connection failed: {e}")
        sys.exit(1)

    if args.test:
        # Quick embedding test
        print("\nTesting embedding...")
        emb = get_embeddings_batch(["test"], args.url)
        print(f"Embedding dimension: {len(emb[0])}")
        return

    # Load chunks
    print(f"\nLoading chunks from {args.input}...")
    chunks, skipped = load_indexable_chunks(args.input)
    print(f"Loaded {len(chunks)} indexable chunks (skipped {skipped} non-indexable)")

    if not chunks:
        print("No chunks to process!")
        return

    # Process
    print(f"\nStarting embedding...")
    print(f"Output directory: {args.output}")
    print()

    process_chunks(chunks, args.url, args.output)


if __name__ == "__main__":
    main()
