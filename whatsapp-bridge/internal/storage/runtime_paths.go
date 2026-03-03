package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	runtimeUserScopeEnv = "WHATSAPP_RUNTIME_USER_SCOPE"
	runtimeECSModeEnv   = "WHATSAPP_RUNTIME_ECS_MODE"
	localDevUserScope   = "local-dev"
)

var uuidScopePattern = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`,
)

// RuntimePaths defines scoped filesystem paths for one WhatsApp runtime user.
type RuntimePaths struct {
	UserScope               string
	PersistentStoreRoot     string
	HotStoreRoot            string
	PersistentMessagesDB    string
	HotMessagesDB           string
	PersistentWhatsAppDB    string
	HotMediaRoot            string
	PersistentUserStorePath string
	HotUserStorePath        string
}

func isTruthyEnv(value string) bool {
	normalized := strings.TrimSpace(strings.ToLower(value))
	switch normalized {
	case "1", "true", "t", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func resolveRuntimeUserScopeFromEnv() (string, error) {
	rawScope := strings.TrimSpace(os.Getenv(runtimeUserScopeEnv))
	ecsMode := isTruthyEnv(os.Getenv(runtimeECSModeEnv))
	if rawScope == "" {
		if ecsMode {
			return "", fmt.Errorf(
				"%s is required when %s=true",
				runtimeUserScopeEnv,
				runtimeECSModeEnv,
			)
		}
		return localDevUserScope, nil
	}
	if !uuidScopePattern.MatchString(rawScope) {
		return "", fmt.Errorf(
			"%s must be a UUID value. Received: %q",
			runtimeUserScopeEnv,
			rawScope,
		)
	}
	return strings.ToLower(rawScope), nil
}

// ResolveRuntimePathsFromEnv computes user-scoped hot and durable store paths.
func ResolveRuntimePathsFromEnv() (RuntimePaths, error) {
	userScope, err := resolveRuntimeUserScopeFromEnv()
	if err != nil {
		return RuntimePaths{}, err
	}

	persistentRoot := strings.TrimSpace(os.Getenv("WHATSAPP_MESSAGE_STORE_PERSISTENT_DIR"))
	if persistentRoot == "" {
		persistentRoot = defaultPersistentStoreDir
	}
	hotRoot := strings.TrimSpace(os.Getenv("WHATSAPP_MESSAGE_STORE_HOT_DIR"))
	if hotRoot == "" {
		hotRoot = defaultHotStoreDir
	}

	persistentUserPath := filepath.Join(persistentRoot, "users", userScope)
	hotUserPath := filepath.Join(hotRoot, "users", userScope)

	return RuntimePaths{
		UserScope:               userScope,
		PersistentStoreRoot:     persistentRoot,
		HotStoreRoot:            hotRoot,
		PersistentMessagesDB:    filepath.Join(persistentUserPath, "messages.db"),
		HotMessagesDB:           filepath.Join(hotUserPath, "messages.db"),
		PersistentWhatsAppDB:    filepath.Join(persistentUserPath, "whatsapp.db"),
		HotMediaRoot:            filepath.Join(hotUserPath, "media"),
		PersistentUserStorePath: persistentUserPath,
		HotUserStorePath:        hotUserPath,
	}, nil
}
