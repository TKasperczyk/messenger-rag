package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"

	metatable "go.mau.fi/mautrix-meta/pkg/messagix/table"
	"go.mau.fi/mautrix-meta/pkg/storage"
)

var (
	dbPath    = flag.String("db", "messenger.db", "Path to SQLite database")
	inputPath = flag.String("input", "", "Path to export (ZIP file for Messenger app export, or directory for Facebook export)")
	verbose   = flag.Bool("v", false, "Verbose output")
	dryRun    = flag.Bool("dry-run", false, "Don't actually import, just show what would be imported")
	dropDB    = flag.Bool("drop-db", false, "Drop and recreate SQLite database before import")
)

// UnifiedMessage is our internal representation after parsing either format
type UnifiedMessage struct {
	SenderName   string
	Text         string
	TimestampMs  int64
	IsUnsent     bool
	Attachments  []UnifiedAttachment
	SourceType   string // export-native message type (best-effort)
	SourceIDHint string // export-native message id (rare; best-effort)
}

type UnifiedAttachment struct {
	Type     metatable.AttachmentType
	URI      string
	Filename string
}

type ExportSource string

const (
	ExportSourceFacebook  ExportSource = "facebook"
	ExportSourceMessenger ExportSource = "messenger"
)

// UnifiedExport is our internal representation after parsing either format
type UnifiedExport struct {
	Source       ExportSource
	ThreadName   string
	ThreadPath   string // for Facebook exports: directory path inside archive or filesystem
	ThreadIDHint int64  // best-effort thread key extracted from path

	Participants []string
	Messages     []UnifiedMessage
}

func main() {
	flag.Parse()

	logLevel := zerolog.InfoLevel
	if *verbose {
		logLevel = zerolog.DebugLevel
	}
	log := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.Kitchen}).
		With().Timestamp().Logger().Level(logLevel)

	if *inputPath == "" {
		log.Fatal().Msg("Usage: import-export -input <path> [-db messenger.db]\n  <path> can be a ZIP file (Messenger app export) or directory (Facebook export)")
	}

	// Check if input is a file or directory
	info, err := os.Stat(*inputPath)
	if err != nil {
		log.Fatal().Err(err).Str("path", *inputPath).Msg("Failed to access input path")
	}

	// Handle drop-db flag
	if *dropDB && !*dryRun {
		log.Warn().Str("db", *dbPath).Msg("Dropping existing database")
		os.Remove(*dbPath)
	}

	// Open database
	store, err := storage.New(*dbPath)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to open database")
	}
	defer store.Close()

	var totalImported, totalSkipped int

	if info.IsDir() {
		// Facebook export format (directory)
		log.Info().Str("path", *inputPath).Msg("Processing Facebook export directory")
		totalImported, totalSkipped = processFacebookExport(log, store, *inputPath)
	} else {
		// ZIP file: detect format (Facebook export ZIP vs Messenger app export ZIP)
		if strings.HasSuffix(strings.ToLower(*inputPath), ".zip") && isFacebookExportZip(*inputPath) {
			log.Info().Str("path", *inputPath).Msg("Processing Facebook export ZIP")
			totalImported, totalSkipped = processFacebookZip(log, store, *inputPath)
		} else {
			log.Info().Str("path", *inputPath).Msg("Processing Messenger app export ZIP")
			totalImported, totalSkipped = processMessengerZip(log, store, *inputPath)
		}
	}

	log.Info().
		Int("imported", totalImported).
		Int("skipped", totalSkipped).
		Msg("Import complete")
}

// ============================================================================
// Facebook Export Format (from facebook.com "Download Your Information")
// ============================================================================

// FBExport represents the Facebook export JSON structure
type FBExport struct {
	Participants []FBParticipant `json:"participants"`
	Messages     []FBMessage     `json:"messages"`
	Title        string          `json:"title"`
	ThreadPath   string          `json:"thread_path"`
}

type FBParticipant struct {
	Name string `json:"name"`
}

type FBMedia struct {
	URI string `json:"uri"`
	// Facebook exports sometimes include creation_timestamp (seconds).
	CreationTimestamp int64 `json:"creation_timestamp"`
}

type FBSticker struct {
	URI string `json:"uri"`
}

// FBShare represents a shared link in Facebook exports.
// When users share URLs (Instagram, YouTube, articles, etc.), Facebook stores
// the link and any accompanying text/comment separately from the main content.
type FBShare struct {
	Link      string `json:"link"`
	ShareText string `json:"share_text"`
}

type FBMessage struct {
	SenderName  string `json:"sender_name"`
	Content     string `json:"content"`
	TimestampMs int64  `json:"timestamp_ms"`
	IsUnsent    bool   `json:"is_unsent"`
	Type        string `json:"type"`

	Photos     []FBMedia  `json:"photos"`
	Videos     []FBMedia  `json:"videos"`
	AudioFiles []FBMedia  `json:"audio_files"`
	Files      []FBMedia  `json:"files"`
	GIFs       []FBMedia  `json:"gifs"`
	Sticker    *FBSticker `json:"sticker"`
	Share      *FBShare   `json:"share"`
}

// fbMessageText combines content, share.share_text, and share.link into a single
// text field, handling duplicates and placeholder content like "You sent a link."
func fbMessageText(msg FBMessage) string {
	content := strings.TrimSpace(fixFBEncoding(msg.Content))

	var shareText, shareLink string
	if msg.Share != nil {
		shareText = strings.TrimSpace(fixFBEncoding(msg.Share.ShareText))
		shareLink = strings.TrimSpace(msg.Share.Link) // URLs don't need encoding fix
	}

	// If no share data, just return content
	if shareText == "" && shareLink == "" {
		return content
	}

	// Check if content is just a placeholder like "You sent a link." or similar.
	// Only match short, specific patterns to avoid false positives on real user content.
	isPlaceholder := isSharePlaceholder(content)

	var parts []string
	var keptContent string

	if !isPlaceholder && content != "" {
		parts = append(parts, content)
		keptContent = content
	}

	// Add share_text if not already in kept content
	if shareText != "" && !strings.Contains(keptContent, shareText) {
		parts = append(parts, shareText)
	}

	// Build accumulated text for dedup check
	accumulated := strings.Join(parts, "\n")

	// Add link if not already present in accumulated text
	if shareLink != "" && !strings.Contains(accumulated, shareLink) {
		parts = append(parts, shareLink)
	}

	return strings.Join(parts, "\n")
}

// isSharePlaceholder returns true if content appears to be a Facebook-generated
// placeholder for shared links (e.g., "You sent a link.", "Ty wysłałeś link.").
// Only matches short, specific patterns to avoid false positives on real user content.
func isSharePlaceholder(content string) bool {
	if content == "" {
		return true
	}

	// Only consider short content as potential placeholder (< 100 chars)
	if len(content) > 100 {
		return false
	}

	lower := strings.ToLower(content)

	// English placeholders
	if strings.HasSuffix(lower, "sent a link.") ||
		strings.HasSuffix(lower, "shared a link.") ||
		strings.HasSuffix(lower, "sent a link") ||
		strings.HasSuffix(lower, "shared a link") {
		return true
	}

	// Polish placeholders (require "link" in the same string to avoid false positives)
	if strings.Contains(lower, "link") &&
		(strings.Contains(lower, "wysłał") ||
			strings.Contains(lower, "udostępnił") ||
			strings.Contains(lower, "wysłała") ||
			strings.Contains(lower, "udostępniła")) {
		return true
	}

	return false
}

func processFacebookExport(log zerolog.Logger, store *storage.Storage, basePath string) (imported, skipped int) {
	// Check if basePath contains ZIP files - if so, process them directly
	zipFiles, _ := filepath.Glob(filepath.Join(basePath, "*.zip"))
	if len(zipFiles) > 0 {
		log.Info().Int("count", len(zipFiles)).Msg("Found ZIP files, processing directly")
		for _, zipFile := range zipFiles {
			imp, skip := processFacebookZip(log, store, zipFile)
			imported += imp
			skipped += skip
		}
		return
	}

	// Otherwise, process as extracted directory
	return processFacebookExtracted(log, store, basePath)
}

func processFacebookZip(log zerolog.Logger, store *storage.Storage, zipPath string) (imported, skipped int) {
	log.Info().Str("zip", filepath.Base(zipPath)).Msg("Processing Facebook export ZIP")

	zipReader, err := zip.OpenReader(zipPath)
	if err != nil {
		log.Error().Err(err).Msg("Failed to open ZIP file")
		return 0, 0
	}
	defer zipReader.Close()

	// Group JSON files by conversation directory
	convFiles := make(map[string][]*zip.File)
	for _, file := range zipReader.File {
		if !strings.HasSuffix(file.Name, ".json") {
			continue
		}
		// Extract conversation path (parent directory of the JSON file)
		dir := filepath.Dir(file.Name)
		// Only process message JSON files (message_1.json, message_2.json, etc.)
		base := filepath.Base(file.Name)
		if !strings.HasPrefix(base, "message_") {
			continue
		}
		convFiles[dir] = append(convFiles[dir], file)
	}

	for convPath, files := range convFiles {
		imp, skip := processFBConversationFromZip(log, store, convPath, files)
		imported += imp
		skipped += skip
	}

	return
}

func processFBConversationFromZip(log zerolog.Logger, store *storage.Storage, convPath string, files []*zip.File) (imported, skipped int) {
	var allMessages []UnifiedMessage
	var threadName string
	var participants []string
	threadIDHint, _ := threadIDFromConversationPath(convPath)

	for _, file := range files {
		rc, err := file.Open()
		if err != nil {
			log.Warn().Err(err).Str("file", file.Name).Msg("Failed to open file in ZIP")
			continue
		}

		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			log.Warn().Err(err).Str("file", file.Name).Msg("Failed to read file")
			continue
		}

		var fbExport FBExport
		if err := json.Unmarshal(data, &fbExport); err != nil {
			log.Warn().Err(err).Str("file", file.Name).Msg("Failed to parse JSON")
			continue
		}

		// Get thread info from first file
		if threadName == "" {
			threadName = fixFBEncoding(fbExport.Title)
			if threadName == "" {
				threadName = filepath.Base(convPath)
			}
			for _, p := range fbExport.Participants {
				participants = append(participants, fixFBEncoding(p.Name))
			}
		}

		// Convert messages
		for _, msg := range fbExport.Messages {
			text := fbMessageText(msg)
			attachments := extractFBAttachments(msg)
			if text == "" && len(attachments) == 0 && !msg.IsUnsent {
				continue
			}
			allMessages = append(allMessages, UnifiedMessage{
				SenderName:  fixFBEncoding(msg.SenderName),
				Text:        text,
				TimestampMs: msg.TimestampMs,
				IsUnsent:    msg.IsUnsent,
				Attachments: attachments,
				SourceType:  msg.Type,
			})
		}
	}

	if len(allMessages) == 0 {
		return 0, 0
	}

	export := UnifiedExport{
		Source:       ExportSourceFacebook,
		ThreadName:   threadName,
		ThreadPath:   convPath,
		ThreadIDHint: threadIDHint,
		Participants: participants,
		Messages:     allMessages,
	}

	return processUnifiedExport(log, store, export)
}

func processFacebookExtracted(log zerolog.Logger, store *storage.Storage, basePath string) (imported, skipped int) {
	// Scan for message directories
	messageDirs := []string{
		filepath.Join(basePath, "your_facebook_activity", "messages", "inbox"),
		filepath.Join(basePath, "your_facebook_activity", "messages", "e2ee_cutover"),
		filepath.Join(basePath, "your_facebook_activity", "messages", "archived_threads"),
		filepath.Join(basePath, "your_facebook_activity", "messages", "filtered_threads"),
		filepath.Join(basePath, "your_facebook_activity", "messages", "message_requests"),
		// Also try without your_facebook_activity prefix (in case user extracted differently)
		filepath.Join(basePath, "messages", "inbox"),
		filepath.Join(basePath, "messages", "e2ee_cutover"),
		filepath.Join(basePath, "messages", "archived_threads"),
	}

	for _, dir := range messageDirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}

		log.Info().Str("dir", dir).Msg("Scanning directory")

		// Each subdirectory is a conversation
		entries, err := os.ReadDir(dir)
		if err != nil {
			log.Warn().Err(err).Str("dir", dir).Msg("Failed to read directory")
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			convPath := filepath.Join(dir, entry.Name())
			imp, skip := processFBConversation(log, store, convPath)
			imported += imp
			skipped += skip
		}
	}

	return
}

func processFBConversation(log zerolog.Logger, store *storage.Storage, convPath string) (imported, skipped int) {
	// Find all message_N.json files
	files, err := filepath.Glob(filepath.Join(convPath, "message_*.json"))
	if err != nil || len(files) == 0 {
		return 0, 0
	}

	// We need to aggregate all messages and get participants from the first file
	var allMessages []UnifiedMessage
	var threadName string
	var participants []string
	threadIDHint, _ := threadIDFromConversationPath(convPath)

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			log.Warn().Err(err).Str("file", file).Msg("Failed to read file")
			continue
		}

		var fbExport FBExport
		if err := json.Unmarshal(data, &fbExport); err != nil {
			log.Warn().Err(err).Str("file", file).Msg("Failed to parse JSON")
			continue
		}

		// Get thread info from first file
		if threadName == "" {
			threadName = fixFBEncoding(fbExport.Title)
			if threadName == "" {
				// Use directory name as fallback
				threadName = filepath.Base(convPath)
			}
			for _, p := range fbExport.Participants {
				participants = append(participants, fixFBEncoding(p.Name))
			}
		}

		// Convert messages
		for _, msg := range fbExport.Messages {
			text := fbMessageText(msg)
			attachments := extractFBAttachments(msg)
			if text == "" && len(attachments) == 0 && !msg.IsUnsent {
				continue
			}
			allMessages = append(allMessages, UnifiedMessage{
				SenderName:  fixFBEncoding(msg.SenderName),
				Text:        text,
				TimestampMs: msg.TimestampMs,
				IsUnsent:    msg.IsUnsent,
				Attachments: attachments,
				SourceType:  msg.Type,
			})
		}
	}

	if len(allMessages) == 0 {
		return 0, 0
	}

	export := UnifiedExport{
		Source:       ExportSourceFacebook,
		ThreadName:   threadName,
		ThreadPath:   convPath,
		ThreadIDHint: threadIDHint,
		Participants: participants,
		Messages:     allMessages,
	}

	return processUnifiedExport(log, store, export)
}

// fixFBEncoding fixes the UTF-8 mojibake in Facebook exports
// Facebook exports UTF-8 text but escapes each byte as \uXXXX treating it as Latin-1
func fixFBEncoding(s string) string {
	// The string is already decoded from JSON, but the bytes were interpreted as Latin-1
	// We need to convert Latin-1 codepoints back to bytes, then interpret as UTF-8
	bytes := make([]byte, 0, len(s))
	for _, r := range s {
		if r < 256 {
			bytes = append(bytes, byte(r))
		} else {
			// Keep non-Latin-1 characters as-is (shouldn't happen but just in case)
			bytes = append(bytes, []byte(string(r))...)
		}
	}
	return string(bytes)
}

// ============================================================================
// Messenger App Export Format (from Messenger mobile app)
// ============================================================================

// MessengerExport represents the Messenger app export JSON structure
type MessengerExport struct {
	Participants []string           `json:"participants"`
	ThreadName   string             `json:"threadName"`
	Messages     []MessengerMessage `json:"messages"`
}

type MessengerMessage struct {
	SenderName string `json:"senderName"`
	Text       string `json:"text"`
	Timestamp  int64  `json:"timestamp"` // Note: might be seconds or milliseconds
	IsUnsent   bool   `json:"isUnsent"`
	Type       string `json:"type"`
}

func processMessengerZip(log zerolog.Logger, store *storage.Storage, zipPath string) (imported, skipped int) {
	zipReader, err := zip.OpenReader(zipPath)
	if err != nil {
		log.Error().Err(err).Msg("Failed to open ZIP file")
		return 0, 0
	}
	defer zipReader.Close()

	for _, file := range zipReader.File {
		if !strings.HasSuffix(file.Name, ".json") {
			continue
		}

		imp, skip := processMessengerZipFile(log, store, file)
		imported += imp
		skipped += skip
	}

	return
}

func processMessengerZipFile(log zerolog.Logger, store *storage.Storage, file *zip.File) (imported, skipped int) {
	rc, err := file.Open()
	if err != nil {
		log.Warn().Err(err).Str("file", file.Name).Msg("Failed to open file in ZIP")
		return 0, 0
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		log.Warn().Err(err).Str("file", file.Name).Msg("Failed to read file")
		return 0, 0
	}

	var export MessengerExport
	if err := json.Unmarshal(data, &export); err != nil {
		log.Warn().Err(err).Str("file", file.Name).Msg("Failed to parse JSON")
		return 0, 0
	}

	if len(export.Messages) == 0 {
		return 0, 0
	}

	// Convert to unified format
	var messages []UnifiedMessage
	for _, msg := range export.Messages {
		// Messenger app export timestamp might be in seconds or milliseconds
		// If timestamp is too small (before year 2000), assume it's seconds
		ts := msg.Timestamp
		if ts < 946684800000 { // Year 2000 in milliseconds
			ts = ts * 1000
		}
		messages = append(messages, UnifiedMessage{
			SenderName:  msg.SenderName,
			Text:        msg.Text,
			TimestampMs: ts,
			IsUnsent:    msg.IsUnsent,
			SourceType:  msg.Type,
		})
	}

	if len(messages) == 0 {
		return 0, 0
	}

	unified := UnifiedExport{
		Source:       ExportSourceMessenger,
		ThreadName:   export.ThreadName,
		Participants: export.Participants,
		Messages:     messages,
	}

	return processUnifiedExport(log, store, unified)
}

// ============================================================================
// Unified Processing (works with either format after conversion)
// ============================================================================

func processUnifiedExport(log zerolog.Logger, store *storage.Storage, export UnifiedExport) (imported, skipped int) {
	threadName := cleanThreadName(export.ThreadName)

	threadID := export.ThreadIDHint
	if threadID == 0 && threadName != "" {
		if id, ok, err := store.FindUniqueThreadIDByName(threadName); err != nil {
			log.Warn().Err(err).Str("thread", threadName).Msg("Failed to look up thread by name")
		} else if ok {
			threadID = id
		}
	}
	if threadID == 0 {
		threadID = generateThreadID(conversationKey(threadName, export.Participants))
	}

	log.Info().
		Str("source", string(export.Source)).
		Str("thread", threadName).
		Int64("thread_id", threadID).
		Int("messages", len(export.Messages)).
		Msg("Processing conversation")

	// Ensure all participants exist as contacts
	participantIDs := make(map[string]int64)
	for _, name := range normalizeNames(export.Participants) {
		if name == "" {
			continue
		}
		contactID := resolveContactID(store, name)
		participantIDs[name] = contactID

		if !*dryRun {
			if err := store.EnsureContactExistsWithName(contactID, name); err != nil {
				log.Warn().Err(err).Str("name", name).Msg("Failed to ensure contact exists")
			}
		}
	}

	// Ensure thread exists with name
	if !*dryRun {
		if err := store.EnsureThreadExistsWithName(threadID, threadName); err != nil {
			log.Warn().Err(err).Int64("thread", threadID).Msg("Failed to ensure thread exists")
		}
	}

	// Process messages
	for _, msg := range export.Messages {
		if msg.IsUnsent {
			skipped++
			continue
		}
		if msg.Text == "" && len(msg.Attachments) == 0 {
			skipped++
			continue
		}

		// Generate message ID from content hash (for deduplication)
		senderName := strings.TrimSpace(msg.SenderName)
		if senderName == "" {
			skipped++
			continue
		}
		messageID := generateMessageID(threadID, senderName, msg.TimestampMs, msg.Text, msg.Attachments)

		// Get sender ID
		senderID, ok := participantIDs[senderName]
		if !ok {
			senderID = resolveContactID(store, senderName)
			participantIDs[senderName] = senderID
			// Also ensure this sender exists as contact
			if !*dryRun {
				store.EnsureContactExistsWithName(senderID, senderName)
			}
		}

		if *dryRun {
			imported++
			continue
		}

		// Insert message (ON CONFLICT DO NOTHING handles duplicates)
		inserted, err := store.InsertExportedMessage(messageID, threadID, senderID, msg.Text, msg.TimestampMs)
		if err != nil {
			log.Warn().Err(err).Str("id", messageID).Msg("Failed to insert message")
			skipped++
			continue
		}
		if inserted {
			imported++
		} else {
			skipped++
		}

		// Store attachments (if any)
		for _, a := range msg.Attachments {
			if a.URI == "" {
				continue
			}
			attID := generateAttachmentID(messageID, a.URI)
			filename := a.Filename
			if filename == "" {
				filename = filepath.Base(a.URI)
			}
			if err := store.UpsertExportedAttachment(attID, messageID, int64(a.Type), a.URI, filename); err != nil {
				log.Warn().Err(err).Str("msg", messageID).Str("uri", a.URI).Msg("Failed to insert attachment")
			}
		}
	}

	return imported, skipped
}

// generateThreadID creates a deterministic thread ID from the thread name
func generateThreadID(name string) int64 {
	hash := sha256.Sum256([]byte("thread:" + name))
	var id int64
	for i := 0; i < 8; i++ {
		id = (id << 8) | int64(hash[i])
	}
	if id < 0 {
		id = -id
	}
	return id
}

// generateContactID creates a deterministic contact ID from the name
func generateContactID(name string) int64 {
	hash := sha256.Sum256([]byte("contact:" + name))
	var id int64
	for i := 0; i < 8; i++ {
		id = (id << 8) | int64(hash[i])
	}
	if id < 0 {
		id = -id
	}
	return id
}

// generateMessageID creates a deterministic message ID for deduplication.
// We include attachment URIs so media-only messages remain stable across imports.
func generateMessageID(threadID int64, sender string, timestamp int64, text string, attachments []UnifiedAttachment) string {
	var attachmentURIs []string
	for _, a := range attachments {
		if a.URI != "" {
			attachmentURIs = append(attachmentURIs, a.URI)
		}
	}
	sort.Strings(attachmentURIs)

	data := fmt.Sprintf("%d:%s:%d:%s:%s", threadID, sender, timestamp, text, strings.Join(attachmentURIs, "|"))
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:16])
}

func generateAttachmentID(messageID, uri string) string {
	hash := sha256.Sum256([]byte(messageID + ":" + uri))
	return hex.EncodeToString(hash[:16])
}

// cleanThreadName removes the _N suffix from exported thread names
func cleanThreadName(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '_' {
			suffix := name[i+1:]
			allDigits := true
			for _, c := range suffix {
				if c < '0' || c > '9' {
					allDigits = false
					break
				}
			}
			if allDigits && len(suffix) > 0 {
				return name[:i]
			}
		}
	}
	return name
}

func normalizeNames(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	var out []string
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func conversationKey(threadName string, participants []string) string {
	parts := normalizeNames(participants)
	return fmt.Sprintf("thread:%s\nparticipants:%s", strings.TrimSpace(threadName), strings.Join(parts, "|"))
}

func resolveContactID(store *storage.Storage, name string) int64 {
	if name == "" {
		return 0
	}
	if id, ok, err := store.FindUniqueContactIDByName(name); err == nil && ok {
		return id
	}
	return generateContactID(name)
}

func threadIDFromConversationPath(convPath string) (int64, bool) {
	base := filepath.Base(convPath)
	underscore := strings.LastIndex(base, "_")
	if underscore == -1 || underscore == len(base)-1 {
		return 0, false
	}

	suffix := base[underscore+1:]
	id, err := strconv.ParseInt(suffix, 10, 64)
	if err != nil || id == 0 {
		return 0, false
	}
	return id, true
}

func extractFBAttachments(m FBMessage) []UnifiedAttachment {
	var out []UnifiedAttachment

	for _, p := range m.Photos {
		if p.URI != "" {
			out = append(out, UnifiedAttachment{Type: metatable.AttachmentTypeImage, URI: p.URI, Filename: filepath.Base(p.URI)})
		}
	}
	for _, v := range m.Videos {
		if v.URI != "" {
			out = append(out, UnifiedAttachment{Type: metatable.AttachmentTypeVideo, URI: v.URI, Filename: filepath.Base(v.URI)})
		}
	}
	for _, a := range m.AudioFiles {
		if a.URI != "" {
			out = append(out, UnifiedAttachment{Type: metatable.AttachmentTypeAudio, URI: a.URI, Filename: filepath.Base(a.URI)})
		}
	}
	for _, f := range m.Files {
		if f.URI != "" {
			out = append(out, UnifiedAttachment{Type: metatable.AttachmentTypeFile, URI: f.URI, Filename: filepath.Base(f.URI)})
		}
	}
	for _, g := range m.GIFs {
		if g.URI != "" {
			out = append(out, UnifiedAttachment{Type: metatable.AttachmentTypeAnimatedImage, URI: g.URI, Filename: filepath.Base(g.URI)})
		}
	}
	if m.Sticker != nil && m.Sticker.URI != "" {
		out = append(out, UnifiedAttachment{Type: metatable.AttachmentTypeSticker, URI: m.Sticker.URI, Filename: filepath.Base(m.Sticker.URI)})
	}

	return out
}

func isFacebookExportZip(zipPath string) bool {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return false
	}
	defer r.Close()

	for _, f := range r.File {
		name := strings.ToLower(f.Name)
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		base := filepath.Base(name)
		if strings.HasPrefix(base, "message_") {
			// Facebook exports typically contain message_N.json in inbox/e2ee_cutover/etc folders.
			if strings.Contains(name, "/messages/") || strings.Contains(name, "your_facebook_activity/") {
				return true
			}
		}
	}
	return false
}
