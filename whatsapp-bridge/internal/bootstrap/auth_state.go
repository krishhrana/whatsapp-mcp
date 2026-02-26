package bootstrap

import (
	"encoding/base64"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

type AuthStatus struct {
	State          string    `json:"state"`
	Connected      bool      `json:"connected"`
	Message        string    `json:"message,omitempty"`
	QRCode         string    `json:"qr_code,omitempty"`
	QRImageDataURL string    `json:"qr_image_data_url,omitempty"`
	SyncProgress   int       `json:"sync_progress,omitempty"`
	SyncCurrent    int       `json:"sync_current,omitempty"`
	SyncTotal      int       `json:"sync_total,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

var authStatusState = struct {
	mu     sync.RWMutex
	status AuthStatus
}{
	status: AuthStatus{State: "disconnected", Connected: false, UpdatedAt: time.Now().UTC()},
}

func GetAuthStatus() AuthStatus {
	authStatusState.mu.RLock()
	defer authStatusState.mu.RUnlock()
	return authStatusState.status
}

func setAuthStatus(status AuthStatus) {
	status.UpdatedAt = time.Now().UTC()
	authStatusState.mu.Lock()
	authStatusState.status = status
	authStatusState.mu.Unlock()
}

func clampProgress(progress int) int {
	switch {
	case progress < 0:
		return 0
	case progress > 100:
		return 100
	default:
		return progress
	}
}

func SetConnecting(message string) {
	setAuthStatus(AuthStatus{
		State:     "connecting",
		Connected: false,
		Message:   message,
	})
}

func SetAwaitingQR(qrCode string, message string) {
	qrImageDataURL := ""
	if qrCode != "" {
		if pngBytes, err := qrcode.Encode(qrCode, qrcode.Medium, 256); err == nil {
			qrImageDataURL = "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)
		}
	}

	setAuthStatus(AuthStatus{
		State:          "awaiting_qr",
		Connected:      false,
		Message:        message,
		QRCode:         qrCode,
		QRImageDataURL: qrImageDataURL,
	})
}

func SetConnected(message string) {
	setAuthStatus(AuthStatus{
		State:        "connected",
		Connected:    true,
		Message:      message,
		SyncProgress: 100,
	})
}

func SetDisconnected(message string) {
	setAuthStatus(AuthStatus{
		State:     "disconnected",
		Connected: false,
		Message:   message,
	})
}

func SetLoggedOut(message string) {
	setAuthStatus(AuthStatus{
		State:     "logged_out",
		Connected: false,
		Message:   message,
	})
}

func SetAuthError(message string) {
	setAuthStatus(AuthStatus{
		State:     "error",
		Connected: false,
		Message:   message,
	})
}

func SetLoggingIn(message string) {
	setAuthStatus(AuthStatus{
		State:        "logging_in",
		Connected:    false,
		Message:      message,
		SyncProgress: 10,
	})
}

func SetSyncing(message string, progress int, current int, total int) {
	setAuthStatus(AuthStatus{
		State:        "syncing",
		Connected:    false,
		Message:      message,
		SyncProgress: clampProgress(progress),
		SyncCurrent:  current,
		SyncTotal:    total,
	})
}

func SetSyncingProgress(progress int, current int, total int) {
	status := GetAuthStatus()
	if status.State != "syncing" {
		status.State = "syncing"
		status.Connected = false
		if status.Message == "" {
			status.Message = "Syncing WhatsApp messages"
		}
	}
	status.SyncProgress = clampProgress(progress)
	status.SyncCurrent = current
	status.SyncTotal = total
	setAuthStatus(status)
}
