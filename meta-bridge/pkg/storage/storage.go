package storage

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.mau.fi/mautrix-meta/pkg/messagix/table"
)

// Storage handles all database operations for message storage
type Storage struct {
	db *sql.DB
}

// New creates a new Storage instance and initializes the database
func New(dbPath string) (*Storage, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	s := &Storage{db: db}
	if err := s.init(); err != nil {
		db.Close()
		return nil, err
	}

	return s, nil
}

// init creates the database schema and runs migrations
func (s *Storage) init() error {
	_, err := s.db.Exec(schema)
	if err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}

	if err := s.runMigrations(); err != nil {
		return err
	}

	return nil
}

func (s *Storage) runMigrations() error {
	currentVersion, err := s.getSchemaVersion()
	if err != nil {
		return err
	}

	for _, m := range migrations {
		if m.Version <= currentVersion {
			continue
		}

		tx, err := s.db.Begin()
		if err != nil {
			return fmt.Errorf("failed to begin migration %d: %w", m.Version, err)
		}

		for _, stmt := range m.Statements {
			if strings.TrimSpace(stmt) == "" {
				continue
			}
			if _, err := tx.Exec(stmt); err != nil && !isIgnorableMigrationError(err) {
				_ = tx.Rollback()
				return fmt.Errorf("migration %d failed: %w", m.Version, err)
			}
		}

		now := time.Now().UnixMilli()
		if _, err := tx.Exec(`
			INSERT INTO sync_metadata (key, value, updated_at)
			VALUES (?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
		`, "schema_version", strconv.Itoa(m.Version), now); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("failed to update schema_version for migration %d: %w", m.Version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit migration %d: %w", m.Version, err)
		}

		currentVersion = m.Version
	}

	return nil
}

func (s *Storage) getSchemaVersion() (int, error) {
	value, err := s.GetSyncMetadata("schema_version")
	if err != nil {
		return 0, err
	}
	if value == "" {
		return 0, nil
	}
	v, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid schema_version %q: %w", value, err)
	}
	return v, nil
}

func isIgnorableMigrationError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "duplicate column name") ||
		strings.Contains(msg, "already exists")
}

// Close closes the database connection
func (s *Storage) Close() error {
	return s.db.Close()
}

// UpsertContact inserts or updates a contact
func (s *Storage) UpsertContact(contact *table.LSDeleteThenInsertContact) error {
	now := time.Now().UnixMilli()
	_, err := s.db.Exec(`
		INSERT INTO contacts (id, name, first_name, username, profile_picture_url, is_messenger_user, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			first_name = excluded.first_name,
			username = excluded.username,
			profile_picture_url = excluded.profile_picture_url,
			is_messenger_user = excluded.is_messenger_user,
			updated_at = excluded.updated_at
	`, contact.Id, contact.Name, contact.FirstName, contact.SecondaryName,
		contact.ProfilePictureUrl, contact.IsMessengerUser, now, now)
	return err
}

// UpsertContactFromVerify inserts or updates a contact from LSVerifyContactRowExists
func (s *Storage) UpsertContactFromVerify(contact *table.LSVerifyContactRowExists) error {
	now := time.Now().UnixMilli()
	_, err := s.db.Exec(`
		INSERT INTO contacts (id, name, first_name, username, profile_picture_url, is_blocked, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = COALESCE(excluded.name, contacts.name),
			first_name = COALESCE(excluded.first_name, contacts.first_name),
			username = COALESCE(excluded.username, contacts.username),
			profile_picture_url = COALESCE(excluded.profile_picture_url, contacts.profile_picture_url),
			is_blocked = excluded.is_blocked,
			updated_at = excluded.updated_at
	`, contact.ContactId, contact.Name, contact.FirstName, contact.SecondaryName,
		contact.ProfilePictureUrl, contact.IsBlocked, now, now)
	return err
}

// EnsureContactExists creates a minimal contact record if it doesn't exist
func (s *Storage) EnsureContactExists(contactID int64) error {
	now := time.Now().UnixMilli()
	_, err := s.db.Exec(`
		INSERT OR IGNORE INTO contacts (id, created_at, updated_at)
		VALUES (?, ?, ?)
	`, contactID, now, now)
	return err
}

// EnsureContactExistsWithName creates a contact record with name if it doesn't exist
func (s *Storage) EnsureContactExistsWithName(contactID int64, name string) error {
	now := time.Now().UnixMilli()
	_, err := s.db.Exec(`
		INSERT INTO contacts (id, name, created_at, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = COALESCE(contacts.name, excluded.name),
			updated_at = excluded.updated_at
	`, contactID, name, now, now)
	return err
}

// EnsureThreadExistsWithName creates a thread record with name if it doesn't exist
func (s *Storage) EnsureThreadExistsWithName(threadID int64, name string) error {
	now := time.Now().UnixMilli()
	_, err := s.db.Exec(`
		INSERT INTO threads (id, thread_type, name, created_at, updated_at)
		VALUES (?, 1, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = COALESCE(threads.name, excluded.name),
			updated_at = excluded.updated_at
	`, threadID, name, now, now)
	return err
}

// UpsertThread inserts or updates a thread
func (s *Storage) UpsertThread(thread *table.LSDeleteThenInsertThread) error {
	now := time.Now().UnixMilli()
	_, err := s.db.Exec(`
		INSERT INTO threads (id, thread_type, name, snippet, picture_url, folder_name,
			mute_expire_time_ms, last_activity_ms, last_read_watermark_ms, member_count, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			thread_type = excluded.thread_type,
			name = excluded.name,
			snippet = excluded.snippet,
			picture_url = excluded.picture_url,
			folder_name = excluded.folder_name,
			mute_expire_time_ms = excluded.mute_expire_time_ms,
			last_activity_ms = excluded.last_activity_ms,
			last_read_watermark_ms = excluded.last_read_watermark_ms,
			member_count = excluded.member_count,
			updated_at = excluded.updated_at
	`, thread.ThreadKey, thread.ThreadType, thread.ThreadName, thread.Snippet,
		thread.ThreadPictureUrl, thread.FolderName, thread.MuteExpireTimeMs,
		thread.LastActivityTimestampMs, thread.LastReadWatermarkTimestampMs,
		thread.MemberCount, now, now)
	return err
}

// UpsertThreadFromOrInsert handles LSUpdateOrInsertThread
func (s *Storage) UpsertThreadFromOrInsert(thread *table.LSUpdateOrInsertThread) error {
	now := time.Now().UnixMilli()
	_, err := s.db.Exec(`
		INSERT INTO threads (id, thread_type, name, snippet, picture_url, folder_name,
			mute_expire_time_ms, last_activity_ms, last_read_watermark_ms, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			thread_type = excluded.thread_type,
			name = COALESCE(excluded.name, threads.name),
			snippet = COALESCE(excluded.snippet, threads.snippet),
			picture_url = COALESCE(excluded.picture_url, threads.picture_url),
			folder_name = COALESCE(excluded.folder_name, threads.folder_name),
			mute_expire_time_ms = excluded.mute_expire_time_ms,
			last_activity_ms = excluded.last_activity_ms,
			last_read_watermark_ms = excluded.last_read_watermark_ms,
			updated_at = excluded.updated_at
	`, thread.ThreadKey, thread.ThreadType, thread.ThreadName, thread.Snippet,
		thread.ThreadPictureUrl, thread.FolderName, thread.MuteExpireTimeMs,
		thread.LastActivityTimestampMs, thread.LastReadWatermarkTimestampMs,
		now, now)
	return err
}

// AddParticipant adds a participant to a thread
func (s *Storage) AddParticipant(p *table.LSAddParticipantIdToGroupThread) error {
	// Ensure contact exists first
	if err := s.EnsureContactExists(p.ContactId); err != nil {
		return err
	}
	// Ensure thread exists
	if err := s.EnsureThreadExistsWithName(p.ThreadKey, ""); err != nil {
		return err
	}

	_, err := s.db.Exec(`
		INSERT INTO thread_participants (
			thread_id, contact_id, nickname, is_admin,
			read_watermark_ms, read_action_timestamp_ms, delivered_watermark_ms
		)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(thread_id, contact_id) DO UPDATE SET
			nickname = excluded.nickname,
			is_admin = excluded.is_admin,
			read_watermark_ms = MAX(COALESCE(thread_participants.read_watermark_ms, 0), COALESCE(excluded.read_watermark_ms, 0)),
			read_action_timestamp_ms = MAX(COALESCE(thread_participants.read_action_timestamp_ms, 0), COALESCE(excluded.read_action_timestamp_ms, 0)),
			delivered_watermark_ms = MAX(COALESCE(thread_participants.delivered_watermark_ms, 0), COALESCE(excluded.delivered_watermark_ms, 0))
	`, p.ThreadKey, p.ContactId, p.Nickname, p.IsAdmin, p.ReadWatermarkTimestampMs, p.ReadActionTimestampMs, p.DeliveredWatermarkTimestampMs)
	return err
}

// InsertMessage inserts a new message
func (s *Storage) InsertMessage(msg *table.LSInsertMessage) error {
	// Ensure thread and sender exist
	if err := s.EnsureThreadExistsWithName(msg.ThreadKey, ""); err != nil {
		return err
	}
	if err := s.EnsureContactExists(msg.SenderId); err != nil {
		return err
	}

	now := time.Now().UnixMilli()
	_, err := s.db.Exec(`
		INSERT INTO messages (id, thread_id, sender_id, text, timestamp_ms, is_unsent,
			is_forwarded, reply_to_message_id, reply_snippet, edit_count, sticker_id,
			offline_threading_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			text = CASE
				WHEN (messages.text IS NULL OR messages.text = '')
					AND excluded.text IS NOT NULL AND excluded.text != ''
					THEN excluded.text
				ELSE messages.text
			END,
			is_unsent = excluded.is_unsent,
			is_forwarded = excluded.is_forwarded,
			reply_to_message_id = COALESCE(excluded.reply_to_message_id, messages.reply_to_message_id),
			reply_snippet = COALESCE(excluded.reply_snippet, messages.reply_snippet),
			edit_count = MAX(messages.edit_count, excluded.edit_count),
			sticker_id = COALESCE(excluded.sticker_id, messages.sticker_id),
			offline_threading_id = COALESCE(excluded.offline_threading_id, messages.offline_threading_id),
			indexed_at = CASE
				WHEN excluded.text IS NOT NULL AND excluded.text != ''
					AND (messages.text IS NULL OR messages.text = '' OR messages.text != excluded.text)
					THEN NULL
				ELSE messages.indexed_at
			END
	`, msg.MessageId, msg.ThreadKey, msg.SenderId, msg.Text, msg.TimestampMs,
		msg.IsUnsent, msg.IsForwarded, nullIfEmpty(msg.ReplySourceId), msg.ReplySnippet,
		msg.EditCount, nullIfZero(msg.StickerId), msg.OfflineThreadingId, now)
	return err
}

// UpsertMessage updates or inserts a message (for edits)
func (s *Storage) UpsertMessage(msg *table.LSUpsertMessage) error {
	// Ensure thread and sender exist
	if err := s.EnsureThreadExistsWithName(msg.ThreadKey, ""); err != nil {
		return err
	}
	if err := s.EnsureContactExists(msg.SenderId); err != nil {
		return err
	}

	now := time.Now().UnixMilli()
	_, err := s.db.Exec(`
		INSERT INTO messages (id, thread_id, sender_id, text, timestamp_ms, is_unsent,
			is_forwarded, reply_to_message_id, reply_snippet, edit_count, sticker_id,
			offline_threading_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			text = excluded.text,
			is_unsent = excluded.is_unsent,
			edit_count = excluded.edit_count,
			indexed_at = CASE
				WHEN excluded.text IS NULL OR excluded.text = '' THEN NULL
				WHEN messages.text IS NULL OR messages.text != excluded.text THEN NULL
				ELSE messages.indexed_at
			END
	`, msg.MessageId, msg.ThreadKey, msg.SenderId, msg.Text, msg.TimestampMs,
		msg.IsUnsent, msg.IsForwarded, nullIfEmpty(msg.ReplySourceId), msg.ReplySnippet,
		msg.EditCount, nullIfZero(msg.StickerId), msg.OfflineThreadingId, now)
	return err
}

// DeleteThenInsertMessage handles LSDeleteThenInsertMessage
func (s *Storage) DeleteThenInsertMessage(msg *table.LSDeleteThenInsertMessage) error {
	// Ensure thread and sender exist
	if err := s.EnsureThreadExistsWithName(msg.ThreadKey, ""); err != nil {
		return err
	}
	if err := s.EnsureContactExists(msg.SenderId); err != nil {
		return err
	}

	now := time.Now().UnixMilli()
	_, err := s.db.Exec(`
		INSERT INTO messages (id, thread_id, sender_id, text, timestamp_ms, is_unsent,
			is_forwarded, reply_to_message_id, reply_snippet, edit_count, sticker_id,
			offline_threading_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			thread_id = excluded.thread_id,
			sender_id = excluded.sender_id,
			text = excluded.text,
			timestamp_ms = excluded.timestamp_ms,
			is_unsent = excluded.is_unsent,
			is_forwarded = excluded.is_forwarded,
			reply_to_message_id = excluded.reply_to_message_id,
			reply_snippet = excluded.reply_snippet,
			edit_count = excluded.edit_count,
			sticker_id = excluded.sticker_id,
			offline_threading_id = excluded.offline_threading_id,
			indexed_at = CASE
				WHEN excluded.text IS NULL OR excluded.text = '' THEN NULL
				WHEN messages.text IS NULL OR messages.text != excluded.text THEN NULL
				ELSE messages.indexed_at
			END
	`, msg.MessageId, msg.ThreadKey, msg.SenderId, msg.Text, msg.TimestampMs,
		msg.IsUnsent, msg.IsForwarded, nullIfEmpty(msg.ReplySourceId), msg.ReplySnippet,
		msg.EditCount, nullIfZero(msg.StickerId), msg.OfflineThreadingId, now)
	return err
}

// DeleteMessage marks a message as deleted (we keep it but clear the text)
func (s *Storage) DeleteMessage(threadKey int64, messageID string) error {
	_, err := s.db.Exec(`
		UPDATE messages SET text = NULL, is_unsent = TRUE, indexed_at = NULL
		WHERE id = ? AND thread_id = ?
	`, messageID, threadKey)
	return err
}

// UpdateReadReceipt updates per-participant read receipts for a thread.
func (s *Storage) UpdateReadReceipt(r *table.LSUpdateReadReceipt) error {
	if err := s.EnsureContactExists(r.ContactId); err != nil {
		return err
	}
	if err := s.EnsureThreadExistsWithName(r.ThreadKey, ""); err != nil {
		return err
	}

	_, err := s.db.Exec(`
		INSERT INTO thread_participants (thread_id, contact_id, read_watermark_ms, read_action_timestamp_ms)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(thread_id, contact_id) DO UPDATE SET
			read_watermark_ms = MAX(COALESCE(thread_participants.read_watermark_ms, 0), COALESCE(excluded.read_watermark_ms, 0)),
			read_action_timestamp_ms = MAX(COALESCE(thread_participants.read_action_timestamp_ms, 0), COALESCE(excluded.read_action_timestamp_ms, 0))
	`, r.ThreadKey, r.ContactId, r.ReadWatermarkTimestampMs, r.ReadActionTimestampMs)
	return err
}

// UpdateDeliveryReceipt updates per-participant delivery receipts for a thread.
func (s *Storage) UpdateDeliveryReceipt(r *table.LSUpdateDeliveryReceipt) error {
	if err := s.EnsureContactExists(r.ContactId); err != nil {
		return err
	}
	if err := s.EnsureThreadExistsWithName(r.ThreadKey, ""); err != nil {
		return err
	}

	_, err := s.db.Exec(`
		INSERT INTO thread_participants (thread_id, contact_id, delivered_watermark_ms)
		VALUES (?, ?, ?)
		ON CONFLICT(thread_id, contact_id) DO UPDATE SET
			delivered_watermark_ms = MAX(COALESCE(thread_participants.delivered_watermark_ms, 0), COALESCE(excluded.delivered_watermark_ms, 0))
	`, r.ThreadKey, r.ContactId, r.DeliveredWatermarkTimestampMs)
	return err
}

// UpdateThreadSnippet updates the snippet/preview for a thread.
func (s *Storage) UpdateThreadSnippet(r *table.LSUpdateThreadSnippet) error {
	now := time.Now().UnixMilli()
	_, err := s.db.Exec(`
		UPDATE threads SET snippet = ?, updated_at = ?
		WHERE id = ?
	`, r.Snippet, now, r.ThreadKey)
	return err
}

// UpsertAttachment stores an attachment record.
func (s *Storage) UpsertAttachment(a *table.LSInsertAttachment) error {
	if a == nil || a.MessageId == "" {
		return nil
	}

	now := time.Now().UnixMilli()

	attID := a.AttachmentFbid
	if attID == "" {
		attID = a.OfflineAttachmentId
	}
	if attID == "" {
		attID = fmt.Sprintf("%s:%d", a.MessageId, a.AttachmentIndex)
	}

	url := firstNonEmpty(a.PlayableUrl, a.PreviewUrl, a.ImageUrl)
	mime := firstNonEmpty(a.AttachmentMimeType, a.PlayableUrlMimeType, a.PreviewUrlMimeType, a.ImageUrlMimeType)

	_, err := s.db.Exec(`
		INSERT INTO attachments (id, message_id, attachment_type, url, filename, mime_type, file_size, width, height, duration_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			message_id = excluded.message_id,
			attachment_type = excluded.attachment_type,
			url = COALESCE(excluded.url, attachments.url),
			filename = COALESCE(excluded.filename, attachments.filename),
			mime_type = COALESCE(excluded.mime_type, attachments.mime_type),
			file_size = COALESCE(excluded.file_size, attachments.file_size),
			width = COALESCE(excluded.width, attachments.width),
			height = COALESCE(excluded.height, attachments.height),
			duration_ms = COALESCE(excluded.duration_ms, attachments.duration_ms)
	`, attID, a.MessageId, int64(a.AttachmentType), nullIfEmpty(url), nullIfEmpty(a.Filename), nullIfEmpty(mime),
		nullIfZero(a.Filesize), nullIfZero(a.PreviewWidth), nullIfZero(a.PreviewHeight), nullIfZero(a.PlayableDurationMs), now)
	return err
}

// UpsertReaction inserts or updates a reaction
func (s *Storage) UpsertReaction(r *table.LSUpsertReaction) error {
	// Ensure actor exists
	if err := s.EnsureContactExists(r.ActorId); err != nil {
		return err
	}

	_, err := s.db.Exec(`
		INSERT INTO reactions (thread_id, message_id, actor_id, reaction, timestamp_ms)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(thread_id, message_id, actor_id) DO UPDATE SET
			reaction = excluded.reaction,
			timestamp_ms = excluded.timestamp_ms
	`, r.ThreadKey, r.MessageId, r.ActorId, r.Reaction, r.TimestampMs)
	return err
}

// DeleteReaction removes a reaction
func (s *Storage) DeleteReaction(r *table.LSDeleteReaction) error {
	_, err := s.db.Exec(`
		DELETE FROM reactions WHERE thread_id = ? AND message_id = ? AND actor_id = ?
	`, r.ThreadKey, r.MessageId, r.ActorId)
	return err
}

// SetSyncMetadata stores a sync metadata value
func (s *Storage) SetSyncMetadata(key, value string) error {
	now := time.Now().UnixMilli()
	_, err := s.db.Exec(`
		INSERT INTO sync_metadata (key, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, key, value, now)
	return err
}

// GetSyncMetadata retrieves a sync metadata value
func (s *Storage) GetSyncMetadata(key string) (string, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM sync_metadata WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

// Query methods for later use (MCP server)

// SearchMessages performs a full-text search on messages
func (s *Storage) SearchMessages(query string, limit int) ([]Message, error) {
	rows, err := s.db.Query(`
		SELECT m.id, m.thread_id, m.sender_id, m.text, m.timestamp_ms,
			   c.name as sender_name, t.name as thread_name
		FROM messages_fts
		JOIN messages m ON messages_fts.docid = m.rowid
		LEFT JOIN contacts c ON m.sender_id = c.id
		LEFT JOIN threads t ON m.thread_id = t.id
		WHERE messages_fts MATCH ?
		ORDER BY m.timestamp_ms DESC
		LIMIT ?
	`, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var m Message
		var threadName sql.NullString
		if err := rows.Scan(&m.ID, &m.ThreadID, &m.SenderID, &m.Text, &m.TimestampMs,
			&m.SenderName, &threadName); err != nil {
			return nil, err
		}
		m.ThreadName = threadName.String
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

// GetConversation retrieves messages from a specific thread
func (s *Storage) GetConversation(threadID int64, limit int, beforeTimestamp int64) ([]Message, error) {
	var rows *sql.Rows
	var err error

	if beforeTimestamp > 0 {
		rows, err = s.db.Query(`
			SELECT m.id, m.thread_id, m.sender_id, m.text, m.timestamp_ms,
				   c.name as sender_name, t.name as thread_name
			FROM messages m
			LEFT JOIN contacts c ON m.sender_id = c.id
			LEFT JOIN threads t ON m.thread_id = t.id
			WHERE m.thread_id = ? AND m.timestamp_ms < ?
			ORDER BY m.timestamp_ms DESC
			LIMIT ?
		`, threadID, beforeTimestamp, limit)
	} else {
		rows, err = s.db.Query(`
			SELECT m.id, m.thread_id, m.sender_id, m.text, m.timestamp_ms,
				   c.name as sender_name, t.name as thread_name
			FROM messages m
			LEFT JOIN contacts c ON m.sender_id = c.id
			LEFT JOIN threads t ON m.thread_id = t.id
			WHERE m.thread_id = ?
			ORDER BY m.timestamp_ms DESC
			LIMIT ?
		`, threadID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var m Message
		var senderName, threadName sql.NullString
		var text sql.NullString
		if err := rows.Scan(&m.ID, &m.ThreadID, &m.SenderID, &text, &m.TimestampMs,
			&senderName, &threadName); err != nil {
			return nil, err
		}
		m.Text = text.String
		m.SenderName = senderName.String
		m.ThreadName = threadName.String
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

// ListContacts returns all contacts
func (s *Storage) ListContacts() ([]Contact, error) {
	rows, err := s.db.Query(`
		SELECT id, name, first_name, username, profile_picture_url
		FROM contacts
		WHERE name IS NOT NULL
		ORDER BY name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var contacts []Contact
	for rows.Next() {
		var c Contact
		if err := rows.Scan(&c.ID, &c.Name, &c.FirstName, &c.Username, &c.ProfilePictureURL); err != nil {
			return nil, err
		}
		contacts = append(contacts, c)
	}
	return contacts, rows.Err()
}

// ListThreads returns all threads ordered by last activity
func (s *Storage) ListThreads(limit int) ([]Thread, error) {
	rows, err := s.db.Query(`
		SELECT id, thread_type, name, snippet, last_activity_ms, member_count
		FROM threads
		ORDER BY last_activity_ms DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var threads []Thread
	for rows.Next() {
		var t Thread
		var name, snippet sql.NullString
		var lastActivity, memberCount sql.NullInt64
		if err := rows.Scan(&t.ID, &t.ThreadType, &name, &snippet,
			&lastActivity, &memberCount); err != nil {
			return nil, err
		}
		t.Name = name.String
		t.Snippet = snippet.String
		t.LastActivityMs = lastActivity.Int64
		t.MemberCount = memberCount.Int64
		threads = append(threads, t)
	}
	return threads, rows.Err()
}

// GetStats returns database statistics
func (s *Storage) GetStats() (Stats, error) {
	var stats Stats
	err := s.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&stats.MessageCount)
	if err != nil {
		return stats, err
	}
	err = s.db.QueryRow(`SELECT COUNT(*) FROM threads`).Scan(&stats.ThreadCount)
	if err != nil {
		return stats, err
	}
	err = s.db.QueryRow(`SELECT COUNT(*) FROM contacts`).Scan(&stats.ContactCount)
	return stats, err
}

// GetMessagesBySenderName retrieves messages by sender name (partial match)
func (s *Storage) GetMessagesBySenderName(name string, limit int) ([]Message, error) {
	rows, err := s.db.Query(`
		SELECT m.id, m.thread_id, m.sender_id, m.text, m.timestamp_ms,
			   c.name as sender_name, t.name as thread_name
		FROM messages m
		LEFT JOIN contacts c ON m.sender_id = c.id
		LEFT JOIN threads t ON m.thread_id = t.id
		WHERE c.name LIKE ? AND m.text IS NOT NULL AND m.text != ''
		ORDER BY m.timestamp_ms DESC
		LIMIT ?
	`, "%"+name+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var m Message
		var threadName sql.NullString
		if err := rows.Scan(&m.ID, &m.ThreadID, &m.SenderID, &m.Text, &m.TimestampMs,
			&m.SenderName, &threadName); err != nil {
			return nil, err
		}
		m.ThreadName = threadName.String
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

// InsertExportedMessage inserts a message from an export file.
// Returns true if a new row was inserted, false if it already existed.
func (s *Storage) InsertExportedMessage(messageID string, threadID, senderID int64, text string, timestampMs int64) (bool, error) {
	now := time.Now().UnixMilli()
	res, err := s.db.Exec(`
		INSERT INTO messages (id, thread_id, sender_id, text, timestamp_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING
	`, messageID, threadID, senderID, text, timestampMs, now)
	if err != nil {
		return false, err
	}
	affected, _ := res.RowsAffected()
	return affected > 0, nil
}

// FindUniqueContactIDByName returns the contact ID if the name matches exactly one contact.
func (s *Storage) FindUniqueContactIDByName(name string) (int64, bool, error) {
	rows, err := s.db.Query(`SELECT id FROM contacts WHERE name = ? LIMIT 2`, name)
	if err != nil {
		return 0, false, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return 0, false, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, false, err
	}
	if len(ids) == 1 {
		return ids[0], true, nil
	}
	return 0, false, nil
}

// FindUniqueThreadIDByName returns the thread ID if the name matches exactly one thread.
func (s *Storage) FindUniqueThreadIDByName(name string) (int64, bool, error) {
	rows, err := s.db.Query(`SELECT id FROM threads WHERE name = ? LIMIT 2`, name)
	if err != nil {
		return 0, false, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return 0, false, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, false, err
	}
	if len(ids) == 1 {
		return ids[0], true, nil
	}
	return 0, false, nil
}

// IsMessageIndexed returns true if the message has an indexed_at timestamp.
func (s *Storage) IsMessageIndexed(messageID string) (bool, error) {
	var indexedAt sql.NullInt64
	err := s.db.QueryRow(`SELECT indexed_at FROM messages WHERE id = ?`, messageID).Scan(&indexedAt)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return indexedAt.Valid, nil
}

// UpsertExportedAttachment stores an attachment from an export (best-effort metadata only).
func (s *Storage) UpsertExportedAttachment(attachmentID, messageID string, attachmentType int64, url, filename string) error {
	now := time.Now().UnixMilli()
	_, err := s.db.Exec(`
		INSERT INTO attachments (id, message_id, attachment_type, url, filename, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			message_id = excluded.message_id,
			attachment_type = excluded.attachment_type,
			url = COALESCE(excluded.url, attachments.url),
			filename = COALESCE(excluded.filename, attachments.filename)
	`, attachmentID, messageID, attachmentType, nullIfEmpty(url), nullIfEmpty(filename), now)
	return err
}

// HasMessage checks if a message with the given ID exists
func (s *Storage) HasMessage(messageID string) (bool, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE id = ?`, messageID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// HasMessageByTimestamp checks if a message exists with the same thread and timestamp
// This is used for deduplication when message IDs differ between sources
func (s *Storage) HasMessageByTimestamp(threadID, timestampMs int64) (bool, error) {
	var count int
	err := s.db.QueryRow(`
		SELECT COUNT(*) FROM messages
		WHERE thread_id = ? AND timestamp_ms = ?
	`, threadID, timestampMs).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// GetUnindexedMessages returns messages that haven't been vector indexed yet
func (s *Storage) GetUnindexedMessages(limit int) ([]Message, error) {
	rows, err := s.db.Query(`
		SELECT m.id, m.thread_id, m.sender_id, m.text, m.timestamp_ms,
			   c.name as sender_name, t.name as thread_name
		FROM messages m
		LEFT JOIN contacts c ON m.sender_id = c.id
		LEFT JOIN threads t ON m.thread_id = t.id
		WHERE m.indexed_at IS NULL AND m.text IS NOT NULL AND m.text != ''
		ORDER BY m.timestamp_ms DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var m Message
		var senderName, threadName, text sql.NullString
		if err := rows.Scan(&m.ID, &m.ThreadID, &m.SenderID, &text, &m.TimestampMs,
			&senderName, &threadName); err != nil {
			return nil, err
		}
		m.Text = text.String
		m.SenderName = senderName.String
		m.ThreadName = threadName.String
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

// GetUnindexedCount returns the number of messages that haven't been indexed
func (s *Storage) GetUnindexedCount() (int64, error) {
	var count int64
	err := s.db.QueryRow(`
		SELECT COUNT(*) FROM messages
		WHERE indexed_at IS NULL AND text IS NOT NULL AND text != ''
	`).Scan(&count)
	return count, err
}

// MarkMessagesIndexed marks the given message IDs as indexed
func (s *Storage) MarkMessagesIndexed(messageIDs []string) error {
	if len(messageIDs) == 0 {
		return nil
	}

	now := time.Now().UnixMilli()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`UPDATE messages SET indexed_at = ? WHERE id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, id := range messageIDs {
		_, err := stmt.Exec(now, id)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// ResetIndexedStatus clears the indexed_at flag for all messages
// Use this when recreating the vector collection
func (s *Storage) ResetIndexedStatus() error {
	_, err := s.db.Exec(`UPDATE messages SET indexed_at = NULL`)
	return err
}

// Helper functions
func nullIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func nullIfZero(i int64) interface{} {
	if i == 0 {
		return nil
	}
	return i
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// Types for query results

type Message struct {
	ID          string
	ThreadID    int64
	SenderID    int64
	Text        string
	TimestampMs int64
	SenderName  string
	ThreadName  string
}

type Contact struct {
	ID                int64
	Name              string
	FirstName         string
	Username          string
	ProfilePictureURL string
}

type Thread struct {
	ID             int64
	ThreadType     int64
	Name           string
	Snippet        string
	LastActivityMs int64
	MemberCount    int64
}

type Stats struct {
	MessageCount int64
	ThreadCount  int64
	ContactCount int64
}
