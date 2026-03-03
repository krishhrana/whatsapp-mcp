package storage

import (
	"path/filepath"
	"testing"
)

func TestResolveRuntimePathsFromEnvDeterministicForUUIDScope(t *testing.T) {
	t.Setenv(runtimeECSModeEnv, "true")
	t.Setenv(runtimeUserScopeEnv, "DA537387-A2E6-4003-93D7-35935DEEC7C9")
	t.Setenv("WHATSAPP_MESSAGE_STORE_PERSISTENT_DIR", "/persist")
	t.Setenv("WHATSAPP_MESSAGE_STORE_HOT_DIR", "/hot")

	paths, err := ResolveRuntimePathsFromEnv()
	if err != nil {
		t.Fatalf("ResolveRuntimePathsFromEnv returned error: %v", err)
	}

	expectedScope := "da537387-a2e6-4003-93d7-35935deec7c9"
	if paths.UserScope != expectedScope {
		t.Fatalf("unexpected scope: got %q want %q", paths.UserScope, expectedScope)
	}

	if paths.PersistentMessagesDB != filepath.Join("/persist", "users", expectedScope, "messages.db") {
		t.Fatalf("unexpected persistent messages path: %q", paths.PersistentMessagesDB)
	}
	if paths.HotMessagesDB != filepath.Join("/hot", "users", expectedScope, "messages.db") {
		t.Fatalf("unexpected hot messages path: %q", paths.HotMessagesDB)
	}
	if paths.PersistentWhatsAppDB != filepath.Join("/persist", "users", expectedScope, "whatsapp.db") {
		t.Fatalf("unexpected persistent whatsapp path: %q", paths.PersistentWhatsAppDB)
	}
	if paths.HotMediaRoot != filepath.Join("/hot", "users", expectedScope, "media") {
		t.Fatalf("unexpected hot media root path: %q", paths.HotMediaRoot)
	}
}

func TestResolveRuntimePathsFromEnvECSRequiresScope(t *testing.T) {
	t.Setenv(runtimeECSModeEnv, "true")
	t.Setenv(runtimeUserScopeEnv, "")

	_, err := ResolveRuntimePathsFromEnv()
	if err == nil {
		t.Fatal("expected error when scope is missing in ECS mode")
	}
}

func TestResolveRuntimePathsFromEnvLocalFallbackScope(t *testing.T) {
	t.Setenv(runtimeECSModeEnv, "false")
	t.Setenv(runtimeUserScopeEnv, "")

	paths, err := ResolveRuntimePathsFromEnv()
	if err != nil {
		t.Fatalf("ResolveRuntimePathsFromEnv returned error: %v", err)
	}

	if paths.UserScope != localDevUserScope {
		t.Fatalf("unexpected local fallback scope: got %q want %q", paths.UserScope, localDevUserScope)
	}
}
