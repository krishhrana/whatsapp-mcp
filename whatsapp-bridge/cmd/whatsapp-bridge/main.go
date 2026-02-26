package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
	waLog "go.mau.fi/whatsmeow/util/log"
	"whatsapp-client/internal/api"
	"whatsapp-client/internal/bootstrap"
	"whatsapp-client/internal/storage"
)

func loadDotenvFile() {
	candidates := []string{".env"}
	if executablePath, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(executablePath), ".env"))
	}

	for _, path := range candidates {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if err := godotenv.Load(path); err != nil {
			fmt.Printf("Warning: failed to load %s: %v\n", path, err)
		}
		return
	}
}

func bridgePortFromEnv() int {
	const defaultPort = 8080
	rawPort := strings.TrimSpace(os.Getenv("WHATSAPP_BRIDGE_PORT"))
	if rawPort == "" {
		return defaultPort
	}
	parsedPort, err := strconv.Atoi(rawPort)
	if err != nil || parsedPort <= 0 {
		fmt.Printf("Warning: invalid WHATSAPP_BRIDGE_PORT=%q, using %d\n", rawPort, defaultPort)
		return defaultPort
	}
	return parsedPort
}

func main() {
	loadDotenvFile()

	logger := waLog.Stdout("Client", "INFO", true)
	logger.Infof("Starting WhatsApp bridge...")

	messageStore, err := storage.NewMessageStore()
	if err != nil {
		logger.Errorf("Failed to initialize message store: %v", err)
		return
	}
	defer messageStore.Close()

	bootstrap.SetDisconnected("Initializing WhatsApp bridge")
	if err := api.StartRESTServer(logger, messageStore, bridgePortFromEnv()); err != nil {
		logger.Errorf("Failed to start REST server: %v", err)
		return
	}

	exitChan := make(chan os.Signal, 1)
	signal.Notify(exitChan, syscall.SIGINT, syscall.SIGTERM)

	fmt.Println("REST server is running. The bridge auto-reconnects on startup when a linked device exists.")
	fmt.Println("For first-time login (no linked device), trigger /api/connect to start QR flow.")
	fmt.Println("Press Ctrl+C to disconnect and exit.")
	<-exitChan

	fmt.Println("Shutting down...")
}
