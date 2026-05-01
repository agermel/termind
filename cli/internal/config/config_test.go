package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad_MissingReturnsEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.ServerURL != "" {
		t.Fatalf("expected empty ServerURL, got %q", c.ServerURL)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	want := &Config{
		ServerURL: "https://openclaw.example.com",
		PairedAt:  time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
	}
	if err := Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// 权限必须 0600
	p := filepath.Join(home, ".config", "termind", "config.json")
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("perm = %v, want 0600", mode)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.ServerURL != want.ServerURL {
		t.Fatalf("ServerURL = %q, want %q", got.ServerURL, want.ServerURL)
	}
	if !got.PairedAt.Equal(want.PairedAt) {
		t.Fatalf("PairedAt = %v, want %v", got.PairedAt, want.PairedAt)
	}
}

func TestLoad_Corrupted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	p := filepath.Join(home, ".config", "termind", "config.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("expected decode error, got nil")
	}
}
