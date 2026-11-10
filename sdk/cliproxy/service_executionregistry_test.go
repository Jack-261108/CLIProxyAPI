package cliproxy

import (
	"context"
	"net/http"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

type blockingServiceCooldownStore struct {
	started chan struct{}
}

func (s *blockingServiceCooldownStore) Load(context.Context) ([]coreauth.CooldownStateRecord, error) {
	return nil, nil
}

func (s *blockingServiceCooldownStore) Save(ctx context.Context, _ []coreauth.CooldownStateRecord) error {
	close(s.started)
	<-ctx.Done()
	return ctx.Err()
}

func TestConfigCommitDoesNotHoldCommitMutexDuringCooldownPersistence(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	auth := &coreauth.Auth{ID: "auth-1", Provider: "xai", Status: coreauth.StatusActive}
	if _, errRegister := manager.Register(coreauth.WithSkipPersist(context.Background()), auth); errRegister != nil {
		t.Fatalf("Register() error = %v", errRegister)
	}
	manager.MarkResult(context.Background(), coreauth.Result{
		AuthID: auth.ID, Provider: auth.Provider, Model: "grok-4", Success: false,
		Error: &coreauth.Error{Message: "rate limited", HTTPStatus: http.StatusTooManyRequests},
	})
	store := &blockingServiceCooldownStore{started: make(chan struct{})}
	manager.SetCooldownStateStore(store)
	service := &Service{cfg: &config.Config{}, coreManager: manager}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	applyDone := make(chan bool, 1)
	go func() {
		applyDone <- service.applyConfigUpdateWithAuthSynthesis(ctx, &config.Config{DisableCooling: true}, false)
	}()
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("old cooldown store persistence did not start")
	}

	commitDone := make(chan struct{})
	go func() {
		service.commitConfigUpdate(&config.Config{})
		close(commitDone)
	}()
	select {
	case <-commitDone:
	case <-time.After(time.Second):
		t.Fatal("config commit mutex remained locked during cooldown persistence")
	}

	cancel()
	select {
	case applied := <-applyDone:
		if applied {
			t.Fatal("config runtime apply succeeded after cooldown persistence cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("config runtime apply did not honor cooldown persistence cancellation")
	}
}
