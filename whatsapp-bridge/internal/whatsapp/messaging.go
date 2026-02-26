package whatsapp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// extractTextContent returns best-effort text content from a protobuf message.
func extractTextContent(msg *waProto.Message) string {
	if msg == nil {
		return ""
	}

	if text := msg.GetConversation(); text != "" {
		return text
	}
	if extendedText := msg.GetExtendedTextMessage(); extendedText != nil {
		return extendedText.GetText()
	}

	return ""
}

// parseRecipientJID accepts either full JID or bare phone number input.
func parseRecipientJID(recipient string) (types.JID, error) {
	recipient = strings.TrimSpace(recipient)
	if strings.Contains(recipient, "@") {
		jid, err := types.ParseJID(recipient)
		if err != nil {
			return types.JID{}, fmt.Errorf("error parsing JID: %w", err)
		}
		return jid, nil
	}

	return types.JID{User: recipient, Server: "s.whatsapp.net"}, nil
}

// detectMediaTypeAndMime maps a file extension to WhatsApp media and MIME types.
func detectMediaTypeAndMime(mediaPath string) (whatsmeow.MediaType, string) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(mediaPath), "."))
	switch ext {
	case "jpg", "jpeg":
		return whatsmeow.MediaImage, "image/jpeg"
	case "png":
		return whatsmeow.MediaImage, "image/png"
	case "gif":
		return whatsmeow.MediaImage, "image/gif"
	case "webp":
		return whatsmeow.MediaImage, "image/webp"
	case "ogg":
		return whatsmeow.MediaAudio, "audio/ogg; codecs=opus"
	case "mp4":
		return whatsmeow.MediaVideo, "video/mp4"
	case "avi":
		return whatsmeow.MediaVideo, "video/avi"
	case "mov":
		return whatsmeow.MediaVideo, "video/quicktime"
	default:
		return whatsmeow.MediaDocument, "application/octet-stream"
	}
}

// buildMediaMessage builds the outbound media payload for SendMessage.
func buildMediaMessage(resp whatsmeow.UploadResponse, mediaType whatsmeow.MediaType, mimeType, mediaPath, caption string, mediaData []byte) (*waProto.Message, error) {
	msg := &waProto.Message{}

	switch mediaType {
	case whatsmeow.MediaImage:
		msg.ImageMessage = &waProto.ImageMessage{
			Caption:       proto.String(caption),
			Mimetype:      proto.String(mimeType),
			URL:           &resp.URL,
			DirectPath:    &resp.DirectPath,
			MediaKey:      resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    &resp.FileLength,
		}
	case whatsmeow.MediaAudio:
		seconds := uint32(30)
		var waveform []byte

		if strings.Contains(mimeType, "ogg") {
			analyzedSeconds, analyzedWaveform, err := analyzeOggOpus(mediaData)
			if err != nil {
				return nil, fmt.Errorf("failed to analyze Ogg Opus file: %w", err)
			}
			seconds = analyzedSeconds
			waveform = analyzedWaveform
		}

		msg.AudioMessage = &waProto.AudioMessage{
			Mimetype:      proto.String(mimeType),
			URL:           &resp.URL,
			DirectPath:    &resp.DirectPath,
			MediaKey:      resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    &resp.FileLength,
			Seconds:       proto.Uint32(seconds),
			PTT:           proto.Bool(true),
			Waveform:      waveform,
		}
	case whatsmeow.MediaVideo:
		msg.VideoMessage = &waProto.VideoMessage{
			Caption:       proto.String(caption),
			Mimetype:      proto.String(mimeType),
			URL:           &resp.URL,
			DirectPath:    &resp.DirectPath,
			MediaKey:      resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    &resp.FileLength,
		}
	case whatsmeow.MediaDocument:
		msg.DocumentMessage = &waProto.DocumentMessage{
			Title:         proto.String(filepath.Base(mediaPath)),
			Caption:       proto.String(caption),
			Mimetype:      proto.String(mimeType),
			URL:           &resp.URL,
			DirectPath:    &resp.DirectPath,
			MediaKey:      resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    &resp.FileLength,
		}
	default:
		return nil, fmt.Errorf("unsupported media type")
	}

	return msg, nil
}

// SendWhatsAppMessage sends text or media messages through the connected client.
func SendWhatsAppMessage(client *whatsmeow.Client, recipient string, message string, mediaPath string) (bool, string) {
	if !client.IsConnected() {
		return false, "Not connected to WhatsApp"
	}

	recipientJID, err := parseRecipientJID(recipient)
	if err != nil {
		return false, err.Error()
	}

	msg := &waProto.Message{}
	if mediaPath != "" {
		mediaData, err := os.ReadFile(mediaPath)
		if err != nil {
			return false, fmt.Sprintf("Error reading media file: %v", err)
		}

		mediaType, mimeType := detectMediaTypeAndMime(mediaPath)
		resp, err := client.Upload(context.Background(), mediaData, mediaType)
		if err != nil {
			return false, fmt.Sprintf("Error uploading media: %v", err)
		}

		msg, err = buildMediaMessage(resp, mediaType, mimeType, mediaPath, message, mediaData)
		if err != nil {
			return false, err.Error()
		}
	} else {
		msg.Conversation = proto.String(message)
	}

	if _, err := client.SendMessage(context.Background(), recipientJID, msg); err != nil {
		return false, fmt.Sprintf("Error sending message: %v", err)
	}

	return true, fmt.Sprintf("Message sent to %s", recipient)
}

// extractMediaInfo extracts media metadata needed for persistence and download.
func extractMediaInfo(msg *waProto.Message) (mediaType string, filename string, url string, mediaKey []byte, fileSHA256 []byte, fileEncSHA256 []byte, fileLength uint64) {
	if msg == nil {
		return "", "", "", nil, nil, nil, 0
	}

	if img := msg.GetImageMessage(); img != nil {
		return "image", "image_" + time.Now().Format("20060102_150405") + ".jpg",
			img.GetURL(), img.GetMediaKey(), img.GetFileSHA256(), img.GetFileEncSHA256(), img.GetFileLength()
	}
	if vid := msg.GetVideoMessage(); vid != nil {
		return "video", "video_" + time.Now().Format("20060102_150405") + ".mp4",
			vid.GetURL(), vid.GetMediaKey(), vid.GetFileSHA256(), vid.GetFileEncSHA256(), vid.GetFileLength()
	}
	if aud := msg.GetAudioMessage(); aud != nil {
		return "audio", "audio_" + time.Now().Format("20060102_150405") + ".ogg",
			aud.GetURL(), aud.GetMediaKey(), aud.GetFileSHA256(), aud.GetFileEncSHA256(), aud.GetFileLength()
	}
	if doc := msg.GetDocumentMessage(); doc != nil {
		docFilename := doc.GetFileName()
		if docFilename == "" {
			docFilename = "document_" + time.Now().Format("20060102_150405")
		}
		return "document", docFilename,
			doc.GetURL(), doc.GetMediaKey(), doc.GetFileSHA256(), doc.GetFileEncSHA256(), doc.GetFileLength()
	}

	return "", "", "", nil, nil, nil, 0
}
