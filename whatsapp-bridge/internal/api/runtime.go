package api

import (
	"fmt"
	"sync"

	"go.mau.fi/whatsmeow"
	waLog "go.mau.fi/whatsmeow/util/log"
	"whatsapp-client/internal/bootstrap"
	"whatsapp-client/internal/storage"
	"whatsapp-client/internal/whatsapp"
)

type whatsAppRuntime struct {
	mu           sync.RWMutex
	client       *whatsmeow.Client
	logger       waLog.Logger
	messageStore *storage.MessageStore
}

func newWhatsAppRuntime(logger waLog.Logger, messageStore *storage.MessageStore) *whatsAppRuntime {
	return &whatsAppRuntime{
		logger:       logger,
		messageStore: messageStore,
	}
}

func (r *whatsAppRuntime) currentClient() *whatsmeow.Client {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.client
}

func (r *whatsAppRuntime) detachClient() *whatsmeow.Client {
	r.mu.Lock()
	defer r.mu.Unlock()
	client := r.client
	r.client = nil
	return client
}

func (r *whatsAppRuntime) newClient() (*whatsmeow.Client, error) {
	client, err := bootstrap.SetupClient(r.logger)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize WhatsApp client: %w", err)
	}
	whatsapp.WireEventHandlers(client, r.messageStore, r.logger)
	return client, nil
}

func (r *whatsAppRuntime) ensureClient() (*whatsmeow.Client, error) {
	r.mu.RLock()
	existing := r.client
	r.mu.RUnlock()
	if existing != nil {
		return existing, nil
	}

	client, err := r.newClient()
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	// Another request may have initialized while we built this one.
	if r.client != nil {
		if client.IsConnected() {
			client.Disconnect()
		}
		return r.client, nil
	}
	r.client = client
	return client, nil
}
