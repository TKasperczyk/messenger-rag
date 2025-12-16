import Database from 'better-sqlite3';
import path from 'node:path';
import { getRagConfig, getRagConfigPath } from './rag-config';

function resolveDbPath(): string {
	const config = getRagConfig();
	const configPath = getRagConfigPath();
	const configDir = path.dirname(configPath);

	// database.sqlite in rag.yaml is relative to the config file location
	return path.resolve(configDir, config.database?.sqlite || 'messenger.db');
}

let db: Database.Database | null = null;
let resolvedDbPath: string | null = null;

export function getDb(): Database.Database {
	if (!db) {
		resolvedDbPath = resolveDbPath();
		db = new Database(resolvedDbPath, { readonly: true });
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
	if (!resolvedDbPath) {
		resolvedDbPath = resolveDbPath();
	}
	return resolvedDbPath;
}
