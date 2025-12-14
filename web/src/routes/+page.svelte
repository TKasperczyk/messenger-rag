<script lang="ts">
	import { onMount, tick } from 'svelte';
	import Avatar from '$lib/components/Avatar.svelte';
	import Sidebar from '$lib/components/Sidebar.svelte';

	// Lazy-load VisualizationTab to keep Three.js out of main bundle
	let VisualizationTab: typeof import('$lib/components/VisualizationTab.svelte').default | null =
		$state(null);

	interface Thread {
		thread_id: string;
		thread_ids: string; // Comma-separated list of all thread IDs (for merged threads)
		thread_name: string | null;
		thread_type: number;
		last_activity_timestamp_ms: number | null;
		message_count: number;
		participant_names: string | null;
		picture_url: string | null;
		contact_id: string | null; // For 1:1 chats, the other person's ID (for local avatar lookup)
	}

	interface ChunkMetadata {
		chunk_id: string;
		message_count: number;
		participant_names: string[];
		start_timestamp_ms: number;
		end_timestamp_ms: number;
		session_idx: number;
		chunk_idx: number;
	}

	interface Message {
		message_id: string;
		thread_id: string;
		sender_id: string;
		sender_name: string | null;
		text: string | null;
		timestamp_ms: number;
		is_from_me: number;
		thread_name?: string;
		score?: number;
		is_chunk?: boolean;
		chunk_metadata?: ChunkMetadata;
	}

	interface Stats {
		messageCount: number;
		threadCount: number;
		contactCount: number;
		vectorCount: number;
		topContacts: { name: string; message_count: number }[];
		messagesByMonth: { month: string; count: number }[];
	}

	type MessageTextPart =
		| { type: 'text'; value: string }
		| { type: 'link'; value: string; href: string };

	type HighlightPart = { value: string; match: boolean };

	interface PreviewMessage {
		sender: string;
		message: string;
	}

	// State
	let threads = $state<Thread[]>([]);
	let messages = $state<Message[]>([]);
	let selectedThread = $state<Thread | null>(null);
	let searchQuery = $state('');
	let searchResults = $state<Message[]>([]);
	let searchMode = $state<'text' | 'semantic' | 'hybrid' | 'bm25'>('hybrid');
	let isSearching = $state(false);
	let stats = $state<Stats | null>(null);
	let activeTab = $state<'chat' | 'search' | 'stats' | 'visualize'>('chat');
	let threadSearch = $state('');
	let loadingThreads = $state(true);
	let loadingMessages = $state(false);
	let threadError = $state<string | null>(null);
	let messageError = $state<string | null>(null);
	let searchError = $state<string | null>(null);
	let isInitialized = $state(false);

	type JumpTarget = { threadId: string; messageId: string } | { threadId: string; timestamp: number };
	let pendingJump = $state<JumpTarget | null>(null);
	let jumpNotice = $state<string | null>(null);
	let highlightedMessageId = $state<string | null>(null);

	let messageScroller = $state<HTMLDivElement | null>(null);
	let highlightTimeout: ReturnType<typeof setTimeout> | null = null;
	let messagesRequestToken = 0;

	// Derived: reversed messages for display (avoid allocation in template)
	let reversedMessages = $derived([...messages].reverse());

	// Derived: max count for stats chart
	let statsMaxCount = $derived(
		stats?.messagesByMonth ? Math.max(...stats.messagesByMonth.map((m) => m.count)) : 0
	);

	// Derived: max messages for top contacts
	let topContactsMax = $derived(
		stats?.topContacts?.length ? Math.max(...stats.topContacts.map((c) => c.message_count)) : 0
	);

	// Fetch threads
	async function fetchThreads(search?: string) {
		loadingThreads = true;
		threadError = null;
		try {
			const params = new URLSearchParams({ limit: '100' });
			if (search) params.set('search', search);
			const res = await fetch(`/api/threads?${params}`);
			if (!res.ok) {
				const text = await res.text();
				throw new Error(text || `HTTP ${res.status}`);
			}
			threads = await res.json();
		} catch (e) {
			threadError = e instanceof Error ? e.message : 'Failed to load threads';
		} finally {
			loadingThreads = false;
		}
	}

	// Fetch messages for a thread (supports merged threads via comma-separated IDs)
	async function requestMessages(threadIds: string, limit: number, offset: number): Promise<Message[]> {
		const primaryId = threadIds.split(',')[0].trim();
		const params = new URLSearchParams({
			limit: String(limit),
			offset: String(offset),
			ids: threadIds
		});
		const res = await fetch(`/api/threads/${primaryId}/messages?${params}`);
		if (!res.ok) {
			const text = await res.text();
			throw new Error(text || `HTTP ${res.status}`);
		}
		return res.json();
	}

	async function fetchMessages(threadIds: string, limit: number = 200, offset: number = 0) {
		const requestToken = ++messagesRequestToken;
		loadingMessages = true;
		messageError = null;
		try {
			const data = await requestMessages(threadIds, limit, offset);
			if (requestToken !== messagesRequestToken) return;
			messages = data;
		} catch (e) {
			if (requestToken !== messagesRequestToken) return;
			messageError = e instanceof Error ? e.message : 'Failed to load messages';
		} finally {
			if (requestToken === messagesRequestToken) {
				loadingMessages = false;
			}
		}
	}

	async function fetchMessagesNearTimestamp(thread: Thread, timestamp: number, limit: number = 500) {
		const requestToken = ++messagesRequestToken;
		loadingMessages = true;
		messageError = null;

		try {
			const total = Math.max(0, thread.message_count ?? 0);
			const threadIds = thread.thread_ids;

			if (total <= limit) {
				const data = await requestMessages(threadIds, limit, 0);
				if (requestToken !== messagesRequestToken) return;
				messages = data;
				return;
			}

			let low = 0;
			let high = total - 1;
			let bestOffset = -1;

			while (low <= high) {
				const mid = Math.floor((low + high) / 2);
				const page = await requestMessages(threadIds, 1, mid);
				if (requestToken !== messagesRequestToken) return;

				const midMsg = page[0];
				if (!midMsg) {
					high = mid - 1;
					continue;
				}

				if (midMsg.timestamp_ms >= timestamp) {
					bestOffset = mid;
					low = mid + 1;
				} else {
					high = mid - 1;
				}
			}

			const maxOffset = Math.max(0, total - limit);
			const half = Math.floor(limit / 2);
			const startOffset =
				bestOffset < 0 ? 0 : Math.max(0, Math.min(bestOffset - half, maxOffset));

			const data = await requestMessages(threadIds, limit, startOffset);
			if (requestToken !== messagesRequestToken) return;
			messages = data;
		} catch (e) {
			if (requestToken !== messagesRequestToken) return;
			messageError = e instanceof Error ? e.message : 'Failed to load messages';
		} finally {
			if (requestToken === messagesRequestToken) {
				loadingMessages = false;
			}
		}
	}

	// Search messages
	async function search() {
		if (!searchQuery.trim()) return;
		isSearching = true;
		searchError = null;
		// 'text' uses legacy FTS search, all others use semantic endpoint with mode param
		const isTextSearch = searchMode === 'text';
		const endpoint = isTextSearch ? '/api/search' : '/api/search/semantic';
		const modeParam = isTextSearch ? '' : `&mode=${searchMode === 'semantic' ? 'vector' : searchMode}`;
		try {
			const res = await fetch(`${endpoint}?q=${encodeURIComponent(searchQuery)}&limit=50${modeParam}`);
			if (!res.ok) {
				const text = await res.text();
				let message = 'Search failed';
				try {
					const err = JSON.parse(text);
					message = err.message || message;
				} catch {
					message = text || message;
				}
				throw new Error(message);
			}
			searchResults = await res.json();
		} catch (e) {
			searchError = e instanceof Error ? e.message : 'Search failed';
			searchResults = [];
		} finally {
			isSearching = false;
		}
	}

	// Fetch stats
	async function fetchStats() {
		try {
			const res = await fetch('/api/stats');
			if (!res.ok) {
				throw new Error(`HTTP ${res.status}`);
			}
			stats = await res.json();
		} catch (e) {
			console.error('Failed to load stats:', e);
		}
	}

	// Select thread
	function selectThread(thread: Thread, jumpTo: JumpTarget | null = null) {
		selectedThread = thread;
		messages = []; // Clear stale messages immediately
		messageError = null;
		jumpNotice = null;
		highlightedMessageId = null;
		pendingJump = jumpTo;

		if (jumpTo && 'timestamp' in jumpTo) {
			fetchMessagesNearTimestamp(thread, jumpTo.timestamp);
			return;
		}

		fetchMessages(thread.thread_ids, jumpTo ? 500 : 200);
	}

	// Format timestamp
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

	function formatFullDate(ms: number): string {
		return new Date(ms).toLocaleString();
	}

	const URL_REGEX = /(https?:\/\/[^\s]+|www\.[^\s]+)/gi;

	function linkify(text: string): MessageTextPart[] {
		if (!text) return [];

		const parts: MessageTextPart[] = [];
		let lastIndex = 0;

		for (const match of text.matchAll(URL_REGEX)) {
			const raw = match[0];
			const start = match.index ?? 0;

			if (start > lastIndex) {
				parts.push({ type: 'text', value: text.slice(lastIndex, start) });
			}

			let url = raw;
			let trailing = '';
			while (url.length > 0 && /[)\]}>,.!?;:]/.test(url[url.length - 1])) {
				trailing = url[url.length - 1] + trailing;
				url = url.slice(0, -1);
			}

			if (!url) {
				parts.push({ type: 'text', value: raw });
			} else {
				const href = url.startsWith('www.') ? `https://${url}` : url;
				parts.push({ type: 'link', value: url, href });
				if (trailing) parts.push({ type: 'text', value: trailing });
			}

			lastIndex = start + raw.length;
		}

		if (lastIndex < text.length) {
			parts.push({ type: 'text', value: text.slice(lastIndex) });
		}

		return parts.length ? parts : [{ type: 'text', value: text }];
	}

	function escapeRegExp(source: string): string {
		return source.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
	}

	function highlightParts(text: string, query: string): HighlightPart[] {
		const content = text ?? '';
		const trimmedQuery = query.trim();
		if (!content || !trimmedQuery) return [{ value: content, match: false }];

		const terms = trimmedQuery
			.split(/\s+/)
			.map((t) => t.trim())
			.filter((t) => t.length > 1)
			.sort((a, b) => b.length - a.length);

		if (!terms.length) return [{ value: content, match: false }];

		const pattern = terms.map(escapeRegExp).join('|');
		if (!pattern) return [{ value: content, match: false }];

		const regex = new RegExp(pattern, 'gi');
		const parts: HighlightPart[] = [];

		let lastIndex = 0;
		let match: RegExpExecArray | null;
		while ((match = regex.exec(content)) !== null) {
			const start = match.index ?? 0;
			const value = match[0] ?? '';

			if (start > lastIndex) {
				parts.push({ value: content.slice(lastIndex, start), match: false });
			}

			parts.push({ value, match: true });
			lastIndex = start + value.length;
		}

		if (lastIndex < content.length) {
			parts.push({ value: content.slice(lastIndex), match: false });
		}

		return parts.length ? parts : [{ value: content, match: false }];
	}

	function parseChunkPreview(text: string, fallbackSender: string): PreviewMessage[] {
		return (text || '')
			.split('\n')
			.map((line) => line.trim())
			.filter(Boolean)
			.map((line) => {
				const match = line.match(/^\[([^\]]+)\]:\s*(.*)$/);
				if (match) {
					return { sender: match[1], message: match[2]?.trim() || '[Attachment]' };
				}

				return { sender: fallbackSender, message: line };
			});
	}

	function getSearchPreview(result: Message, limit: number = 4): { items: PreviewMessage[]; total: number } {
		if (result.is_chunk) {
			const fallbackSender = result.sender_name || 'Unknown';
			const all = parseChunkPreview(result.text || '', fallbackSender);
			const total = result.chunk_metadata?.message_count ?? all.length;
			return { items: all.slice(0, limit), total };
		}

		const sender = result.sender_name || 'Unknown';
		return { items: [{ sender, message: result.text || '[Attachment]' }], total: 1 };
	}

	const MESSAGE_GROUP_GAP_MS = 5 * 60 * 1000;

	function isSameDay(aMs: number, bMs: number): boolean {
		const a = new Date(aMs);
		const b = new Date(bMs);
		return (
			a.getFullYear() === b.getFullYear() &&
			a.getMonth() === b.getMonth() &&
			a.getDate() === b.getDate()
		);
	}

	function formatMessageTime(ms: number): string {
		return new Date(ms).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
	}

	function formatDateDivider(ms: number): string {
		const date = new Date(ms);
		const today = new Date();

		const startOf = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
		const diffDays = Math.round((startOf(date) - startOf(today)) / (24 * 60 * 60 * 1000));

		if (diffDays === 0) return 'Today';
		if (diffDays === -1) return 'Yesterday';

		const sameYear = date.getFullYear() === today.getFullYear();
		return date.toLocaleDateString([], {
			weekday: 'short',
			month: 'short',
			day: 'numeric',
			...(sameYear ? {} : { year: 'numeric' })
		});
	}

	// Get display name for thread
	function getThreadName(thread: Thread): string {
		return thread.thread_name || thread.participant_names || `Thread ${thread.thread_id}`;
	}

	// Get local avatar URL for a contact (1:1 chats) - tries .jpg first
	function getLocalAvatarUrl(thread: Thread): string | null {
		if (thread.contact_id) {
			return `/avatars/${thread.contact_id}.jpg`;
		}
		return null;
	}

	// Get PNG fallback URL for a contact
	function getLocalAvatarPngUrl(thread: Thread): string | null {
		if (thread.contact_id) {
			return `/avatars/${thread.contact_id}.png`;
		}
		return null;
	}

	// Navigate to thread from search result
	function goToThread(msg: Message) {
		const thread = threads.find((t) => t.thread_id === msg.thread_id);
		if (!thread) return;

		activeTab = 'chat';
		if (msg.is_chunk) {
			const timestamp = msg.chunk_metadata?.start_timestamp_ms ?? msg.timestamp_ms;
			const target: JumpTarget = { threadId: String(msg.thread_id), timestamp };
			selectThread(thread, target);
			return;
		}

		const target: JumpTarget = { threadId: String(msg.thread_id), messageId: String(msg.message_id) };
		selectThread(thread, target);
	}

	// Format large numbers
	function formatNumber(n: number): string {
		if (n >= 1000000) return (n / 1000000).toFixed(1) + 'M';
		if (n >= 1000) return (n / 1000).toFixed(1) + 'K';
		return n.toString();
	}

	function getLinkClass(isFromMe: boolean): string {
		return isFromMe
			? 'break-words underline decoration-white/35 underline-offset-2 text-white/90 hover:decoration-white/70'
			: 'break-words underline decoration-sky-300/35 underline-offset-2 text-sky-300 hover:decoration-sky-300/70';
	}

	let lastThreadSearch = '';

	onMount(async () => {
		// Initial fetch - only threads needed on mount
		await fetchThreads();
		isInitialized = true;
	});

	// Debounced thread search with cleanup
	$effect(() => {
		const query = threadSearch; // Track the reactive value

		// Skip if not initialized or query hasn't changed
		if (!isInitialized || query === lastThreadSearch) return;

		const timeout = setTimeout(() => {
			lastThreadSearch = query;
			fetchThreads(query || undefined);
		}, 300);

		// Cleanup on dependency change or unmount
		return () => clearTimeout(timeout);
	});

	// Lazy-load stats when stats tab is opened
	$effect(() => {
		if (activeTab === 'stats' && !stats) {
			fetchStats();
		}
	});

	// Lazy-load VisualizationTab when visualize tab is opened
	$effect(() => {
		if (activeTab === 'visualize' && !VisualizationTab) {
			import('$lib/components/VisualizationTab.svelte').then((module) => {
				VisualizationTab = module.default;
			});
		}
	});

	function escapeSelectorValue(value: string): string {
		if (typeof CSS !== 'undefined' && typeof CSS.escape === 'function') {
			return CSS.escape(value);
		}
		return value.replace(/[\\"]/g, '\\$&');
	}

	function prefersReducedMotion(): boolean {
		return (
			typeof window !== 'undefined' &&
			typeof window.matchMedia === 'function' &&
			window.matchMedia('(prefers-reduced-motion: reduce)').matches
		);
	}

	$effect(() => {
		const jump = pendingJump;
		const thread = selectedThread;
		const isLoading = loadingMessages;
		const scroller = messageScroller;

		if (!jump || !thread || !scroller || isLoading || activeTab !== 'chat') return;

		const threadIds = new Set(thread.thread_ids.split(',').map((id) => id.trim()));
		if (!threadIds.has(jump.threadId)) return;

		let cancelled = false;
		(async () => {
			await tick();
			if (typeof requestAnimationFrame === 'function') {
				await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
			}
			if (cancelled) return;

			let el: HTMLElement | null = null;
			let targetMessageId: string | null = null;

			if ('messageId' in jump) {
				targetMessageId = jump.messageId;
				const selector = `[data-message-id="${escapeSelectorValue(jump.messageId)}"]`;
				el = scroller.querySelector(selector) as HTMLElement | null;
			} else if ('timestamp' in jump) {
				const messageById = new Map<string, Message>(
					messages.map((msg) => [String(msg.message_id), msg])
				);

				let fallbackEl: HTMLElement | null = null;
				let fallbackId: string | null = null;

				const messageEls = scroller.querySelectorAll<HTMLElement>('[data-message-id]');
				for (const candidate of messageEls) {
					const id = candidate.getAttribute('data-message-id');
					if (!id) continue;
					const msg = messageById.get(id);
					if (!msg) continue;

					fallbackEl = candidate;
					fallbackId = id;

					if (msg.timestamp_ms >= jump.timestamp) {
						el = candidate;
						targetMessageId = id;
						break;
					}
				}

				if (!el && fallbackEl && fallbackId) {
					el = fallbackEl;
					targetMessageId = fallbackId;
				}
			}

			if (!el || !targetMessageId) {
				jumpNotice =
					'messageId' in jump
						? `That message isn't in the currently loaded window (${messages.length} messages). It may be further in history.`
						: `That timestamp isn't in the currently loaded window (${messages.length} messages). It may be further in history.`;
				pendingJump = null;
				return;
			}

			el.scrollIntoView({ behavior: prefersReducedMotion() ? 'auto' : 'smooth', block: 'center' });
			highlightedMessageId = targetMessageId;
			jumpNotice = null;
			pendingJump = null;

			if (highlightTimeout) clearTimeout(highlightTimeout);
			highlightTimeout = setTimeout(() => {
				highlightedMessageId = null;
				highlightTimeout = null;
			}, 1800);
		})();

		return () => {
			cancelled = true;
		};
	});
</script>

<style>
	.jump-highlight {
		position: relative;
	}

	.jump-highlight::after {
		content: '';
		position: absolute;
		inset: -3px;
		border-radius: inherit;
		pointer-events: none;
		box-shadow:
			0 0 0 2px color-mix(in srgb, var(--color-primary) 62%, transparent),
			0 0 24px color-mix(in srgb, var(--color-primary) 32%, transparent),
			0 0 48px color-mix(in srgb, var(--color-primary) 18%, transparent);
		opacity: 0;
		animation: jump-highlight-fade 1800ms ease-out;
	}

	@keyframes jump-highlight-fade {
		0% {
			opacity: 1;
		}
		65% {
			opacity: 1;
		}
		100% {
			opacity: 0;
		}
	}

	@media (prefers-reduced-motion: reduce) {
		.jump-highlight::after {
			animation: none;
			opacity: 1;
		}
	}
</style>

<div class="flex h-screen overflow-hidden">
	<!-- Sidebar -->
	<Sidebar
		threads={threads}
		selectedThread={selectedThread}
		threadSearch={threadSearch}
		loadingThreads={loadingThreads}
		threadError={threadError}
		onThreadSearch={(value) => (threadSearch = value)}
		onSelectThread={selectThread}
		onRetry={() => fetchThreads(threadSearch || undefined)}
	/>

	<!-- Main Content -->
	<main class="flex flex-1 flex-col overflow-hidden">
		<!-- Tab Navigation -->
		<div class="flex gap-1 border-b border-[var(--color-border)] bg-[var(--color-bg-darker)] p-1" role="tablist" aria-label="Main navigation">
			<button
				onclick={() => (activeTab = 'chat')}
				role="tab"
					aria-selected={activeTab === 'chat'}
					aria-controls="panel-chat"
					id="tab-chat"
					class="rounded-lg px-5 py-2.5 font-medium transition-all duration-200 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)] focus-visible:ring-inset {activeTab === 'chat' ? 'bg-[var(--color-bg-card)] text-[var(--color-text)] shadow-sm shadow-black/20 ring-1 ring-white/5 tab-underline' : 'text-[var(--color-text-muted)] hover:bg-[var(--color-bg-card-hover)] hover:text-[var(--color-text)] hover:shadow-sm hover:shadow-black/20 hover:ring-1 hover:ring-white/10'}"
				>
					Chat
				</button>
			<button
				onclick={() => (activeTab = 'search')}
				role="tab"
					aria-selected={activeTab === 'search'}
					aria-controls="panel-search"
					id="tab-search"
					class="rounded-lg px-5 py-2.5 font-medium transition-all duration-200 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)] focus-visible:ring-inset {activeTab === 'search' ? 'bg-[var(--color-bg-card)] text-[var(--color-text)] shadow-sm shadow-black/20 ring-1 ring-white/5 tab-underline' : 'text-[var(--color-text-muted)] hover:bg-[var(--color-bg-card-hover)] hover:text-[var(--color-text)] hover:shadow-sm hover:shadow-black/20 hover:ring-1 hover:ring-white/10'}"
				>
					Search
				</button>
			<button
				onclick={() => (activeTab = 'stats')}
				role="tab"
					aria-selected={activeTab === 'stats'}
					aria-controls="panel-stats"
					id="tab-stats"
					class="rounded-lg px-5 py-2.5 font-medium transition-all duration-200 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)] focus-visible:ring-inset {activeTab === 'stats' ? 'bg-[var(--color-bg-card)] text-[var(--color-text)] shadow-sm shadow-black/20 ring-1 ring-white/5 tab-underline' : 'text-[var(--color-text-muted)] hover:bg-[var(--color-bg-card-hover)] hover:text-[var(--color-text)] hover:shadow-sm hover:shadow-black/20 hover:ring-1 hover:ring-white/10'}"
				>
					Stats
				</button>
			<button
				onclick={() => (activeTab = 'visualize')}
				role="tab"
					aria-selected={activeTab === 'visualize'}
					aria-controls="panel-visualize"
					id="tab-visualize"
					class="rounded-lg px-5 py-2.5 font-medium transition-all duration-200 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)] focus-visible:ring-inset {activeTab === 'visualize' ? 'bg-[var(--color-bg-card)] text-[var(--color-text)] shadow-sm shadow-black/20 ring-1 ring-white/5 tab-underline' : 'text-[var(--color-text-muted)] hover:bg-[var(--color-bg-card-hover)] hover:text-[var(--color-text)] hover:shadow-sm hover:shadow-black/20 hover:ring-1 hover:ring-white/10'}"
				>
					Visualize
				</button>
		</div>

		<!-- Tab Content -->
		{#if activeTab === 'chat'}
			<!-- Chat View -->
			{#if selectedThread}
				{@const headerLocalAvatar = getLocalAvatarUrl(selectedThread)}
				{@const headerLocalPng = getLocalAvatarPngUrl(selectedThread)}
				<div class="flex flex-1 flex-col overflow-hidden">
					<!-- Chat Header -->
					<header class="flex items-center gap-3 border-b border-[var(--color-border)] bg-[var(--color-bg-card)] px-4 py-3 shadow-sm shadow-black/30">
						<Avatar
							name={getThreadName(selectedThread)}
							srcs={[headerLocalAvatar, headerLocalPng, selectedThread.picture_url]}
							size={40}
							class="flex-shrink-0"
						/>
						<div>
							<h2 class="font-semibold">{getThreadName(selectedThread)}</h2>
							<p class="text-sm text-[var(--color-text-muted)]">
								{formatNumber(selectedThread.message_count)} messages
							</p>
						</div>
					</header>

					{#if jumpNotice}
						<div
							class="mx-4 mt-3 rounded-lg bg-[color-mix(in_srgb,var(--color-primary)_12%,transparent)] px-3 py-2 text-xs text-[var(--color-text)] ring-1 ring-[color-mix(in_srgb,var(--color-primary)_28%,transparent)]"
							role="status"
						>
							{jumpNotice}
						</div>
					{/if}

					<!-- Messages -->
					<div class="flex flex-1 flex-col-reverse overflow-y-auto p-4" bind:this={messageScroller}>
						{#if loadingMessages}
							<div class="flex items-center justify-center py-8">
								<div class="h-6 w-6 animate-spin rounded-full border-2 border-[var(--color-primary)] border-t-transparent"></div>
							</div>
						{:else if messageError}
							<div class="flex items-center justify-center py-8 text-red-400">
								<div class="text-center">
									<p class="text-sm">{messageError}</p>
									<button onclick={() => selectedThread && fetchMessages(selectedThread.thread_ids)} class="mt-2 text-xs underline hover:no-underline">Retry</button>
								</div>
							</div>
						{:else}
							<div>
								{#each reversedMessages as msg, i (msg.message_id)}
									{@const messageId = String(msg.message_id)}
									{@const prev = reversedMessages[i - 1]}
									{@const next = reversedMessages[i + 1]}
									{@const sameDayPrev = prev ? isSameDay(msg.timestamp_ms, prev.timestamp_ms) : false}
									{@const sameDayNext = next ? isSameDay(msg.timestamp_ms, next.timestamp_ms) : false}
									{@const gapPrevOk = prev ? msg.timestamp_ms - prev.timestamp_ms < MESSAGE_GROUP_GAP_MS : false}
									{@const gapNextOk = next ? next.timestamp_ms - msg.timestamp_ms < MESSAGE_GROUP_GAP_MS : false}
									{@const sameSenderPrev = !!(prev && sameDayPrev && gapPrevOk && prev.sender_id === msg.sender_id && prev.is_from_me === msg.is_from_me)}
									{@const sameSenderNext = !!(next && sameDayNext && gapNextOk && next.sender_id === msg.sender_id && next.is_from_me === msg.is_from_me)}
									{@const groupStart = !sameSenderPrev}
									{@const groupEnd = !sameSenderNext}
									{@const showDateDivider = i === 0 || !sameDayPrev}
									{@const showSender = !msg.is_from_me && groupStart}
									{@const showTime = groupStart}

									{#if showDateDivider}
										<div class="{i === 0 ? 'mt-0' : 'mt-6'} mb-4 flex items-center justify-center">
											<div class="rounded-full bg-[var(--color-bg-darker)] px-3 py-1 text-xs text-[var(--color-text-muted)] ring-1 ring-white/5">
												{formatDateDivider(msg.timestamp_ms)}
											</div>
										</div>
									{/if}

									<div
										class="{showDateDivider ? 'mt-0' : sameSenderPrev ? 'mt-1.5' : 'mt-4'} flex {msg.is_from_me ? 'justify-end' : 'justify-start'}"
										data-message-id={messageId}
									>
										<div
											class="flex max-w-[78%] items-end gap-2 {msg.is_from_me ? 'flex-row-reverse' : 'flex-row'}"
										>
											{#if !msg.is_from_me}
												<div class="w-8 flex-shrink-0">
													{#if groupEnd}
														<Avatar
															name={msg.sender_name || 'Unknown'}
															size={32}
															class="ring-white/5"
															title={msg.sender_name || 'Unknown'}
														/>
													{/if}
												</div>
											{/if}

												<div class="min-w-0">
													{#if showSender}
														<div class="mb-1 flex items-baseline gap-2 text-xs text-[var(--color-text-muted)]">
															<span class="font-medium text-[var(--color-text)]">
																{msg.sender_name || 'Unknown'}
															</span>
														</div>
													{/if}

												<div class="group">
													<div
														class="rounded-2xl px-4 py-2.5 text-sm leading-relaxed shadow-sm ring-1 ring-inset transition-all duration-200 ease-out hover:shadow-md {msg.is_from_me
															? 'bg-gradient-to-br from-sky-500/95 via-blue-500/95 to-indigo-500/95 text-white shadow-sky-500/25 ring-white/15 hover:brightness-[1.02]'
															: 'bg-[var(--color-bg-card)] shadow-black/30 ring-white/7 hover:bg-[var(--color-bg-card-hover)] hover:ring-white/10'} {msg.is_from_me
															? groupStart ? '' : 'rounded-tr-md'
															: groupStart ? '' : 'rounded-tl-md'} {msg.is_from_me
															? groupEnd ? '' : 'rounded-br-md'
															: groupEnd ? '' : 'rounded-bl-md'}"
														class:jump-highlight={messageId === highlightedMessageId}
													>
														{#if msg.text}
															<p class="whitespace-pre-wrap break-words">
																{#each linkify(msg.text) as part, idx (idx)}
																	{#if part.type === 'link'}
																		<a
																			href={part.href}
																			target="_blank"
																			rel="noopener noreferrer"
																			class={getLinkClass(!!msg.is_from_me)}
																		>
																			{part.value}
																		</a>
																	{:else}
																		{part.value}
																	{/if}
																{/each}
															</p>
														{:else}
															<p class="italic {msg.is_from_me ? 'text-white/75' : 'text-[var(--color-text-muted)]'}">
																[Attachment]
															</p>
														{/if}
													</div>

													<div
														class="mt-1 text-[11px] leading-none text-[var(--color-text-muted)] transition-[opacity,transform] duration-200 ease-out {msg.is_from_me ? 'text-right' : 'text-left'} {showTime ? 'opacity-100 translate-y-0' : 'opacity-0 -translate-y-1 group-hover:opacity-100 group-hover:translate-y-0'}"
													>
														<time title={formatFullDate(msg.timestamp_ms)}>{formatMessageTime(msg.timestamp_ms)}</time>
													</div>
												</div>
											</div>
										</div>
									</div>
								{/each}
							</div>
						{/if}
					</div>
				</div>
			{:else}
				<div class="flex flex-1 items-center justify-center text-[var(--color-text-muted)]">
					<div class="text-center">
						<svg class="mx-auto mb-4 h-16 w-16 opacity-50" fill="none" stroke="currentColor" viewBox="0 0 24 24">
							<path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M8 12h.01M12 12h.01M16 12h.01M21 12c0 4.418-4.03 8-9 8a9.863 9.863 0 01-4.255-.949L3 20l1.395-3.72C3.512 15.042 3 13.574 3 12c0-4.418 4.03-8 9-8s9 3.582 9 8z" />
						</svg>
						<p>Select a conversation to view messages</p>
					</div>
				</div>
			{/if}
		{:else if activeTab === 'search'}
			<!-- Search View -->
			<div class="flex flex-1 flex-col overflow-hidden p-4">
				<div class="mb-4 flex gap-2">
					<div class="relative flex-1">
						<label for="message-search" class="sr-only">Search messages</label>
						<input
							id="message-search"
							type="text"
							placeholder="Search messages..."
							bind:value={searchQuery}
							onkeydown={(e) => e.key === 'Enter' && search()}
							class="w-full rounded-lg bg-[var(--color-bg-card)] px-4 py-3 pr-24 outline-none placeholder:text-[var(--color-text-muted)] transition-shadow hover:ring-1 hover:ring-white/5 focus:ring-2 focus:ring-[var(--color-primary)]"
						/>
						<label for="search-mode" class="sr-only">Search mode</label>
						<select
							id="search-mode"
							bind:value={searchMode}
							class="absolute right-2 top-1/2 -translate-y-1/2 rounded bg-[var(--color-bg-darker)] px-2 py-1 text-sm outline-none ring-1 ring-white/5"
							title="Hybrid: combines semantic + keyword matching (best for mixed queries)&#10;BM25: keyword matching only (best for exact Polish words)&#10;Semantic: meaning-based only&#10;Text: legacy per-message search"
						>
							<option value="hybrid">Hybrid</option>
							<option value="bm25">BM25</option>
							<option value="semantic">Semantic</option>
							<option value="text">Text</option>
						</select>
					</div>
					<button
						onclick={search}
						disabled={isSearching}
						class="rounded-lg bg-[var(--color-primary)] px-6 py-3 font-medium transition-colors hover:bg-[var(--color-primary-dark)] disabled:opacity-50"
					>
						{isSearching ? 'Searching...' : 'Search'}
					</button>
				</div>

				<div class="flex-1 overflow-y-auto">
					{#if searchError}
						<div class="flex items-center justify-center py-8 text-red-400">
							<div class="text-center">
								<p class="text-sm">{searchError}</p>
								<button onclick={search} class="mt-2 text-xs underline hover:no-underline">Retry</button>
							</div>
						</div>
						{:else if searchResults.length > 0}
							<div class="space-y-3">
								{#each searchResults as result (result.message_id)}
									{@const displayName = result.thread_name || result.sender_name || 'Unknown'}
									{@const scorePct =
										result.score !== undefined ? Math.max(0, Math.min(1, result.score)) * 100 : null}
									{@const preview = getSearchPreview(result, 4)}
									{@const remaining = Math.max(0, preview.total - preview.items.length)}
									<button
										type="button"
										onclick={() => goToThread(result)}
										class="group w-full rounded-xl border-l-4 bg-[var(--color-bg-card)] p-5 text-left shadow-sm shadow-black/20 ring-1 ring-white/5 transition hover:bg-[var(--color-bg-card-hover)] hover:ring-white/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)] {scorePct !== null && scorePct >= 75
											? 'border-green-500/60'
											: scorePct !== null && scorePct >= 50
												? 'border-blue-500/60'
												: 'border-white/10'}"
									>
										<div class="flex items-start gap-4">
											<Avatar name={displayName} size={40} class="flex-shrink-0" />

											<div class="min-w-0 flex-1">
												<div class="flex items-start justify-between gap-4">
													<div class="min-w-0">
														<div class="truncate text-base font-semibold text-[var(--color-text)]">
															{displayName}
														</div>
														{#if result.is_chunk &&
															result.chunk_metadata &&
															result.chunk_metadata.participant_names.length > 1}
															<div class="mt-0.5 truncate text-xs text-[var(--color-text-muted)]">
																{result.chunk_metadata.participant_names.join(', ')}
															</div>
														{/if}
													</div>
													<div class="shrink-0 text-right text-xs text-[var(--color-text-muted)]">
														{formatFullDate(result.timestamp_ms)}
													</div>
												</div>

												<div class="mt-2 flex items-center gap-3">
														{#if scorePct !== null}
															<div class="h-2 flex-1 overflow-hidden rounded-full bg-[var(--color-bg-darker)] ring-1 ring-white/5">
																<div
																	class="h-full rounded-full fill-glow {scorePct >= 75
																		? 'bg-gradient-to-r from-green-400 to-emerald-500 text-emerald-400'
																		: scorePct >= 50
																			? 'bg-gradient-to-r from-[var(--color-primary)] to-purple-500 text-[var(--color-primary)]'
																			: 'bg-gradient-to-r from-white/20 to-white/10 text-white/40'}"
																	style="width: {scorePct}%"
																></div>
															</div>
															<span class="shrink-0 text-xs font-medium text-[var(--color-text)]">
															{scorePct.toFixed(0)}%
														</span>
													{:else}
														<div class="flex-1 text-xs text-[var(--color-text-muted)]">Text match</div>
													{/if}

													{#if result.is_chunk && result.chunk_metadata}
														<span class="shrink-0 rounded-md bg-purple-500/15 px-2 py-0.5 text-xs text-purple-300 ring-1 ring-purple-500/20">
															{result.chunk_metadata.message_count} msgs
														</span>
													{/if}
												</div>

												<div class="mt-4 space-y-1.5 text-sm">
													{#each preview.items as msg, i}
														<div
															class="flex min-w-0 items-start gap-2 rounded-lg px-3 py-2 {i % 2 === 1
																? 'bg-white/5'
																: 'bg-white/0'}"
														>
															<span class="shrink-0 font-medium text-[var(--color-text)]">
																{msg.sender}:
															</span>
															<span class="min-w-0 flex-1 truncate text-[var(--color-text-muted)]">
																{#each highlightParts(msg.message, searchQuery) as part}
																	{#if part.match}
																		<mark
																			class="rounded px-1 text-[var(--color-text)] [background:color-mix(in_srgb,var(--color-primary)_22%,transparent)] [box-shadow:inset_0_0_0_1px_color-mix(in_srgb,var(--color-primary)_35%,transparent)]"
																		>
																			{part.value}
																		</mark>
																	{:else}
																		{part.value}
																	{/if}
																{/each}
															</span>
														</div>
													{/each}

													{#if remaining > 0}
														<div class="px-3 pt-0.5 text-xs text-[var(--color-text-muted)]">
															… +{remaining} more messages
														</div>
													{/if}
												</div>

												<div class="mt-4 flex items-center justify-end border-t border-white/5 pt-3">
													<span class="inline-flex items-center gap-1 text-sm font-medium text-[var(--color-primary)] opacity-80 transition group-hover:opacity-100">
														View Conversation
														<svg
															class="h-4 w-4 transition-transform group-hover:translate-x-0.5"
															viewBox="0 0 20 20"
															fill="currentColor"
															aria-hidden="true"
														>
															<path
																fill-rule="evenodd"
																d="M10.293 15.707a1 1 0 010-1.414L13.586 11H4a1 1 0 110-2h9.586l-3.293-3.293a1 1 0 111.414-1.414l5 5a1 1 0 010 1.414l-5 5a1 1 0 01-1.414 0z"
																clip-rule="evenodd"
															/>
														</svg>
													</span>
												</div>
											</div>
										</div>
									</button>
								{/each}
							</div>
					{:else if searchQuery && !isSearching}
						<div class="flex items-center justify-center py-8 text-[var(--color-text-muted)]">
							No results found
						</div>
					{:else}
						<div class="flex items-center justify-center py-8 text-[var(--color-text-muted)]">
							<div class="text-center">
								<svg class="mx-auto mb-4 h-16 w-16 opacity-50" fill="none" stroke="currentColor" viewBox="0 0 24 24">
									<path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
								</svg>
								<p>Search your message archive</p>
								<p class="mt-1 text-sm">Use semantic search to find messages by meaning</p>
							</div>
						</div>
					{/if}
				</div>
			</div>
			{:else if activeTab === 'stats'}
				<!-- Stats View -->
				<div class="flex-1 overflow-y-auto p-6">
					{#if stats}
						<div class="mb-8 grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
							<div class="group relative overflow-hidden rounded-xl p-6 card-premium accent-left shadow-sm shadow-black/20 ring-1 ring-white/5 transition-all duration-200 hover:ring-white/10">
								<div class="pointer-events-none absolute -right-16 -top-16 h-40 w-40 rounded-full bg-[var(--color-primary)] opacity-0 blur-3xl transition-opacity duration-200 group-hover:opacity-25"></div>
								<div class="relative flex items-start justify-between gap-4">
									<div>
										<div class="text-3xl font-bold text-[var(--color-primary)] glow-amber">
											{formatNumber(stats.messageCount)}
										</div>
										<div class="mt-1 text-sm text-[var(--color-text-muted)]">Total Messages</div>
									</div>
									<div class="flex h-11 w-11 items-center justify-center rounded-xl bg-[var(--color-bg-darker)] ring-1 ring-white/5">
										<svg class="h-5 w-5 text-[var(--color-primary)]" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
											<path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M8 12h.01M12 12h.01M16 12h.01M21 12c0 4.418-4.03 8-9 8a9.863 9.863 0 01-4.255-.949L3 20l1.395-3.72C3.512 15.042 3 13.574 3 12c0-4.418 4.03-8 9-8s9 3.582 9 8z" />
										</svg>
									</div>
								</div>
							</div>

							<div class="group relative overflow-hidden rounded-xl p-6 card-premium accent-left shadow-sm shadow-black/20 ring-1 ring-white/5 transition-all duration-200 hover:ring-white/10">
								<div class="pointer-events-none absolute -right-16 -top-16 h-40 w-40 rounded-full bg-green-500 opacity-0 blur-3xl transition-opacity duration-200 group-hover:opacity-20"></div>
								<div class="relative flex items-start justify-between gap-4">
									<div>
										<div class="text-3xl font-bold text-green-400 glow-amber">{formatNumber(stats.threadCount)}</div>
										<div class="mt-1 text-sm text-[var(--color-text-muted)]">Conversations</div>
									</div>
									<div class="flex h-11 w-11 items-center justify-center rounded-xl bg-[var(--color-bg-darker)] ring-1 ring-white/5">
										<svg class="h-5 w-5 text-green-300" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
											<path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M7.5 8.25h9m-9 3H12m-8.25 3.75h9.303c1.13 0 2.12.741 2.43 1.828L18 21l-.923-3.692A2.5 2.5 0 0014.65 15H6.75A3.75 3.75 0 013 11.25v-3A3.75 3.75 0 016.75 4.5h10.5A3.75 3.75 0 0121 8.25v3A3.75 3.75 0 0117.25 15" />
										</svg>
									</div>
								</div>
							</div>

							<div class="group relative overflow-hidden rounded-xl p-6 card-premium accent-left shadow-sm shadow-black/20 ring-1 ring-white/5 transition-all duration-200 hover:ring-white/10">
								<div class="pointer-events-none absolute -right-16 -top-16 h-40 w-40 rounded-full bg-purple-500 opacity-0 blur-3xl transition-opacity duration-200 group-hover:opacity-20"></div>
								<div class="relative flex items-start justify-between gap-4">
									<div>
										<div class="text-3xl font-bold text-purple-400 glow-amber">{formatNumber(stats.contactCount)}</div>
										<div class="mt-1 text-sm text-[var(--color-text-muted)]">Contacts</div>
									</div>
									<div class="flex h-11 w-11 items-center justify-center rounded-xl bg-[var(--color-bg-darker)] ring-1 ring-white/5">
										<svg class="h-5 w-5 text-purple-300" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
											<path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M15.75 6a3.75 3.75 0 11-7.5 0 3.75 3.75 0 017.5 0zM4.5 20.118a7.5 7.5 0 0115 0A18.833 18.833 0 0112 21.75c-2.676 0-5.216-.584-7.5-1.632z" />
										</svg>
									</div>
								</div>
							</div>

							<div class="group relative overflow-hidden rounded-xl p-6 card-premium accent-left shadow-sm shadow-black/20 ring-1 ring-white/5 transition-all duration-200 hover:ring-white/10">
								<div class="pointer-events-none absolute -right-16 -top-16 h-40 w-40 rounded-full bg-orange-500 opacity-0 blur-3xl transition-opacity duration-200 group-hover:opacity-20"></div>
								<div class="relative flex items-start justify-between gap-4">
									<div>
										<div class="text-3xl font-bold text-orange-400 glow-amber">{formatNumber(stats.vectorCount)}</div>
										<div class="mt-1 text-sm text-[var(--color-text-muted)]">Indexed Vectors</div>
									</div>
									<div class="flex h-11 w-11 items-center justify-center rounded-xl bg-[var(--color-bg-darker)] ring-1 ring-white/5">
										<svg class="h-5 w-5 text-orange-300" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
											<path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M3.75 3.75h7.5v7.5h-7.5v-7.5zm9 0h7.5v7.5h-7.5v-7.5zm-9 9h7.5v7.5h-7.5v-7.5zm9 0h7.5v7.5h-7.5v-7.5z" />
										</svg>
									</div>
								</div>
							</div>
						</div>

							<div class="grid grid-cols-1 gap-6 lg:grid-cols-2">
								<!-- Top Contacts -->
								<div class="rounded-xl p-6 card-premium shadow-sm shadow-black/20 ring-1 ring-white/5 transition hover:ring-white/10">
									<h3 class="mb-4 text-lg font-semibold">Top Contacts</h3>
									<div class="space-y-2">
										{#each stats.topContacts as contact, i}
										{@const rank = i + 1}
										{@const pct = topContactsMax > 0 ? (contact.message_count / topContactsMax) * 100 : 0}
											<div class="relative overflow-hidden rounded-lg bg-[var(--color-bg-darker)] p-3 ring-1 ring-white/5">
												<div
													class="fill-glow absolute inset-y-0 left-0 -z-10 bg-gradient-to-r from-[var(--color-primary)] to-purple-500 text-[var(--color-primary)] opacity-25"
													style="width: {pct}%"
												></div>

												<div class="flex items-center gap-3">
													<div class="relative flex h-9 w-9 items-center justify-center rounded-full text-sm font-bold ring-1 shadow-sm shadow-black/30 fill-glow {rank === 1
														? 'bg-yellow-500/20 text-yellow-300 ring-yellow-500/30'
														: rank === 2
															? 'bg-slate-400/20 text-slate-200 ring-slate-400/30'
															: rank === 3
																? 'bg-amber-600/20 text-amber-300 ring-amber-600/30'
																: 'bg-[var(--color-bg-card)] text-[var(--color-primary)] ring-white/10'}">
														{rank}
													</div>

												<div class="min-w-0 flex-1">
													<div class="flex items-baseline justify-between gap-3">
														<div class="truncate font-medium text-[var(--color-text)]">{contact.name}</div>
														<div class="shrink-0 text-sm font-medium text-[var(--color-text)]">
															{formatNumber(contact.message_count)}
														</div>
													</div>
													<div class="text-xs text-[var(--color-text-muted)]">messages</div>
												</div>
											</div>
										</div>
									{/each}
								</div>
								</div>

								<!-- Messages by Month -->
								<div class="rounded-xl p-6 card-premium shadow-sm shadow-black/20 ring-1 ring-white/5 transition hover:ring-white/10">
									<h3 class="mb-4 text-lg font-semibold">Messages Over Time</h3>
									<div class="overflow-x-auto pb-2">
										<div class="flex h-48 min-w-max items-stretch gap-2">
										{#each stats.messagesByMonth as month}
											{@const height = statsMaxCount > 0 ? (month.count / statsMaxCount) * 100 : 0}
											<div class="group relative flex w-4 flex-col justify-end" title="{month.month}: {month.count} messages">
												<div
													class="chart-bar w-full min-h-[4px] rounded-t bg-gradient-to-t from-[var(--color-primary)] to-purple-600"
													style="height: {height}%"
												></div>
												<div class="absolute bottom-full left-1/2 mb-2 hidden -translate-x-1/2 whitespace-nowrap rounded-lg bg-[var(--color-bg-darker)] px-2 py-1 text-xs text-[var(--color-text)] shadow-sm shadow-black/30 ring-1 ring-white/10 group-hover:block">
													{month.month}: {formatNumber(month.count)}
												</div>
											</div>
										{/each}
									</div>
								</div>
								<div class="mt-2 flex justify-between text-xs text-[var(--color-text-muted)]">
									<span>{stats.messagesByMonth[0]?.month}</span>
									<span>{stats.messagesByMonth[stats.messagesByMonth.length - 1]?.month}</span>
								</div>
							</div>
						</div>
				{:else}
					<div class="flex items-center justify-center py-8">
						<div class="h-6 w-6 animate-spin rounded-full border-2 border-[var(--color-primary)] border-t-transparent"></div>
					</div>
				{/if}
			</div>
		{:else if activeTab === 'visualize'}
			<div class="flex-1 overflow-hidden">
				{#if VisualizationTab}
					<VisualizationTab />
				{:else}
					<div class="flex h-full items-center justify-center">
						<div class="h-6 w-6 animate-spin rounded-full border-2 border-[var(--color-primary)] border-t-transparent"></div>
					</div>
				{/if}
			</div>
		{/if}
	</main>
</div>
