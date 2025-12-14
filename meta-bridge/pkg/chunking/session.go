package chunking

import (
	"go.mau.fi/mautrix-meta/pkg/ragconfig"
)

// SplitIntoSessions splits messages into sessions based on time gaps.
// Default gap is 45 minutes.
func SplitIntoSessions(messages []CoalescedMessage, cfg *ragconfig.Config) [][]CoalescedMessage {
	if len(messages) == 0 {
		return nil
	}

	sessionGapMs := int64(cfg.Chunking.Session.GapMinutes * 60 * 1000)

	var sessions [][]CoalescedMessage
	currentSession := []CoalescedMessage{messages[0]}

	for i := 1; i < len(messages); i++ {
		msg := messages[i]
		prevMsg := currentSession[len(currentSession)-1]
		gap := msg.StartTimestampMs - prevMsg.EndTimestampMs

		if gap > sessionGapMs {
			// Start new session
			sessions = append(sessions, currentSession)
			currentSession = []CoalescedMessage{msg}
		} else {
			currentSession = append(currentSession, msg)
		}
	}

	if len(currentSession) > 0 {
		sessions = append(sessions, currentSession)
	}

	return sessions
}
