package whatsapp

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"whatsapp-client/internal/bootstrap"
	"whatsapp-client/internal/storage"
)

// syncSenderAliases upserts sender aliases and rewrites old sender IDs.
func syncSenderAliases(store *storage.MessageStore, logger waLog.Logger, canonicalID string, aliases []string, ts time.Time, contextLabel string) {
	if err := store.StoreSenderAliases(canonicalID, aliases, ts); err != nil {
		logger.Warnf("Failed to store %s aliases: %v", contextLabel, err)
	}
	if err := store.PromoteCanonicalSender(canonicalID, aliases); err != nil {
		logger.Warnf("Failed to promote %s IDs: %v", contextLabel, err)
	}
}

// syncChatAliases upserts chat aliases and rewrites old chat IDs.
func syncChatAliases(store *storage.MessageStore, logger waLog.Logger, canonicalID string, aliases []string, ts time.Time, contextLabel string) {
	if err := store.StoreSenderAliases(canonicalID, aliases, ts); err != nil {
		logger.Warnf("Failed to store %s chat aliases: %v", contextLabel, err)
	}
	if err := store.PromoteCanonicalChat(canonicalID, aliases); err != nil {
		logger.Warnf("Failed to promote %s chat IDs: %v", contextLabel, err)
	}
}

// WireEventHandlers attaches WhatsApp event processors for live + history sync.
func WireEventHandlers(client *whatsmeow.Client, messageStore *storage.MessageStore, logger waLog.Logger) {
	client.AddEventHandler(func(evt interface{}) {
		switch v := evt.(type) {
		case *events.Message:
			handleMessage(client, messageStore, v, logger)
		case *events.HistorySync:
			handleHistorySync(client, messageStore, v, logger)
		case *events.Connected:
			logger.Infof("Connected to WhatsApp")
			status := bootstrap.GetAuthStatus()
			if status.State == "awaiting_qr" || status.State == "logging_in" || status.State == "syncing" {
				bootstrap.SetSyncing("Syncing WhatsApp messages", 20, 0, 0)
				go func() {
					// If no history sync payload arrives, avoid staying in syncing forever.
					// Once history sync starts, SyncTotal/SyncCurrent will be populated and
					// completion is driven by handleHistorySync() instead of this fallback.
					time.Sleep(20 * time.Second)
					current := bootstrap.GetAuthStatus()
					if current.State == "syncing" && current.SyncTotal == 0 && current.SyncCurrent == 0 {
						bootstrap.SetConnected("WhatsApp connected")
					}
				}()
			} else {
				bootstrap.SetConnected("WhatsApp connected")
			}
		case *events.LoggedOut:
			logger.Warnf("Device logged out, please scan QR code to log in again")
			bootstrap.SetLoggedOut("WhatsApp logged out, reconnect required")
		}
	})
}

// handleMessage processes live incoming messages and stores them in sqlite.
func handleMessage(client *whatsmeow.Client, messageStore *storage.MessageStore, msg *events.Message, logger waLog.Logger) {
	chatJID := msg.Info.Chat.ToNonAD()
	chatID := canonicalizeChatID(client, chatJID)
	sender := canonicalizeSender(client, msg.Info.Sender, msg.Info.SenderAlt)

	name := getChatName(client, messageStore, chatJID, chatID, nil, sender, logger)
	if err := messageStore.StoreChat(chatID, name, msg.Info.Timestamp); err != nil {
		logger.Warnf("Failed to store chat: %v", err)
	}

	content := extractTextContent(msg.Message)
	mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength := extractMediaInfo(msg.Message)
	if content == "" && mediaType == "" {
		return
	}

	aliasIDs := senderAliasIDs(client, msg.Info.Sender, msg.Info.SenderAlt, sender)
	syncSenderAliases(messageStore, logger, sender, aliasIDs, msg.Info.Timestamp, "sender")

	if chatJID.Server != "g.us" {
		chatAliases := chatAliasIDs(client, chatJID, chatID)
		syncChatAliases(messageStore, logger, chatID, chatAliases, msg.Info.Timestamp, "live")
	}

	err := messageStore.StoreMessage(
		msg.Info.ID,
		chatID,
		sender,
		content,
		msg.Info.Timestamp,
		msg.Info.IsFromMe,
		mediaType,
		filename,
		url,
		mediaKey,
		fileSHA256,
		fileEncSHA256,
		fileLength,
	)
	if err != nil {
		logger.Warnf("Failed to store message: %v", err)
		return
	}

	timestamp := msg.Info.Timestamp.Format("2006-01-02 15:04:05")
	direction := "←"
	if msg.Info.IsFromMe {
		direction = "→"
	}
	messageRef := obfuscatedMessageRef(msg.Info.ID)
	if mediaType != "" {
		logger.Infof(
			"Stored live media message: message_ref=%s direction=%s type=%s ts=%s",
			messageRef,
			direction,
			mediaType,
			timestamp,
		)
	} else if content != "" {
		logger.Infof(
			"Stored live text message: message_ref=%s direction=%s ts=%s",
			messageRef,
			direction,
			timestamp,
		)
	}
}

// getChatName determines the best available chat display name.
func getChatName(client *whatsmeow.Client, messageStore *storage.MessageStore, jid types.JID, chatJID string, conversation interface{}, sender string, logger waLog.Logger) string {
	chatRef := obfuscatedChatRef(chatJID)
	existingName, err := messageStore.GetChatName(chatJID)
	if err == nil && existingName != "" {
		logger.Infof("Using existing chat name: chat_ref=%s", chatRef)
		return existingName
	}

	var name string
	if jid.Server == "g.us" {
		logger.Infof("Resolving group chat name: chat_ref=%s", chatRef)
		if conversation != nil {
			var displayName, convName *string
			v := reflect.ValueOf(conversation)
			if v.Kind() == reflect.Ptr && !v.IsNil() {
				v = v.Elem()
				if displayNameField := v.FieldByName("DisplayName"); displayNameField.IsValid() && displayNameField.Kind() == reflect.Ptr && !displayNameField.IsNil() {
					dn := displayNameField.Elem().String()
					displayName = &dn
				}
				if nameField := v.FieldByName("Name"); nameField.IsValid() && nameField.Kind() == reflect.Ptr && !nameField.IsNil() {
					n := nameField.Elem().String()
					convName = &n
				}
			}
			if displayName != nil && *displayName != "" {
				name = *displayName
			} else if convName != nil && *convName != "" {
				name = *convName
			}
		}

		if name == "" {
			groupInfo, err := client.GetGroupInfo(context.Background(), jid)
			if err == nil && groupInfo.Name != "" {
				name = groupInfo.Name
			} else {
				name = fmt.Sprintf("Group %s", jid.User)
			}
		}
		logger.Infof("Resolved group chat name: chat_ref=%s", chatRef)
		return name
	}

	logger.Infof("Resolving contact chat name: chat_ref=%s", chatRef)
	contact, err := client.Store.Contacts.GetContact(context.Background(), jid)
	if err == nil && contact.FullName != "" {
		name = contact.FullName
	} else if sender != "" {
		name = sender
	} else {
		name = jid.User
	}
	logger.Infof("Resolved contact chat name: chat_ref=%s", chatRef)
	return name
}

// handleHistorySync processes historical conversation snapshots pushed by WhatsApp.
func handleHistorySync(client *whatsmeow.Client, messageStore *storage.MessageStore, historySync *events.HistorySync, logger waLog.Logger) {
	totalConversations := len(historySync.Data.Conversations)
	logger.Infof("Received history sync event with %d conversations", totalConversations)
	if totalConversations > 0 {
		bootstrap.SetSyncing("Syncing WhatsApp messages", 25, 0, totalConversations)
	}

	updateProgress := func(processed int) {
		if totalConversations <= 0 {
			return
		}
		progress := 25 + int(float64(processed)/float64(totalConversations)*70)
		if progress > 95 {
			progress = 95
		}
		bootstrap.SetSyncingProgress(progress, processed, totalConversations)
	}

	syncedCount := 0
	for idx, conversation := range historySync.Data.Conversations {
		processedConversations := idx + 1
		if conversation.ID == nil {
			updateProgress(processedConversations)
			continue
		}

		chatJID := *conversation.ID
		jid, err := types.ParseJID(chatJID)
		if err != nil {
			logger.Warnf("Failed to parse chat JID (chat_ref=%s): %v", obfuscatedChatRef(chatJID), err)
			updateProgress(processedConversations)
			continue
		}

		chatID := canonicalizeChatID(client, jid)
		name := getChatName(client, messageStore, jid, chatID, conversation, "", logger)

		messages := conversation.Messages
		if len(messages) == 0 {
			updateProgress(processedConversations)
			continue
		}

		latestMsg := messages[0]
		if latestMsg == nil || latestMsg.Message == nil {
			updateProgress(processedConversations)
			continue
		}

		timestamp := time.Time{}
		if ts := latestMsg.Message.GetMessageTimestamp(); ts != 0 {
			timestamp = time.Unix(int64(ts), 0)
		} else {
			updateProgress(processedConversations)
			continue
		}

		if err := messageStore.StoreChat(chatID, name, timestamp); err != nil {
			logger.Warnf("Failed to store history chat: %v", err)
		}

		if jid.Server != "g.us" {
			chatAliases := chatAliasIDs(client, jid, chatID)
			syncChatAliases(messageStore, logger, chatID, chatAliases, timestamp, "history")
		}

		for _, msg := range messages {
			if msg == nil || msg.Message == nil {
				continue
			}

			var content string
			if msg.Message.Message != nil {
				if conv := msg.Message.Message.GetConversation(); conv != "" {
					content = conv
				} else if ext := msg.Message.Message.GetExtendedTextMessage(); ext != nil {
					content = ext.GetText()
				}
			}

			var mediaType, filename, url string
			var mediaKey, fileSHA256, fileEncSHA256 []byte
			var fileLength uint64
			if msg.Message.Message != nil {
				mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength = extractMediaInfo(msg.Message.Message)
			}

			if content == "" && mediaType == "" {
				continue
			}

			var senderJID types.JID
			isFromMe := false
			if msg.Message.Key != nil {
				if msg.Message.Key.FromMe != nil {
					isFromMe = *msg.Message.Key.FromMe
				}
				if !isFromMe && msg.Message.Key.Participant != nil && *msg.Message.Key.Participant != "" {
					senderJID = parseSenderJID(*msg.Message.Key.Participant)
				} else if isFromMe {
					if client != nil && client.Store != nil && client.Store.ID != nil {
						senderJID = client.Store.ID.ToNonAD()
					} else {
						senderJID = jid.ToNonAD()
					}
				} else {
					senderJID = jid.ToNonAD()
				}
			} else {
				senderJID = jid.ToNonAD()
			}
			sender := canonicalizeSender(client, senderJID, types.JID{})

			msgID := ""
			if msg.Message.Key != nil && msg.Message.Key.ID != nil {
				msgID = *msg.Message.Key.ID
			}

			timestamp := time.Time{}
			if ts := msg.Message.GetMessageTimestamp(); ts != 0 {
				timestamp = time.Unix(int64(ts), 0)
			} else {
				continue
			}

			aliasIDs := senderAliasIDs(client, senderJID, types.JID{}, sender)
			syncSenderAliases(messageStore, logger, sender, aliasIDs, timestamp, "history sender")

			err = messageStore.StoreMessage(
				msgID,
				chatID,
				sender,
				content,
				timestamp,
				isFromMe,
				mediaType,
				filename,
				url,
				mediaKey,
				fileSHA256,
				fileEncSHA256,
				fileLength,
			)
			if err != nil {
				logger.Warnf("Failed to store history message: %v", err)
				continue
			}

			syncedCount++
			if mediaType != "" {
				logger.Infof("Stored history media message: message_ref=%s type=%s ts=%s",
					obfuscatedMessageRef(msgID), mediaType, timestamp.Format("2006-01-02 15:04:05"))
			} else {
				logger.Infof("Stored history text message: message_ref=%s ts=%s",
					obfuscatedMessageRef(msgID), timestamp.Format("2006-01-02 15:04:05"))
			}
		}

		updateProgress(processedConversations)
	}

	logger.Infof("History sync complete. Stored %d messages.", syncedCount)
	if totalConversations > 0 {
		bootstrap.SetConnected("WhatsApp connected")
	}
}

// requestHistorySync explicitly requests additional history from WhatsApp.
func requestHistorySync(client *whatsmeow.Client) {
	if client == nil {
		fmt.Println("Client is not initialized. Cannot request history sync.")
		return
	}
	if !client.IsConnected() {
		fmt.Println("Client is not connected. Please ensure you are connected to WhatsApp first.")
		return
	}
	if client.Store.ID == nil {
		fmt.Println("Client is not logged in. Please scan the QR code first.")
		return
	}

	historyMsg := client.BuildHistorySyncRequest(nil, 100)
	if historyMsg == nil {
		fmt.Println("Failed to build history sync request.")
		return
	}

	_, err := client.SendMessage(context.Background(), types.JID{Server: "s.whatsapp.net", User: "status"}, historyMsg)
	if err != nil {
		fmt.Printf("Failed to request history sync: %v\n", err)
		return
	}

	fmt.Println("History sync requested. Waiting for server response...")
}
