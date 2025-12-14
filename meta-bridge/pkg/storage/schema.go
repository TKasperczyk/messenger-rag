package storage

// Schema defines the SQLite database schema for storing Messenger data
const schema = `
-- Contacts table: stores information about people you've messaged
CREATE TABLE IF NOT EXISTS contacts (
    id INTEGER PRIMARY KEY,  -- Facebook user ID
    name TEXT,
    first_name TEXT,
    username TEXT,           -- Secondary name / handle
    profile_picture_url TEXT,
    is_messenger_user BOOLEAN DEFAULT TRUE,
    is_blocked BOOLEAN DEFAULT FALSE,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- Threads table: stores conversation threads (1:1 and group chats)
CREATE TABLE IF NOT EXISTS threads (
    id INTEGER PRIMARY KEY,  -- Thread key
    thread_type INTEGER NOT NULL,  -- 1 = 1:1, 2 = group, etc.
    name TEXT,               -- Thread name (for groups) or NULL for 1:1
    snippet TEXT,            -- Last message preview
    picture_url TEXT,
    folder_name TEXT DEFAULT 'inbox',
    mute_expire_time_ms INTEGER DEFAULT 0,
    last_activity_ms INTEGER,
    last_read_watermark_ms INTEGER,
    member_count INTEGER DEFAULT 2,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- Thread participants: maps contacts to threads (for group chats)
CREATE TABLE IF NOT EXISTS thread_participants (
    thread_id INTEGER NOT NULL,
    contact_id INTEGER NOT NULL,
    nickname TEXT,
    is_admin BOOLEAN DEFAULT FALSE,
    joined_at INTEGER,
    read_watermark_ms INTEGER,
    read_action_timestamp_ms INTEGER,
    delivered_watermark_ms INTEGER,
    PRIMARY KEY (thread_id, contact_id),
    FOREIGN KEY (thread_id) REFERENCES threads(id),
    FOREIGN KEY (contact_id) REFERENCES contacts(id)
);

-- Messages table: stores all messages
CREATE TABLE IF NOT EXISTS messages (
    id TEXT PRIMARY KEY,              -- Message ID (string)
    thread_id INTEGER NOT NULL,
    sender_id INTEGER NOT NULL,
    text TEXT,
    timestamp_ms INTEGER NOT NULL,
    is_unsent BOOLEAN DEFAULT FALSE,
    is_forwarded BOOLEAN DEFAULT FALSE,
    reply_to_message_id TEXT,         -- For replies
    reply_snippet TEXT,
    edit_count INTEGER DEFAULT 0,
    sticker_id INTEGER,
    offline_threading_id TEXT,
    created_at INTEGER NOT NULL,
    indexed_at INTEGER,               -- NULL = not vector indexed, timestamp when indexed
    FOREIGN KEY (thread_id) REFERENCES threads(id),
    FOREIGN KEY (sender_id) REFERENCES contacts(id)
);

-- Attachments table: stores message attachments
CREATE TABLE IF NOT EXISTS attachments (
    id TEXT PRIMARY KEY,
    message_id TEXT NOT NULL,
    attachment_type INTEGER NOT NULL,  -- Image, video, audio, file, etc.
    url TEXT,
    filename TEXT,
    mime_type TEXT,
    file_size INTEGER,
    width INTEGER,
    height INTEGER,
    duration_ms INTEGER,               -- For audio/video
    created_at INTEGER NOT NULL,
    FOREIGN KEY (message_id) REFERENCES messages(id)
);

-- Reactions table: stores reactions to messages
CREATE TABLE IF NOT EXISTS reactions (
    thread_id INTEGER NOT NULL,
    message_id TEXT NOT NULL,
    actor_id INTEGER NOT NULL,
    reaction TEXT NOT NULL,            -- Unicode emoji
    timestamp_ms INTEGER NOT NULL,
    PRIMARY KEY (thread_id, message_id, actor_id),
    FOREIGN KEY (message_id) REFERENCES messages(id),
    FOREIGN KEY (actor_id) REFERENCES contacts(id)
);

-- Indexes for common queries
CREATE INDEX IF NOT EXISTS idx_messages_thread_id ON messages(thread_id);
CREATE INDEX IF NOT EXISTS idx_messages_sender_id ON messages(sender_id);
CREATE INDEX IF NOT EXISTS idx_messages_timestamp ON messages(timestamp_ms);
CREATE INDEX IF NOT EXISTS idx_messages_indexed_at ON messages(indexed_at);
CREATE INDEX IF NOT EXISTS idx_threads_last_activity ON threads(last_activity_ms);
CREATE INDEX IF NOT EXISTS idx_attachments_message_id ON attachments(message_id);
CREATE INDEX IF NOT EXISTS idx_reactions_message_id ON reactions(message_id);
CREATE INDEX IF NOT EXISTS idx_thread_participants_contact ON thread_participants(contact_id);

-- Full-text search virtual table for message content (using FTS4 for broader compatibility)
CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts4(
    text,
    tokenize=unicode61
);

-- Triggers to keep FTS in sync
CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts(docid, text)
    SELECT NEW.rowid, NEW.text
    WHERE NEW.text IS NOT NULL AND NEW.text != '';
END;

CREATE TRIGGER IF NOT EXISTS messages_ad AFTER DELETE ON messages BEGIN
    DELETE FROM messages_fts WHERE docid = OLD.rowid;
END;

CREATE TRIGGER IF NOT EXISTS messages_au AFTER UPDATE ON messages BEGIN
    DELETE FROM messages_fts WHERE docid = OLD.rowid;
    INSERT INTO messages_fts(docid, text)
    SELECT NEW.rowid, NEW.text
    WHERE NEW.text IS NOT NULL AND NEW.text != '';
END;

-- Metadata table for tracking sync state
CREATE TABLE IF NOT EXISTS sync_metadata (
    key TEXT PRIMARY KEY,
    value TEXT,
    updated_at INTEGER NOT NULL
);
`

type migration struct {
	Version    int
	Statements []string
}

// migrations contains SQL migrations to run in order (tracked via sync_metadata.schema_version).
var migrations = []migration{
	{
		Version: 1,
		Statements: []string{
			`ALTER TABLE messages ADD COLUMN indexed_at INTEGER;`,
			`CREATE INDEX IF NOT EXISTS idx_messages_indexed_at ON messages(indexed_at);`,
		},
	},
	{
		Version: 2,
		Statements: []string{
			`DROP INDEX IF EXISTS idx_messages_text;`,
		},
	},
	{
		Version: 3,
		Statements: []string{
			`ALTER TABLE thread_participants ADD COLUMN read_watermark_ms INTEGER;`,
			`ALTER TABLE thread_participants ADD COLUMN read_action_timestamp_ms INTEGER;`,
			`ALTER TABLE thread_participants ADD COLUMN delivered_watermark_ms INTEGER;`,
		},
	},
	{
		Version: 4,
		Statements: []string{
			`DROP TRIGGER IF EXISTS messages_ai;`,
			`DROP TRIGGER IF EXISTS messages_ad;`,
			`DROP TRIGGER IF EXISTS messages_au;`,
			`DROP TABLE IF EXISTS messages_fts;`,
			`CREATE VIRTUAL TABLE messages_fts USING fts4(
				text,
				tokenize=unicode61
			);`,
			`CREATE TRIGGER messages_ai AFTER INSERT ON messages BEGIN
				INSERT INTO messages_fts(docid, text)
				SELECT NEW.rowid, NEW.text
				WHERE NEW.text IS NOT NULL AND NEW.text != '';
			END;`,
			`CREATE TRIGGER messages_ad AFTER DELETE ON messages BEGIN
				DELETE FROM messages_fts WHERE docid = OLD.rowid;
			END;`,
			`CREATE TRIGGER messages_au AFTER UPDATE ON messages BEGIN
				DELETE FROM messages_fts WHERE docid = OLD.rowid;
				INSERT INTO messages_fts(docid, text)
				SELECT NEW.rowid, NEW.text
				WHERE NEW.text IS NOT NULL AND NEW.text != '';
			END;`,
			`INSERT INTO messages_fts(docid, text)
			 SELECT rowid, text FROM messages
			 WHERE text IS NOT NULL AND text != '';`,
		},
	},
}
