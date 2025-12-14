<script lang="ts">
	import ScatterPlot3D from './ScatterPlot3D.svelte';
	import { cubicOut } from 'svelte/easing';
	import { fly } from 'svelte/transition';

	interface VisualizationPoint {
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

	let query = $state('');
	let points = $state<VisualizationPoint[]>([]);
	let isLoading = $state(false);
	let error = $state<string | null>(null);
	let selectedPoint = $state<VisualizationPoint | null>(null);

	const SIMILARITY_GRADIENT =
		'linear-gradient(90deg, hsl(42, 18%, 24%), hsl(42, 55%, 44%), hsl(42, 92%, 62%))';

	async function visualize() {
		if (!query.trim()) return;

		isLoading = true;
		error = null;
		selectedPoint = null;

		try {
			const res = await fetch('/api/visualization', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ query, limit: 50 })
			});

			if (!res.ok) {
				const text = await res.text();
				let message = 'Visualization failed';
				try {
					const err = JSON.parse(text);
					message = err.message || message;
				} catch {
					message = text || message;
				}
				throw new Error(message);
			}

			const data = await res.json();
			points = data.points;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Unknown error';
			points = [];
		} finally {
			isLoading = false;
		}
	}

	function handlePointSelect(point: VisualizationPoint | null) {
		selectedPoint = point;
	}

	function formatDate(ms: number): string {
		return new Date(ms).toLocaleString();
	}
</script>

<div class="flex h-full flex-col">
	<!-- Search bar -->
	<div class="flex gap-2 border-b border-[var(--color-border)] bg-[var(--color-bg-darker)] p-4">
		<label for="visualization-query" class="sr-only">Visualization query</label>
		<input
			id="visualization-query"
			type="text"
			placeholder="Enter a query to visualize semantic similarity..."
			bind:value={query}
			onkeydown={(e) => e.key === 'Enter' && visualize()}
			class="flex-1 rounded-lg bg-[var(--color-bg-card)] px-4 py-3 outline-none placeholder:text-[var(--color-text-muted)] focus:ring-2 focus:ring-[var(--color-primary)]"
		/>
		<button
			onclick={visualize}
			disabled={isLoading || !query.trim()}
			class="rounded-lg bg-[var(--color-primary)] px-6 py-3 font-medium transition-colors hover:bg-[var(--color-primary-dark)] disabled:opacity-50"
		>
			{isLoading ? 'Computing...' : 'Visualize'}
		</button>
	</div>

	<!-- Main content -->
	<div class="relative flex flex-1 overflow-hidden">
		<!-- 3D View -->
		<div class="min-w-0 flex-1">
			{#if error}
				<div class="flex h-full items-center justify-center text-red-400">
					<div class="text-center">
						<svg class="mx-auto mb-4 h-12 w-12" fill="none" stroke="currentColor" viewBox="0 0 24 24">
							<path
								stroke-linecap="round"
								stroke-linejoin="round"
								stroke-width="2"
								d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z"
							/>
						</svg>
						<p>{error}</p>
					</div>
				</div>
			{:else if isLoading}
				<div class="flex h-full items-center justify-center">
					<div class="text-center">
						<div
							class="mx-auto mb-4 h-12 w-12 animate-spin rounded-full border-4 border-[var(--color-primary)] border-t-transparent"
						></div>
						<p class="text-[var(--color-text-muted)]">Computing 3D projection...</p>
						<p class="mt-1 text-sm text-[var(--color-text-muted)]">This may take a few seconds</p>
					</div>
				</div>
			{:else if points.length > 0}
				<ScatterPlot3D {points} onPointSelect={handlePointSelect} />
				{:else}
					<div class="flex h-full items-center justify-center text-[var(--color-text-muted)]">
						<div class="text-center">
							<svg
								class="mx-auto mb-4 h-16 w-16 opacity-60"
								viewBox="0 0 24 24"
								fill="none"
								stroke="currentColor"
								aria-hidden="true"
							>
								<circle cx="12" cy="12" r="10" stroke-width="1.5" class="opacity-50" />

								<!-- Slow orbit for subtle motion -->
								<g class="origin-center motion-reduce:animate-none animate-[spin_12s_linear_infinite]">
									<circle cx="12" cy="4" r="1.4" fill="currentColor" class="motion-reduce:animate-none animate-pulse [animation-delay:0ms]" />
									<circle cx="19.5" cy="12" r="1.4" fill="currentColor" class="motion-reduce:animate-none animate-pulse [animation-delay:250ms]" />
									<circle cx="12" cy="20" r="1.4" fill="currentColor" class="motion-reduce:animate-none animate-pulse [animation-delay:500ms]" />
									<circle cx="4.5" cy="12" r="1.4" fill="currentColor" class="motion-reduce:animate-none animate-pulse [animation-delay:750ms]" />
								</g>

								<!-- Center point -->
								<circle cx="12" cy="12" r="1.8" fill="currentColor" class="motion-reduce:animate-none animate-pulse [animation-delay:150ms]" />
							</svg>
							<p class="text-lg">3D Semantic Visualization</p>
							<p class="mt-2 text-sm">Enter a query to see messages in 3D space</p>
							<p class="text-sm">Similar messages appear closer together</p>
						</div>
					</div>
			{/if}
		</div>

		<!-- Selected message details panel -->
		{#if selectedPoint}
			<aside
				transition:fly={{ x: 340, duration: 260, opacity: 0, easing: cubicOut }}
				class="absolute inset-y-0 right-0 z-[3000] w-80 overflow-y-auto border-l border-[var(--color-border)] bg-[var(--color-bg-darker)] p-4 shadow-xl shadow-black/30"
			>
				<div class="mb-4 flex items-center justify-between">
					<h3 class="font-semibold">Message Details</h3>
					<button
						onclick={() => (selectedPoint = null)}
						class="rounded p-1 hover:bg-[var(--color-bg-card)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)]"
						aria-label="Close message details"
						title="Close"
					>
						<svg class="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
							<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
						</svg>
					</button>
				</div>

				<div class="space-y-4">
					<div>
						<div class="mb-1 text-xs uppercase text-[var(--color-text-muted)]">Sender</div>
						<div class="font-medium">{selectedPoint.sender_name}</div>
					</div>

					<div>
						<div class="mb-1 text-xs uppercase text-[var(--color-text-muted)]">Conversation</div>
						<div class="text-sm">{selectedPoint.thread_name}</div>
					</div>

					<div>
						<div class="mb-1 text-xs uppercase text-[var(--color-text-muted)]">Message</div>
						<div class="whitespace-pre-wrap rounded bg-[var(--color-bg-card)] p-3 text-sm">
							{selectedPoint.text || '[No text content]'}
						</div>
					</div>

					<div>
						<div class="mb-1 text-xs uppercase text-[var(--color-text-muted)]">Date</div>
						<div class="text-sm">{formatDate(selectedPoint.timestamp_ms)}</div>
					</div>

					<div>
						<div class="mb-1 text-xs uppercase text-[var(--color-text-muted)]">Similarity Score</div>
						<div class="flex items-center gap-2">
							<div class="h-2 flex-1 overflow-hidden rounded-full bg-[var(--color-bg-card)]">
								<div
									class="h-full rounded-full"
									style={`width: ${selectedPoint.score * 100}%; background: ${SIMILARITY_GRADIENT};`}
								></div>
							</div>
							<span class="text-sm font-medium">{(selectedPoint.score * 100).toFixed(1)}%</span>
						</div>
					</div>

					<div class="pt-2 text-xs text-[var(--color-text-muted)]">
						<div class="mb-1">3D Coordinates:</div>
						<div class="font-mono">
							x: {selectedPoint.x.toFixed(3)}, y: {selectedPoint.y.toFixed(3)}, z: {selectedPoint.z.toFixed(3)}
						</div>
					</div>
				</div>
			</aside>
		{/if}
	</div>

	<!-- Legend -->
	{#if points.length > 0}
		<div class="flex items-center justify-between border-t border-[var(--color-border)] bg-[var(--color-bg-darker)] px-4 py-2 text-sm">
			<div class="flex items-center gap-4">
				<span class="text-[var(--color-text-muted)]">{points.length} messages</span>
				<div class="flex items-center gap-2">
					<span class="text-[var(--color-text-muted)]">Similarity:</span>
					<div class="h-3 w-24 overflow-hidden rounded" style={`background: ${SIMILARITY_GRADIENT};`}></div>
					<span class="text-xs text-[var(--color-text-muted)]">Low → High</span>
				</div>
			</div>
			<div class="text-[var(--color-text-muted)]">
				Drag to rotate • Scroll to zoom • Click point for details
			</div>
		</div>
	{/if}
</div>
