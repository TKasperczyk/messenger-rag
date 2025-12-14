import { json, error } from '@sveltejs/kit';
import { RAG_SERVER_URL } from '$env/static/private';
import { search as ragSearch, type SearchHit, type SearchMode } from '$lib/server/rag-backend';
import { semanticSearch, chunkSemanticSearch, hasChunkCollection, type ChunkSearchResult } from '$lib/server/milvus';
import { hybridSearch, hybridSearchWithContext, bm25Search, type HybridSearchResult, type HybridSearchResultWithContext } from '$lib/server/hybrid';
import type { RequestHandler } from './$types';

// Check if Go backend is configured
const useGoBackend = !!RAG_SERVER_URL;

// Transform Go backend results to frontend format
function transformGoHit(hit: SearchHit) {
	const primarySender = hit.participant_names[0] || 'Unknown';

	return {
		message_id: hit.chunk_id,
		thread_id: String(hit.thread_id),
		sender_id: String(hit.participant_ids[0] || 0),
		sender_name: primarySender,
		thread_name: hit.thread_name,
		text: hit.text,
		timestamp_ms: hit.start_timestamp_ms,
		score: hit.rrf_score ?? hit.vector_score ?? hit.bm25_score ?? 0,
		is_chunk: true,
		chunk_metadata: {
			chunk_id: hit.chunk_id,
			message_count: hit.message_count,
			participant_names: hit.participant_names,
			start_timestamp_ms: hit.start_timestamp_ms,
			end_timestamp_ms: hit.end_timestamp_ms,
			session_idx: hit.session_idx,
			chunk_idx: hit.chunk_idx
		},
		hybrid_metadata: {
			vector_rank: hit.vector_rank,
			bm25_rank: hit.bm25_rank,
			rrf_score: hit.rrf_score
		},
		context: hit.context_before || hit.context_after ? {
			before: (hit.context_before || []).map((c) => ({
				chunk_id: c.chunk_id,
				text: c.text,
				is_indexable: c.is_indexable
			})),
			after: (hit.context_after || []).map((c) => ({
				chunk_id: c.chunk_id,
				text: c.text,
				is_indexable: c.is_indexable
			}))
		} : undefined
	};
}

// Transform chunk results to a format compatible with the frontend Message interface
function transformChunkToMessage(chunk: ChunkSearchResult) {
	const primarySender = chunk.participant_names[0] || 'Unknown';

	return {
		message_id: chunk.chunk_id,
		thread_id: chunk.thread_id,
		sender_id: String(chunk.participant_ids[0] || 0),
		sender_name: primarySender,
		thread_name: chunk.thread_name,
		text: chunk.text,
		timestamp_ms: chunk.start_timestamp_ms,
		score: chunk.score,
		is_chunk: true,
		chunk_metadata: {
			chunk_id: chunk.chunk_id,
			message_count: chunk.message_count,
			participant_names: chunk.participant_names,
			start_timestamp_ms: chunk.start_timestamp_ms,
			end_timestamp_ms: chunk.end_timestamp_ms,
			session_idx: chunk.session_idx,
			chunk_idx: chunk.chunk_idx
		}
	};
}

// Transform hybrid results with additional ranking info
function transformHybridToMessage(result: HybridSearchResult) {
	const base = transformChunkToMessage(result);
	return {
		...base,
		hybrid_metadata: {
			vector_rank: result.vector_rank,
			bm25_rank: result.bm25_rank,
			rrf_score: result.rrf_score
		}
	};
}

// Transform hybrid results with context
function transformHybridWithContext(result: HybridSearchResultWithContext) {
	const base = transformHybridToMessage(result);
	return {
		...base,
		context: {
			before: result.context_before.map((c) => ({
				chunk_id: c.chunk_id,
				text: c.text,
				is_indexable: c.is_indexable === 1
			})),
			after: result.context_after.map((c) => ({
				chunk_id: c.chunk_id,
				text: c.text,
				is_indexable: c.is_indexable === 1
			}))
		}
	};
}

// Map frontend mode to Go backend mode
function mapMode(mode: string): SearchMode {
	switch (mode) {
		case 'hybrid':
		case 'hybrid-context':
			return 'hybrid';
		case 'bm25':
			return 'bm25';
		case 'vector':
		case 'chunks':
		default:
			return 'vector';
	}
}

export const GET: RequestHandler = async ({ url }) => {
	const query = url.searchParams.get('q');
	const limitParam = parseInt(url.searchParams.get('limit') || '20');
	const mode = url.searchParams.get('mode') || 'vector';
	const contextRadius = parseInt(url.searchParams.get('context') || '1');

	if (!query) {
		throw error(400, 'Query parameter "q" is required');
	}

	if (isNaN(limitParam)) {
		throw error(400, 'Invalid limit parameter');
	}
	const limit = Math.max(1, Math.min(limitParam, 100));

	try {
		// Use Go backend if configured
		if (useGoBackend) {
			const goMode = mapMode(mode);
			const context = mode === 'hybrid-context' ? contextRadius : 0;

			const response = await ragSearch({
				q: query,
				mode: goMode,
				limit,
				context
			});

			return json(response.results.map(transformGoHit));
		}

		// Fallback: use TypeScript implementation directly

		// Hybrid search modes (recommended)
		if (mode === 'hybrid') {
			const results = await hybridSearch(query, limit);
			return json(results.map(transformHybridToMessage));
		}

		if (mode === 'hybrid-context') {
			const results = await hybridSearchWithContext(query, limit, contextRadius);
			return json(results.map(transformHybridWithContext));
		}

		// Pure BM25 search (no embedding needed)
		if (mode === 'bm25') {
			const results = bm25Search(query, limit);
			return json(results.map(transformChunkToMessage));
		}

		// Vector-only search (backwards compatible default)
		if (mode === 'vector' || mode === 'chunks') {
			const chunksAvailable = await hasChunkCollection();
			if (chunksAvailable) {
				const chunkResults = await chunkSemanticSearch(query, limit);
				return json(chunkResults.map(transformChunkToMessage));
			}
			throw error(503, 'Vector collection not available');
		}

		// Legacy per-message search
		if (mode === 'messages') {
			return json(await semanticSearch(query, limit));
		}

		throw error(400, `Unknown mode: ${mode}`);
	} catch (err) {
		console.error('Semantic search failed:', err);
		if (err && typeof err === 'object' && 'status' in err) {
			throw err as any;
		}
		throw error(500, 'Semantic search failed. Is the RAG server running?');
	}
};
