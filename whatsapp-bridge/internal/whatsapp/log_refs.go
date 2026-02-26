package whatsapp

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// obfuscatedRef returns a stable, non-reversible short reference for logs.
func obfuscatedRef(prefix string, raw string) string {
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" {
		return prefix + "_unknown"
	}

	digest := sha256.Sum256([]byte(cleaned))
	return prefix + "_" + hex.EncodeToString(digest[:6])
}

func obfuscatedChatRef(chatID string) string {
	return obfuscatedRef("chat", chatID)
}

func obfuscatedMessageRef(messageID string) string {
	return obfuscatedRef("msg", messageID)
}
