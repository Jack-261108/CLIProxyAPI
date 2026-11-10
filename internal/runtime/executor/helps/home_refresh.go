package helps

import (
	"context"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

// RefreshAuthViaHome is a stub that returns (nil, false, nil) since home control plane integration has been removed.
func RefreshAuthViaHome(ctx context.Context, cfg *config.Config, auth *cliproxyauth.Auth) (*cliproxyauth.Auth, bool, error) {
	return nil, false, nil
}

func authAccessTokenSHA256(auth *cliproxyauth.Auth) string {
	return cliproxyauth.AccessTokenSHA256(auth)
}
