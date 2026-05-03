package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"termind/internal/identity"
	"termind/internal/pairing"
)

type initPairingServer struct {
	attempts atomic.Int32
}

func (s *initPairingServer) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer ws.Close(websocket.StatusNormalClosure, "bye")

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		if err := ws.Write(ctx, websocket.MessageText, mustJSON(map[string]any{
			"type":    "event",
			"event":   "connect.challenge",
			"payload": map[string]string{"nonce": "nonce"},
		})); err != nil {
			return
		}

		_, connectRaw, err := ws.Read(ctx)
		if err != nil {
			return
		}
		var req struct {
			ID     string          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(connectRaw, &req)
		if req.Method != "connect" {
			t.Errorf("method=%q", req.Method)
			return
		}
		var params struct {
			Auth struct {
				BootstrapToken string `json:"bootstrapToken"`
				Token          string `json:"token"`
				DeviceToken    string `json:"deviceToken"`
			} `json:"auth"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			t.Errorf("connect params: %v", err)
			return
		}
		if params.Auth.BootstrapToken != "boot" || params.Auth.Token != "" || params.Auth.DeviceToken != "" {
			t.Errorf("auth=%+v", params.Auth)
			return
		}

		attempt := s.attempts.Add(1)
		if attempt == 1 {
			_ = ws.Write(ctx, websocket.MessageText, mustJSON(map[string]any{
				"type": "res",
				"id":   req.ID,
				"ok":   false,
				"error": map[string]any{
					"code":    "PAIRING_REQUIRED",
					"message": "device pairing required",
					"details": map[string]string{
						"code":      "PAIRING_REQUIRED",
						"reason":    "not-paired",
						"requestId": "req-123",
					},
				},
			}))
			return
		}

		_ = ws.Write(ctx, websocket.MessageText, mustJSON(map[string]any{
			"type": "res",
			"id":   req.ID,
			"ok":   true,
			"payload": map[string]any{
				"type":     "hello-ok",
				"protocol": 3,
				"server":   map[string]string{"version": "test", "connId": "conn-1"},
				"auth":     map[string]any{"role": "operator", "scopes": pairing.DefaultScopes(), "deviceToken": "device-token"},
			},
		}))
	}
}

func TestWaitForDeviceApproval_PendingThenApproved(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	id, err := identity.LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}

	ms := &initPairingServer{}
	srv := httptest.NewServer(ms.handler(t))
	defer srv.Close()
	serverURL := "ws://" + strings.TrimPrefix(srv.URL, "http://")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	setup := &pairing.SetupCode{URL: serverURL, BootstrapToken: "boot"}
	path, err := waitForDeviceApproval(ctx, setup, id, 5*time.Second)
	if err != nil {
		t.Fatalf("waitForDeviceApproval: %v", err)
	}
	if path == "" {
		t.Fatal("expected device auth path")
	}
	if ms.attempts.Load() != 2 {
		t.Fatalf("attempts=%d want 2", ms.attempts.Load())
	}
	auth, err := pairing.LoadDeviceAuth(id.DeviceID(), "operator")
	if err != nil {
		t.Fatal(err)
	}
	if auth == nil || auth.Token != "device-token" {
		t.Fatalf("auth=%+v", auth)
	}
}

func TestResolveInitSetupCode_FromFlag(t *testing.T) {
	code := base64.RawURLEncoding.EncodeToString([]byte(`{"url":"ws://127.0.0.1:18789/v1/gateway","bootstrapToken":"boot"}`))
	oldCode := initSetupCode
	oldManual := initManualSetupCode
	initSetupCode = code
	initManualSetupCode = false
	t.Cleanup(func() {
		initSetupCode = oldCode
		initManualSetupCode = oldManual
	})

	setup, err := resolveInitSetupCode(context.Background(), initCmd)
	if err != nil {
		t.Fatalf("resolveInitSetupCode: %v", err)
	}
	if setup.URL != "ws://127.0.0.1:18789/v1/gateway" || setup.BootstrapToken != "boot" {
		t.Fatalf("setup=%+v", setup)
	}
}

func TestGenerateLocalOpenClawSetupCode(t *testing.T) {
	dir := t.TempDir()
	code := base64.RawURLEncoding.EncodeToString([]byte(`{"url":"ws://127.0.0.1:18789/v1/gateway","bootstrapToken":"boot"}`))
	openclaw := filepath.Join(dir, "openclaw")
	script := "#!/bin/sh\nif [ \"$1 $2 $3\" = \"config get gateway.port\" ]; then printf '18789\\n'; exit 0; fi\nprintf '%s\\n' " + code + "\n"
	if err := os.WriteFile(openclaw, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	setup, err := generateLocalOpenClawSetupCode(context.Background())
	if err != nil {
		t.Fatalf("generateLocalOpenClawSetupCode: %v", err)
	}
	if setup.URL != "ws://127.0.0.1:18789/v1/gateway" || setup.BootstrapToken != "boot" {
		t.Fatalf("setup=%+v", setup)
	}
}

func TestPrintInitNextSteps_WhenContinuingShell(t *testing.T) {
	var out bytes.Buffer
	printInitNextSteps(&out, true)

	got := out.String()
	if !strings.Contains(got, "正在进入 termind shell") {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "跑 termind shell") {
		t.Fatalf("should not ask user to run shell again: %q", got)
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
