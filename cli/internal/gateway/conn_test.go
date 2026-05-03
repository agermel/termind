package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"termind/internal/identity"
	"termind/internal/pairing"
)

// mockServer 提供一个最小可用的 OpenClaw Gateway-like WebSocket 端点。
//
// 行为:
//  1. 握手: 推 connect.challenge,等 req/connect,按 onConnect 决定 hello-ok 或错误
//  2. 握手后: 所有入站 req 交给 onMessage;server 可随时 push event
type mockServer struct {
	challengeNonce string
	helloProtocol  int
	onConnect      func(connectParams) *frameError
	onMessage      func(ws *websocket.Conn, raw []byte)
}

func (s *mockServer) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer ws.Close(websocket.StatusNormalClosure, "bye")

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		nonce := s.challengeNonce
		if nonce == "" {
			nonce = "nonce"
		}
		challenge := map[string]any{
			"type":    "event",
			"event":   "connect.challenge",
			"payload": map[string]string{"nonce": nonce},
		}
		if err := ws.Write(ctx, websocket.MessageText, mustJSON(challenge)); err != nil {
			return
		}

		_, connectBytes, err := ws.Read(ctx)
		if err != nil {
			return
		}
		var req frame
		if err := json.Unmarshal(connectBytes, &req); err != nil {
			_ = ws.Write(ctx, websocket.MessageText, mustJSON(connectError("connect-1", "BAD_JSON", "bad connect json")))
			return
		}
		if req.Type != "req" || req.Method != "connect" {
			_ = ws.Write(ctx, websocket.MessageText, mustJSON(connectError(req.ID, "BAD_HANDSHAKE", "first request must be connect")))
			return
		}
		var params connectParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			_ = ws.Write(ctx, websocket.MessageText, mustJSON(connectError(req.ID, "BAD_CONNECT", "bad connect params")))
			return
		}
		if s.onConnect != nil {
			if ferr := s.onConnect(params); ferr != nil {
				_ = ws.Write(ctx, websocket.MessageText, mustJSON(frame{
					Type:  "res",
					ID:    req.ID,
					OK:    false,
					Error: ferr,
				}))
				return
			}
		}
		helloProtocol := s.helloProtocol
		if helloProtocol == 0 {
			helloProtocol = protocolVersion
		}
		hello := helloOK{Type: "hello-ok", Protocol: helloProtocol}
		hello.Server.Version = "test"
		hello.Server.ConnID = "conn-1"
		hello.Auth.DeviceToken = "rotated-device-token"
		hello.Auth.Role = params.Role
		hello.Auth.Scopes = params.Scopes
		if err := ws.Write(ctx, websocket.MessageText, mustJSON(frame{
			Type:    "res",
			ID:      req.ID,
			OK:      true,
			Payload: mustJSONRaw(hello),
		})); err != nil {
			return
		}

		for {
			_, raw, err := ws.Read(ctx)
			if err != nil {
				return
			}
			if s.onMessage != nil {
				s.onMessage(ws, raw)
			}
		}
	}
}

func connectError(id, code, message string) frame {
	return frame{Type: "res", ID: id, OK: false, Error: &frameError{Code: code, Message: message}}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func mustJSONRaw(v any) json.RawMessage { return json.RawMessage(mustJSON(v)) }

func wsURL(srv *httptest.Server) string {
	return "ws://" + strings.TrimPrefix(srv.URL, "http://")
}

func testIdentity(t *testing.T) *identity.Identity {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	id, err := identity.LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestDial_ConnectOK(t *testing.T) {
	id := testIdentity(t)
	var savedRole, savedToken string
	var savedScopes []string
	ms := &mockServer{
		challengeNonce: "nonce-xyz",
		onConnect: func(p connectParams) *frameError {
			if p.Auth.Token != "" || p.Auth.DeviceToken != "tok" {
				return &frameError{Code: "BAD_AUTH", Message: "bad device token auth"}
			}
			if p.Device == nil || p.Device.ID != id.DeviceID() || p.Device.Signature == "" {
				return &frameError{Code: "BAD_DEVICE", Message: "missing device"}
			}
			if p.Client.ID != "cli" || p.Client.Mode != "cli" {
				return &frameError{Code: "BAD_CLIENT", Message: "bad client"}
			}
			if p.Role != "operator" || !reflect.DeepEqual(p.Scopes, pairing.DefaultScopes()) {
				return &frameError{Code: "BAD_ROLE", Message: "bad role"}
			}
			return nil
		},
	}
	srv := httptest.NewServer(ms.handler(t))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := Dial(ctx, DialOptions{
		ServerURL:     wsURL(srv),
		Identity:      id,
		Token:         "tok",
		ClientVersion: "test",
		OnDeviceToken: func(role, token string, scopes []string) {
			savedRole, savedToken, savedScopes = role, token, scopes
		},
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	if savedRole != "operator" || savedToken != "rotated-device-token" {
		t.Fatalf("device token callback role=%q token=%q", savedRole, savedToken)
	}
	if !reflect.DeepEqual(savedScopes, pairing.DefaultScopes()) {
		t.Fatalf("scopes=%v", savedScopes)
	}
}

func TestDial_SharedTokenAndDeviceToken(t *testing.T) {
	id := testIdentity(t)
	ms := &mockServer{
		challengeNonce: "nonce-xyz",
		onConnect: func(p connectParams) *frameError {
			if p.Auth.Token != "gateway-shared" || p.Auth.DeviceToken != "device-token" {
				return &frameError{Code: "BAD_AUTH", Message: "bad gateway/device token auth"}
			}
			if err := pairing.VerifyDeviceDescriptor(id.PublicKey(), p.Device, pairing.DeviceAuthInput{
				Identity:     id,
				Token:        "gateway-shared",
				ClientID:     p.Client.ID,
				ClientMode:   p.Client.Mode,
				Role:         p.Role,
				Scopes:       p.Scopes,
				Platform:     p.Client.Platform,
				DeviceFamily: p.Client.DeviceFamily,
			}); err != nil {
				return &frameError{Code: "BAD_SIGNATURE", Message: err.Error()}
			}
			return nil
		},
	}
	srv := httptest.NewServer(ms.handler(t))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := Dial(ctx, DialOptions{
		ServerURL:     wsURL(srv),
		Identity:      id,
		Token:         "device-token",
		SharedToken:   "gateway-shared",
		ClientVersion: "test",
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
}

func TestDial_BootstrapConnectOK(t *testing.T) {
	id := testIdentity(t)
	ms := &mockServer{
		challengeNonce: "nonce-xyz",
		onConnect: func(p connectParams) *frameError {
			if p.Auth.BootstrapToken != "boot" || p.Auth.Token != "" || p.Auth.DeviceToken != "" {
				return &frameError{Code: "BAD_AUTH", Message: "bad bootstrap auth"}
			}
			if p.Role != "operator" || !reflect.DeepEqual(p.Scopes, pairing.DefaultScopes()) {
				return &frameError{Code: "BAD_ROLE", Message: "bad bootstrap role"}
			}
			if err := pairing.VerifyDeviceDescriptor(id.PublicKey(), p.Device, pairing.DeviceAuthInput{
				Identity:     id,
				Token:        "boot",
				ClientID:     p.Client.ID,
				ClientMode:   p.Client.Mode,
				Role:         p.Role,
				Scopes:       p.Scopes,
				Platform:     p.Client.Platform,
				DeviceFamily: p.Client.DeviceFamily,
			}); err != nil {
				return &frameError{Code: "BAD_SIGNATURE", Message: err.Error()}
			}
			return nil
		},
	}
	srv := httptest.NewServer(ms.handler(t))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := Dial(ctx, DialOptions{
		ServerURL:      wsURL(srv),
		Identity:       id,
		BootstrapToken: "boot",
		ClientVersion:  "test",
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
}

func TestDial_NoTokenCanCreatePairingRequest(t *testing.T) {
	id := testIdentity(t)
	ms := &mockServer{
		challengeNonce: "nonce-xyz",
		onConnect: func(p connectParams) *frameError {
			if p.Auth.Token != "" || p.Auth.BootstrapToken != "" || p.Auth.DeviceToken != "" {
				return &frameError{Code: "BAD_AUTH", Message: "auth should be empty"}
			}
			if p.Role != "operator" || !reflect.DeepEqual(p.Scopes, pairing.DefaultScopes()) {
				return &frameError{Code: "BAD_ROLE", Message: "bad pairing role"}
			}
			if err := pairing.VerifyDeviceDescriptor(id.PublicKey(), p.Device, pairing.DeviceAuthInput{
				Identity:     id,
				Token:        "",
				ClientID:     p.Client.ID,
				ClientMode:   p.Client.Mode,
				Role:         p.Role,
				Scopes:       p.Scopes,
				Platform:     p.Client.Platform,
				DeviceFamily: p.Client.DeviceFamily,
			}); err != nil {
				return &frameError{Code: "BAD_SIGNATURE", Message: err.Error()}
			}
			return &frameError{
				Code:    "PAIRING_REQUIRED",
				Message: "device pairing required",
				Details: mustJSONRaw(map[string]string{
					"code":      "PAIRING_REQUIRED",
					"reason":    "not-paired",
					"requestId": "req-123",
				}),
			}
		},
	}
	srv := httptest.NewServer(ms.handler(t))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := Dial(ctx, DialOptions{
		ServerURL:     wsURL(srv),
		Identity:      id,
		ClientVersion: "test",
	})
	var ce *ConnectError
	if !errors.As(err, &ce) {
		t.Fatalf("want ConnectError, got %T: %v", err, err)
	}
	if !ce.IsPairingRequired() || ce.PairingRequestID() != "req-123" || ce.PairingReason() != "not-paired" {
		t.Fatalf("bad connect error: %+v", ce)
	}
}

func TestDial_ConnectRejected(t *testing.T) {
	ms := &mockServer{
		challengeNonce: "n",
		onConnect: func(connectParams) *frameError {
			return &frameError{Code: "NOPE", Message: "nope"}
		},
	}
	srv := httptest.NewServer(ms.handler(t))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := Dial(ctx, DialOptions{
		ServerURL:     wsURL(srv),
		Identity:      testIdentity(t),
		Token:         "tok",
		ClientVersion: "test",
	})
	if err == nil {
		t.Fatal("expected auth rejection error")
	}
	var ce *ConnectError
	if !errors.As(err, &ce) {
		t.Fatalf("want ConnectError, got %T: %v", err, err)
	}
	if ce.Code != "NOPE" || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("error should contain reason: %v", err)
	}
}

func TestDial_ProtocolMismatch(t *testing.T) {
	ms := &mockServer{challengeNonce: "n", helloProtocol: protocolVersion + 1}
	srv := httptest.NewServer(ms.handler(t))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := Dial(ctx, DialOptions{
		ServerURL:     wsURL(srv),
		Identity:      testIdentity(t),
		Token:         "tok",
		ClientVersion: "test",
	})
	if err == nil {
		t.Fatal("expected protocol mismatch")
	}
	if !strings.Contains(err.Error(), "protocol mismatch") {
		t.Fatalf("error should mention protocol mismatch: %v", err)
	}
}

func TestCall_EchoResult(t *testing.T) {
	ms := &mockServer{
		challengeNonce: "n",
		onMessage: func(ws *websocket.Conn, raw []byte) {
			var req struct {
				ID     string `json:"id"`
				Params struct {
					Value string `json:"value"`
				} `json:"params"`
			}
			_ = json.Unmarshal(raw, &req)
			resp := frame{
				Type:    "res",
				ID:      req.ID,
				OK:      true,
				Payload: mustJSONRaw(map[string]string{"echo": req.Params.Value}),
			}
			_ = ws.Write(context.Background(), websocket.MessageText, mustJSON(resp))
		},
	}
	srv := httptest.NewServer(ms.handler(t))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := Dial(ctx, DialOptions{
		ServerURL: wsURL(srv), Identity: testIdentity(t), Token: "tok", ClientVersion: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	var res struct {
		Echo string `json:"echo"`
	}
	if err := conn.Call(ctx, "diagnose.ping", map[string]string{"value": "hi"}, &res); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if res.Echo != "hi" {
		t.Fatalf("echo=%q", res.Echo)
	}
}

func TestCall_RPCError(t *testing.T) {
	ms := &mockServer{
		challengeNonce: "n",
		onMessage: func(ws *websocket.Conn, raw []byte) {
			var req struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(raw, &req)
			resp := frame{
				Type:  "res",
				ID:    req.ID,
				OK:    false,
				Error: &frameError{Code: "BOOM", Message: "boom"},
			}
			_ = ws.Write(context.Background(), websocket.MessageText, mustJSON(resp))
		},
	}
	srv := httptest.NewServer(ms.handler(t))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := Dial(ctx, DialOptions{
		ServerURL: wsURL(srv), Identity: testIdentity(t), Token: "tok", ClientVersion: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	err = conn.Call(ctx, "x", nil, nil)
	var re *RPCError
	if !errors.As(err, &re) {
		t.Fatalf("want RPCError, got %T: %v", err, err)
	}
	if re.Code != "BOOM" || re.Message != "boom" {
		t.Fatalf("bad rpc error: %+v", re)
	}
}

func TestNotification_Delivered(t *testing.T) {
	ms := &mockServer{
		challengeNonce: "n",
		onMessage: func(ws *websocket.Conn, _ []byte) {
			for i := 0; i < 3; i++ {
				n := map[string]any{
					"type":    "event",
					"event":   "diagnose.token",
					"payload": map[string]int{"seq": i},
				}
				_ = ws.Write(context.Background(), websocket.MessageText, mustJSON(n))
			}
		},
	}
	srv := httptest.NewServer(ms.handler(t))
	defer srv.Close()

	var (
		mu  sync.Mutex
		got []int
	)
	done := make(chan struct{})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := Dial(ctx, DialOptions{
		ServerURL: wsURL(srv), Identity: testIdentity(t), Token: "tok", ClientVersion: "test",
		OnNotify: func(method string, params json.RawMessage) {
			if method != "diagnose.token" {
				return
			}
			var p struct {
				Seq int `json:"seq"`
			}
			_ = json.Unmarshal(params, &p)
			mu.Lock()
			got = append(got, p.Seq)
			if len(got) == 3 {
				close(done)
			}
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	_ = conn.Notify(ctx, "trigger", nil)

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting notifications")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 3 {
		t.Fatalf("got %d notifications, want 3", len(got))
	}
}

func TestCall_AfterServerClose(t *testing.T) {
	ms := &mockServer{
		challengeNonce: "n",
		onMessage: func(ws *websocket.Conn, _ []byte) {
			ws.Close(websocket.StatusNormalClosure, "bye")
		},
	}
	srv := httptest.NewServer(ms.handler(t))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := Dial(ctx, DialOptions{
		ServerURL: wsURL(srv), Identity: testIdentity(t), Token: "tok", ClientVersion: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	_ = conn.Notify(ctx, "trigger", nil)

	select {
	case <-conn.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("conn should have been closed")
	}

	err = conn.Call(ctx, "x", nil, nil)
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("want ErrClosed, got %v", err)
	}
}

func TestNormalizeWSURL(t *testing.T) {
	cases := map[string]string{
		"https://openclaw.example.com":           "wss://openclaw.example.com/v1/gateway",
		"http://localhost:8080":                  "ws://localhost:8080/v1/gateway",
		"wss://openclaw.example.com/custom/path": "wss://openclaw.example.com/custom/path",
		"ws://localhost:8080/":                   "ws://localhost:8080/v1/gateway",
		"127.0.0.1:18789":                        "ws://127.0.0.1:18789/v1/gateway",
		"localhost:18789":                        "ws://localhost:18789/v1/gateway",
		"192.168.1.10:18789":                     "ws://192.168.1.10:18789/v1/gateway",
		"openclaw.example.com":                   "wss://openclaw.example.com/v1/gateway",
	}
	for in, want := range cases {
		if got := NormalizeGatewayURL(in); got != want {
			t.Errorf("NormalizeGatewayURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateSecureWSURL(t *testing.T) {
	cases := map[string]bool{
		"ws://localhost:8080/v1/gateway":          true,
		"ws://127.0.0.1:8080/v1/gateway":          true,
		"ws://192.168.1.10:8080/v1/gateway":       true,
		"ws://openclaw.example.com/v1/gateway":    false,
		"wss://openclaw.example.com/v1/gateway":   true,
		"https://openclaw.example.com/v1/gateway": true,
	}
	for in, wantOK := range cases {
		err := validateSecureWSURL(in)
		if wantOK && err != nil {
			t.Errorf("validateSecureWSURL(%q) unexpected error: %v", in, err)
		}
		if !wantOK && err == nil {
			t.Errorf("validateSecureWSURL(%q) expected error", in)
		}
	}
}
