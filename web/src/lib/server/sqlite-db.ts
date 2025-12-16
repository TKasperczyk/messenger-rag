import Database from 'better-sqlite3';
import { DB_PATH } from '$env/static/private';

const dbPath = DB_PATH || '../meta-bridge/messenger.db';

let db: Database.Database | null = null;

export function getDb(): Database.Database {
	if (!db) {
		db = new Database(dbPath, { readonly: true });
		// Best-effort: enabling WAL on a readonly connection can throw.
		// WAL should be configured by the writer side (Go tooling) anyway.
		try {
			db.pragma('journal_mode = WAL');
		} catch {
			// Ignore
		}
	}
	return db;
}

export function getDbPath(): string {
	return dbPath;
}

