import { MilvusClient } from '@zilliz/milvus2-sdk-node';
import { MILVUS_ADDRESS, LMSTUDIO_URL, EMBED_MODEL } from '$env/static/private';
import { milvusConfig, embeddingConfig, qualityConfig } from './rag-config';

// Use env vars with fallback to unified config from rag-config.ts
const milvusAddress = MILVUS_ADDRESS || milvusConfig.address;
const lmstudioUrl = LMSTUDIO_URL || embeddingConfig.baseUrl;
const embedModel = EMBED_MODEL || embeddingConfig.model;

// Collection name from unified config
const CHUNK_COLLECTION = milvusConfig.chunkCollection;

// Search parameters from rag.yaml (loaded at runtime via rag-config.ts)
const SEARCH_METRIC_TYPE = milvusConfig.index.metric;
const SEARCH_MIN_EF = milvusConfig.search.ef;
const SEARCH_FETCH_MULTIPLIER = Math.max(1, milvusConfig.search.fetchMultiplier);

let client: MilvusClient | null = null;

const EMBEDDING_TIMEOUT_MS = 30_000;

export async function getMilvusClient(): Promise<MilvusClient> {
	if (!client) {
		client = new MilvusClient({ address: milvusAddress });
	}
	return client;
}

function safeJsonParseArray(value: unknown): unknown[] {
	if (Array.isArray(value)) return value;
	if (typeof value !== 'string') return [];
	const trimmed = value.trim();
	if (!trimmed) return [];
	try {
		const parsed = JSON.parse(trimmed);
		return Array.isArray(parsed) ? parsed : [];
	} catch {
		return [];
	}
}

export function safeJsonParseNumberArray(value: unknown): number[] {
	return safeJsonParseArray(value)
		.map((v) => (typeof v === 'number' ? v : Number(v)))
		.filter((n): n is number => Number.isFinite(n));
}

export function safeJsonParseStringArray(value: unknown): string[] {
	return safeJsonParseArray(value)
		.map((v) => (v == null ? '' : String(v)))
		.filter((s) => s.length > 0);
}

// Legacy message-based search result (for backward compatibility)
export interface SemanticSearchResult {
	message_id: string;
	thread_id: string;
	sender_id: string;
	sender_name: string;
	thread_name: string;
	text: string;
	timestamp_ms: number;
	score: number;
}

export interface SemanticSearchResultWithEmbedding extends SemanticSearchResult {
	embedding: number[];
}

// New chunk-based search result
export interface ChunkSearchResult {
	chunk_id: string;
	thread_id: string;
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
	score: number;
}

export interface ChunkSearchResultWithEmbedding extends ChunkSearchResult {
	embedding: number[];
}

async function getEmbedding(text: string): Promise<number[]> {
	const controller = new AbortController();
	const timeoutId = setTimeout(() => controller.abort(), EMBEDDING_TIMEOUT_MS);

	try {
		const response = await fetch(`${lmstudioUrl}/embeddings`, {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({
				model: embedModel,
				input: text
			}),
			signal: controller.signal
		});

		if (!response.ok) {
			throw new Error(`Embedding API error: ${response.statusText}`);
		}

		const data = await response.json();
		return data.data[0].embedding;
	} catch (err) {
		if ((err as any)?.name === 'AbortError') {
			throw new Error(`Embedding request timed out after ${Math.round(EMBEDDING_TIMEOUT_MS / 1000)}s`);
		}
		throw err;
	} finally {
		clearTimeout(timeoutId);
	}
}

/**
 * Legacy semantic search - now delegates to chunk search
 * @deprecated Use chunkSemanticSearch directly
 */
export async function semanticSearch(
	query: string,
	limit = 20
): Promise<SemanticSearchResult[]> {
	// Delegate to chunk search and transform results
	const chunks = await chunkSemanticSearch(query, limit);
	return chunks.map((chunk) => ({
		message_id: chunk.chunk_id,
		thread_id: chunk.thread_id,
		sender_id: String(chunk.participant_ids[0] || 0),
		sender_name: chunk.participant_names[0] || 'Unknown',
		thread_name: chunk.thread_name,
		text: chunk.text,
		timestamp_ms: chunk.start_timestamp_ms,
		score: chunk.score
	}));
}

/**
 * Legacy semantic search with embeddings - now delegates to chunk search
 * @deprecated Use chunkSemanticSearchWithEmbeddings directly
 */
export async function semanticSearchWithEmbeddings(
	query: string,
	limit = 50
): Promise<SemanticSearchResultWithEmbedding[]> {
	// Delegate to chunk search and transform results
	const chunks = await chunkSemanticSearchWithEmbeddings(query, limit);
	return chunks.map((chunk) => ({
		message_id: chunk.chunk_id,
		thread_id: chunk.thread_id,
		sender_id: String(chunk.participant_ids[0] || 0),
		sender_name: chunk.participant_names[0] || 'Unknown',
		thread_name: chunk.thread_name,
		text: chunk.text,
		timestamp_ms: chunk.start_timestamp_ms,
		score: chunk.score,
		embedding: chunk.embedding
	}));
}

/**
 * @deprecated Use getChunkCollectionStats instead
 */
export async function getCollectionStats() {
	// Delegate to chunk collection stats
	return getChunkCollectionStats();
}

/**
 * Get stats for the chunks collection
 */
export async function getChunkCollectionStats() {
	try {
		const milvus = await getMilvusClient();
		const stats = await milvus.getCollectionStatistics({
			collection_name: CHUNK_COLLECTION
		});
		return {
			rowCount: parseInt(stats.data.row_count || '0')
		};
	} catch (error) {
		console.error('Failed to get chunk collection stats:', error);
		return { rowCount: 0 };
	}
}

// Quality filtering must stay in sync with meta-bridge/pkg/chunking/quality_filter.go.
// Thresholds come from rag.yaml (loaded at runtime via rag-config.ts).
const MIN_CHUNK_TEXT_LENGTH = qualityConfig.minChars;
const MAX_CHUNK_TEXT_LENGTH = 3000;
const MAX_URL_DENSITY = qualityConfig.filters.maxUrlDensity;
const URL_SPECIAL_CASE_MIN_ALNUM = qualityConfig.urlSpecialCase.minAlnumChars;

const DATA_URI_IMAGE_BASE64_PATTERN = /\bdata:image\/[a-z0-9.+-]+;base64,/i;
const BASE64_RUN_PATTERN = /[A-Za-z0-9+/]{500,}={0,2}/;
const URL_PATTERN = /https?:\/\/\S+/gi;
const SENDER_PREFIX_PATTERN = /^\[[^\]]+\]:\s*/gm;

function countUrlChars(text: string): number {
	let total = 0;
	for (const match of text.matchAll(URL_PATTERN)) {
		total += match[0].length;
	}
	return total;
}

function countAlnumChars(text: string): number {
	let count = 0;
	for (const ch of text) {
		const code = ch.codePointAt(0);
		if (code == null) continue;
		if (code >= 48 && code <= 57) {
			count += 1;
			continue;
		}
		if (ch.toLowerCase() !== ch.toUpperCase()) {
			count += 1;
		}
	}
	return count;
}

/**
 * Check if a chunk text is low quality and should be filtered out.
 * Uses thresholds from unified config (rag-config.ts / rag.yaml).
 */
function isLowQualityChunkText(text: string): boolean {
	const trimmed = text.trim();
	if (!trimmed) return true;
	if (trimmed.length > MAX_CHUNK_TEXT_LENGTH) return true;

	// Skip base64 blobs (configurable)
	if (qualityConfig.filters.skipBase64Blobs) {
		if (DATA_URI_IMAGE_BASE64_PATTERN.test(trimmed)) return true;
		if (BASE64_RUN_PATTERN.test(trimmed)) return true;
	}

	const withoutPrefixes = trimmed.replace(SENDER_PREFIX_PATTERN, '');
	const urlChars = countUrlChars(withoutPrefixes);

	// Drop URL-dense chunks (threshold from config)
	if (urlChars > 0 && urlChars / withoutPrefixes.length > MAX_URL_DENSITY) return true;

	// Drop URL-only-ish chunks (little to no text outside URLs)
	if (urlChars > 0) {
		const withoutUrls = withoutPrefixes.replace(URL_PATTERN, '').trim();
		const nonUrlAlnum = countAlnumChars(withoutUrls);
		if (qualityConfig.urlSpecialCase.enabled && nonUrlAlnum < URL_SPECIAL_CASE_MIN_ALNUM) return true;
		// Skip "sent an attachment" messages (configurable)
		if (qualityConfig.filters.skipAttachmentOnly && /sent an attachment/i.test(withoutUrls) && nonUrlAlnum < 100) {
			return true;
		}
	}

	// Drop very long, low-whitespace payloads (e.g., blobs/logs).
	if (trimmed.length > 2000) {
		const whitespaceChars = Array.from(trimmed).filter((c) => c.trim().length === 0).length;
		if (whitespaceChars / trimmed.length < 0.02) return true;
	}

	return false;
}

/**
 * Semantic search using message chunks for better context
 */
export async function chunkSemanticSearch(
	query: string,
	limit = 20
): Promise<ChunkSearchResult[]> {
	try {
		const milvus = await getMilvusClient();
		const embedding = await getEmbedding(query);

		// Fetch more results than needed to account for filtering
		const fetchLimit = limit * SEARCH_FETCH_MULTIPLIER;
		const results = await milvus.search({
			collection_name: CHUNK_COLLECTION,
			data: [embedding],
			anns_field: 'embedding',
			metric_type: SEARCH_METRIC_TYPE,
			params: { ef: Math.max(SEARCH_MIN_EF, fetchLimit) },
			limit: fetchLimit,
			output_fields: [
				'chunk_id',
				'thread_id',
				'thread_name',
				'participant_ids',
				'participant_names',
				'text',
				'message_ids',
				'start_timestamp_ms',
				'end_timestamp_ms',
				'message_count',
				'session_idx',
				'chunk_idx'
			]
		});

		if (!results.results || results.results.length === 0) {
			return [];
		}

		// Filter out short chunks (emojis, single words) and take top N
		return results.results
			.filter((r) => String(r.text ?? '').length >= MIN_CHUNK_TEXT_LENGTH)
			.filter((r) => !isLowQualityChunkText(String(r.text ?? '')))
			.slice(0, limit)
			.map((r) => ({
				chunk_id: r.chunk_id as string,
				thread_id: String(r.thread_id),
				thread_name: String(r.thread_name ?? ''),
				participant_ids: safeJsonParseNumberArray(r.participant_ids),
				participant_names: safeJsonParseStringArray(r.participant_names),
				text: String(r.text ?? ''),
				message_ids: safeJsonParseStringArray(r.message_ids),
				start_timestamp_ms: Number(r.start_timestamp_ms),
				end_timestamp_ms: Number(r.end_timestamp_ms),
				message_count: Number(r.message_count),
				session_idx: Number(r.session_idx),
				chunk_idx: Number(r.chunk_idx),
				score: r.score
			}));
	} catch (error) {
		console.error('Chunk semantic search error:', error);
		throw error;
	}
}

/**
 * Semantic search using chunks, with embeddings for visualization
 */
export async function chunkSemanticSearchWithEmbeddings(
	query: string,
	limit = 50
): Promise<ChunkSearchResultWithEmbedding[]> {
	try {
		const milvus = await getMilvusClient();
		const embedding = await getEmbedding(query);

		// Fetch more results than needed to account for filtering
		const fetchLimit = limit * SEARCH_FETCH_MULTIPLIER;
		const results = await milvus.search({
			collection_name: CHUNK_COLLECTION,
			data: [embedding],
			anns_field: 'embedding',
			metric_type: SEARCH_METRIC_TYPE,
			params: { ef: Math.max(SEARCH_MIN_EF, fetchLimit) },
			limit: fetchLimit,
			output_fields: [
				'chunk_id',
				'thread_id',
				'thread_name',
				'participant_ids',
				'participant_names',
				'text',
				'message_ids',
				'start_timestamp_ms',
				'end_timestamp_ms',
				'message_count',
				'session_idx',
				'chunk_idx',
				'embedding'
			]
		});

		if (!results.results || results.results.length === 0) {
			return [];
		}

		// Filter out short chunks (emojis, single words) and take top N
		return results.results
			.filter((r) => String(r.text ?? '').length >= MIN_CHUNK_TEXT_LENGTH)
			.filter((r) => !isLowQualityChunkText(String(r.text ?? '')))
			.slice(0, limit)
			.map((r) => ({
				chunk_id: r.chunk_id as string,
				thread_id: String(r.thread_id),
				thread_name: String(r.thread_name ?? ''),
				participant_ids: safeJsonParseNumberArray(r.participant_ids),
				participant_names: safeJsonParseStringArray(r.participant_names),
				text: String(r.text ?? ''),
				message_ids: safeJsonParseStringArray(r.message_ids),
				start_timestamp_ms: Number(r.start_timestamp_ms),
				end_timestamp_ms: Number(r.end_timestamp_ms),
				message_count: Number(r.message_count),
				session_idx: Number(r.session_idx),
				chunk_idx: Number(r.chunk_idx),
				score: r.score,
				embedding: r.embedding as number[]
			}));
	} catch (error) {
		console.error('Chunk semantic search with embeddings error:', error);
		throw error;
	}
}

/**
 * Check if the chunks collection exists and is populated
 */
export async function hasChunkCollection(): Promise<boolean> {
	const now = Date.now();
	// Cache to avoid hitting Milvus stats on every semantic search request.
	const CACHE_TTL_MS = 30_000;
	if (hasChunkCollectionCache && now - hasChunkCollectionCache.checkedAtMs < CACHE_TTL_MS) {
		return hasChunkCollectionCache.value;
	}

	try {
		const milvus = await getMilvusClient();
		const exists = await milvus.hasCollection({ collection_name: CHUNK_COLLECTION });
		if (!exists.value) {
			hasChunkCollectionCache = { value: false, checkedAtMs: now };
			return false;
		}

		const stats = await getChunkCollectionStats();
		const populated = stats.rowCount > 0;
		hasChunkCollectionCache = { value: populated, checkedAtMs: now };
		return populated;
	} catch {
		hasChunkCollectionCache = { value: false, checkedAtMs: now };
		return false;
	}
}

let hasChunkCollectionCache: { value: boolean; checkedAtMs: number } | null = null;
