package pairing

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if s, err := LoadToken(); err != nil || s != "" {
		t.Fatalf("LoadToken on empty: s=%q err=%v", s, err)
	}

	path, err := SaveToken("my-super-token")
	if err != nil {
		t.Fatalf("SaveToken: %v", err)
	}
	if path != filepath.Join(home, ".config", "termind", "token") {
		t.Fatalf("unexpected path: %s", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("token perm = %v, want 0600", mode)
	}

	got, err := LoadToken()
	if err != nil {
		t.Fatal(err)
	}
	if got != "my-super-token" {
		t.Fatalf("got %q, want my-super-token", got)
	}
}

func TestSaveLoadDeviceAuth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := SaveDeviceAuth("device-1", "operator", "dev-token", []string{"operator.write"})
	if err != nil {
		t.Fatalf("SaveDeviceAuth: %v", err)
	}
	if path != filepath.Join(home, ".config", "termind", "device-auth.json") {
		t.Fatalf("unexpected path: %s", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("device auth perm = %v, want 0600", mode)
	}

	got, err := LoadDeviceAuth("device-1", "operator")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected device auth")
	}
	if got.Token != "dev-token" || got.Role != "operator" {
		t.Fatalf("bad auth: %+v", got)
	}
	wantScopes := []string{"operator.read", "operator.write"}
	if len(got.Scopes) != len(wantScopes) {
		t.Fatalf("scopes=%v want %v", got.Scopes, wantScopes)
	}
	for i := range wantScopes {
		if got.Scopes[i] != wantScopes[i] {
			t.Fatalf("scopes=%v want %v", got.Scopes, wantScopes)
		}
	}

	legacy, err := LoadToken()
	if err != nil {
		t.Fatal(err)
	}
	if legacy != "dev-token" {
		t.Fatalf("legacy token=%q", legacy)
	}
}
