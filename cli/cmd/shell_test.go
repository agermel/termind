package cmd

import (
	"testing"

	"termind/internal/config"
	"termind/internal/identity"
	"termind/internal/pairing"
)

func TestHasCurrentDeviceAuthRequiresDefaultScopes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	id, err := identity.LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pairing.SaveDeviceAuth(id.DeviceID(), pairing.DefaultRole, "tok", []string{"operator.read"}); err != nil {
		t.Fatal(err)
	}

	ready, err := hasCurrentDeviceAuth(&config.Config{ServerURL: "ws://127.0.0.1:18789/v1/gateway", Role: pairing.DefaultRole})
	if err != nil {
		t.Fatalf("hasCurrentDeviceAuth: %v", err)
	}
	if ready {
		t.Fatal("read-only token should not satisfy current default scopes")
	}

	if _, err := pairing.SaveDeviceAuth(id.DeviceID(), pairing.DefaultRole, "tok-default", pairing.DefaultScopes()); err != nil {
		t.Fatal(err)
	}
	ready, err = hasCurrentDeviceAuth(&config.Config{ServerURL: "ws://127.0.0.1:18789/v1/gateway", Role: pairing.DefaultRole})
	if err != nil {
		t.Fatalf("hasCurrentDeviceAuth: %v", err)
	}
	if !ready {
		t.Fatal("default scopes should satisfy current default scope")
	}
}
