import { json, error } from '@sveltejs/kit';
import { searchMessages } from '$lib/server/db';
import type { RequestHandler } from './$types';

export const GET: RequestHandler = async ({ url }) => {
	const query = url.searchParams.get('q');
	const limitParam = parseInt(url.searchParams.get('limit') || '50');

	if (!query) {
		throw error(400, 'Query parameter "q" is required');
	}

	// Validate and clamp limit
	if (isNaN(limitParam)) {
		throw error(400, 'Invalid limit parameter');
	}
	const limit = Math.max(1, Math.min(limitParam, 200));

	try {
		const results = searchMessages(query, limit);
		return json(results);
	} catch (e) {
		// FTS queries can fail on malformed input
		if (e instanceof Error && e.message.includes('fts')) {
			throw error(400, 'Invalid search query. Try simpler terms.');
		}
		throw e;
	}
};
