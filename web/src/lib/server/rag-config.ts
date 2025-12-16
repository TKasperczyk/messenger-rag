/**
 * RAG Configuration for TypeScript/SvelteKit
 *
 * These values should match rag.yaml in the project root.
 * This is a TypeScript mirror to avoid adding yaml parsing dependencies to the web app.
 *
 * IMPORTANT: Keep this in sync with /rag.yaml - if you change values there,
 * update them here too, or consider implementing yaml parsing.
 */

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

/**
 * Default configuration matching rag.yaml
 */
export const ragConfig: RagConfig = {
	milvus: {
		address: 'localhost:19530',
		chunkCollection: 'messenger_message_chunks_v2',
		legacyMessageCollection: 'messenger_messages',
		index: {
			type: 'HNSW',
			metric: 'COSINE',
			m: 16,
			efConstruction: 256
		},
		search: {
			ef: 128,
			fetchMultiplier: 3
		}
	},
	embedding: {
		baseUrl: 'http://127.0.0.1:1235/v1',
		model: 'mmlw-roberta-large',
		dimension: 1024,
		batchSize: 32
	},
	quality: {
		minChars: 250,
		minAlnumChars: 140,
		minUniqueWords: 8,
		urlSpecialCase: {
			enabled: true,
			minAlnumChars: 60
		},
		filters: {
			maxUrlDensity: 0.5,
			skipAttachmentOnly: true,
			skipBase64Blobs: true
		}
	},
	hybrid: {
		enabled: true,
		rrf: {
			k: 60
		},
		weights: {
			vector: 0.5,
			bm25: 0.5
		},
		bm25: {
			table: 'chunks_fts'
		}
	}
};

// Export individual config sections for convenience
export const milvusConfig = ragConfig.milvus;
export const embeddingConfig = ragConfig.embedding;
export const qualityConfig = ragConfig.quality;
export const hybridConfig = ragConfig.hybrid;
