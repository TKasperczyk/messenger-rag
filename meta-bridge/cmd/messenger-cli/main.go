package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waConsumerApplication"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"

	"go.mau.fi/mautrix-meta/pkg/messagix"
	"go.mau.fi/mautrix-meta/pkg/messagix/cookies"
	"go.mau.fi/mautrix-meta/pkg/messagix/table"
	metatypes "go.mau.fi/mautrix-meta/pkg/messagix/types"
	"go.mau.fi/mautrix-meta/pkg/storage"
	"go.mau.fi/mautrix-meta/pkg/util"
)

var (
	dbPath       = flag.String("db", "messenger.db", "Path to SQLite database")
	verbose      = flag.Bool("v", false, "Enable verbose logging")
	showStats    = flag.Bool("stats", false, "Show database stats and exit")
	searchTerm   = flag.String("search", "", "Search messages (FTS) and exit")
	fromPerson   = flag.String("from", "", "Get messages from a person (by name) and exit")
	listContacts = flag.Bool("contacts", false, "List all contacts and exit")
	enableE2EE   = flag.Bool("e2ee", true, "Enable E2EE (encrypted messages)")
)

type App struct {
	log        zerolog.Logger
	store      *storage.Storage
	client     *messagix.Client
	e2eeClient *whatsmeow.Client
	e2eeStore  *sqlstore.Container
	waDevice   *store.Device
	verbose    bool
	currentUser int64

	namesMu      sync.RWMutex
	contactNames map[int64]string
	threadNames  map[int64]string
}

func main() {
	flag.Parse()

	// Set up pretty console logging
	logLevel := zerolog.InfoLevel
	if *verbose {
		logLevel = zerolog.DebugLevel
	}
	log := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.Kitchen}).
		With().Timestamp().Logger().Level(logLevel)

	// Open database
	store, err := storage.New(*dbPath)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to open database")
	}
	defer store.Close()

	// Handle stats mode
	if *showStats {
		stats, err := store.GetStats()
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to get stats")
		}
		fmt.Printf("Database Statistics:\n")
		fmt.Printf("  Messages: %d\n", stats.MessageCount)
		fmt.Printf("  Threads:  %d\n", stats.ThreadCount)
		fmt.Printf("  Contacts: %d\n", stats.ContactCount)
		return
	}

	// Handle list contacts mode
	if *listContacts {
		contacts, err := store.ListContacts()
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to list contacts")
		}
		fmt.Printf("Contacts (%d):\n\n", len(contacts))
		for _, c := range contacts {
			fmt.Printf("  [%d] %s\n", c.ID, c.Name)
		}
		return
	}

	// Handle messages from person mode
	if *fromPerson != "" {
		messages, err := store.GetMessagesBySenderName(*fromPerson, 100)
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to get messages")
		}
		fmt.Printf("Messages from '%s' (%d):\n\n", *fromPerson, len(messages))
		for _, m := range messages {
			t := time.UnixMilli(m.TimestampMs)
			threadInfo := ""
			if m.ThreadName != "" {
				threadInfo = fmt.Sprintf(" [%s]", m.ThreadName)
			}
			fmt.Printf("[%s]%s %s: %s\n", t.Format("2006-01-02 15:04"), threadInfo, m.SenderName, util.Truncate(m.Text, 100))
		}
		return
	}

	// Handle FTS search mode
	if *searchTerm != "" {
		messages, err := store.SearchMessages(*searchTerm, 50)
		if err != nil {
			log.Fatal().Err(err).Msg("Search failed")
		}
		fmt.Printf("Found %d messages matching '%s':\n\n", len(messages), *searchTerm)
		for _, m := range messages {
			t := time.UnixMilli(m.TimestampMs)
			senderName := m.SenderName
			if senderName == "" {
				senderName = fmt.Sprintf("User %d", m.SenderID)
			}
			fmt.Printf("[%s] %s: %s\n", t.Format("2006-01-02 15:04"), senderName, util.Truncate(m.Text, 100))
		}
		return
	}

	// Normal mode: connect and sync
	args := flag.Args()
	if len(args) < 1 {
		log.Fatal().Msg("Usage: messenger-cli [options] <cookies.json>")
	}

	// Load cookies from file
	cookieFile, err := os.ReadFile(args[0])
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to read cookies file")
	}

	var c cookies.Cookies
	if err := json.Unmarshal(cookieFile, &c); err != nil {
		log.Fatal().Err(err).Msg("Failed to parse cookies")
	}
	c.Platform = metatypes.Messenger

	log.Info().Str("platform", c.Platform.String()).Str("db", *dbPath).Bool("e2ee", *enableE2EE).Msg("Starting messenger-cli")

	app := &App{
		log:          log,
		store:        store,
		verbose:      *verbose,
		contactNames: make(map[int64]string),
		threadNames:  make(map[int64]string),
	}

	// Initialize E2EE store if enabled
	if *enableE2EE {
		waLogger := waLog.Zerolog(log.With().Str("component", "whatsmeow").Logger())
		app.e2eeStore = sqlstore.NewWithDB(store.GetDB(), "sqlite3", waLogger)
		if err := app.e2eeStore.Upgrade(context.Background()); err != nil {
			log.Fatal().Err(err).Msg("Failed to initialize E2EE store")
		}
	}

	// Create the client
	app.client = messagix.NewClient(&c, log, &messagix.Config{})

	// Set up the event handler
	app.client.SetEventHandler(func(ctx context.Context, evt any) {
		switch e := evt.(type) {
		case *messagix.Event_Ready:
			log.Info().Msg("Connected to Messenger!")
			// Connect E2EE after main connection is ready
			if *enableE2EE {
				go app.connectE2EE(ctx)
			}

		case *messagix.Event_Reconnected:
			log.Info().Msg("Reconnected to Messenger")

		case *messagix.Event_SocketError:
			log.Warn().Err(e.Err).Int("attempts", e.ConnectionAttempts).Msg("Socket error")

		case *messagix.Event_PermanentError:
			log.Error().Err(e.Err).Msg("Permanent error - check your cookies")

		case *messagix.Event_PublishResponse:
			app.handleTable(e.Table)
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Load initial page and authenticate
	log.Info().Msg("Loading messages page...")
	currentUser, initialTable, err := app.client.LoadMessagesPage(ctx)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load messages page")
	}

	app.currentUser = currentUser.GetFBID()
	log.Info().
		Str("name", currentUser.GetName()).
		Int64("id", currentUser.GetFBID()).
		Msg("Logged in as")

	// Save current user as a contact
	if err := store.EnsureContactExists(currentUser.GetFBID()); err != nil {
		log.Warn().Err(err).Msg("Failed to save current user")
	}
	store.SetSyncMetadata("current_user_id", fmt.Sprintf("%d", currentUser.GetFBID()))
	store.SetSyncMetadata("current_user_name", currentUser.GetName())

	// Handle any messages from initial load
	if initialTable != nil {
		app.handleTable(initialTable)
	}

	// Show stats after initial sync
	stats, _ := store.GetStats()
	log.Info().
		Int64("messages", stats.MessageCount).
		Int64("threads", stats.ThreadCount).
		Int64("contacts", stats.ContactCount).
		Msg("Database loaded")

	// Connect to the WebSocket for real-time messages
	log.Info().Msg("Connecting to real-time socket...")
	if err := app.client.Connect(ctx); err != nil {
		log.Fatal().Err(err).Msg("Failed to connect")
	}

	// Wait for interrupt signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Info().Msg("Shutting down...")
	if app.e2eeClient != nil {
		app.e2eeClient.Disconnect()
	}
	app.client.Disconnect()

	// Final stats
	stats, _ = store.GetStats()
	log.Info().
		Int64("messages", stats.MessageCount).
		Int64("threads", stats.ThreadCount).
		Int64("contacts", stats.ContactCount).
		Msg("Final database stats")
}

func (app *App) connectE2EE(ctx context.Context) {
	log := app.log.With().Str("component", "e2ee").Logger()
	ctx = log.WithContext(ctx)

	// Check if we have an existing device
	e2eeMeta, err := app.store.GetE2EEMetadata()
	if err != nil {
		log.Warn().Err(err).Msg("Failed to get E2EE metadata, will create new device")
		e2eeMeta = nil // Ensure we treat this as a new device
	}

	isNewDevice := false
	if e2eeMeta != nil && e2eeMeta.DeviceID != 0 {
		// Try to load existing device
		jid := types.JID{
			User:   strconv.FormatInt(app.currentUser, 10),
			Device: e2eeMeta.DeviceID,
			Server: types.MessengerServer,
		}
		app.waDevice, err = app.e2eeStore.GetDevice(ctx, jid)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to load existing E2EE device")
			app.waDevice = nil
		}
	}

	if app.waDevice == nil {
		log.Info().Msg("Creating new E2EE device")
		app.waDevice = app.e2eeStore.NewDevice()
		isNewDevice = true
	}

	// Set the device on the messagix client
	app.client.SetDevice(app.waDevice)

	// Register if new device
	if isNewDevice {
		log.Info().Msg("Registering E2EE device with Facebook...")
		if err := app.client.RegisterE2EE(ctx, app.currentUser); err != nil {
			log.Error().Err(err).Msg("Failed to register E2EE device")
			return
		}

		// Save the device
		if err := app.waDevice.Save(ctx); err != nil {
			log.Error().Err(err).Msg("Failed to save E2EE device")
			return
		}

		// Save metadata
		if err := app.store.SaveE2EEMetadata(&storage.E2EEMetadata{
			DeviceID:     app.waDevice.ID.Device,
			FacebookUUID: app.waDevice.FacebookUUID,
			Registered:   true,
		}); err != nil {
			log.Warn().Err(err).Msg("Failed to save E2EE metadata")
		}

		log.Info().
			Stringer("jid", app.waDevice.ID).
			Uint16("device_id", app.waDevice.ID.Device).
			Msg("E2EE device registered")
	}

	// Prepare and connect E2EE client
	app.e2eeClient, err = app.client.PrepareE2EEClient()
	if err != nil {
		log.Error().Err(err).Msg("Failed to prepare E2EE client")
		return
	}

	// Set up E2EE event handler
	app.e2eeClient.AddEventHandler(app.handleE2EEEvent)

	// Connect to E2EE socket
	log.Info().Msg("Connecting to E2EE socket...")
	if err := app.e2eeClient.Connect(); err != nil {
		log.Error().Err(err).Msg("Failed to connect to E2EE socket")
		return
	}
}

func (app *App) handleE2EEEvent(rawEvt any) {
	log := app.log.With().Str("component", "e2ee").Logger()

	switch evt := rawEvt.(type) {
	case *events.Connected:
		log.Info().Msg("Connected to E2EE socket!")

	case *events.Disconnected:
		log.Warn().Msg("Disconnected from E2EE socket")

	case *events.FBMessage:
		app.handleE2EEMessage(evt)

	case *events.Receipt:
		if app.verbose {
			log.Debug().
				Str("type", string(evt.Type)).
				Stringer("chat", evt.Chat).
				Int("count", len(evt.MessageIDs)).
				Msg("E2EE receipt")
		}

	case *events.OfflineSyncPreview:
		log.Info().Int("messages", evt.Messages).Msg("E2EE offline sync starting")

	case *events.OfflineSyncCompleted:
		log.Info().Int("count", evt.Count).Msg("E2EE offline sync completed")

	case *events.CATRefreshError:
		log.Warn().Err(evt.Error).Msg("CAT refresh error")

	default:
		if app.verbose {
			log.Debug().Type("type", rawEvt).Msg("Unhandled E2EE event")
		}
	}
}

func (app *App) handleE2EEMessage(evt *events.FBMessage) {
	log := app.log.With().Str("component", "e2ee").Logger()

	// Extract chat/thread ID
	threadID, _ := strconv.ParseInt(evt.Info.Chat.User, 10, 64)
	senderID, _ := strconv.ParseInt(evt.Info.Sender.User, 10, 64)

	// Get message text from the typed message
	var text string
	switch typedMsg := evt.Message.(type) {
	case *waConsumerApplication.ConsumerApplication:
		// Consumer application messages (regular Messenger E2EE)
		content := typedMsg.GetPayload().GetContent()
		if content != nil {
			switch inner := content.GetContent().(type) {
			case *waConsumerApplication.ConsumerApplication_Content_MessageText:
				text = inner.MessageText.GetText()
			case *waConsumerApplication.ConsumerApplication_Content_EditMessage:
				text = inner.EditMessage.GetMessage().GetText()
			}
		}
	default:
		// Other message types (Armadillo, etc.) - just log them for now
		if evt.Message != nil {
			log.Debug().Type("type", evt.Message).Msg("E2EE message type")
		}
	}

	timestamp := evt.Info.Timestamp

	log.Info().
		Int64("thread", threadID).
		Int64("sender", senderID).
		Str("id", evt.Info.ID).
		Time("time", timestamp).
		Str("text", util.Truncate(text, 80)).
		Msg("E2EE MESSAGE")

	// Store in database
	if text != "" {
		// Create a message record
		msg := &table.LSInsertMessage{
			MessageId:   evt.Info.ID,
			ThreadKey:   threadID,
			SenderId:    senderID,
			Text:        text,
			TimestampMs: timestamp.UnixMilli(),
		}
		if err := app.store.InsertMessage(msg); err != nil {
			log.Warn().Err(err).Str("id", evt.Info.ID).Msg("Failed to save E2EE message")
		}
	}
}

func (app *App) handleTable(tbl *table.LSTable) {
	if tbl == nil {
		return
	}

	// Process contacts first (so we have sender info)
	for _, contact := range tbl.LSDeleteThenInsertContact {
		if err := app.store.UpsertContact(contact); err != nil {
			app.log.Warn().Err(err).Int64("id", contact.Id).Msg("Failed to save contact")
		} else {
			if contact.Name != "" {
				app.namesMu.Lock()
				app.contactNames[contact.Id] = contact.Name
				app.namesMu.Unlock()
			}
			if app.verbose {
				app.log.Debug().Int64("id", contact.Id).Str("name", contact.Name).Msg("CONTACT")
			}
		}
	}

	for _, contact := range tbl.LSVerifyContactRowExists {
		if err := app.store.UpsertContactFromVerify(contact); err != nil {
			app.log.Warn().Err(err).Int64("id", contact.ContactId).Msg("Failed to verify contact")
		} else {
			if contact.Name != "" {
				app.namesMu.Lock()
				app.contactNames[contact.ContactId] = contact.Name
				app.namesMu.Unlock()
			}
		}
	}

	// Process threads
	for _, thread := range tbl.LSDeleteThenInsertThread {
		if err := app.store.UpsertThread(thread); err != nil {
			app.log.Warn().Err(err).Int64("id", thread.ThreadKey).Msg("Failed to save thread")
		} else {
			if thread.ThreadName != "" {
				app.namesMu.Lock()
				app.threadNames[thread.ThreadKey] = thread.ThreadName
				app.namesMu.Unlock()
			}
			if app.verbose {
				app.log.Debug().
					Int64("id", thread.ThreadKey).
					Str("name", thread.ThreadName).
					Int64("type", int64(thread.ThreadType)).
					Msg("THREAD")
			}
		}
	}

	for _, thread := range tbl.LSUpdateOrInsertThread {
		if err := app.store.UpsertThreadFromOrInsert(thread); err != nil {
			app.log.Warn().Err(err).Int64("id", thread.ThreadKey).Msg("Failed to upsert thread")
		} else {
			if thread.ThreadName != "" {
				app.namesMu.Lock()
				app.threadNames[thread.ThreadKey] = thread.ThreadName
				app.namesMu.Unlock()
			}
		}
	}

	// Process participants
	for _, p := range tbl.LSAddParticipantIdToGroupThread {
		if err := app.store.AddParticipant(p); err != nil {
			app.log.Warn().Err(err).Int64("thread", p.ThreadKey).Int64("contact", p.ContactId).Msg("Failed to add participant")
		}
	}

	// Process new messages
	for _, msg := range tbl.LSInsertMessage {
		if err := app.store.InsertMessage(msg); err != nil {
			app.log.Warn().Err(err).Str("id", msg.MessageId).Msg("Failed to save message")
		} else {
			app.log.Info().
				Int64("thread", msg.ThreadKey).
				Int64("sender", msg.SenderId).
				Str("id", msg.MessageId).
				Time("time", time.UnixMilli(msg.TimestampMs)).
				Str("text", util.Truncate(msg.Text, 80)).
				Msg("NEW MESSAGE")
		}
	}

	// Process message updates (edits)
	for _, msg := range tbl.LSUpsertMessage {
		if err := app.store.UpsertMessage(msg); err != nil {
			app.log.Warn().Err(err).Str("id", msg.MessageId).Msg("Failed to update message")
		} else {
			app.log.Info().
				Int64("thread", msg.ThreadKey).
				Str("id", msg.MessageId).
				Str("text", util.Truncate(msg.Text, 80)).
				Msg("MESSAGE UPDATE")
		}
	}

	// Process delete-then-insert messages
	for _, msg := range tbl.LSDeleteThenInsertMessage {
		if err := app.store.DeleteThenInsertMessage(msg); err != nil {
			app.log.Warn().Err(err).Str("id", msg.MessageId).Msg("Failed to replace message")
		} else if app.verbose {
			app.log.Debug().
				Int64("thread", msg.ThreadKey).
				Str("id", msg.MessageId).
				Msg("MESSAGE REPLACED")
		}
	}

	// Process deleted messages
	for _, del := range tbl.LSDeleteMessage {
		if err := app.store.DeleteMessage(del.ThreadKey, del.MessageId); err != nil {
			app.log.Warn().Err(err).Str("id", del.MessageId).Msg("Failed to delete message")
		} else {
			app.log.Info().
				Int64("thread", del.ThreadKey).
				Str("id", del.MessageId).
				Msg("MESSAGE DELETED")
		}
	}

	// Process thread snippet updates
	for _, s := range tbl.LSUpdateThreadSnippet {
		if err := app.store.UpdateThreadSnippet(s); err != nil {
			app.log.Warn().Err(err).Int64("thread", s.ThreadKey).Msg("Failed to update thread snippet")
		} else if app.verbose {
			app.log.Debug().Int64("thread", s.ThreadKey).Str("snippet", util.Truncate(s.Snippet, 80)).Msg("THREAD SNIPPET")
		}
	}

	// Process attachments
	for _, a := range tbl.LSInsertAttachment {
		if err := app.store.UpsertAttachment(a); err != nil {
			app.log.Warn().Err(err).Str("msg", a.MessageId).Msg("Failed to save attachment")
		} else if app.verbose {
			app.log.Debug().Str("msg", a.MessageId).Str("url", util.Truncate(a.PlayableUrl, 80)).Msg("ATTACHMENT")
		}
	}

	// Process delivery receipts
	for _, d := range tbl.LSUpdateDeliveryReceipt {
		if err := app.store.UpdateDeliveryReceipt(d); err != nil {
			app.log.Warn().Err(err).Int64("thread", d.ThreadKey).Int64("contact", d.ContactId).Msg("Failed to save delivery receipt")
		} else if app.verbose {
			app.log.Debug().Int64("thread", d.ThreadKey).Int64("contact", d.ContactId).Time("delivered_at", time.UnixMilli(d.DeliveredWatermarkTimestampMs)).Msg("DELIVERY RECEIPT")
		}
	}

	// Process read receipts
	for _, r := range tbl.LSUpdateReadReceipt {
		if err := app.store.UpdateReadReceipt(r); err != nil {
			app.log.Warn().Err(err).Int64("thread", r.ThreadKey).Int64("contact", r.ContactId).Msg("Failed to save read receipt")
		} else if app.verbose {
			app.log.Debug().Int64("thread", r.ThreadKey).Int64("contact", r.ContactId).Time("read_at", time.UnixMilli(r.ReadActionTimestampMs)).Msg("READ RECEIPT")
		}
	}

	// Process reactions
	for _, reaction := range tbl.LSUpsertReaction {
		if err := app.store.UpsertReaction(reaction); err != nil {
			app.log.Warn().Err(err).Str("msg", reaction.MessageId).Msg("Failed to save reaction")
		} else {
			app.log.Info().
				Int64("thread", reaction.ThreadKey).
				Int64("actor", reaction.ActorId).
				Str("message", reaction.MessageId).
				Str("emoji", reaction.Reaction).
				Msg("REACTION")
		}
	}

	for _, reaction := range tbl.LSDeleteReaction {
		if err := app.store.DeleteReaction(reaction); err != nil {
			app.log.Warn().Err(err).Str("msg", reaction.MessageId).Msg("Failed to delete reaction")
		} else if app.verbose {
			app.log.Debug().
				Int64("thread", reaction.ThreadKey).
				Str("message", reaction.MessageId).
				Msg("REACTION DELETED")
		}
	}

	// Log typing indicators (not stored, just for real-time awareness)
	if app.verbose {
		for _, typing := range tbl.LSUpdateTypingIndicator {
			action := "stopped typing"
			if typing.IsTyping {
				action = "is typing"
			}
			app.log.Debug().
				Int64("thread", typing.ThreadKey).
				Int64("sender", typing.SenderId).
				Str("action", action).
				Msg("TYPING")
		}
	}
}
