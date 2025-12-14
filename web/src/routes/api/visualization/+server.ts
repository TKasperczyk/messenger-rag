import { json, error } from '@sveltejs/kit';
import { semanticSearchWithEmbeddings } from '$lib/server/milvus';
import { reduceToThreeDimensions, normalizeCoordinates } from '$lib/server/umap';
import type { RequestHandler } from './$types';

export interface VisualizationPoint {
	message_id: string;
	thread_id: string;
	sender_name: string;
	thread_name: string;
	text: string;
	timestamp_ms: number;
	score: number;
	x: number;
	y: number;
	z: number;
}

export const POST: RequestHandler = async ({ request }) => {
	let body: unknown;
	try {
		body = await request.json();
	} catch {
		throw error(400, 'Invalid JSON body');
	}

	// Validate request body
	if (typeof body !== 'object' || body === null) {
		throw error(400, 'Request body must be an object');
	}

	const { query, limit = 50 } = body as { query?: unknown; limit?: unknown };

	if (!query || typeof query !== 'string') {
		throw error(400, 'Query is required and must be a string');
	}

	if (query.length > 1000) {
		throw error(400, 'Query too long (max 1000 characters)');
	}

	// Validate and cap limit
	const parsedLimit = typeof limit === 'number' ? limit : parseInt(String(limit));
	if (isNaN(parsedLimit)) {
		throw error(400, 'Invalid limit parameter');
	}
	const cappedLimit = Math.max(3, Math.min(parsedLimit, 100));

	const results = await semanticSearchWithEmbeddings(query, cappedLimit);

	if (results.length < 3) {
		throw error(400, 'Need at least 3 results for visualization');
	}

	// Extract embeddings for UMAP
	const embeddings = results.map((r) => r.embedding);

	// Reduce to 3D
	const coords3d = reduceToThreeDimensions(embeddings);
	const normalizedCoords = normalizeCoordinates(coords3d);

	// Combine with message data
	const points: VisualizationPoint[] = results.map((result, i) => ({
		message_id: result.message_id,
		thread_id: result.thread_id,
		sender_name: result.sender_name,
		thread_name: result.thread_name,
		text: result.text,
		timestamp_ms: result.timestamp_ms,
		score: result.score,
		x: normalizedCoords[i][0],
		y: normalizedCoords[i][1],
		z: normalizedCoords[i][2]
	}));

	return json({ points });
};
