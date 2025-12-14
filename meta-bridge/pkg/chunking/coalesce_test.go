package chunking

import (
	"testing"

	"go.mau.fi/mautrix-meta/pkg/ragconfig"
)

func TestCoalesceMessagesUsesRuneCount(t *testing.T) {
	cfg := ragconfig.Default()
	cfg.Chunking.Coalesce.MaxGapSeconds = 120
	cfg.Chunking.Coalesce.MaxCombinedChars = 5 // 2 emojis + newline = 3 runes; 9 bytes

	messages := []Message{
		{
			ID:          "1",
			ThreadID:    1,
			SenderID:    1,
			Text:        "😀",
			TimestampMs: 1_000,
		},
		{
			ID:          "2",
			ThreadID:    1,
			SenderID:    1,
			Text:        "😀",
			TimestampMs: 2_000,
		},
	}

	coalesced := CoalesceMessages(messages, cfg)
	if len(coalesced) != 1 {
		t.Fatalf("expected 1 coalesced message, got %d", len(coalesced))
	}
	if got := coalesced[0].Text; got != "😀\n😀" {
		t.Fatalf("unexpected coalesced text: %q", got)
	}
}
