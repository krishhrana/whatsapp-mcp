package whatsapp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.mau.fi/whatsmeow"
	"whatsapp-client/internal/storage"
)

// MediaDownloader implements whatsmeow.DownloadableMessage.
type MediaDownloader struct {
	URL           string
	DirectPath    string
	MediaKey      []byte
	FileLength    uint64
	FileSHA256    []byte
	FileEncSHA256 []byte
	MediaType     whatsmeow.MediaType
}

func (d *MediaDownloader) GetDirectPath() string {
	return d.DirectPath
}

func (d *MediaDownloader) GetURL() string {
	return d.URL
}

func (d *MediaDownloader) GetMediaKey() []byte {
	return d.MediaKey
}

func (d *MediaDownloader) GetFileLength() uint64 {
	return d.FileLength
}

func (d *MediaDownloader) GetFileSHA256() []byte {
	return d.FileSHA256
}

func (d *MediaDownloader) GetFileEncSHA256() []byte {
	return d.FileEncSHA256
}

func (d *MediaDownloader) GetMediaType() whatsmeow.MediaType {
	return d.MediaType
}

// DownloadMedia fetches message media from WhatsApp and persists it locally.
func DownloadMedia(client *whatsmeow.Client, messageStore *storage.MessageStore, messageID, chatJID string) (bool, string, string, string, error) {
	mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength, err := messageStore.GetMediaInfo(messageID, chatJID)
	if err != nil {
		if mediaType, filename, err = messageStore.GetMessageMediaTypeAndFilename(messageID, chatJID); err != nil {
			return false, "", "", "", fmt.Errorf("failed to find message: %v", err)
		}
	}

	if mediaType == "" {
		return false, "", "", "", fmt.Errorf("not a media message")
	}

	chatDir := filepath.Join("store", strings.ReplaceAll(chatJID, ":", "_"))
	if err := os.MkdirAll(chatDir, 0o755); err != nil {
		return false, "", "", "", fmt.Errorf("failed to create chat directory: %v", err)
	}

	localPath := filepath.Join(chatDir, filename)
	absPath, err := filepath.Abs(localPath)
	if err != nil {
		return false, "", "", "", fmt.Errorf("failed to get absolute path: %v", err)
	}

	if _, err := os.Stat(localPath); err == nil {
		return true, mediaType, filename, absPath, nil
	}

	if url == "" || len(mediaKey) == 0 || len(fileSHA256) == 0 || len(fileEncSHA256) == 0 || fileLength == 0 {
		return false, "", "", "", fmt.Errorf("incomplete media information for download")
	}

	directPath := extractDirectPathFromURL(url)

	var waMediaType whatsmeow.MediaType
	switch mediaType {
	case "image":
		waMediaType = whatsmeow.MediaImage
	case "video":
		waMediaType = whatsmeow.MediaVideo
	case "audio":
		waMediaType = whatsmeow.MediaAudio
	case "document":
		waMediaType = whatsmeow.MediaDocument
	default:
		return false, "", "", "", fmt.Errorf("unsupported media type: %s", mediaType)
	}

	downloader := &MediaDownloader{
		URL:           url,
		DirectPath:    directPath,
		MediaKey:      mediaKey,
		FileLength:    fileLength,
		FileSHA256:    fileSHA256,
		FileEncSHA256: fileEncSHA256,
		MediaType:     waMediaType,
	}

	mediaData, err := client.Download(context.Background(), downloader)
	if err != nil {
		return false, "", "", "", fmt.Errorf("failed to download media: %v", err)
	}

	if err := os.WriteFile(localPath, mediaData, 0o644); err != nil {
		return false, "", "", "", fmt.Errorf("failed to save media file: %v", err)
	}

	fmt.Printf(
		"Successfully downloaded %s media (message_ref=%s, size=%d bytes)\n",
		mediaType,
		obfuscatedMessageRef(messageID),
		len(mediaData),
	)
	return true, mediaType, filename, absPath, nil
}

// extractDirectPathFromURL derives a WhatsApp direct path from media URL.
func extractDirectPathFromURL(url string) string {
	parts := strings.SplitN(url, ".net/", 2)
	if len(parts) < 2 {
		return url
	}

	pathPart := parts[1]
	pathPart = strings.SplitN(pathPart, "?", 2)[0]
	return "/" + pathPart
}
