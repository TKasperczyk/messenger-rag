package chunking

import (
	"unicode/utf8"

	"go.mau.fi/mautrix-meta/pkg/ragconfig"
)

// CoalesceMessages merges consecutive messages from the same sender
// within the configured time gap (default 120 seconds).
// Stops merging when combined text exceeds max chars (default 900).
func CoalesceMessages(messages []Message, cfg *ragconfig.Config) []CoalescedMessage {
	if len(messages) == 0 {
		return nil
	}

	gapMs := int64(cfg.Chunking.Coalesce.MaxGapSeconds * 1000)
	maxChars := cfg.Chunking.Coalesce.MaxCombinedChars

	var coalesced []CoalescedMessage
	var current *CoalescedMessage

	for _, msg := range messages {
		if current == nil {
			// Start new coalesced message
			current = &CoalescedMessage{
				MessageIDs:       []string{msg.ID},
				ThreadID:         msg.ThreadID,
				SenderID:         msg.SenderID,
				SenderName:       msg.SenderName,
				Text:             msg.Text,
				StartTimestampMs: msg.TimestampMs,
				EndTimestampMs:   msg.TimestampMs,
			}
		} else if msg.SenderID == current.SenderID &&
			msg.TimestampMs-current.EndTimestampMs <= gapMs &&
			utf8.RuneCountInString(current.Text)+utf8.RuneCountInString(msg.Text)+1 <= maxChars {
			// Merge into current
			current.MessageIDs = append(current.MessageIDs, msg.ID)
			current.Text = current.Text + "\n" + msg.Text
			current.EndTimestampMs = msg.TimestampMs
		} else {
			// Save current and start new
			coalesced = append(coalesced, *current)
			current = &CoalescedMessage{
				MessageIDs:       []string{msg.ID},
				ThreadID:         msg.ThreadID,
				SenderID:         msg.SenderID,
				SenderName:       msg.SenderName,
				Text:             msg.Text,
				StartTimestampMs: msg.TimestampMs,
				EndTimestampMs:   msg.TimestampMs,
			}
		}
	}

	if current != nil {
		coalesced = append(coalesced, *current)
	}

	return coalesced
}
