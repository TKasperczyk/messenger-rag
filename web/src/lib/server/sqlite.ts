import Database from 'better-sqlite3';
import { DB_PATH } from '$env/static/private';
import { hybridConfig } from './rag-config';

const dbPath = DB_PATH || '../meta-bridge/messenger.db';

let db: Database.Database | null = null;

const DEFAULT_BM25_TABLE = 'chunks_fts';
const bm25Table =
	/^[A-Za-z_][A-Za-z0-9_]*$/.test(hybridConfig.bm25.table) ? hybridConfig.bm25.table : DEFAULT_BM25_TABLE;

export function getDb(): Database.Database {
	if (!db) {
		db = new Database(dbPath, { readonly: true });
	}
	return db;
}

export interface FtsSearchResult {
	chunk_id: string;
	thread_id: number;
	thread_name: string | null;
	session_idx: number;
	chunk_idx: number;
	participant_ids: string;
	participant_names: string;
	text: string;
	message_ids: string;
	start_timestamp_ms: number;
	end_timestamp_ms: number;
	message_count: number;
	is_indexable: number;
	bm25_score: number;
}

/**
 * BM25 full-text search using SQLite FTS5
 */
export function ftsSearch(query: string, limit = 50): FtsSearchResult[] {
	const db = getDb();

	// Escape special FTS5 characters and create search query
	const escapedQuery = query
		.replace(/['"]/g, '')
		.split(/\s+/)
		.filter((w) => w.length > 1)
		.map((w) => `"${w}"`)
		.join(' OR ');

	if (!escapedQuery) {
		return [];
	}

	const stmt = db.prepare(`
		SELECT
			c.chunk_id,
			c.thread_id,
			c.thread_name,
			c.session_idx,
			c.chunk_idx,
			c.participant_ids,
			c.participant_names,
			c.text,
			c.message_ids,
			c.start_timestamp_ms,
			c.end_timestamp_ms,
			c.message_count,
			c.is_indexable,
			bm25(${bm25Table}) as bm25_score
		FROM ${bm25Table} fts
		JOIN chunks c ON c.chunk_id = fts.chunk_id
		WHERE ${bm25Table} MATCH ?
		AND c.is_indexable = 1
		ORDER BY bm25(${bm25Table})
		LIMIT ?
	`);

	return stmt.all(escapedQuery, limit) as FtsSearchResult[];
}

export interface ChunkContext {
	chunk_id: string;
	thread_id: number;
	thread_name: string | null;
	session_idx: number;
	chunk_idx: number;
	participant_ids: string;
	participant_names: string;
	text: string;
	message_ids: string;
	start_timestamp_ms: number;
	end_timestamp_ms: number;
	message_count: number;
	is_indexable: number;
}

/**
 * Get neighboring chunks for context expansion
 */
export function getChunkContext(
	threadId: number,
	sessionIdx: number,
	chunkIdx: number,
	radius = 1
): ChunkContext[] {
	const db = getDb();

	const stmt = db.prepare(`
		SELECT
			chunk_id,
			thread_id,
			thread_name,
			session_idx,
			chunk_idx,
			participant_ids,
			participant_names,
			text,
			message_ids,
			start_timestamp_ms,
			end_timestamp_ms,
			message_count,
			is_indexable
		FROM chunks
		WHERE thread_id = ?
		AND session_idx = ?
		AND chunk_idx BETWEEN ? AND ?
		ORDER BY chunk_idx
	`);

	return stmt.all(threadId, sessionIdx, chunkIdx - radius, chunkIdx + radius) as ChunkContext[];
}

/**
 * Get chunk by ID from SQLite
 */
export function getChunkById(chunkId: string): ChunkContext | null {
	const db = getDb();

	const stmt = db.prepare(`
		SELECT
			chunk_id,
			thread_id,
			thread_name,
			session_idx,
			chunk_idx,
			participant_ids,
			participant_names,
			text,
			message_ids,
			start_timestamp_ms,
			end_timestamp_ms,
			message_count,
			is_indexable
		FROM chunks
		WHERE chunk_id = ?
	`);

	return (stmt.get(chunkId) as ChunkContext) || null;
}

/**
 * Get stats about the chunks table
 */
export function getChunksStats(): { total: number; indexable: number } {
	const db = getDb();

	const totalStmt = db.prepare('SELECT COUNT(*) as count FROM chunks');
	const indexableStmt = db.prepare('SELECT COUNT(*) as count FROM chunks WHERE is_indexable = 1');

	const total = (totalStmt.get() as { count: number }).count;
	const indexable = (indexableStmt.get() as { count: number }).count;

	return { total, indexable };
}
