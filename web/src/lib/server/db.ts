import { getDb } from './sqlite-db';

const db = getDb();

// Get current user ID and name from sync_metadata
const currentUserRow = db.prepare(
	"SELECT value FROM sync_metadata WHERE key = 'current_user_id'"
).get() as { value: string } | undefined;
const currentUserId = currentUserRow?.value || '0';

// Get all IDs that belong to the current user (there may be multiple due to Facebook/Messenger ID differences)
const currentUserName = (
	db.prepare("SELECT value FROM sync_metadata WHERE key = 'current_user_name'").get() as
		| { value: string }
		| undefined
)?.value;

// Get all IDs that belong to the current user, with fallback to prevent empty array
const currentUserIdsFromName = currentUserName
	? (
			db.prepare('SELECT id FROM contacts WHERE name = ?').all(currentUserName) as { id: number }[]
		).map((r) => r.id)
	: [];

// Guard against empty array - always include the primary currentUserId as fallback
const currentUserIds =
	currentUserIdsFromName.length > 0
		? currentUserIdsFromName
		: [Number(currentUserId)].filter((id) => !isNaN(id) && id !== 0);

// Final safety check - if still empty, use a placeholder that won't match anything
const currentUserIdsPlaceholder = currentUserIds.length > 0 ? currentUserIds.join(',') : '-1';

export interface Thread {
	thread_id: string;
	thread_ids: string; // Comma-separated list of all thread IDs (for merged threads)
	thread_name: string | null;
	thread_type: number;
	last_activity_timestamp_ms: number | null;
	message_count?: number;
	participant_names?: string;
	picture_url?: string | null;
	contact_id?: string | null; // For 1:1 chats, the other person's ID (for local avatar lookup)
}

export interface Message {
	message_id: string;
	thread_id: string;
	sender_id: string;
	sender_name: string | null;
	text: string | null;
	timestamp_ms: number;
	is_from_me: number;
	reply_to_id: string | null;
}

export interface Contact {
	contact_id: string;
	name: string | null;
	profile_picture_url: string | null;
}

export function getThreads(limit = 100, offset = 0, search?: string): Thread[] {
	// Simple query - no merging, just show all threads
	// For 1:1 threads (thread_type=1), derive contact_id from thread_participants (not messages)
	// This ensures we get the other person's ID even if they never sent a message
	let query = `
		SELECT
			CAST(t.id AS TEXT) as thread_id,
			CAST(t.id AS TEXT) as thread_ids,
			t.name as thread_name,
			t.thread_type,
			COALESCE(MAX(m.timestamp_ms), t.last_activity_ms, 0) as last_activity_timestamp_ms,
			COUNT(DISTINCT m.id) as message_count,
			GROUP_CONCAT(DISTINCT c.name) as participant_names,
			CASE
				WHEN t.thread_type = 1 THEN COALESCE(
					(SELECT c2.profile_picture_url FROM contacts c2
					 WHERE c2.id = (
						SELECT tp2.contact_id FROM thread_participants tp2
						WHERE tp2.thread_id = t.id AND tp2.contact_id NOT IN (${currentUserIdsPlaceholder})
						LIMIT 1
					)),
					t.picture_url
				)
				ELSE t.picture_url
			END as picture_url,
			CASE
				WHEN t.thread_type = 1 THEN (
					SELECT CAST(tp2.contact_id AS TEXT) FROM thread_participants tp2
					WHERE tp2.thread_id = t.id AND tp2.contact_id NOT IN (${currentUserIdsPlaceholder})
					LIMIT 1
				)
				ELSE NULL
			END as contact_id
		FROM threads t
		LEFT JOIN messages m ON t.id = m.thread_id
		LEFT JOIN thread_participants tp ON t.id = tp.thread_id
		LEFT JOIN contacts c ON tp.contact_id = c.id
	`;

	const params: (string | number)[] = [];

	if (search) {
		query += ` WHERE t.name LIKE ? OR c.name LIKE ?`;
		params.push(`%${search}%`, `%${search}%`);
	}

	query += `
		GROUP BY t.id
		ORDER BY last_activity_timestamp_ms DESC
		LIMIT ? OFFSET ?
	`;
	params.push(limit, offset);

	return db.prepare(query).all(...params) as Thread[];
}

export function getThread(threadId: string): Thread | undefined {
	// Use BigInt for IDs that exceed Number.MAX_SAFE_INTEGER
	let bigIntId: bigint;
	try {
		bigIntId = BigInt(threadId);
	} catch {
		return undefined;
	}

	return db
		.prepare(
			`
		SELECT
			CAST(t.id AS TEXT) as thread_id,
			t.name as thread_name,
			t.thread_type,
			t.last_activity_ms as last_activity_timestamp_ms,
			COUNT(DISTINCT m.id) as message_count,
			GROUP_CONCAT(DISTINCT c.name) as participant_names
		FROM threads t
		LEFT JOIN messages m ON t.id = m.thread_id
		LEFT JOIN thread_participants tp ON t.id = tp.thread_id
		LEFT JOIN contacts c ON tp.contact_id = c.id
		WHERE t.id = ?
		GROUP BY t.id
	`
		)
		.get(bigIntId) as Thread | undefined;
}

export function getMessages(threadIds: string, limit = 100, offset = 0): Message[] {
	// threadIds can be a single ID or comma-separated list of IDs
	// Use BigInt for IDs that exceed Number.MAX_SAFE_INTEGER
	const bigIntIds = threadIds
		.split(',')
		.map((id) => {
			try {
				return BigInt(id.trim());
			} catch {
				return null;
			}
		})
		.filter((id): id is bigint => id !== null);

	if (bigIntIds.length === 0) return [];

	const placeholders = bigIntIds.map(() => '?').join(',');

	return db
		.prepare(
			`
		SELECT
			m.id as message_id,
			CAST(m.thread_id AS TEXT) as thread_id,
			CAST(m.sender_id AS TEXT) as sender_id,
			c.name as sender_name,
			m.text,
			m.timestamp_ms,
			CASE WHEN m.sender_id IN (${currentUserIdsPlaceholder}) THEN 1 ELSE 0 END as is_from_me,
			m.reply_to_message_id as reply_to_id
		FROM messages m
		LEFT JOIN contacts c ON m.sender_id = c.id
		WHERE m.thread_id IN (${placeholders})
		ORDER BY m.timestamp_ms DESC
		LIMIT ? OFFSET ?
	`
		)
		.all(...bigIntIds, limit, offset) as Message[];
}

export function searchMessages(query: string, limit = 50): (Message & { thread_name: string })[] {
	return db
		.prepare(
			`
		SELECT
			m.id as message_id,
			CAST(m.thread_id AS TEXT) as thread_id,
			CAST(m.sender_id AS TEXT) as sender_id,
			c.name as sender_name,
			m.text,
			m.timestamp_ms,
			CASE WHEN m.sender_id IN (${currentUserIdsPlaceholder}) THEN 1 ELSE 0 END as is_from_me,
			m.reply_to_message_id as reply_to_id,
			t.name as thread_name
		FROM messages_fts fts
		JOIN messages m ON fts.rowid = m.rowid
		LEFT JOIN contacts c ON m.sender_id = c.id
		LEFT JOIN threads t ON m.thread_id = t.id
		WHERE messages_fts MATCH ?
		ORDER BY m.timestamp_ms DESC
		LIMIT ?
	`
		)
		.all(query, limit) as (Message & { thread_name: string })[];
}

export function getStats() {
	const messageCount = db.prepare('SELECT COUNT(*) as count FROM messages').get() as {
		count: number;
	};
	const threadCount = db.prepare('SELECT COUNT(*) as count FROM threads').get() as {
		count: number;
	};
	const contactCount = db.prepare('SELECT COUNT(*) as count FROM contacts').get() as {
		count: number;
	};

	const topContacts = db
		.prepare(
			`
		SELECT c.name, COUNT(*) as message_count
		FROM messages m
		JOIN contacts c ON m.sender_id = c.id
		WHERE c.name IS NOT NULL
		GROUP BY m.sender_id
		ORDER BY message_count DESC
		LIMIT 10
	`
		)
		.all() as { name: string; message_count: number }[];

	const messagesByMonth = db
		.prepare(
			`
		SELECT
			strftime('%Y-%m', timestamp_ms/1000, 'unixepoch') as month,
			COUNT(*) as count
		FROM messages
		GROUP BY month
		ORDER BY month DESC
		LIMIT 24
	`
		)
		.all() as { month: string; count: number }[];

	return {
		messageCount: messageCount.count,
		threadCount: threadCount.count,
		contactCount: contactCount.count,
		topContacts,
		messagesByMonth: messagesByMonth.reverse()
	};
}
