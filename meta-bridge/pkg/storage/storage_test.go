package storage

import (
	"database/sql"
	"testing"

	"go.mau.fi/mautrix-meta/pkg/messagix/table"
)

func TestInsertMessage_UpsertsMissingText(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	if err := s.EnsureContactExists(1); err != nil {
		t.Fatalf("EnsureContactExists: %v", err)
	}
	if err := s.EnsureThreadExistsWithName(2, ""); err != nil {
		t.Fatalf("EnsureThreadExistsWithName: %v", err)
	}

	if err := s.InsertMessage(&table.LSInsertMessage{
		MessageId:   "mid.1",
		ThreadKey:   2,
		SenderId:    1,
		Text:        "",
		TimestampMs: 123,
	}); err != nil {
		t.Fatalf("InsertMessage (empty): %v", err)
	}

	if err := s.InsertMessage(&table.LSInsertMessage{
		MessageId:   "mid.1",
		ThreadKey:   2,
		SenderId:    1,
		Text:        "hello",
		TimestampMs: 123,
	}); err != nil {
		t.Fatalf("InsertMessage (text): %v", err)
	}

	var text sql.NullString
	if err := s.db.QueryRow(`SELECT text FROM messages WHERE id = ?`, "mid.1").Scan(&text); err != nil {
		t.Fatalf("query text: %v", err)
	}
	if text.String != "hello" {
		t.Fatalf("expected updated text %q, got %q", "hello", text.String)
	}
}

func TestDeleteThenInsertMessage_DoesNotBreakReactions(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	if err := s.EnsureContactExists(1); err != nil {
		t.Fatalf("EnsureContactExists: %v", err)
	}
	if err := s.EnsureThreadExistsWithName(2, ""); err != nil {
		t.Fatalf("EnsureThreadExistsWithName: %v", err)
	}

	if err := s.InsertMessage(&table.LSInsertMessage{
		MessageId:   "mid.2",
		ThreadKey:   2,
		SenderId:    1,
		Text:        "before",
		TimestampMs: 111,
	}); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	if err := s.UpsertReaction(&table.LSUpsertReaction{
		ThreadKey:   2,
		MessageId:   "mid.2",
		ActorId:     1,
		Reaction:    "👍",
		TimestampMs: 222,
	}); err != nil {
		t.Fatalf("UpsertReaction: %v", err)
	}

	if err := s.DeleteThenInsertMessage(&table.LSDeleteThenInsertMessage{
		MessageId:   "mid.2",
		ThreadKey:   2,
		SenderId:    1,
		Text:        "after",
		TimestampMs: 333,
	}); err != nil {
		t.Fatalf("DeleteThenInsertMessage: %v", err)
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM reactions WHERE message_id = ?`, "mid.2").Scan(&count); err != nil {
		t.Fatalf("query reaction count: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 reaction to remain, got %d", count)
	}
}

func TestAddParticipant_StoresWatermarks(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	if err := s.EnsureContactExists(1); err != nil {
		t.Fatalf("EnsureContactExists: %v", err)
	}
	if err := s.EnsureThreadExistsWithName(2, ""); err != nil {
		t.Fatalf("EnsureThreadExistsWithName: %v", err)
	}

	if err := s.AddParticipant(&table.LSAddParticipantIdToGroupThread{
		ThreadKey:                     2,
		ContactId:                     1,
		ReadWatermarkTimestampMs:      10,
		ReadActionTimestampMs:         11,
		DeliveredWatermarkTimestampMs: 12,
	}); err != nil {
		t.Fatalf("AddParticipant: %v", err)
	}

	var readWM, readAction, delivered sql.NullInt64
	if err := s.db.QueryRow(`
		SELECT read_watermark_ms, read_action_timestamp_ms, delivered_watermark_ms
		FROM thread_participants
		WHERE thread_id = ? AND contact_id = ?
	`, 2, 1).Scan(&readWM, &readAction, &delivered); err != nil {
		t.Fatalf("query watermarks: %v", err)
	}

	if readWM.Int64 != 10 || readAction.Int64 != 11 || delivered.Int64 != 12 {
		t.Fatalf("unexpected watermarks: read=%d action=%d delivered=%d", readWM.Int64, readAction.Int64, delivered.Int64)
	}
}

func TestFTSTriggers_SkipEmptyAndNullText(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	if err := s.EnsureContactExists(1); err != nil {
		t.Fatalf("EnsureContactExists: %v", err)
	}
	if err := s.EnsureThreadExistsWithName(2, ""); err != nil {
		t.Fatalf("EnsureThreadExistsWithName: %v", err)
	}

	if err := s.InsertMessage(&table.LSInsertMessage{
		MessageId:   "mid.3",
		ThreadKey:   2,
		SenderId:    1,
		Text:        "",
		TimestampMs: 1,
	}); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH ?`, "hi").Scan(&count); err != nil {
		t.Fatalf("query fts match count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected FTS match count 0, got %d", count)
	}

	if err := s.UpsertMessage(&table.LSUpsertMessage{
		MessageId:   "mid.3",
		ThreadKey:   2,
		SenderId:    1,
		Text:        "hi",
		TimestampMs: 1,
	}); err != nil {
		t.Fatalf("UpsertMessage: %v", err)
	}

	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH ?`, "hi").Scan(&count); err != nil {
		t.Fatalf("query fts match count after update: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected FTS match count 1, got %d", count)
	}

	if err := s.DeleteMessage(2, "mid.3"); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH ?`, "hi").Scan(&count); err != nil {
		t.Fatalf("query fts match count after delete: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected FTS match count 0 after delete, got %d", count)
	}
}
