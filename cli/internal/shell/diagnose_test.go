package shell

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"termind/internal/config"
	"termind/internal/identity"
	"termind/internal/pairing"
)

func TestEnsureConnectedReconnectsClosedGateway(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var connects atomic.Int32
	srv := httptest.NewServer(testGatewayHandler(t, &connects))
	defer srv.Close()

	id, err := identity.LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Save(&config.Config{ServerURL: testWSURL(srv), Role: pairing.DefaultRole}); err != nil {
		t.Fatal(err)
	}
	if _, err := pairing.SaveDeviceAuth(id.DeviceID(), pairing.DefaultRole, "tok", pairing.DefaultScopes()); err != nil {
		t.Fatal(err)
	}

	var out, stderr bytes.Buffer
	d := newDispatcher(context.Background(), &out, &stderr, "zsh", nil)
	defer d.Close()
	if d.conn == nil || d.dc == nil {
		t.Fatalf("dispatcher offline, stderr=%q", stderr.String())
	}
	firstConn := d.conn
	firstClient := d.dc

	if err := firstConn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstConn.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("first connection did not close")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dc, err := d.ensureConnected(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if dc == nil || dc == firstClient {
		t.Fatalf("client was not replaced")
	}
	if d.conn == nil || d.conn == firstConn {
		t.Fatalf("connection was not replaced")
	}
	if got := connects.Load(); got < 2 {
		t.Fatalf("connects=%d, want at least 2", got)
	}
}

func testGatewayHandler(t *testing.T, connects *atomic.Int32) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		connects.Add(1)
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer ws.Close(websocket.StatusNormalClosure, "bye")

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		challenge := map[string]any{
			"type":    "event",
			"event":   "connect.challenge",
			"payload": map[string]string{"nonce": "nonce"},
		}
		if err := ws.Write(ctx, websocket.MessageText, mustTestJSON(challenge)); err != nil {
			return
		}

		_, raw, err := ws.Read(ctx)
		if err != nil {
			return
		}
		var req struct {
			Type   string          `json:"type"`
			ID     string          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Errorf("connect request json: %v", err)
			return
		}
		if req.Type != "req" || req.Method != "connect" || req.ID == "" {
			t.Errorf("bad connect request: %+v", req)
			return
		}
		var params struct {
			Role string `json:"role"`
			Auth struct {
				DeviceToken string `json:"deviceToken"`
			} `json:"auth"`
			Device struct {
				ID        string `json:"id"`
				PublicKey string `json:"publicKey"`
				Signature string `json:"signature"`
				Nonce     string `json:"nonce"`
			} `json:"device"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			t.Errorf("connect params json: %v", err)
			return
		}
		if params.Role != pairing.DefaultRole || params.Auth.DeviceToken != "tok" || params.Device.ID == "" || params.Device.PublicKey == "" || params.Device.Signature == "" || params.Device.Nonce != "nonce" {
			t.Errorf("bad connect params: %+v", params)
			return
		}

		hello := map[string]any{
			"type":     "hello-ok",
			"protocol": 3,
			"server": map[string]string{
				"version": "test",
				"connId":  "conn-test",
			},
			"auth": map[string]any{
				"deviceToken": "tok",
				"role":        pairing.DefaultRole,
				"scopes":      pairing.DefaultScopes(),
			},
		}
		res := map[string]any{
			"type":    "res",
			"id":      req.ID,
			"ok":      true,
			"payload": hello,
		}
		if err := ws.Write(ctx, websocket.MessageText, mustTestJSON(res)); err != nil {
			return
		}

		for {
			if _, _, err := ws.Read(ctx); err != nil {
				return
			}
		}
	}
}

func testWSURL(srv *httptest.Server) string {
	return "ws://" + strings.TrimPrefix(srv.URL, "http://")
}

func mustTestJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
