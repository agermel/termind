package cmd

import (
	"context"
	"fmt"
	"time"

	"termind/internal/config"
	"termind/internal/diagnose"
	"termind/internal/gateway"
	"termind/internal/identity"
	"termind/internal/pairing"
)

func initLarkDiagnoseClient(ctx context.Context, openClawGatewayURL string) (*gateway.Conn, *diagnose.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}
	id, err := identity.LoadOrCreate()
	if err != nil {
		return nil, nil, fmt.Errorf("load identity: %w", err)
	}
	role := cfg.Role
	if role == "" {
		role = pairing.DefaultRole
	}
	auth, err := pairing.LoadDeviceAuth(id.DeviceID(), role)
	if err != nil {
		return nil, nil, fmt.Errorf("load device auth: %w", err)
	}
	token := ""
	scopes := []string(nil)
	if auth != nil {
		token = auth.Token
		scopes = auth.Scopes
	}
	if token == "" {
		legacyToken, err := pairing.LoadToken()
		if err != nil {
			return nil, nil, fmt.Errorf("load token: %w", err)
		}
		token = legacyToken
	}
	if token == "" {
		return nil, nil, fmt.Errorf("OpenClaw device token not found")
	}
	if role == pairing.DefaultRole && len(scopes) == 0 {
		scopes = pairing.DefaultScopes()
	}
	if role == pairing.DefaultRole && !pairing.HasScopes(scopes, pairing.DefaultScopes()) {
		return nil, nil, fmt.Errorf("OpenClaw device token has insufficient scopes: got %v, need %v; run termind init again", scopes, pairing.DefaultScopes())
	}
	serverURL := gateway.NormalizeGatewayURL(openClawGatewayURL)
	if serverURL == "" {
		serverURL = cfg.ServerURL
	}
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := gateway.Dial(dialCtx, gateway.DialOptions{
		ServerURL:     serverURL,
		Identity:      id,
		Token:         token,
		Role:          role,
		Scopes:        scopes,
		ClientVersion: Version,
		OnDeviceToken: func(role, deviceToken string, scopes []string) {
			_, _ = pairing.SaveDeviceAuth(id.DeviceID(), role, deviceToken, scopes)
		},
	})
	if err != nil {
		return nil, nil, err
	}
	return conn, diagnose.NewClient(conn), nil
}
