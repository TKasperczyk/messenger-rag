import { json, error } from '@sveltejs/kit';
import { getMessages } from '$lib/server/db';
import type { RequestHandler } from './$types';

export const GET: RequestHandler = async ({ params, url }) => {
	// Use 'ids' query param if provided (for merged threads), otherwise use URL param
	const threadIds = url.searchParams.get('ids') || params.id;
	const limitParam = parseInt(url.searchParams.get('limit') || '100');
	const offsetParam = parseInt(url.searchParams.get('offset') || '0');

	// Validate and clamp parameters
	if (isNaN(limitParam) || isNaN(offsetParam)) {
		throw error(400, 'Invalid limit or offset parameter');
	}
	const limit = Math.max(1, Math.min(limitParam, 500));
	const offset = Math.max(0, offsetParam);

	const messages = getMessages(threadIds, limit, offset);
	return json(messages);
};
