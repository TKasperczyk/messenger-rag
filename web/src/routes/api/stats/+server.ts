import { json } from '@sveltejs/kit';
import { getStats } from '$lib/server/db';
import { getCollectionStats } from '$lib/server/milvus';
import type { RequestHandler } from './$types';

export const GET: RequestHandler = async () => {
	const dbStats = getStats();
	const vectorStats = await getCollectionStats();

	return json({
		...dbStats,
		vectorCount: vectorStats.rowCount
	});
};
