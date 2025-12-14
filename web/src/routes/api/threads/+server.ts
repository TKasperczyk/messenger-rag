import { json, error } from '@sveltejs/kit';
import { getThreads } from '$lib/server/db';
import type { RequestHandler } from './$types';

export const GET: RequestHandler = async ({ url }) => {
	const limitParam = parseInt(url.searchParams.get('limit') || '50');
	const offsetParam = parseInt(url.searchParams.get('offset') || '0');
	const search = url.searchParams.get('search') || undefined;

	// Validate and clamp parameters
	if (isNaN(limitParam) || isNaN(offsetParam)) {
		throw error(400, 'Invalid limit or offset parameter');
	}
	const limit = Math.max(1, Math.min(limitParam, 500));
	const offset = Math.max(0, offsetParam);

	const threads = getThreads(limit, offset, search);
	return json(threads);
};
