/**
 * Hybrid search combining vector (Milvus) and BM25 (SQLite FTS5) results
 * Uses Reciprocal Rank Fusion (RRF) for score combination
 */

import { chunkSemanticSearch, type ChunkSearchResult } from './milvus';
import { ftsSearch, getChunkContext, type FtsSearchResult, type ChunkContext } from './sqlite';
import { hybridConfig } from './rag-config';

const DEFAULT_RRF_K = 60;

function getRrfK(): number {
	const k = hybridConfig.rrf.k;
	return Number.isFinite(k) && k > 0 ? k : DEFAULT_RRF_K;
}

function getHybridWeights(): { vector: number; bm25: number } {
	const vector = hybridConfig.weights.vector;
	const bm25 = hybridConfig.weights.bm25;
	const sum = vector + bm25;

	if (!Number.isFinite(vector) || !Number.isFinite(bm25) || sum <= 0) {
		return { vector: 0.5, bm25: 0.5 };
	}

	return { vector: vector / sum, bm25: bm25 / sum };
}

// RRF constant (typically 60)
const RRF_K = getRrfK();
const { vector: VECTOR_WEIGHT, bm25: BM25_WEIGHT } = getHybridWeights();

export interface HybridSearchResult extends ChunkSearchResult {
	vector_rank: number | null;
	bm25_rank: number | null;
	rrf_score: number;
}

export interface HybridSearchResultWithContext extends HybridSearchResult {
	context_before: ChunkContext[];
	context_after: ChunkContext[];
}

/**
 * Reciprocal Rank Fusion score calculation
 */
function rrfScore(vectorRank: number | null, bm25Rank: number | null): number {
	let score = 0;
	if (vectorRank !== null) {
		score += VECTOR_WEIGHT / (RRF_K + vectorRank);
	}
	if (bm25Rank !== null) {
		score += BM25_WEIGHT / (RRF_K + bm25Rank);
	}
	return score;
}

/**
 * Convert FTS result to ChunkSearchResult format
 */
function ftsToChunk(fts: FtsSearchResult): ChunkSearchResult {
	return {
		chunk_id: fts.chunk_id,
		thread_id: String(fts.thread_id),
		thread_name: fts.thread_name ?? '',
		participant_ids: JSON.parse(fts.participant_ids || '[]'),
		participant_names: JSON.parse(fts.participant_names || '[]'),
		text: fts.text,
		message_ids: JSON.parse(fts.message_ids || '[]'),
		start_timestamp_ms: fts.start_timestamp_ms,
		end_timestamp_ms: fts.end_timestamp_ms,
		message_count: fts.message_count,
		session_idx: fts.session_idx,
		chunk_idx: fts.chunk_idx,
		score: Math.abs(fts.bm25_score) // BM25 scores are negative, lower is better
	};
}

/**
 * Hybrid search combining vector and BM25 results using RRF
 */
export async function hybridSearch(query: string, limit = 20): Promise<HybridSearchResult[]> {
	if (!hybridConfig.enabled) {
		const vectorResults = await chunkSemanticSearch(query, limit);
		return vectorResults.map((chunk, i) => {
			const vectorRank = i + 1;
			return {
				...chunk,
				vector_rank: vectorRank,
				bm25_rank: null,
				rrf_score: rrfScore(vectorRank, null)
			};
		});
	}

	// Run both searches in parallel
	const [vectorResults, bm25Results] = await Promise.all([
		chunkSemanticSearch(query, limit * 2), // Get more for fusion
		Promise.resolve(ftsSearch(query, limit * 2))
	]);

	// Build rank maps
	const vectorRanks = new Map<string, number>();
	vectorResults.forEach((r, i) => vectorRanks.set(r.chunk_id, i + 1));

	const bm25Ranks = new Map<string, number>();
	bm25Results.forEach((r, i) => bm25Ranks.set(r.chunk_id, i + 1));

	// Collect all unique chunk IDs
	const allChunkIds = new Set([
		...vectorResults.map((r) => r.chunk_id),
		...bm25Results.map((r) => r.chunk_id)
	]);

	// Build chunk data map (prefer vector results as they have more complete data)
	const chunkDataMap = new Map<string, ChunkSearchResult>();
	for (const r of bm25Results) {
		chunkDataMap.set(r.chunk_id, ftsToChunk(r));
	}
	for (const r of vectorResults) {
		chunkDataMap.set(r.chunk_id, r);
	}

	// Calculate RRF scores and create results
	const fusedResults: HybridSearchResult[] = [];

	for (const chunkId of allChunkIds) {
		const vectorRank = vectorRanks.get(chunkId) ?? null;
		const bm25Rank = bm25Ranks.get(chunkId) ?? null;
		const chunk = chunkDataMap.get(chunkId)!;

		fusedResults.push({
			...chunk,
			vector_rank: vectorRank,
			bm25_rank: bm25Rank,
			rrf_score: rrfScore(vectorRank, bm25Rank)
		});
	}

	// Sort by RRF score (descending)
	fusedResults.sort((a, b) => {
		const scoreDiff = b.rrf_score - a.rrf_score;
		if (scoreDiff !== 0) return scoreDiff;

		const aHasBoth = a.vector_rank !== null && a.bm25_rank !== null;
		const bHasBoth = b.vector_rank !== null && b.bm25_rank !== null;
		if (aHasBoth && !bHasBoth) return -1;
		if (!aHasBoth && bHasBoth) return 1;

		const aBm25 = a.bm25_rank ?? Number.POSITIVE_INFINITY;
		const bBm25 = b.bm25_rank ?? Number.POSITIVE_INFINITY;
		if (aBm25 !== bBm25) return aBm25 - bBm25;

		const aVec = a.vector_rank ?? Number.POSITIVE_INFINITY;
		const bVec = b.vector_rank ?? Number.POSITIVE_INFINITY;
		if (aVec !== bVec) return aVec - bVec;

		return 0;
	});

	return fusedResults.slice(0, limit);
}

/**
 * Hybrid search with context expansion
 * Returns search results with surrounding chunks for better context
 */
export async function hybridSearchWithContext(
	query: string,
	limit = 10,
	contextRadius = 1
): Promise<HybridSearchResultWithContext[]> {
	const results = await hybridSearch(query, limit);

	return results.map((result) => {
		// Get context from SQLite
		const context = getChunkContext(
			Number(result.thread_id),
			result.session_idx,
			result.chunk_idx,
			contextRadius
		);

		// Split into before and after
		const contextBefore: ChunkContext[] = [];
		const contextAfter: ChunkContext[] = [];

		for (const chunk of context) {
			if (chunk.chunk_id === result.chunk_id) continue;
			if (chunk.chunk_idx < result.chunk_idx) {
				contextBefore.push(chunk);
			} else {
				contextAfter.push(chunk);
			}
		}

		return {
			...result,
			context_before: contextBefore,
			context_after: contextAfter
		};
	});
}

/**
 * Pure BM25 search (for comparison/fallback)
 */
export function bm25Search(query: string, limit = 20): ChunkSearchResult[] {
	const results = ftsSearch(query, limit);
	return results.map(ftsToChunk);
}
