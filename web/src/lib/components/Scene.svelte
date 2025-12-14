<script lang="ts">
	import { T } from '@threlte/core';
	import { Billboard, OrbitControls, Grid, HTML, interactivity } from '@threlte/extras';

	interface Point {
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

	let {
		points,
		onPointSelect
	}: {
		points: Point[];
		onPointSelect?: (point: Point | null) => void;
	} = $props();

	let hoveredIndex = $state<number | null>(null);
	let selectedIndex = $state<number | null>(null);
	let hoverTimeout: ReturnType<typeof setTimeout> | null = null;
	// eslint-disable-next-line @typescript-eslint/no-explicit-any
	let controls: any = $state(undefined);

	function setHovered(idx: number) {
		if (hoverTimeout) {
			clearTimeout(hoverTimeout);
			hoverTimeout = null;
		}
		hoveredIndex = idx;
	}

	function clearHovered(idx: number) {
		// Only clear if we're still hovering the same point
		hoverTimeout = setTimeout(() => {
			if (hoveredIndex === idx) {
				hoveredIndex = null;
			}
		}, 50);
	}

	// Scale factor for the visualization
	const scale = 5;

	// Calculate min/max scores for normalization
	let minScore = $derived(Math.min(...points.map((p) => p.score)));
	let maxScore = $derived(Math.max(...points.map((p) => p.score)));
	let scoreRange = $derived(maxScore - minScore || 1);

	const AVATAR_GRADIENTS = [
		{ from: 'rgba(56, 189, 248, 0.35)', to: 'rgba(59, 130, 246, 0.35)' }, // sky → blue
		{ from: 'rgba(59, 130, 246, 0.35)', to: 'rgba(99, 102, 241, 0.35)' }, // blue → indigo
		{ from: 'rgba(99, 102, 241, 0.35)', to: 'rgba(168, 85, 247, 0.35)' }, // indigo → violet
		{ from: 'rgba(168, 85, 247, 0.33)', to: 'rgba(236, 72, 153, 0.30)' }, // violet → pink
		{ from: 'rgba(236, 72, 153, 0.30)', to: 'rgba(244, 63, 94, 0.28)' }, // pink → rose
		{ from: 'rgba(244, 63, 94, 0.28)', to: 'rgba(249, 115, 22, 0.28)' }, // rose → orange
		{ from: 'rgba(249, 115, 22, 0.28)', to: 'rgba(245, 158, 11, 0.28)' }, // orange → amber
		{ from: 'rgba(16, 185, 129, 0.30)', to: 'rgba(20, 184, 166, 0.30)' }, // emerald → teal
		{ from: 'rgba(20, 184, 166, 0.30)', to: 'rgba(34, 211, 238, 0.28)' }, // teal → cyan
		{ from: 'rgba(148, 163, 184, 0.28)', to: 'rgba(100, 116, 139, 0.28)' } // slate
	] as const;

	function fnv1a(input: string): number {
		let hash = 0x811c9dc5;
		for (let i = 0; i < input.length; i++) {
			hash ^= input.charCodeAt(i);
			hash = Math.imul(hash, 0x01000193);
		}
		return hash >>> 0;
	}

	function getInitials(displayName: string): string {
		const parts = displayName
			.trim()
			.split(/\s+/)
			.filter(Boolean);

		if (parts.length === 0) return '?';
		if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
		return (parts[0][0] + parts[1][0]).toUpperCase();
	}

	function gradientFor(displayName: string): string {
		const idx = fnv1a(displayName.toLowerCase()) % AVATAR_GRADIENTS.length;
		const g = AVATAR_GRADIENTS[idx];
		return `linear-gradient(135deg, ${g.from}, ${g.to})`;
	}

	function clamp01(value: number): number {
		return Math.max(0, Math.min(1, value));
	}

	function lerp(a: number, b: number, t: number): number {
		return a + (b - a) * t;
	}

	// Premium, muted single-hue theme (dim → vibrant)
	const SIMILARITY_HUE = 42;
	function similarityT(score: number): number {
		return clamp01((score - minScore) / scoreRange);
	}
	function similarityColor(score: number, alpha: number): string {
		const t = clamp01((score - minScore) / scoreRange);
		const sat = lerp(22, 92, t);
		const light = lerp(28, 62, t);
		return `hsla(${SIMILARITY_HUE}, ${sat}%, ${light}%, ${alpha})`;
	}

	function truncateText(text: string, maxLength: number = 50): string {
		if (!text) return '[No text]';
		return text.length > maxLength ? text.slice(0, maxLength) + '...' : text;
	}

	// Enable interactivity plugin - must be called inside Canvas context
	interactivity();

	function handleBackgroundClick() {
		if (selectedIndex !== null) {
			selectedIndex = null;
			onPointSelect?.(null);
			controls?.target.set(0, 0, 0);
		}
	}
</script>

<T.PerspectiveCamera makeDefault position={[12, 12, 12]} fov={50}>
	<OrbitControls bind:ref={controls} enableDamping dampingFactor={0.1} />
</T.PerspectiveCamera>

<T.AmbientLight intensity={0.6} />
<T.DirectionalLight position={[10, 10, 5]} intensity={0.8} />

<!-- Invisible background plane to catch clicks on empty space -->
<T.Mesh rotation.x={-Math.PI / 2} position.y={-0.01} onclick={handleBackgroundClick}>
	<T.PlaneGeometry args={[200, 200]} />
	<T.MeshBasicMaterial transparent opacity={0} />
</T.Mesh>

<!-- Grid for reference -->
<Grid
	cellColor="#3a3a5c"
	sectionColor="#25253d"
	fadeDistance={50}
	cellSize={1}
	sectionSize={5}
/>

<!-- Render each point as an avatar-style billboard -->
{#each points as point, idx (point.message_id)}
	{@const isSelected = selectedIndex === idx}
	{@const isHovered = hoveredIndex === idx}
	{@const similarity = similarityT(point.score)}
	{@const initials = getInitials(point.sender_name)}
	{@const gradient = gradientFor(point.sender_name)}
	{@const ringAlpha = isSelected ? 0.98 : isHovered ? 0.9 : 0.82}
	{@const glowAlpha = isSelected ? 0.78 : isHovered ? 0.6 : 0.42}
	{@const ringColor = similarityColor(point.score, ringAlpha)}
	{@const glowColor = similarityColor(point.score, glowAlpha)}
	{@const outerGlowColor = similarityColor(point.score, glowAlpha * 0.45)}
	{@const ringThicknessPx = lerp(5, 6, similarity) + (isSelected ? 1.25 : isHovered ? 0.75 : 0)}
	{@const ringSpreadPx = ringThicknessPx + 1}
	{@const glowBlurPx = lerp(24, 34, similarity) + (isSelected ? 10 : isHovered ? 6 : 0)}
	{@const glowBlurOuterPx = glowBlurPx + 24}
	{@const avatarScaleClass = isSelected ? 'scale-110' : isHovered ? 'scale-105' : 'scale-100'}
	{@const shouldPulse = !isSelected && !isHovered && similarity > 0.92}
	<T.Group position={[point.x * scale, point.y * scale, point.z * scale]}>
		<Billboard>
			<!-- Invisible circle for hit testing + billboarded interaction -->
			<T.Mesh
				position.z={0.02}
				onpointerover={() => setHovered(idx)}
				onpointerout={() => clearHovered(idx)}
				onclick={(e: { stopPropagation: () => void }) => {
					e.stopPropagation();
					if (selectedIndex === idx) {
						// Unselect: reset to origin
						selectedIndex = null;
						onPointSelect?.(null);
						controls?.target.set(0, 0, 0);
					} else {
						// Select: center on this point
						selectedIndex = idx;
						onPointSelect?.(point);
						controls?.target.set(point.x * scale, point.y * scale, point.z * scale);
					}
				}}
			>
				<!-- ~36px avatar (plus ring/glow), so keep hit radius generously sized -->
				<T.CircleGeometry args={[isSelected ? 0.72 : isHovered ? 0.7 : 0.68, 48]} />
				<T.MeshBasicMaterial transparent opacity={0} depthWrite={false} />
			</T.Mesh>

			<!-- Avatar-style marker -->
			<HTML center pointerEvents="none" class="pointer-events-none" zIndexRange={[1000, 0]}>
				<div
					class={`viz-avatar pointer-events-none relative grid select-none place-items-center overflow-hidden rounded-full ${avatarScaleClass} transition-transform duration-150 ease-out ${shouldPulse ? 'viz-avatar--pulse' : ''}`}
					style={`width:36px;height:36px;background-color: var(--color-bg-card); background-image: ${gradient}; --viz-ring-color: ${ringColor}; --viz-glow-color: ${glowColor}; --viz-glow-outer-color: ${outerGlowColor}; --viz-ring-spread: ${ringSpreadPx}px; --viz-glow-blur: ${glowBlurPx}px; --viz-glow-outer-blur: ${glowBlurOuterPx}px;`}
					aria-hidden="true"
				>
					<span class="pointer-events-none font-semibold tracking-wide text-white/90" style="font-size: 15px;">
						{initials}
					</span>
				</div>
			</HTML>

			<!-- Hover/selected label -->
			{#if isHovered || isSelected}
				<HTML
					position={[0, 0.7, 0]}
					center
					pointerEvents="none"
					class="pointer-events-none"
					zIndexRange={[1000, 0]}
				>
					<div
						class="pointer-events-none whitespace-nowrap rounded-lg px-3 py-2 text-sm shadow-lg"
						style="background: rgba(20, 20, 32, 0.95); border: 1px solid rgba(120, 120, 180, 0.35);"
					>
						<div class="font-semibold text-white">{point.sender_name}</div>
						<div class="max-w-xs text-gray-300">{truncateText(point.text)}</div>
						<div class="mt-1 text-xs text-gray-400">
							{point.thread_name} • {(point.score * 100).toFixed(1)}%
						</div>
					</div>
				</HTML>
			{/if}
		</Billboard>
	</T.Group>
{/each}

<!-- Axis lines -->
<T.Line>
	<T.BufferGeometry>
		<T.BufferAttribute
			attach="attributes.position"
			args={[new Float32Array([-6, 0, 0, 6, 0, 0]), 3]}
		/>
	</T.BufferGeometry>
	<T.LineBasicMaterial color="#ff4444" opacity={0.5} transparent />
</T.Line>
<T.Line>
	<T.BufferGeometry>
		<T.BufferAttribute
			attach="attributes.position"
			args={[new Float32Array([0, -6, 0, 0, 6, 0]), 3]}
		/>
	</T.BufferGeometry>
	<T.LineBasicMaterial color="#44ff44" opacity={0.5} transparent />
</T.Line>

<style>
	.viz-avatar {
		box-shadow:
			0 0 0 1px rgba(255, 255, 255, 0.14),
			0 0 0 var(--viz-ring-spread) var(--viz-ring-color),
			0 0 var(--viz-glow-blur) var(--viz-glow-color),
			0 0 var(--viz-glow-outer-blur) var(--viz-glow-outer-color),
			0 10px 22px rgba(0, 0, 0, 0.35);
	}

	.viz-avatar--pulse {
		animation: vizSimilarityPulse 1.65s ease-in-out infinite;
	}

	@media (prefers-reduced-motion: reduce) {
		.viz-avatar--pulse {
			animation: none;
		}
	}

	@keyframes vizSimilarityPulse {
		0%,
		100% {
			box-shadow:
				0 0 0 1px rgba(255, 255, 255, 0.14),
				0 0 0 var(--viz-ring-spread) var(--viz-ring-color),
				0 0 var(--viz-glow-blur) var(--viz-glow-color),
				0 0 var(--viz-glow-outer-blur) var(--viz-glow-outer-color),
				0 10px 22px rgba(0, 0, 0, 0.35);
		}

		50% {
			box-shadow:
				0 0 0 1px rgba(255, 255, 255, 0.14),
				0 0 0 calc(var(--viz-ring-spread) + 1px) var(--viz-ring-color),
				0 0 calc(var(--viz-glow-blur) + 8px) var(--viz-glow-color),
				0 0 calc(var(--viz-glow-outer-blur) + 14px) var(--viz-glow-outer-color),
				0 10px 22px rgba(0, 0, 0, 0.35);
		}
	}
</style>
<T.Line>
	<T.BufferGeometry>
		<T.BufferAttribute
			attach="attributes.position"
			args={[new Float32Array([0, 0, -6, 0, 0, 6]), 3]}
		/>
	</T.BufferGeometry>
	<T.LineBasicMaterial color="#4444ff" opacity={0.5} transparent />
</T.Line>
