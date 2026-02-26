package bootstrap

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// SetupClient initializes the WhatsApp client and device store.
func SetupClient(logger waLog.Logger) (*whatsmeow.Client, error) {
	dbLog := waLog.Stdout("Database", "INFO", true)
	SetConnecting("Initializing WhatsApp client")

	if err := os.MkdirAll("store", 0o755); err != nil {
		return nil, fmt.Errorf("failed to create store directory: %w", err)
	}

	container, err := sqlstore.New(context.Background(), "sqlite3", "file:store/whatsapp.db?_foreign_keys=on", dbLog)
	if err != nil {
		SetAuthError("Failed to initialize WhatsApp device store")
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil {
		if err == sql.ErrNoRows {
			deviceStore = container.NewDevice()
			logger.Infof("Created new device")
		} else {
			SetAuthError("Failed to load WhatsApp device state")
			return nil, fmt.Errorf("failed to get device: %w", err)
		}
	}

	client := whatsmeow.NewClient(deviceStore, logger)
	if client == nil {
		SetAuthError("Failed to create WhatsApp client")
		return nil, fmt.Errorf("failed to create WhatsApp client")
	}

	return client, nil
}

// ConnectClient establishes a stable WhatsApp connection (QR flow if needed).
func ConnectClient(client *whatsmeow.Client) error {
	SetConnecting("Connecting to WhatsApp")

	// After logout/revoke, Store.Delete() clears Store.ID but leaves session-specific
	// store bindings initialized for the previous JID. Reset initialization so the
	// next successful pair rebinds SQL stores to the new device JID.
	if client.Store != nil && client.Store.ID == nil {
		client.Store.Initialized = false
	}

	if client.Store.ID == nil {
		qrChan, err := client.GetQRChannel(context.Background())
		if err != nil {
			SetAuthError("Failed to initialize WhatsApp QR flow")
			return fmt.Errorf("failed to initialize QR channel: %w", err)
		}
		if err := client.Connect(); err != nil {
			SetAuthError("Failed to connect to WhatsApp")
			return fmt.Errorf("failed to connect: %w", err)
		}

		SetAwaitingQR("", "Waiting for WhatsApp QR code")
		go func() {
			for evt := range qrChan {
				switch evt.Event {
				case "code":
					SetAwaitingQR(evt.Code, "Scan this QR code with WhatsApp")
					fmt.Println("\nWhatsApp QR is ready for UI retrieval via the auth status API.")
				case "success":
					SetLoggingIn("Logging into WhatsApp")
					fmt.Println("\nQR scanned. Logging into WhatsApp...")
				case "timeout":
					SetAuthError("QR code scan timed out")
				default:
					if evt.Event == "error" {
						SetAuthError("WhatsApp login error")
					}
				}
			}
		}()
		return nil
	}

	if err := client.Connect(); err != nil {
		SetAuthError("Failed to connect to WhatsApp")
		return fmt.Errorf("failed to connect: %w", err)
	}

	time.Sleep(2 * time.Second)
	if !client.IsConnected() {
		SetAuthError("Failed to establish stable WhatsApp connection")
		return fmt.Errorf("failed to establish stable connection")
	}

	SetConnected("WhatsApp connected")
	return nil
}
