/**
 * RAG Backend Client
 *
 * Calls the Go rag-server HTTP API instead of doing search directly.
 * This ensures all search logic is centralized in the Go backend.
 */

import { RAG_SERVER_URL } from '$env/static/private';

const baseUrl = RAG_SERVER_URL || 'http://localhost:8090';

export type SearchMode = 'vector' | 'bm25' | 'hybrid';

export interface SearchRequest {
	q: string;
	mode?: SearchMode;
	limit?: number;
	context?: number;
	rrf_k?: number;
	w_vector?: number;
	w_bm25?: number;
}

export interface Weights {
	vector: number;
	bm25: number;
}

export interface ContextChunk {
	chunk_id: string;
	chunk_idx: number;
	text: string;
	is_indexable: boolean;
}

export interface SearchHit {
	chunk_id: string;
	thread_id: number;
	thread_name: string;
	participant_ids: number[];
	participant_names: string[];
	text: string;
	message_ids: string[];
	start_timestamp_ms: number;
	end_timestamp_ms: number;
	message_count: number;
	session_idx: number;
	chunk_idx: number;

	// Scoring
	vector_rank: number | null;
	vector_score: number | null;
	bm25_rank: number | null;
	bm25_score: number | null;
	rrf_score: number | null;

	// Context (if requested)
	context_before?: ContextChunk[];
	context_after?: ContextChunk[];
}

export interface SearchResponse {
	query: string;
	mode: SearchMode;
	limit: number;
	context: number;
	rrf_k: number;
	weights: Weights;
	took_ms: number;
	results: SearchHit[];
}

export interface StatsResponse {
	milvus: {
		connected: boolean;
		collection: string;
		row_count: number;
		index_type: string;
		embedding_model: string;
		embedding_dim: number;
	};
	sqlite: {
		connected: boolean;
		chunks_total: number;
		chunks_indexed: number;
		fts_table: string;
		fts_available: boolean;
	};
	config: {
		hash: string;
		collection: string;
		model: string;
		dimension: number;
	};
	timestamp: string;
}

export interface HealthResponse {
	status: 'ok' | 'degraded' | 'unhealthy';
	milvus: boolean;
	sqlite: boolean;
	embedding: boolean;
	timestamp: string;
}

/**
 * Search using the Go RAG backend
 */
export async function search(req: SearchRequest): Promise<SearchResponse> {
	const params = new URLSearchParams();
	params.set('q', req.q);
	if (req.mode) params.set('mode', req.mode);
	if (req.limit) params.set('limit', String(req.limit));
	if (req.context !== undefined) params.set('context', String(req.context));
	if (req.rrf_k) params.set('rrf_k', String(req.rrf_k));
	if (req.w_vector) params.set('w_vector', String(req.w_vector));
	if (req.w_bm25) params.set('w_bm25', String(req.w_bm25));

	const response = await fetch(`${baseUrl}/search?${params.toString()}`);

	if (!response.ok) {
		const error = await response.json().catch(() => ({ error: 'Unknown error' }));
		throw new Error(error.error || `Search failed: ${response.status}`);
	}

	return response.json();
}

/**
 * Get backend statistics
 */
export async function stats(): Promise<StatsResponse> {
	const response = await fetch(`${baseUrl}/stats`);

	if (!response.ok) {
		throw new Error(`Stats failed: ${response.status}`);
	}

	return response.json();
}

/**
 * Check backend health
 */
export async function health(): Promise<HealthResponse> {
	const response = await fetch(`${baseUrl}/health`);

	if (!response.ok) {
		throw new Error(`Health check failed: ${response.status}`);
	}

	return response.json();
}

/**
 * Check if backend is available
 */
export async function isBackendAvailable(): Promise<boolean> {
	try {
		const h = await health();
		return h.status !== 'unhealthy';
	} catch {
		return false;
	}
}
