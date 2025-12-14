import { UMAP } from 'umap-js';

export interface UMAPConfig {
	nNeighbors?: number;
	minDist?: number;
	nComponents?: number;
}

export function reduceToThreeDimensions(
	embeddings: number[][],
	config: UMAPConfig = {}
): number[][] {
	// nNeighbors must be less than the number of data points
	const maxNeighbors = Math.max(2, embeddings.length - 1);
	const nNeighbors = Math.min(config.nNeighbors ?? 15, maxNeighbors);

	const umap = new UMAP({
		nNeighbors,
		minDist: config.minDist ?? 0.1,
		nComponents: config.nComponents ?? 3
	});

	return umap.fit(embeddings);
}

export function normalizeCoordinates(coords: number[][]): number[][] {
	if (coords.length === 0) return [];

	const mins = [Infinity, Infinity, Infinity];
	const maxs = [-Infinity, -Infinity, -Infinity];

	for (const point of coords) {
		for (let i = 0; i < 3; i++) {
			mins[i] = Math.min(mins[i], point[i]);
			maxs[i] = Math.max(maxs[i], point[i]);
		}
	}

	return coords.map((point) =>
		point.map((v, i) => {
			const range = maxs[i] - mins[i];
			if (range === 0) return 0;
			return 2 * ((v - mins[i]) / range) - 1;
		})
	);
}
