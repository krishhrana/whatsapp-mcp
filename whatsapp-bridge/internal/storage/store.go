package storage

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"
)

// Message represents a chat message for our client.
type Message struct {
	Time      time.Time
	Sender    string
	Content   string
	IsFromMe  bool
	MediaType string
	Filename  string
}

// MessageStore manages chat/message persistence.
type MessageStore struct {
	db *sql.DB
}

type schemaColumn struct {
	name       string
	definition string
}

// ensureTableColumns adds any missing required columns to an existing table.
func ensureTableColumns(db *sql.DB, table string, required []schemaColumn) error {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return fmt.Errorf("failed to inspect %s schema: %v", table, err)
	}
	defer rows.Close()

	existing := make(map[string]struct{})
	for rows.Next() {
		var cid int
		var name string
		var colType sql.NullString
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("failed to scan %s schema row: %v", table, err)
		}
		existing[name] = struct{}{}
	}

	for _, column := range required {
		if _, ok := existing[column.name]; ok {
			continue
		}
		stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column.name, column.definition)
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("failed to add %s.%s: %v", table, column.name, err)
		}
	}

	return nil
}

// runSchemaMigrations applies compatibility and normalization migrations.
func runSchemaMigrations(db *sql.DB) error {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS sender_id_aliases (
			alias_id TEXT PRIMARY KEY,
			canonical_id TEXT NOT NULL,
			updated_at TIMESTAMP NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_sender_id_aliases_canonical_id ON sender_id_aliases(canonical_id);
	`); err != nil {
		return fmt.Errorf("failed to ensure sender_id_aliases table: %v", err)
	}

	if err := ensureTableColumns(db, "chats", []schemaColumn{
		{name: "jid", definition: "TEXT"},
		{name: "name", definition: "TEXT"},
		{name: "last_message_time", definition: "TIMESTAMP"},
	}); err != nil {
		return err
	}

	if err := ensureTableColumns(db, "messages", []schemaColumn{
		{name: "id", definition: "TEXT"},
		{name: "chat_jid", definition: "TEXT"},
		{name: "sender", definition: "TEXT"},
		{name: "content", definition: "TEXT"},
		{name: "timestamp", definition: "TIMESTAMP"},
		{name: "is_from_me", definition: "BOOLEAN"},
		{name: "media_type", definition: "TEXT"},
		{name: "filename", definition: "TEXT"},
		{name: "url", definition: "TEXT"},
		{name: "media_key", definition: "BLOB"},
		{name: "file_sha256", definition: "BLOB"},
		{name: "file_enc_sha256", definition: "BLOB"},
		{name: "file_length", definition: "INTEGER"},
	}); err != nil {
		return err
	}

	if _, err := db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_chats_last_message_time ON chats(last_message_time DESC);
		CREATE INDEX IF NOT EXISTS idx_messages_chat_timestamp ON messages(chat_jid, timestamp DESC);
		CREATE INDEX IF NOT EXISTS idx_messages_sender_timestamp ON messages(sender, timestamp DESC);
	`); err != nil {
		return fmt.Errorf("failed to ensure performance indexes: %v", err)
	}

	if _, err := db.Exec(`
		UPDATE messages SET sender = SUBSTR(sender, 1, INSTR(sender, '@') - 1)
		WHERE INSTR(sender, '@') > 1
	`); err != nil {
		return fmt.Errorf("failed to normalize messages.sender: %v", err)
	}

	if _, err := db.Exec(`
		INSERT INTO sender_id_aliases(alias_id, canonical_id, updated_at)
		SELECT sender, sender, MAX(timestamp)
		FROM messages
		WHERE sender IS NOT NULL AND sender <> ''
		GROUP BY sender
		ON CONFLICT(alias_id) DO UPDATE SET
			canonical_id = excluded.canonical_id,
			updated_at = CASE
				WHEN excluded.updated_at > sender_id_aliases.updated_at THEN excluded.updated_at
				ELSE sender_id_aliases.updated_at
				END
		`); err != nil {
		return fmt.Errorf("failed to backfill sender_id_aliases: %v", err)
	}

	if _, err := db.Exec(`
		CREATE TEMP TABLE IF NOT EXISTS chat_id_map (
			old_id TEXT PRIMARY KEY,
			new_id TEXT NOT NULL
		);
		DELETE FROM chat_id_map;

		INSERT OR REPLACE INTO chat_id_map(old_id, new_id)
		SELECT source_id,
			CASE
				WHEN source_id LIKE '%@g.us' THEN source_id
				WHEN INSTR(source_id, '@') > 0 THEN COALESCE(
					(SELECT canonical_id FROM sender_id_aliases WHERE alias_id = SUBSTR(source_id, 1, INSTR(source_id, '@') - 1) LIMIT 1),
					SUBSTR(source_id, 1, INSTR(source_id, '@') - 1)
				)
				ELSE COALESCE(
					(SELECT canonical_id FROM sender_id_aliases WHERE alias_id = source_id LIMIT 1),
					source_id
				)
			END AS normalized_id
		FROM (
			SELECT jid AS source_id FROM chats
			UNION
			SELECT chat_jid AS source_id FROM messages
		)
		WHERE source_id IS NOT NULL AND source_id <> '';

		INSERT INTO chats (jid, name, last_message_time)
		SELECT DISTINCT new_id, NULL, NULL
		FROM chat_id_map
		WHERE new_id <> old_id
		ON CONFLICT(jid) DO NOTHING;

		INSERT INTO chats (jid, name, last_message_time)
		SELECT
			map.new_id,
			c.name,
			c.last_message_time
		FROM chats c
		JOIN chat_id_map map ON map.old_id = c.jid
		WHERE map.new_id <> map.old_id
		ON CONFLICT(jid) DO UPDATE SET
			name = CASE
				WHEN chats.name IS NOT NULL AND chats.name <> '' THEN chats.name
				ELSE excluded.name
			END,
			last_message_time = CASE
				WHEN chats.last_message_time IS NULL THEN excluded.last_message_time
				WHEN excluded.last_message_time IS NULL THEN chats.last_message_time
				WHEN excluded.last_message_time > chats.last_message_time THEN excluded.last_message_time
				ELSE chats.last_message_time
			END;

		UPDATE messages
		SET chat_jid = (
			SELECT new_id FROM chat_id_map WHERE old_id = messages.chat_jid
		)
		WHERE EXISTS (
			SELECT 1 FROM chat_id_map WHERE old_id = messages.chat_jid AND new_id <> old_id
		);

		DELETE FROM chats
		WHERE jid IN (
			SELECT old_id FROM chat_id_map WHERE new_id <> old_id
		);

		DROP TABLE IF EXISTS chat_id_map;
	`); err != nil {
		return fmt.Errorf("failed to normalize chats/messages chat IDs: %v", err)
	}

	return nil
}

// NewMessageStore initializes the sqlite store and runs schema migrations.
func NewMessageStore() (*MessageStore, error) {
	if err := os.MkdirAll("store", 0o755); err != nil {
		return nil, fmt.Errorf("failed to create store directory: %v", err)
	}

	db, err := sql.Open("sqlite3", "file:store/messages.db?_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("failed to open message database: %v", err)
	}

	if _, err := db.Exec(`PRAGMA journal_mode=WAL;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to set sqlite journal_mode: %v", err)
	}
	if _, err := db.Exec(`PRAGMA synchronous=NORMAL;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to set sqlite synchronous mode: %v", err)
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to set sqlite busy timeout: %v", err)
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS chats (
			jid TEXT PRIMARY KEY,
			name TEXT,
			last_message_time TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS messages (
			id TEXT,
			chat_jid TEXT,
			sender TEXT,
			content TEXT,
			timestamp TIMESTAMP,
			is_from_me BOOLEAN,
			media_type TEXT,
			filename TEXT,
			url TEXT,
			media_key BLOB,
			file_sha256 BLOB,
			file_enc_sha256 BLOB,
			file_length INTEGER,
			PRIMARY KEY (id, chat_jid),
			FOREIGN KEY (chat_jid) REFERENCES chats(jid)
		);

		CREATE TABLE IF NOT EXISTS sender_id_aliases (
			alias_id TEXT PRIMARY KEY,
			canonical_id TEXT NOT NULL,
			updated_at TIMESTAMP NOT NULL
		);

		CREATE INDEX IF NOT EXISTS idx_sender_id_aliases_canonical_id
		ON sender_id_aliases(canonical_id);
	`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create tables: %v", err)
	}

	if err := runSchemaMigrations(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to run schema migrations: %v", err)
	}

	return &MessageStore{db: db}, nil
}

// Close closes the underlying sqlite connection.
func (store *MessageStore) Close() error {
	return store.db.Close()
}

// Reset deletes all locally cached chat and message data.
func (store *MessageStore) Reset() error {
	if store == nil || store.db == nil {
		return nil
	}

	tx, err := store.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to start reset transaction: %v", err)
	}

	statements := []string{
		"DELETE FROM messages;",
		"DELETE FROM chats;",
		"DELETE FROM sender_id_aliases;",
	}
	for _, stmt := range statements {
		if _, execErr := tx.Exec(stmt); execErr != nil {
			_ = tx.Rollback()
			return fmt.Errorf("failed to reset message store: %v", execErr)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit reset transaction: %v", err)
	}
	return nil
}

// StoreChat upserts chat metadata with its latest message timestamp.
func (store *MessageStore) StoreChat(jid, name string, lastMessageTime time.Time) error {
	_, err := store.db.Exec(
		"INSERT OR REPLACE INTO chats (jid, name, last_message_time) VALUES (?, ?, ?)",
		jid, name, lastMessageTime,
	)
	return err
}

// normalizeSenderID strips server suffixes and surrounding whitespace.
func normalizeSenderID(id string) string {
	normalized := strings.TrimSpace(id)
	if normalized == "" {
		return ""
	}
	if strings.Contains(normalized, "@") {
		return strings.SplitN(normalized, "@", 2)[0]
	}
	return normalized
}

// StoreSenderAliases upserts alias-to-canonical mappings for a sender.
func (store *MessageStore) StoreSenderAliases(canonicalID string, aliases []string, updatedAt time.Time) error {
	canonical := normalizeSenderID(canonicalID)
	if canonical == "" {
		return nil
	}

	unique := map[string]struct{}{canonical: {}}
	for _, alias := range aliases {
		normalized := normalizeSenderID(alias)
		if normalized == "" {
			continue
		}
		unique[normalized] = struct{}{}
	}

	tx, err := store.db.Begin()
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare(`INSERT INTO sender_id_aliases (alias_id, canonical_id, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(alias_id) DO UPDATE SET
		 	canonical_id = excluded.canonical_id,
		 	updated_at = CASE
		 		WHEN excluded.updated_at > sender_id_aliases.updated_at THEN excluded.updated_at
		 		ELSE sender_id_aliases.updated_at
		 	END`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	for alias := range unique {
		if _, err := stmt.Exec(alias, canonical, updatedAt); err != nil {
			tx.Rollback()
			return err
		}
	}

	return tx.Commit()
}

// PromoteCanonicalSender rewrites message sender IDs to their canonical form.
func (store *MessageStore) PromoteCanonicalSender(canonicalID string, aliases []string) error {
	canonical := normalizeSenderID(canonicalID)
	if canonical == "" {
		return nil
	}

	unique := map[string]struct{}{}
	for _, alias := range aliases {
		normalized := normalizeSenderID(alias)
		if normalized == "" || normalized == canonical {
			continue
		}
		unique[normalized] = struct{}{}
	}
	if len(unique) == 0 {
		return nil
	}

	promoteFrom := make([]string, 0, len(unique))
	for alias := range unique {
		promoteFrom = append(promoteFrom, alias)
	}

	args := make([]interface{}, 0, len(promoteFrom)+1)
	args = append(args, canonical)
	placeholders := make([]string, 0, len(promoteFrom))
	for _, alias := range promoteFrom {
		placeholders = append(placeholders, "?")
		args = append(args, alias)
	}

	query := fmt.Sprintf(
		"UPDATE messages SET sender = ? WHERE sender IN (%s)",
		strings.Join(placeholders, ","),
	)
	_, err := store.db.Exec(query, args...)
	return err
}

// PromoteCanonicalChat rewrites chat IDs to a canonical contact ID.
func (store *MessageStore) PromoteCanonicalChat(canonicalID string, aliases []string) error {
	canonical := normalizeSenderID(canonicalID)
	if canonical == "" {
		return nil
	}

	unique := map[string]struct{}{}
	for _, alias := range aliases {
		normalized := normalizeSenderID(alias)
		if normalized == "" || normalized == canonical {
			continue
		}
		unique[normalized] = struct{}{}
	}
	if len(unique) == 0 {
		return nil
	}

	tx, err := store.db.Begin()
	if err != nil {
		return err
	}

	for alias := range unique {
		if _, err := tx.Exec(
			`INSERT INTO chats (jid, name, last_message_time)
			 SELECT ?, name, last_message_time
			 FROM chats
			 WHERE jid = ?
			 ON CONFLICT(jid) DO UPDATE SET
			 	name = CASE
			 		WHEN chats.name IS NOT NULL AND chats.name <> '' THEN chats.name
			 		ELSE excluded.name
			 	END,
			 	last_message_time = CASE
			 		WHEN chats.last_message_time IS NULL THEN excluded.last_message_time
			 		WHEN excluded.last_message_time IS NULL THEN chats.last_message_time
			 		WHEN excluded.last_message_time > chats.last_message_time THEN excluded.last_message_time
			 		ELSE chats.last_message_time
			 	END`,
			canonical, alias,
		); err != nil {
			tx.Rollback()
			return err
		}

		if _, err := tx.Exec(
			"UPDATE messages SET chat_jid = ? WHERE chat_jid = ?",
			canonical, alias,
		); err != nil {
			tx.Rollback()
			return err
		}

		if _, err := tx.Exec("DELETE FROM chats WHERE jid = ?", alias); err != nil {
			tx.Rollback()
			return err
		}
	}

	return tx.Commit()
}

// StoreMessage upserts a message row and media metadata when present.
func (store *MessageStore) StoreMessage(
	id,
	chatJID,
	sender,
	content string,
	timestamp time.Time,
	isFromMe bool,
	mediaType,
	filename,
	url string,
	mediaKey,
	fileSHA256,
	fileEncSHA256 []byte,
	fileLength uint64,
) error {
	if content == "" && mediaType == "" {
		return nil
	}

	_, err := store.db.Exec(
		`INSERT OR REPLACE INTO messages
		(id, chat_jid, sender, content, timestamp, is_from_me, media_type, filename, url, media_key, file_sha256, file_enc_sha256, file_length)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, chatJID, sender, content, timestamp, isFromMe, mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength,
	)
	return err
}

// GetMessages returns recent messages for a chat ordered by timestamp desc.
func (store *MessageStore) GetMessages(chatJID string, limit int) ([]Message, error) {
	rows, err := store.db.Query(
		"SELECT sender, content, timestamp, is_from_me, media_type, filename FROM messages WHERE chat_jid = ? ORDER BY timestamp DESC LIMIT ?",
		chatJID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var msg Message
		var timestamp time.Time
		if err := rows.Scan(&msg.Sender, &msg.Content, &timestamp, &msg.IsFromMe, &msg.MediaType, &msg.Filename); err != nil {
			return nil, err
		}
		msg.Time = timestamp
		messages = append(messages, msg)
	}

	return messages, nil
}

// GetChats returns chats keyed by JID with their latest message timestamp.
func (store *MessageStore) GetChats() (map[string]time.Time, error) {
	rows, err := store.db.Query("SELECT jid, last_message_time FROM chats ORDER BY last_message_time DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	chats := make(map[string]time.Time)
	for rows.Next() {
		var jid string
		var lastMessageTime time.Time
		if err := rows.Scan(&jid, &lastMessageTime); err != nil {
			return nil, err
		}
		chats[jid] = lastMessageTime
	}

	return chats, nil
}

// GetChatName returns a stored display name for the given chat JID.
func (store *MessageStore) GetChatName(jid string) (string, error) {
	var name string
	err := store.db.QueryRow("SELECT name FROM chats WHERE jid = ?", jid).Scan(&name)
	return name, err
}

// StoreMediaInfo updates a stored message row with full media download metadata.
func (store *MessageStore) StoreMediaInfo(id, chatJID, url string, mediaKey, fileSHA256, fileEncSHA256 []byte, fileLength uint64) error {
	_, err := store.db.Exec(
		"UPDATE messages SET url = ?, media_key = ?, file_sha256 = ?, file_enc_sha256 = ?, file_length = ? WHERE id = ? AND chat_jid = ?",
		url, mediaKey, fileSHA256, fileEncSHA256, fileLength, id, chatJID,
	)
	return err
}

// GetMediaInfo returns media metadata required to download message media.
func (store *MessageStore) GetMediaInfo(id, chatJID string) (string, string, string, []byte, []byte, []byte, uint64, error) {
	var mediaType, filename, url string
	var mediaKey, fileSHA256, fileEncSHA256 []byte
	var fileLength uint64

	err := store.db.QueryRow(
		"SELECT media_type, filename, url, media_key, file_sha256, file_enc_sha256, file_length FROM messages WHERE id = ? AND chat_jid = ?",
		id, chatJID,
	).Scan(&mediaType, &filename, &url, &mediaKey, &fileSHA256, &fileEncSHA256, &fileLength)

	return mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength, err
}

// GetMessageMediaTypeAndFilename returns basic media fields for a message row.
func (store *MessageStore) GetMessageMediaTypeAndFilename(id, chatJID string) (string, string, error) {
	var mediaType, filename string
	err := store.db.QueryRow(
		"SELECT media_type, filename FROM messages WHERE id = ? AND chat_jid = ?",
		id, chatJID,
	).Scan(&mediaType, &filename)
	return mediaType, filename, err
}
