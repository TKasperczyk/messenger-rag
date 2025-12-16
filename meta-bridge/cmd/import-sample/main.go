// import-sample imports sample conversations for demo purposes.
//
// Usage:
//
//	import-sample -json ../sample_data/conversations.json -db demo.db
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

var (
	jsonPath = flag.String("json", "", "Path to sample conversations JSON")
	dbPath   = flag.String("db", "demo.db", "Path to output SQLite database")
)

type SampleData struct {
	Contacts []Contact `json:"contacts"`
	Threads  []Thread  `json:"threads"`
}

type Contact struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type Thread struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	Type         int       `json:"type"`
	Participants []int64   `json:"participants"`
	Messages     []Message `json:"messages"`
}

type Message struct {
	Sender int64  `json:"sender"`
	Text   string `json:"text"`
	TS     string `json:"ts"`
}

func main() {
	flag.Parse()

	if *jsonPath == "" {
		fmt.Println("Usage: import-sample -json <path> -db <output.db>")
		os.Exit(1)
	}

	// Read JSON
	data, err := os.ReadFile(*jsonPath)
	if err != nil {
		fmt.Printf("Error reading JSON: %v\n", err)
		os.Exit(1)
	}

	var sample SampleData
	if err := json.Unmarshal(data, &sample); err != nil {
		fmt.Printf("Error parsing JSON: %v\n", err)
		os.Exit(1)
	}

	// Remove existing DB
	os.Remove(*dbPath)

	// Create database
	db, err := sql.Open("sqlite3", *dbPath)
	if err != nil {
		fmt.Printf("Error creating database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	ctx := context.Background()

	// Create schema
	if err := createSchema(ctx, db); err != nil {
		fmt.Printf("Error creating schema: %v\n", err)
		os.Exit(1)
	}

	// Import data
	if err := importData(ctx, db, &sample); err != nil {
		fmt.Printf("Error importing data: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Imported %d contacts, %d threads to %s\n", len(sample.Contacts), len(sample.Threads), *dbPath)
}

func createSchema(ctx context.Context, db *sql.DB) error {
	schema := `
	CREATE TABLE contacts (
		id INTEGER PRIMARY KEY,
		name TEXT,
		first_name TEXT,
		username TEXT,
		profile_picture_url TEXT,
		is_messenger_user BOOLEAN DEFAULT TRUE,
		is_blocked BOOLEAN DEFAULT FALSE,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);

	CREATE TABLE threads (
		id INTEGER PRIMARY KEY,
		name TEXT,
		thread_type INTEGER NOT NULL,
		folder TEXT DEFAULT 'inbox',
		mute_until INTEGER,
		is_archived BOOLEAN DEFAULT FALSE,
		is_pinned BOOLEAN DEFAULT FALSE,
		last_activity_ms INTEGER,
		picture_url TEXT,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);

	CREATE TABLE thread_participants (
		thread_id INTEGER NOT NULL,
		contact_id INTEGER NOT NULL,
		nickname TEXT,
		is_admin BOOLEAN DEFAULT FALSE,
		joined_at INTEGER,
		PRIMARY KEY (thread_id, contact_id),
		FOREIGN KEY (thread_id) REFERENCES threads(id),
		FOREIGN KEY (contact_id) REFERENCES contacts(id)
	);

	CREATE TABLE messages (
		id TEXT PRIMARY KEY,
		thread_id INTEGER NOT NULL,
		sender_id INTEGER NOT NULL,
		text TEXT,
		timestamp_ms INTEGER NOT NULL,
		is_unsent BOOLEAN DEFAULT FALSE,
		is_forwarded BOOLEAN DEFAULT FALSE,
		reply_to_message_id TEXT,
		reply_snippet TEXT,
		edit_count INTEGER DEFAULT 0,
		sticker_id INTEGER,
		offline_threading_id TEXT,
		created_at INTEGER NOT NULL,
		indexed_at INTEGER,
		FOREIGN KEY (thread_id) REFERENCES threads(id),
		FOREIGN KEY (sender_id) REFERENCES contacts(id)
	);

	CREATE INDEX idx_messages_thread_id ON messages(thread_id);
	CREATE INDEX idx_messages_sender_id ON messages(sender_id);
	CREATE INDEX idx_messages_timestamp ON messages(timestamp_ms);

	CREATE VIRTUAL TABLE messages_fts USING fts4(text, content=messages, tokenize=unicode61);

	CREATE TRIGGER messages_ai AFTER INSERT ON messages BEGIN
		INSERT INTO messages_fts(docid, text)
		SELECT NEW.rowid, NEW.text
		WHERE NEW.text IS NOT NULL AND NEW.text != '';
	END;

	CREATE TABLE sync_metadata (
		key TEXT PRIMARY KEY,
		value TEXT
	);
	`

	_, err := db.ExecContext(ctx, schema)
	return err
}

func importData(ctx context.Context, db *sql.DB, sample *SampleData) error {
	now := time.Now().UnixMilli()

	// Insert contacts
	contactStmt, err := db.PrepareContext(ctx, `
		INSERT INTO contacts (id, name, first_name, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer contactStmt.Close()

	for _, c := range sample.Contacts {
		firstName := c.Name
		if len(c.Name) > 0 {
			for i, r := range c.Name {
				if r == ' ' {
					firstName = c.Name[:i]
					break
				}
			}
		}
		if _, err := contactStmt.ExecContext(ctx, c.ID, c.Name, firstName, now, now); err != nil {
			return fmt.Errorf("inserting contact %d: %w", c.ID, err)
		}
	}

	// Insert threads and messages
	threadStmt, err := db.PrepareContext(ctx, `
		INSERT INTO threads (id, name, thread_type, last_activity_ms, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer threadStmt.Close()

	participantStmt, err := db.PrepareContext(ctx, `
		INSERT INTO thread_participants (thread_id, contact_id)
		VALUES (?, ?)
	`)
	if err != nil {
		return err
	}
	defer participantStmt.Close()

	messageStmt, err := db.PrepareContext(ctx, `
		INSERT INTO messages (id, thread_id, sender_id, text, timestamp_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer messageStmt.Close()

	msgCount := 0
	for _, t := range sample.Threads {
		var lastActivity int64
		for _, m := range t.Messages {
			ts, _ := time.Parse(time.RFC3339, m.TS)
			if ts.UnixMilli() > lastActivity {
				lastActivity = ts.UnixMilli()
			}
		}

		if _, err := threadStmt.ExecContext(ctx, t.ID, t.Name, t.Type, lastActivity, now, now); err != nil {
			return fmt.Errorf("inserting thread %d: %w", t.ID, err)
		}

		for _, p := range t.Participants {
			if _, err := participantStmt.ExecContext(ctx, t.ID, p); err != nil {
				return fmt.Errorf("inserting participant: %w", err)
			}
		}

		for i, m := range t.Messages {
			ts, _ := time.Parse(time.RFC3339, m.TS)
			msgID := fmt.Sprintf("sample_%d_%d", t.ID, i)
			if _, err := messageStmt.ExecContext(ctx, msgID, t.ID, m.Sender, m.Text, ts.UnixMilli(), now); err != nil {
				return fmt.Errorf("inserting message: %w", err)
			}
			msgCount++
		}
	}

	fmt.Printf("Imported %d messages across %d threads\n", msgCount, len(sample.Threads))

	// Set demo user as current user (ID 1006 = "Demo User" in sample data)
	_, err = db.ExecContext(ctx, `
		INSERT INTO sync_metadata (key, value) VALUES
			('current_user_id', '1006'),
			('current_user_name', 'Demo User')
	`)
	if err != nil {
		return fmt.Errorf("inserting sync_metadata: %w", err)
	}

	return nil
}
