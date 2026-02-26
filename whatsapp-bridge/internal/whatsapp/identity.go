package whatsapp

import (
	"context"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

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

// parseSenderJID parses a sender string into a non-AD JID.
func parseSenderJID(sender string) types.JID {
	sender = strings.TrimSpace(sender)
	if sender == "" {
		return types.JID{}
	}

	if strings.Contains(sender, "@") {
		parsed, err := types.ParseJID(sender)
		if err == nil {
			return parsed.ToNonAD()
		}
	}

	return types.NewJID(sender, types.DefaultUserServer)
}

// senderAliasIDs builds a deduplicated list of known aliases for a sender.
func senderAliasIDs(client *whatsmeow.Client, senderJID types.JID, senderAlt types.JID, canonicalID string) []string {
	ids := map[string]struct{}{}
	add := func(id string) {
		normalized := normalizeSenderID(id)
		if normalized != "" {
			ids[normalized] = struct{}{}
		}
	}

	senderJID = senderJID.ToNonAD()
	senderAlt = senderAlt.ToNonAD()

	add(canonicalID)
	add(senderJID.User)
	add(senderAlt.User)

	if client != nil && client.Store != nil && client.Store.LIDs != nil {
		if senderJID.Server == types.HiddenUserServer {
			if pn, err := client.Store.LIDs.GetPNForLID(context.Background(), senderJID); err == nil && !pn.IsEmpty() {
				add(pn.User)
			}
		} else if senderJID.Server == types.DefaultUserServer {
			if lid, err := client.Store.LIDs.GetLIDForPN(context.Background(), senderJID); err == nil && !lid.IsEmpty() {
				add(lid.User)
			}
		}

		if senderAlt.Server == types.HiddenUserServer {
			if pn, err := client.Store.LIDs.GetPNForLID(context.Background(), senderAlt); err == nil && !pn.IsEmpty() {
				add(pn.User)
			}
		} else if senderAlt.Server == types.DefaultUserServer {
			if lid, err := client.Store.LIDs.GetLIDForPN(context.Background(), senderAlt); err == nil && !lid.IsEmpty() {
				add(lid.User)
			}
		}
	}

	aliases := make([]string, 0, len(ids))
	for id := range ids {
		aliases = append(aliases, id)
	}
	return aliases
}

// canonicalizeSender resolves a sender into a canonical personal identifier.
func canonicalizeSender(client *whatsmeow.Client, senderJID types.JID, senderAlt types.JID) string {
	senderJID = senderJID.ToNonAD()
	senderAlt = senderAlt.ToNonAD()

	if senderJID.IsEmpty() {
		return ""
	}

	canonical := senderJID
	if !senderAlt.IsEmpty() && senderAlt.Server == types.DefaultUserServer {
		canonical = senderAlt
	}

	if canonical.Server == types.HiddenUserServer && client != nil && client.Store != nil && client.Store.LIDs != nil {
		if pn, err := client.Store.LIDs.GetPNForLID(context.Background(), canonical); err == nil && !pn.IsEmpty() {
			canonical = pn.ToNonAD()
		}
	}

	if canonical.User != "" {
		return canonical.User
	}

	return senderJID.User
}

// canonicalizeChatID resolves personal chat IDs to canonical sender IDs.
func canonicalizeChatID(client *whatsmeow.Client, chatJID types.JID) string {
	normalized := chatJID.ToNonAD()
	if normalized.IsEmpty() {
		return ""
	}
	if normalized.Server == "g.us" {
		return normalized.String()
	}
	return canonicalizeSender(client, normalized, types.JID{})
}

// chatAliasIDs returns aliases used for non-group chat ID normalization.
func chatAliasIDs(client *whatsmeow.Client, chatJID types.JID, canonicalChatID string) []string {
	normalized := chatJID.ToNonAD()
	if normalized.IsEmpty() || normalized.Server == "g.us" {
		return nil
	}
	return senderAliasIDs(client, normalized, types.JID{}, canonicalChatID)
}
