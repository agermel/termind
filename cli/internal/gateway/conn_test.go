package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"termind/internal/identity"
	"termind/internal/pairing"
)

// mockServer 提供一个最小可用的 openclaw-like WebSocket 端点。
//
// 行为:
//  1. 握手: 发一帧 challenge,等 auth,按 onAuth 决定发 auth_ok 还是 auth_fail
//  2. 握手后: 所有入站消息交给 onMessage;server 可随时 push notification
type mockServer struct {
	challengeNonce string
	// onAuth 返回 "" 表示通过,返回非空当作拒绝 reason
	onAuth func(pairing.AuthMessage) string
	// onMessage 收到 client 的 rpc 帧时调用;允许直接写回
	onMessage func(ws *websocket.Conn, raw []byte)
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

		// 1) 发 challenge
		ch := pairing.ChallengeMessage{Type: pairing.MsgTypeChallenge, Nonce: s.challengeNonce, Realm: "test"}
		chBytes, _ := json.Marshal(ch)
		if err := ws.Write(ctx, websocket.MessageText, chBytes); err != nil {
			return
		}

		// 2) 读 auth
		_, authBytes, err := ws.Read(ctx)
		if err != nil {
			return
		}
		var auth pairing.AuthMessage
		if err := json.Unmarshal(authBytes, &auth); err != nil {
			_ = ws.Write(ctx, websocket.MessageText, mustJSON(pairing.AuthResultMessage{Type: pairing.MsgTypeAuthFail, Reason: "bad auth json"}))
			return
		}
		reason := ""
		if s.onAuth != nil {
			reason = s.onAuth(auth)
		}
		if reason != "" {
			_ = ws.Write(ctx, websocket.MessageText, mustJSON(pairing.AuthResultMessage{Type: pairing.MsgTypeAuthFail, Reason: reason}))
			return
		}
		if err := ws.Write(ctx, websocket.MessageText, mustJSON(pairing.AuthResultMessage{Type: pairing.MsgTypeAuthOK})); err != nil {
			return
		}

		// 3) 业务循环
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

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

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

func TestDial_HandshakeOK(t *testing.T) {
	ms := &mockServer{
		challengeNonce: "nonce-xyz",
		onAuth: func(a pairing.AuthMessage) string {
			// 验签: 从 auth.DeviceID 也可以反查公钥,但这里直接用 pairing.VerifyAuth
			// 不方便因为我们没存公钥。简单校验 token/sig 非空就行,真实 server 自行验签。
			if a.Token == "" || a.Signature == "" {
				return "missing fields"
			}
			sig, err := base64.StdEncoding.DecodeString(a.Signature)
			if err != nil || len(sig) != 64 {
				return "bad sig"
			}
			return ""
		},
	}
	srv := httptest.NewServer(ms.handler(t))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := Dial(ctx, DialOptions{
		ServerURL:     wsURL(srv),
		Identity:      testIdentity(t),
		Token:         "tok",
		ClientVersion: "test",
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
}

func TestDial_AuthRejected(t *testing.T) {
	ms := &mockServer{
		challengeNonce: "n",
		onAuth:         func(pairing.AuthMessage) string { return "nope" },
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
	if !strings.Contains(err.Error(), "nope") {
		t.Fatalf("error should contain reason: %v", err)
	}
}

func TestCall_EchoResult(t *testing.T) {
	ms := &mockServer{
		challengeNonce: "n",
		onMessage: func(ws *websocket.Conn, raw []byte) {
			// 把 client 发来的 params.value 原封回 result
			var req struct {
				ID     json.RawMessage `json:"id"`
				Params struct {
					Value string `json:"value"`
				} `json:"params"`
			}
			_ = json.Unmarshal(raw, &req)
			resp := map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(req.ID),
				"result":  map[string]string{"echo": req.Params.Value},
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
			var req struct{ ID json.RawMessage `json:"id"` }
			_ = json.Unmarshal(raw, &req)
			resp := map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error":   map[string]any{"code": -32000, "message": "boom"},
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
	if re.Code != -32000 || re.Message != "boom" {
		t.Fatalf("bad rpc error: %+v", re)
	}
}

func TestNotification_Delivered(t *testing.T) {
	ms := &mockServer{
		challengeNonce: "n",
		onMessage: func(ws *websocket.Conn, _ []byte) {
			// 收到 client 任意 call,就主动发 3 个 notification
			for i := 0; i < 3; i++ {
				n := map[string]any{
					"jsonrpc": "2.0",
					"method":  "diagnose.token",
					"params":  map[string]int{"seq": i},
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
			var p struct{ Seq int `json:"seq"` }
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

	// 触发 server 发 notification(onMessage 会在收到任意帧时启动)
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

	// 先触发 server 关连接
	_ = conn.Notify(ctx, "trigger", nil)

	// 等 Done 被关
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
		"https://openclaw.example.com":            "wss://openclaw.example.com/v1/gateway",
		"http://localhost:8080":                   "ws://localhost:8080/v1/gateway",
		"wss://openclaw.example.com/custom/path":  "wss://openclaw.example.com/custom/path",
		"ws://localhost:8080/":                    "ws://localhost:8080/v1/gateway",
	}
	for in, want := range cases {
		if got := normalizeWSURL(in); got != want {
			t.Errorf("normalizeWSURL(%q) = %q, want %q", in, got, want)
		}
	}
}
