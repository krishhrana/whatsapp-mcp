package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.mau.fi/whatsmeow"
	waLog "go.mau.fi/whatsmeow/util/log"
	"whatsapp-client/internal/bootstrap"
	"whatsapp-client/internal/storage"
	"whatsapp-client/internal/whatsapp"
)

type SendMessageResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type SendMessageRequest struct {
	Recipient string `json:"recipient"`
	Message   string `json:"message"`
	MediaPath string `json:"media_path,omitempty"`
}

type DownloadMediaRequest struct {
	MessageID string `json:"message_id"`
	ChatJID   string `json:"chat_jid"`
}

type DownloadMediaResponse struct {
	Success  bool   `json:"success"`
	Message  string `json:"message"`
	Filename string `json:"filename,omitempty"`
	Path     string `json:"path,omitempty"`
}

type AuthStatusResponse struct {
	State          string `json:"state"`
	Connected      bool   `json:"connected"`
	Message        string `json:"message,omitempty"`
	QRCode         string `json:"qr_code,omitempty"`
	QRImageDataURL string `json:"qr_image_data_url,omitempty"`
	SyncProgress   int    `json:"sync_progress,omitempty"`
	SyncCurrent    int    `json:"sync_current,omitempty"`
	SyncTotal      int    `json:"sync_total,omitempty"`
	UpdatedAt      string `json:"updated_at"`
}

type DisconnectResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type ConnectResponse struct {
	Success        bool   `json:"success"`
	Message        string `json:"message"`
	State          string `json:"state,omitempty"`
	Connected      bool   `json:"connected,omitempty"`
	QRCode         string `json:"qr_code,omitempty"`
	QRImageDataURL string `json:"qr_image_data_url,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
}

type HealthResponse struct {
	Status    string `json:"status"`
	State     string `json:"state,omitempty"`
	Connected bool   `json:"connected"`
	UpdatedAt string `json:"updated_at"`
}

type bridgeAuthConfig struct {
	jwtSecret              []byte
	audience               string
	issuer                 string
	allowedSubjectPrefixes []string
}

type bridgeJWTClaims struct {
	Scope     string `json:"scope"`
	RuntimeID string `json:"runtime_id,omitempty"`
	jwt.RegisteredClaims
}

// decodeJSONBody parses a bounded JSON payload and rejects unknown fields.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst interface{}) bool {
	defer r.Body.Close()

	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		http.Error(w, "Invalid request format", http.StatusBadRequest)
		return false
	}

	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "Invalid request format", http.StatusBadRequest)
		return false
	}

	return true
}

// writeJSON writes the provided payload with the given HTTP status code.
func writeJSON(w http.ResponseWriter, statusCode int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		fmt.Printf("failed to write JSON response: %v\n", err)
	}
}

// sendHandler handles POST requests for outbound WhatsApp messages.
func sendHandler(runtime *whatsAppRuntime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req SendMessageRequest
		if ok := decodeJSONBody(w, r, &req); !ok {
			return
		}

		if req.Recipient == "" {
			http.Error(w, "Recipient is required", http.StatusBadRequest)
			return
		}
		if req.Message == "" && req.MediaPath == "" {
			http.Error(w, "Message or media path is required", http.StatusBadRequest)
			return
		}

		client := runtime.currentClient()
		if client == nil {
			writeJSON(w, http.StatusServiceUnavailable, SendMessageResponse{
				Success: false,
				Message: "WhatsApp client is not initialized. Start connect first.",
			})
			return
		}

		success, message := whatsapp.SendWhatsAppMessage(client, req.Recipient, req.Message, req.MediaPath)
		statusCode := http.StatusOK
		if !success {
			statusCode = http.StatusInternalServerError
		}

		writeJSON(w, statusCode, SendMessageResponse{Success: success, Message: message})
	}
}

// downloadHandler handles POST requests for message media download.
func downloadHandler(runtime *whatsAppRuntime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req DownloadMediaRequest
		if ok := decodeJSONBody(w, r, &req); !ok {
			return
		}

		if req.MessageID == "" || req.ChatJID == "" {
			http.Error(w, "Message ID and Chat JID are required", http.StatusBadRequest)
			return
		}

		client := runtime.currentClient()
		if client == nil {
			writeJSON(w, http.StatusServiceUnavailable, DownloadMediaResponse{
				Success: false,
				Message: "WhatsApp client is not initialized. Start connect first.",
			})
			return
		}
		messageStore := runtime.currentMessageStore()
		if messageStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, DownloadMediaResponse{
				Success: false,
				Message: "Message store is not initialized. Start connect first.",
			})
			return
		}

		success, mediaType, filename, path, err := whatsapp.DownloadMedia(client, messageStore, req.MessageID, req.ChatJID)
		if !success || err != nil {
			errMsg := "Unknown error"
			if err != nil {
				errMsg = err.Error()
			}
			writeJSON(w, http.StatusInternalServerError, DownloadMediaResponse{
				Success: false,
				Message: fmt.Sprintf("Failed to download media: %s", errMsg),
			})
			return
		}

		writeJSON(w, http.StatusOK, DownloadMediaResponse{
			Success:  true,
			Message:  fmt.Sprintf("Successfully downloaded %s media", mediaType),
			Filename: filename,
			Path:     path,
		})
	}
}

func loadBridgeAuthConfig() (bridgeAuthConfig, error) {
	secret := strings.TrimSpace(os.Getenv("WHATSAPP_BRIDGE_JWT_SECRET"))
	if secret == "" {
		return bridgeAuthConfig{}, errors.New("WHATSAPP_BRIDGE_JWT_SECRET is required for bridge JWT auth")
	}

	audience := strings.TrimSpace(os.Getenv("WHATSAPP_BRIDGE_JWT_AUDIENCE"))
	if audience == "" {
		audience = "whatsapp-bridge"
	}

	issuer := strings.TrimSpace(os.Getenv("WHATSAPP_BRIDGE_JWT_ISSUER"))
	if issuer == "" {
		issuer = "omicron-api"
	}

	allowedSubjectPrefixes := parseAllowedSubjectPrefixes(
		os.Getenv("WHATSAPP_INTERNAL_ALLOWED_SUBJECT_PREFIXES"),
		[]string{"omicron-api:", "whatsapp-session-controller:"},
	)

	return bridgeAuthConfig{
		jwtSecret:              []byte(secret),
		audience:               audience,
		issuer:                 issuer,
		allowedSubjectPrefixes: allowedSubjectPrefixes,
	}, nil
}

func parseAllowedSubjectPrefixes(raw string, defaults []string) []string {
	source := strings.TrimSpace(raw)
	if source == "" {
		return defaults
	}
	parts := strings.FieldsFunc(source, func(r rune) bool { return r == ',' || r == ' ' })
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		normalized := strings.TrimSpace(part)
		if normalized != "" {
			result = append(result, normalized)
		}
	}
	if len(result) == 0 {
		return defaults
	}
	return result
}

func hasAllowedSubjectPrefix(subject string, allowedPrefixes []string) bool {
	normalizedSubject := strings.TrimSpace(subject)
	if normalizedSubject == "" {
		return false
	}
	for _, prefix := range allowedPrefixes {
		normalizedPrefix := strings.TrimSpace(prefix)
		if normalizedPrefix == "" {
			continue
		}
		if strings.HasPrefix(normalizedSubject, normalizedPrefix) {
			return true
		}
	}
	return false
}

func requiredScopeForRoute(method string, path string) (string, bool) {
	switch {
	case method == http.MethodPost && path == "/api/send":
		return "whatsapp:send", true
	case method == http.MethodPost && path == "/api/download":
		return "whatsapp:download", true
	case method == http.MethodPost && path == "/api/connect":
		return "whatsapp:connect", true
	case method == http.MethodGet && path == "/api/auth/status":
		return "whatsapp:status", true
	case method == http.MethodPost && path == "/api/disconnect":
		return "whatsapp:disconnect", true
	case method == http.MethodPost && path == "/api/disconnect/revoke":
		return "whatsapp:disconnect", true
	default:
		return "", false
	}
}

func hasRequiredScope(claimScope string, requiredScope string) bool {
	if requiredScope == "" {
		return false
	}

	for _, scope := range strings.FieldsFunc(claimScope, func(r rune) bool { return r == ',' || r == ' ' }) {
		if scope == requiredScope || scope == "whatsapp:*" {
			return true
		}
	}
	return false
}

func withRequiredBridgeJWTAuth(authConfig bridgeAuthConfig, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
		if len(authHeader) <= len("Bearer ") || !strings.HasPrefix(authHeader, "Bearer ") {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		requiredScope, ok := requiredScopeForRoute(r.Method, r.URL.Path)
		if !ok {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		rawToken := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		claims := &bridgeJWTClaims{}
		parsedToken, err := jwt.ParseWithClaims(
			rawToken,
			claims,
			func(token *jwt.Token) (interface{}, error) {
				if token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
					return nil, fmt.Errorf("unexpected signing algorithm: %s", token.Method.Alg())
				}
				return authConfig.jwtSecret, nil
			},
			jwt.WithAudience(authConfig.audience),
			jwt.WithIssuer(authConfig.issuer),
		)
		if err != nil || !parsedToken.Valid {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		if claims.ExpiresAt == nil || claims.IssuedAt == nil || strings.TrimSpace(claims.Subject) == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		if !hasAllowedSubjectPrefix(claims.Subject, authConfig.allowedSubjectPrefixes) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		if strings.TrimSpace(claims.RuntimeID) == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		if !hasRequiredScope(claims.Scope, requiredScope) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		next(w, r)
	}
}

func connectReady(status bootstrap.AuthStatus) bool {
	switch status.State {
	case "connected", "logging_in", "syncing", "error", "logged_out":
		return true
	case "awaiting_qr":
		return status.QRCode != "" || status.QRImageDataURL != ""
	default:
		return false
	}
}

func autoConnectOnStartup(runtime *whatsAppRuntime) {
	client, err := runtime.ensureClient()
	if err != nil {
		bootstrap.SetDisconnected("WhatsApp startup initialization failed")
		fmt.Printf("WhatsApp startup client init failed: %v\n", err)
		return
	}

	hasLinkedDevice := client.Store != nil && client.Store.ID != nil
	if !hasLinkedDevice {
		bootstrap.SetDisconnected("WhatsApp ready. Call /api/connect for first-time login.")
		fmt.Println("No linked WhatsApp device found. Waiting for explicit /api/connect.")
		return
	}

	if client.IsConnected() {
		bootstrap.SetConnected("WhatsApp connected")
		return
	}

	fmt.Println("Linked WhatsApp device found. Auto-reconnecting on startup...")
	if err := bootstrap.ConnectClient(client); err != nil {
		fmt.Printf("WhatsApp auto-reconnect failed: %v\n", err)
		return
	}

	status := waitForPostConnectStatus(8 * time.Second)
	if client.IsConnected() && status.State != "logging_in" && status.State != "syncing" {
		bootstrap.SetConnected("WhatsApp connected")
	}
}

func waitForPostConnectStatus(timeout time.Duration) bootstrap.AuthStatus {
	deadline := time.Now().Add(timeout)
	last := bootstrap.GetAuthStatus()
	for {
		last = bootstrap.GetAuthStatus()
		if connectReady(last) || time.Now().After(deadline) {
			return last
		}
		time.Sleep(120 * time.Millisecond)
	}
}

// healthHandler returns basic liveness/readiness metadata for orchestration probes.
func healthHandler(runtime *whatsAppRuntime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		status := bootstrap.GetAuthStatus()
		connected := status.Connected
		client := runtime.currentClient()
		if client != nil && client.IsConnected() {
			connected = true
		}

		writeJSON(w, http.StatusOK, HealthResponse{
			Status:    "ok",
			State:     status.State,
			Connected: connected,
			UpdatedAt: status.UpdatedAt.Format(time.RFC3339),
		})
	}
}

// authStatusHandler returns WhatsApp auth state and QR data for first-time login.
func authStatusHandler(runtime *whatsAppRuntime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		client := runtime.currentClient()
		status := bootstrap.GetAuthStatus()
		hasLinkedDevice := client != nil && client.Store != nil && client.Store.ID != nil
		if hasLinkedDevice &&
			client.IsConnected() &&
			(status.State == "connected" || status.State == "disconnected") {
			status.State = "connected"
			status.Connected = true
			if status.Message == "" {
				status.Message = "WhatsApp connected"
			}
		}

		writeJSON(w, http.StatusOK, AuthStatusResponse{
			State:          status.State,
			Connected:      status.Connected,
			Message:        status.Message,
			QRCode:         status.QRCode,
			QRImageDataURL: status.QRImageDataURL,
			SyncProgress:   status.SyncProgress,
			SyncCurrent:    status.SyncCurrent,
			SyncTotal:      status.SyncTotal,
			UpdatedAt:      status.UpdatedAt.Format(time.RFC3339),
		})
	}
}

// disconnectHandler disconnects the current websocket session and releases in-memory runtime state.
func disconnectHandler(runtime *whatsAppRuntime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		client := runtime.detachClient()
		if client == nil {
			writeJSON(w, http.StatusOK, DisconnectResponse{
				Success: true,
				Message: "WhatsApp client is not initialized",
			})
			return
		}

		if client.IsConnected() {
			client.Disconnect()
		}
		bootstrap.SetDisconnected("WhatsApp disconnected")

		writeJSON(w, http.StatusOK, DisconnectResponse{
			Success: true,
			Message: "WhatsApp disconnected",
		})
	}
}

func clearLocalDeviceCredentials(ctx context.Context, client *whatsmeow.Client) error {
	if client == nil || client.Store == nil || client.Store.ID == nil {
		return nil
	}
	return client.Store.Delete(ctx)
}

func removeSQLiteDatabaseArtifacts(dbPath string) error {
	trimmedPath := strings.TrimSpace(dbPath)
	if trimmedPath == "" {
		return nil
	}
	artifacts := []string{
		trimmedPath,
		trimmedPath + "-wal",
		trimmedPath + "-shm",
		trimmedPath + "-journal",
	}
	var failures []string
	for _, artifact := range artifacts {
		if err := os.Remove(artifact); err != nil && !os.IsNotExist(err) {
			failures = append(failures, fmt.Sprintf("%s: %v", artifact, err))
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func clearLocalRuntimeStorage(runtime *whatsAppRuntime) error {
	if runtime == nil {
		return nil
	}

	messageStore := runtime.detachMessageStore()
	if messageStore != nil {
		if err := messageStore.Close(); err != nil {
			return fmt.Errorf("failed to close message store before cleanup: %w", err)
		}
	}

	runtimePaths, err := storage.ResolveRuntimePathsFromEnv()
	if err != nil {
		return fmt.Errorf("failed to resolve runtime storage paths for cleanup: %w", err)
	}

	seen := map[string]struct{}{}
	toDelete := []string{
		runtimePaths.HotMessagesDB,
		runtimePaths.PersistentMessagesDB,
		runtimePaths.PersistentWhatsAppDB,
	}
	for _, dbPath := range toDelete {
		normalized := strings.TrimSpace(dbPath)
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		if err := removeSQLiteDatabaseArtifacts(normalized); err != nil {
			return fmt.Errorf("failed to remove database artifacts for %s: %w", normalized, err)
		}
	}
	return nil
}

// revokeDisconnectHandler revokes the linked device and clears local WhatsApp state.
func revokeDisconnectHandler(runtime *whatsAppRuntime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		client := runtime.detachClient()
		if client == nil {
			var err error
			client, err = runtime.newClient()
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, DisconnectResponse{
					Success: false,
					Message: err.Error(),
				})
				return
			}
		}

		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()

		if client.Store != nil && client.Store.ID != nil {
			if err := client.Logout(ctx); err != nil {
				client.Disconnect()
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
				cleanupErr := clearLocalDeviceCredentials(cleanupCtx, client)
				cleanupCancel()
				if cleanupErr != nil {
					writeJSON(w, http.StatusInternalServerError, DisconnectResponse{
						Success: false,
						Message: fmt.Sprintf(
							"Failed to revoke WhatsApp device (%v) and local cleanup also failed (%v)",
							err,
							cleanupErr,
						),
					})
					return
				}

				if cacheErr := clearLocalRuntimeStorage(runtime); cacheErr != nil {
					writeJSON(w, http.StatusInternalServerError, DisconnectResponse{
						Success: false,
						Message: fmt.Sprintf(
							"Failed to revoke WhatsApp device (%v); local credentials were cleared but local storage cleanup failed (%v)",
							err,
							cacheErr,
						),
					})
					return
				}

				bootstrap.SetLoggedOut("WhatsApp local credentials cleared. Re-authentication is required.")
				writeJSON(w, http.StatusBadGateway, DisconnectResponse{
					Success: false,
					Message: "Failed to revoke WhatsApp device remotely. Local credentials were cleared.",
				})
				return
			}
		} else {
			client.Disconnect()
		}

		if client.IsConnected() {
			client.Disconnect()
		}

		if err := clearLocalRuntimeStorage(runtime); err != nil {
			writeJSON(w, http.StatusInternalServerError, DisconnectResponse{
				Success: false,
				Message: fmt.Sprintf("Failed to clear local WhatsApp data: %v", err),
			})
			return
		}

		bootstrap.SetLoggedOut("WhatsApp revoked and local credentials cleared")
		writeJSON(w, http.StatusOK, DisconnectResponse{
			Success: true,
			Message: "WhatsApp device revoked and local credentials cleared",
		})
	}
}

// connectHandler attempts a reconnect and triggers QR flow for first-time login.
func connectHandler(runtime *whatsAppRuntime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		client, err := runtime.ensureClient()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, ConnectResponse{
				Success: false,
				Message: err.Error(),
			})
			return
		}

		hasLinkedDevice := client.Store != nil && client.Store.ID != nil
		if client.IsConnected() {
			if hasLinkedDevice {
				status := bootstrap.GetAuthStatus()
				writeJSON(w, http.StatusOK, ConnectResponse{
					Success:        true,
					Message:        "WhatsApp already connected",
					State:          status.State,
					Connected:      true,
					QRCode:         status.QRCode,
					QRImageDataURL: status.QRImageDataURL,
					UpdatedAt:      status.UpdatedAt.Format(time.RFC3339),
				})
				return
			}
			client.Disconnect()
		}

		if err := bootstrap.ConnectClient(client); err != nil {
			writeJSON(w, http.StatusInternalServerError, ConnectResponse{
				Success: false,
				Message: err.Error(),
			})
			return
		}

		status := waitForPostConnectStatus(6 * time.Second)
		if client.IsConnected() && status.State != "logging_in" && status.State != "syncing" {
			status.State = "connected"
			status.Connected = true
		}

		writeJSON(w, http.StatusOK, ConnectResponse{
			Success:        true,
			Message:        "WhatsApp connect requested",
			State:          status.State,
			Connected:      status.Connected,
			QRCode:         status.QRCode,
			QRImageDataURL: status.QRImageDataURL,
			UpdatedAt:      status.UpdatedAt.Format(time.RFC3339),
		})
	}
}

// StartRESTServer starts the bridge HTTP API for send and download routes.
// It binds to 127.0.0.1 by default and can be overridden with WHATSAPP_BRIDGE_HOST.
func StartRESTServer(logger waLog.Logger, messageStore *storage.MessageStore, port int) error {
	authConfig, err := loadBridgeAuthConfig()
	if err != nil {
		return err
	}
	runtime := newWhatsAppRuntime(logger, messageStore)
	autoConnectOnStartup(runtime)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler(runtime))
	mux.HandleFunc("/api/send", withRequiredBridgeJWTAuth(authConfig, sendHandler(runtime)))
	mux.HandleFunc("/api/download", withRequiredBridgeJWTAuth(authConfig, downloadHandler(runtime)))
	mux.HandleFunc("/api/connect", withRequiredBridgeJWTAuth(authConfig, connectHandler(runtime)))
	mux.HandleFunc("/api/auth/status", withRequiredBridgeJWTAuth(authConfig, authStatusHandler(runtime)))
	mux.HandleFunc("/api/disconnect", withRequiredBridgeJWTAuth(authConfig, disconnectHandler(runtime)))
	mux.HandleFunc("/api/disconnect/revoke", withRequiredBridgeJWTAuth(authConfig, revokeDisconnectHandler(runtime)))

	host := os.Getenv("WHATSAPP_BRIDGE_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	serverAddr := net.JoinHostPort(host, strconv.Itoa(port))
	server := &http.Server{
		Addr:              serverAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	fmt.Printf("Starting REST API server on %s...\n", serverAddr)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("REST API server error: %v\n", err)
		}
	}()

	return nil
}
