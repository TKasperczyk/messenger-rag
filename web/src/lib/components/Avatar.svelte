<script lang="ts">
	type AvatarProps = {
		name: string;
		srcs?: Array<string | null | undefined>;
		size?: number;
		title?: string;
		class?: string;
	};

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

	let { name, srcs = [], size = 40, title, class: className = '' }: AvatarProps = $props();

	let srcIndex = $state(0);
	let candidates = $derived(srcs.filter((s): s is string => typeof s === 'string' && s.length > 0));
	let currentSrc = $derived(candidates[srcIndex]);

	$effect(() => {
		// Reset image fallback chain when the candidate list changes
		candidates;
		srcIndex = 0;
	});

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

	let initials = $derived(getInitials(name));
	let gradient = $derived(gradientFor(name));
	let fontSizePx = $derived(Math.max(12, Math.round(size * 0.42)));

	function handleImgError() {
		srcIndex += 1;
	}
</script>

<div
	class={`relative inline-grid select-none place-items-center overflow-hidden rounded-full bg-[var(--color-bg-card)] ring-1 ring-white/10 shadow-sm shadow-black/20 ${className}`}
	style={`width:${size}px;height:${size}px;`}
	title={title ?? name}
>
	{#if currentSrc}
		<img
			src={currentSrc}
			alt={name}
			class="h-full w-full object-cover"
			loading="lazy"
			decoding="async"
			onerror={handleImgError}
		/>
	{:else}
		<div
			class="grid h-full w-full place-items-center font-semibold tracking-wide text-white/90"
			style={`background:${gradient}; font-size:${fontSizePx}px;`}
			aria-hidden="true"
		>
			{initials}
		</div>
	{/if}
</div>
