package chunking

import (
	"crypto/md5"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"go.mau.fi/mautrix-meta/pkg/ragconfig"
)

const (
	// IntraSessionGapMs is the time gap within a session that suggests a topic boundary.
	// This is not configurable via rag.yaml yet.
	IntraSessionGapMs = 20 * 60 * 1000 // 20 minutes

	// ChunkMinUtterances is the minimum number of utterances per chunk.
	ChunkMinUtterances = 2
)

var (
	// Topic markers that suggest a topic shift (Polish and English)
	topicMarkerPattern = regexp.MustCompile(`(?i)^[\s\p{P}\p{S}]*(?:btw|anyway|anyways|a tak w og[oó]le|swo[ij][aą] drog[aą]|zmiana tematu|na inny temat|wracaj[aą]c do|oh i jeszcze|a propos|à propos|speaking of|by the way|changing subject)(?:$|[^\p{L}\p{N}_])`)
)

// GenerateChunkID generates a deterministic chunk ID.
func GenerateChunkID(threadID int64, sessionIdx, chunkIdx int, startTs int64) string {
	raw := fmt.Sprintf("%d_%d_%d_%d", threadID, sessionIdx, chunkIdx, startTs)
	hash := md5.Sum([]byte(raw))
	return fmt.Sprintf("%x", hash)[:16]
}

// HasTopicMarker checks if text starts with a topic shift marker.
func HasTopicMarker(text string) bool {
	// Check first 50 runes (Unicode chars) for topic markers
	check := text
	if utf8.RuneCountInString(check) > 50 {
		runes := []rune(check)
		check = string(runes[:50])
	}
	return topicMarkerPattern.MatchString(check)
}

// FormatSingleMessage formats a single coalesced message with sender prefix.
func FormatSingleMessage(msg *CoalescedMessage, useSenderPrefix bool) string {
	sender := msg.SenderName
	if sender == "" {
		sender = fmt.Sprintf("User_%d", msg.SenderID)
	}

	if !useSenderPrefix {
		var lines []string
		for _, line := range strings.Split(msg.Text, "\n") {
			if strings.TrimSpace(line) != "" {
				lines = append(lines, line)
			}
		}
		return strings.Join(lines, "\n")
	}

	var lines []string
	for _, line := range strings.Split(msg.Text, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, fmt.Sprintf("[%s]: %s", sender, line))
		}
	}
	return strings.Join(lines, "\n")
}

// ShouldSplitChunk determines if we should start a new chunk before adding nextMsg.
// currentTextLen should be in runes (Unicode chars), not bytes.
func ShouldSplitChunk(
	currentChunk []CoalescedMessage,
	nextMsg *CoalescedMessage,
	currentTextLen int,
	cfg *ragconfig.Config,
) bool {
	if len(currentChunk) == 0 {
		return false
	}

	prevMsg := currentChunk[len(currentChunk)-1]
	nextText := FormatSingleMessage(nextMsg, cfg.Chunking.Format.SenderPrefix)
	newLen := currentTextLen + utf8.RuneCountInString(nextText) + 1 // +1 for newline

	targetChars := cfg.Chunking.Size.TargetChars
	maxChars := cfg.Chunking.Size.MaxChars

	// Hard limit - always split
	if newLen > maxChars {
		return true
	}

	// Reached target and have minimum utterances - good place to split
	if currentTextLen >= targetChars && len(currentChunk) >= ChunkMinUtterances {
		return true
	}

	// Time gap within session - suggests topic boundary
	gap := nextMsg.StartTimestampMs - prevMsg.EndTimestampMs
	if gap > IntraSessionGapMs && len(currentChunk) >= ChunkMinUtterances {
		return true
	}

	// Topic marker at start of message
	if HasTopicMarker(nextMsg.Text) && len(currentChunk) >= ChunkMinUtterances {
		return true
	}

	// URL-only message often starts micro-topic
	if HasURL(nextMsg.Text) && utf8.RuneCountInString(strings.TrimSpace(nextMsg.Text)) < 200 && len(currentChunk) >= ChunkMinUtterances {
		return true
	}

	return false
}

// CreateGreedyChunks creates chunks using greedy packing algorithm.
func CreateGreedyChunks(
	session []CoalescedMessage,
	threadID int64,
	threadName string,
	sessionIdx int,
	cfg *ragconfig.Config,
) []Chunk {
	var chunks []Chunk
	var currentChunk []CoalescedMessage
	var currentText string
	chunkIdx := 0

	useSenderPrefix := cfg.Chunking.Format.SenderPrefix

	for i := range session {
		msg := &session[i]
		msgText := FormatSingleMessage(msg, useSenderPrefix)

		// Use rune count for Unicode-aware text length
		if ShouldSplitChunk(currentChunk, msg, utf8.RuneCountInString(currentText), cfg) {
			// Save current chunk
			if len(currentChunk) > 0 {
				chunk := FinalizeChunk(currentChunk, currentText, threadID, threadName, sessionIdx, chunkIdx, cfg)
				chunks = append(chunks, chunk)
				chunkIdx++
			}

			// Start new chunk
			currentChunk = []CoalescedMessage{*msg}
			currentText = msgText
		} else {
			// Add to current chunk
			if len(currentChunk) > 0 {
				currentText = currentText + "\n" + msgText
			} else {
				currentText = msgText
			}
			currentChunk = append(currentChunk, *msg)
		}
	}

	// Don't forget the last chunk
	if len(currentChunk) > 0 {
		chunk := FinalizeChunk(currentChunk, currentText, threadID, threadName, sessionIdx, chunkIdx, cfg)
		chunks = append(chunks, chunk)
	}

	return chunks
}

// FinalizeChunk creates a Chunk object from accumulated messages.
func FinalizeChunk(
	messages []CoalescedMessage,
	text string,
	threadID int64,
	threadName string,
	sessionIdx, chunkIdx int,
	cfg *ragconfig.Config,
) Chunk {
	// Collect all message IDs
	var allIDs []string
	for _, msg := range messages {
		allIDs = append(allIDs, msg.MessageIDs...)
	}

	// Collect unique participants (preserve order)
	participants := make(map[int64]string)
	var participantIDs []int64
	var participantNames []string

	for _, msg := range messages {
		if _, exists := participants[msg.SenderID]; !exists {
			name := msg.SenderName
			if name == "" {
				name = fmt.Sprintf("User_%d", msg.SenderID)
			}
			participants[msg.SenderID] = name
			participantIDs = append(participantIDs, msg.SenderID)
			participantNames = append(participantNames, name)
		}
	}

	chunk := Chunk{
		ChunkID:          GenerateChunkID(threadID, sessionIdx, chunkIdx, messages[0].StartTimestampMs),
		ThreadID:         threadID,
		ThreadName:       threadName,
		SessionIdx:       sessionIdx,
		ChunkIdx:         chunkIdx,
		MessageIDs:       allIDs,
		ParticipantIDs:   participantIDs,
		ParticipantNames: participantNames,
		Text:             text,
		StartTimestampMs: messages[0].StartTimestampMs,
		EndTimestampMs:   messages[len(messages)-1].EndTimestampMs,
		MessageCount:     len(messages),
	}

	// Compute indexability
	isIndexable, charCount, alnumCount, uniqueWords := ComputeIndexability(text, cfg)
	chunk.IsIndexable = isIndexable
	chunk.CharCount = charCount
	chunk.AlnumCount = alnumCount
	chunk.UniqueWordCount = uniqueWords

	return chunk
}
