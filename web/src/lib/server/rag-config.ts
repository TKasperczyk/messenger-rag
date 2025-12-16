/**
 * RAG Configuration for TypeScript/SvelteKit
 *
 * `rag.yaml` is the single source of truth. The web server loads it at runtime
 * (server-side) to avoid duplicating config defaults in TypeScript.
 */

import fs from 'node:fs';
import path from 'node:path';
import YAML from 'yaml';

export interface RagConfig {
	milvus: {
		address: string;
		chunkCollection: string;
		legacyMessageCollection: string;
		index: {
			type: string;
			metric: string;
			m: number;
			efConstruction: number;
		};
		search: {
			ef: number;
			fetchMultiplier: number;
		};
	};
	embedding: {
		baseUrl: string;
		model: string;
		dimension: number;
		batchSize: number;
	};
	quality: {
		minChars: number;
		minAlnumChars: number;
		minUniqueWords: number;
		urlSpecialCase: {
			enabled: boolean;
			minAlnumChars: number;
		};
		filters: {
			maxUrlDensity: number;
			skipAttachmentOnly: boolean;
			skipBase64Blobs: boolean;
		};
	};
	hybrid: {
		enabled: boolean;
		rrf: {
			k: number;
		};
		weights: {
			vector: number;
			bm25: number;
		};
		bm25: {
			table: string;
		};
	};
}

let cachedConfig: RagConfig | null = null;

function findRagYamlPath(startDir: string): string {
	let current = startDir;
	for (;;) {
		const candidate = path.join(current, 'rag.yaml');
		if (fs.existsSync(candidate)) return candidate;
		const parent = path.dirname(current);
		if (parent === current) break;
		current = parent;
	}
	throw new Error(`rag.yaml not found starting at: ${startDir}`);
}

function asObject(value: unknown, name: string): Record<string, unknown> {
	if (value == null || typeof value !== 'object' || Array.isArray(value)) {
		throw new Error(`Invalid rag.yaml: expected ${name} to be an object`);
	}
	return value as Record<string, unknown>;
}

function asString(value: unknown, name: string): string {
	if (typeof value !== 'string' || value.trim() === '') {
		throw new Error(`Invalid rag.yaml: expected ${name} to be a non-empty string`);
	}
	return value;
}

function asNumber(value: unknown, name: string): number {
	if (typeof value !== 'number' || !Number.isFinite(value)) {
		throw new Error(`Invalid rag.yaml: expected ${name} to be a finite number`);
	}
	return value;
}

function asBoolean(value: unknown, name: string): boolean {
	if (typeof value !== 'boolean') {
		throw new Error(`Invalid rag.yaml: expected ${name} to be a boolean`);
	}
	return value;
}

function parseRagYaml(contents: string): RagConfig {
	const root = asObject(YAML.parse(contents), 'root');

	const milvus = asObject(root.milvus, 'milvus');
	const milvusIndex = asObject(milvus.index, 'milvus.index');
	const milvusSearch = asObject(milvus.search, 'milvus.search');

	const embedding = asObject(root.embedding, 'embedding');

	const quality = asObject(root.quality, 'quality');
	const urlSpecialCase = asObject(quality.url_special_case, 'quality.url_special_case');
	const qualityFilters = asObject(quality.filters, 'quality.filters');

	const hybrid = asObject(root.hybrid, 'hybrid');
	const hybridRrf = asObject(hybrid.rrf, 'hybrid.rrf');
	const hybridWeights = asObject(hybrid.weights, 'hybrid.weights');
	const hybridBm25 = asObject(hybrid.bm25, 'hybrid.bm25');

	const bm25Table = asString(hybridBm25.table, 'hybrid.bm25.table');
	if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(bm25Table)) {
		throw new Error(
			`Invalid rag.yaml: expected hybrid.bm25.table to be a valid SQL identifier, got: ${JSON.stringify(bm25Table)}`
		);
	}

	const rrfK = asNumber(hybridRrf.k, 'hybrid.rrf.k');
	if (rrfK <= 0) {
		throw new Error(`Invalid rag.yaml: expected hybrid.rrf.k to be > 0`);
	}

	const weightVector = asNumber(hybridWeights.vector, 'hybrid.weights.vector');
	const weightBm25 = asNumber(hybridWeights.bm25, 'hybrid.weights.bm25');
	if (weightVector < 0 || weightBm25 < 0 || weightVector+weightBm25 <= 0) {
		throw new Error(`Invalid rag.yaml: expected hybrid.weights to have a positive sum`);
	}

	return {
		milvus: {
			address: asString(milvus.address, 'milvus.address'),
			chunkCollection: asString(milvus.chunk_collection, 'milvus.chunk_collection'),
			legacyMessageCollection: asString(
				milvus.legacy_message_collection,
				'milvus.legacy_message_collection'
			),
			index: {
				type: asString(milvusIndex.type, 'milvus.index.type'),
				metric: asString(milvusIndex.metric, 'milvus.index.metric'),
				m: asNumber(milvusIndex.m, 'milvus.index.m'),
				efConstruction: asNumber(milvusIndex.ef_construction, 'milvus.index.ef_construction')
			},
			search: {
				ef: asNumber(milvusSearch.ef, 'milvus.search.ef'),
				fetchMultiplier: asNumber(milvusSearch.fetch_multiplier, 'milvus.search.fetch_multiplier')
			}
		},
		embedding: {
			baseUrl: asString(embedding.base_url, 'embedding.base_url'),
			model: asString(embedding.model, 'embedding.model'),
			dimension: asNumber(embedding.dimension, 'embedding.dimension'),
			batchSize: asNumber(embedding.batch_size, 'embedding.batch_size')
		},
		quality: {
			minChars: asNumber(quality.min_chars, 'quality.min_chars'),
			minAlnumChars: asNumber(quality.min_alnum_chars, 'quality.min_alnum_chars'),
			minUniqueWords: asNumber(quality.min_unique_words, 'quality.min_unique_words'),
			urlSpecialCase: {
				enabled: asBoolean(urlSpecialCase.enabled, 'quality.url_special_case.enabled'),
				minAlnumChars: asNumber(
					urlSpecialCase.min_alnum_chars,
					'quality.url_special_case.min_alnum_chars'
				)
			},
			filters: {
				maxUrlDensity: asNumber(qualityFilters.max_url_density, 'quality.filters.max_url_density'),
				skipAttachmentOnly: asBoolean(
					qualityFilters.skip_attachment_only,
					'quality.filters.skip_attachment_only'
				),
				skipBase64Blobs: asBoolean(
					qualityFilters.skip_base64_blobs,
					'quality.filters.skip_base64_blobs'
				)
			}
		},
		hybrid: {
			enabled: asBoolean(hybrid.enabled, 'hybrid.enabled'),
			rrf: {
				k: rrfK
			},
			weights: {
				vector: weightVector,
				bm25: weightBm25
			},
			bm25: {
				table: bm25Table
			}
		}
	};
}

export function getRagConfig(): RagConfig {
	if (cachedConfig) return cachedConfig;
	const configPath = findRagYamlPath(process.cwd());
	const contents = fs.readFileSync(configPath, 'utf8');
	cachedConfig = parseRagYaml(contents);
	return cachedConfig;
}

export const ragConfig: RagConfig = getRagConfig();

// Export individual config sections for convenience
export const milvusConfig = ragConfig.milvus;
export const embeddingConfig = ragConfig.embedding;
export const qualityConfig = ragConfig.quality;
export const hybridConfig = ragConfig.hybrid;
