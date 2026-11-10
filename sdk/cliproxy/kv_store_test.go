package cliproxy

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/kvstore"
)

func TestServiceApplyKVStoreConfig_LifecycleAndHotReload(t *testing.T) {
	t.Cleanup(func() {
		_ = kvstore.ShutdownLocal()
	})

	dir := t.TempDir()
	authDir := filepath.Join(dir, "auths")
	defaultAuthDBPath := filepath.Join(authDir, config.DefaultKVStorePath)
	dbPath1 := filepath.Join(dir, "kv1.db")
	dbPath2 := filepath.Join(dir, "kv2.db")

	// Unmanaged service (before applyKVStoreConfig / Run) does not open KVStore on applyConfigUpdate.
	unmanagedSvc := &Service{}
	unmanagedSvc.applyConfigUpdate(&config.Config{
		AuthDir: authDir,
	})
	if client, active, errClient := kvstore.CurrentKVClient(); client != nil || active || errClient != nil {
		t.Fatalf("CurrentKVClient(unmanaged applyConfigUpdate) = %v, %v, %v; want nil, false, nil", client, active, errClient)
	}

	// Default configuration (Disabled: false, Path: "") opens <auth-dir>/kv.db.
	svc := &Service{}
	svc.applyKVStoreConfig(&config.Config{
		AuthDir: authDir,
	})
	clientDefault, active, errClient := kvstore.CurrentKVClient()
	if errClient != nil || !active || clientDefault == nil {
		t.Fatalf("CurrentKVClient(default enabled) = %v, %v, %v; want client, true, nil", clientDefault, active, errClient)
	}
	if _, errStat := os.Stat(defaultAuthDBPath); errStat != nil {
		t.Fatalf("expected defaultAuthDBPath %s to exist: %v", defaultAuthDBPath, errStat)
	}

	// Explicit Path overrides AuthDir.
	svc.applyKVStoreConfig(&config.Config{
		AuthDir: authDir,
		KVStore: config.KVStoreConfig{Disabled: false, Path: dbPath1},
	})
	client1, active, errClient := kvstore.CurrentKVClient()
	if errClient != nil || !active || client1 == nil || client1 == clientDefault {
		t.Fatalf("CurrentKVClient(explicit path) = %v, %v, %v; want new client, true, nil", client1, active, errClient)
	}
	if _, errStat := os.Stat(dbPath1); errStat != nil {
		t.Fatalf("expected dbPath1 %s to exist: %v", dbPath1, errStat)
	}

	// Same path reload reuses the store instance.
	svc.applyKVStoreConfig(&config.Config{
		AuthDir: authDir,
		KVStore: config.KVStoreConfig{Disabled: false, Path: dbPath1},
	})
	clientSame, active, errClient := kvstore.CurrentKVClient()
	if errClient != nil || !active || clientSame != client1 {
		t.Fatalf("same path reload did not reuse store: got %v want %v", clientSame, client1)
	}

	// Switching path closes old store and opens new store.
	svc.applyKVStoreConfig(&config.Config{
		KVStore: config.KVStoreConfig{Disabled: false, Path: dbPath2},
	})
	client2, active, errClient := kvstore.CurrentKVClient()
	if errClient != nil || !active || client2 == nil || client2 == client1 {
		t.Fatalf("path switch failed: client=%v active=%v err=%v", client2, active, errClient)
	}

	// Disabling (Disabled: true) closes the local store and falls back to memory mode.
	svc.applyKVStoreConfig(&config.Config{
		KVStore: config.KVStoreConfig{Disabled: true, Path: dbPath2},
	})
	if client, active, errClient := kvstore.CurrentKVClient(); client != nil || active || errClient != nil {
		t.Fatalf("CurrentKVClient(disabled) = %v, %v, %v; want nil, false, nil", client, active, errClient)
	}

	// applyKVStoreConfig(nil) shuts down the local store cleanly.
	svc.applyKVStoreConfig(&config.Config{
		KVStore: config.KVStoreConfig{Disabled: false, Path: dbPath2},
	})
	svc.applyKVStoreConfig(nil)
	if client, active, errClient := kvstore.CurrentKVClient(); client != nil || active || errClient != nil {
		t.Fatalf("CurrentKVClient(nil cfg) = %v, %v, %v; want nil, false, nil", client, active, errClient)
	}

	// applyConfigUpdate hot-reloads KVStore when svc is managed, and Shutdown closes it and resets managed state.
	svc.applyConfigUpdate(&config.Config{
		KVStore: config.KVStoreConfig{Disabled: false, Path: dbPath1},
	})
	if client, active, errClient := kvstore.CurrentKVClient(); errClient != nil || !active || client == nil {
		t.Fatalf("CurrentKVClient(after applyConfigUpdate) = %v, %v, %v; want client, true, nil", client, active, errClient)
	}
	if errShutdown := svc.Shutdown(context.Background()); errShutdown != nil {
		t.Fatalf("Shutdown() error = %v", errShutdown)
	}
	if client, active, errClient := kvstore.CurrentKVClient(); client != nil || active || errClient != nil {
		t.Fatalf("CurrentKVClient(after Shutdown) = %v, %v, %v; want nil, false, nil", client, active, errClient)
	}

	var nilSvc *Service
	nilSvc.applyKVStoreConfig(&config.Config{
		KVStore: config.KVStoreConfig{Disabled: false, Path: dbPath1},
	})
	if errNilShutdown := nilSvc.shutdownKVStore(); errNilShutdown != nil {
		t.Fatalf("nilSvc.shutdownKVStore() error = %v; want nil", errNilShutdown)
	}
}
