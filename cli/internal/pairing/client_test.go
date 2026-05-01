package pairing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"termind/internal/identity"
)

// 一个空的 identity,绕开 HOME 逻辑:直接替换 HOME 为 tempdir。
func newTestIdentity(t *testing.T) *identity.Identity {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	id, err := identity.LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestStart_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/pair/start" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}
		var req StartRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.DeviceID == "" || req.PublicKey == "" {
			t.Errorf("request missing device_id/public_key: %+v", req)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(StartResponse{
			PairCode:        "ABC-DEF",
			ChallengeID:     "ch-123",
			ExpiresAt:       "2026-01-01T00:00:00Z",
			PollIntervalSec: 1,
		})
	}))
	defer srv.Close()

	id := newTestIdentity(t)
	c := NewClient(srv.URL, id, "0.0.1-test")
	resp, err := c.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if resp.PairCode != "ABC-DEF" || resp.ChallengeID != "ch-123" {
		t.Fatalf("bad resp: %+v", resp)
	}
}

func TestStart_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "db_down", Message: "try later"})
	}))
	defer srv.Close()

	id := newTestIdentity(t)
	c := NewClient(srv.URL, id, "test")
	_, err := c.Start(context.Background())
	if err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestWaitApproval_PendingThenApproved(t *testing.T) {
	var count atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/pair/poll" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		n := count.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if n < 3 {
			json.NewEncoder(w).Encode(PollResponse{Status: StatusPending})
			return
		}
		json.NewEncoder(w).Encode(PollResponse{Status: StatusApproved, Token: "tok-xyz"})
	}))
	defer srv.Close()

	id := newTestIdentity(t)
	c := NewClient(srv.URL, id, "test")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := c.WaitApproval(ctx, "ch-123", 50*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitApproval: %v", err)
	}
	if res.Status != StatusApproved || res.Token != "tok-xyz" {
		t.Fatalf("bad: %+v", res)
	}
	if count.Load() < 3 {
		t.Fatalf("expected at least 3 polls, got %d", count.Load())
	}
}

func TestWaitApproval_Denied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(PollResponse{Status: StatusDenied, Reason: "operator rejected"})
	}))
	defer srv.Close()

	id := newTestIdentity(t)
	c := NewClient(srv.URL, id, "test")
	res, err := c.WaitApproval(context.Background(), "ch", 10*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitApproval: %v", err)
	}
	if res.Status != StatusDenied {
		t.Fatalf("bad: %+v", res)
	}
}

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
