<script lang="ts">
	import Avatar from '$lib/components/Avatar.svelte';

	interface Thread {
		thread_id: string;
		thread_ids: string;
		thread_name: string | null;
		thread_type: number;
		last_activity_timestamp_ms: number | null;
		message_count: number;
		participant_names: string | null;
		picture_url: string | null;
		contact_id: string | null;
	}

	type SidebarProps = {
		threads?: Thread[];
		selectedThread?: Thread | null;
		threadSearch?: string;
		loadingThreads?: boolean;
		threadError?: string | null;
		onThreadSearch?: (value: string) => void;
		onSelectThread?: (thread: Thread) => void;
		onRetry?: () => void;
	};

	let {
		threads = [],
		selectedThread = null,
		threadSearch = '',
		loadingThreads = false,
		threadError = null,
		onThreadSearch = () => {},
		onSelectThread = () => {},
		onRetry = () => {}
	}: SidebarProps = $props();

	function getThreadName(thread: Thread): string {
		return thread.thread_name || thread.participant_names || `Thread ${thread.thread_id}`;
	}

	function formatTime(ms: number | null): string {
		if (!ms) return 'Jan 1';
		const date = new Date(ms);
		const now = new Date();
		const diff = now.getTime() - date.getTime();
		const days = Math.floor(diff / (1000 * 60 * 60 * 24));

		if (days === 0) {
			return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
		} else if (days === 1) {
			return 'Yesterday';
		} else if (days < 7) {
			return date.toLocaleDateString([], { weekday: 'short' });
		} else {
			return date.toLocaleDateString([], { month: 'short', day: 'numeric' });
		}
	}

	function formatNumber(n: number): string {
		if (n >= 1000000) return (n / 1000000).toFixed(1) + 'M';
		if (n >= 1000) return (n / 1000).toFixed(1) + 'K';
		return n.toString();
	}

	function getLocalAvatarUrl(thread: Thread): string | null {
		if (thread.contact_id) return `/avatars/${thread.contact_id}.jpg`;
		return null;
	}

	function getLocalAvatarPngUrl(thread: Thread): string | null {
		if (thread.contact_id) return `/avatars/${thread.contact_id}.png`;
		return null;
	}

	function handleSearchInput(event: Event) {
		onThreadSearch((event.target as HTMLInputElement).value);
	}
</script>

<aside class="sidebar-surface flex w-80 flex-shrink-0 flex-col border-r border-[var(--color-border)]">
	<!-- Sidebar Header -->
	<div class="border-b border-[var(--color-border)] p-4">
		<h1 class="mb-3 text-xl font-bold">Messenger Archive</h1>
		<label for="thread-search" class="sr-only">Search conversations</label>
		<input
			id="thread-search"
			type="text"
			placeholder="Search conversations..."
			value={threadSearch}
			oninput={handleSearchInput}
			class="w-full rounded-lg bg-[var(--color-bg-card)] px-3 py-2 text-sm outline-none placeholder:text-[var(--color-text-muted)] transition-shadow hover:ring-1 hover:ring-white/5 focus:ring-2 focus:ring-[var(--color-primary)]"
		/>
	</div>

	<!-- Thread List -->
	<div class="flex-1 overflow-y-auto">
		{#if loadingThreads}
			<div class="flex items-center justify-center py-8">
				<div class="h-6 w-6 animate-spin rounded-full border-2 border-[var(--color-primary)] border-t-transparent"></div>
			</div>
		{:else if threadError}
			<div class="p-4 text-center text-red-400">
				<p class="text-sm">{threadError}</p>
				<button onclick={onRetry} class="mt-2 text-xs underline hover:no-underline">Retry</button>
			</div>
		{:else}
			{#each threads as thread (thread.thread_id)}
				{@const localAvatar = getLocalAvatarUrl(thread)}
				{@const localPng = getLocalAvatarPngUrl(thread)}
				<button
					type="button"
					onclick={() => onSelectThread(thread)}
					class="group relative flex w-full items-center gap-3 rounded-xl px-4 py-3 text-left transition-all duration-200 hover:bg-[var(--color-bg-card-hover)] hover:ring-1 hover:ring-[color-mix(in_srgb,var(--color-primary)_18%,transparent)] hover:shadow-sm hover:shadow-black/30 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)] focus-visible:ring-inset after:absolute after:inset-y-3 after:left-0 after:w-2 after:rounded-r after:bg-gradient-to-b after:from-[var(--color-primary)] after:to-purple-500 after:content-[''] after:opacity-0 after:transition-all after:duration-200 hover:after:opacity-60 {selectedThread?.thread_id === thread.thread_id
						? 'bg-[var(--color-bg-card-hover)] ring-1 ring-[color-mix(in_srgb,var(--color-primary)_40%,transparent)] shadow-sm shadow-black/40 after:opacity-100 after:shadow-[0_0_18px_rgba(229,165,75,0.22)]'
						: ''}"
				>
					<Avatar
						name={getThreadName(thread)}
						srcs={[localAvatar, localPng, thread.picture_url]}
						size={48}
						class="flex-shrink-0"
					/>
					<div class="min-w-0 flex-1">
						<div class="flex items-center justify-between gap-2">
							<span class="truncate font-medium">{getThreadName(thread)}</span>
							<span class="flex-shrink-0 text-xs text-[var(--color-text-muted)]">
								{formatTime(thread.last_activity_timestamp_ms)}
							</span>
						</div>
						<div class="text-sm text-[var(--color-text-muted)]">{formatNumber(thread.message_count)} messages</div>
					</div>
				</button>
			{/each}
		{/if}
	</div>
</aside>

