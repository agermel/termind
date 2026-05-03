package diagnose

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"termind/internal/gateway"
	"termind/internal/identity"
)

type mockDiagnoseServer struct {
	assistantText string
	waitStatus    string
	waitError     string
	seenMethods   []string
	agentRequests []agentRequest
}

func (m *mockDiagnoseServer) handler(t *testing.T) http.HandlerFunc {
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
			"payload": map[string]string{"nonce": "n"},
		})); err != nil {
			return
		}
		_, connectRaw, err := ws.Read(ctx)
		if err != nil {
			return
		}
		var connectReq struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(connectRaw, &connectReq)
		hello := map[string]any{
			"type":     "hello-ok",
			"protocol": 3,
			"server":   map[string]string{"version": "test", "connId": "conn-1"},
			"features": map[string]any{"methods": []string{}, "events": []string{}},
			"snapshot": map[string]any{},
			"auth": map[string]any{
				"role":        "operator",
				"scopes":      []string{"operator.read", "operator.write"},
				"deviceToken": "tok",
			},
			"policy": map[string]int{"maxPayload": 1, "maxBufferedBytes": 1, "tickIntervalMs": 30000},
		}
		if err := ws.Write(ctx, websocket.MessageText, mustJSON(map[string]any{
			"type":    "res",
			"id":      connectReq.ID,
			"ok":      true,
			"payload": hello,
		})); err != nil {
			return
		}

		for {
			_, raw, err := ws.Read(ctx)
			if err != nil {
				return
			}
			var req struct {
				ID     string          `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			_ = json.Unmarshal(raw, &req)
			m.seenMethods = append(m.seenMethods, req.Method)

			switch req.Method {
			case MethodAgent:
				var p agentRequest
				_ = json.Unmarshal(req.Params, &p)
				m.agentRequests = append(m.agentRequests, p)
				writeResponse(ctx, ws, req.ID, map[string]any{
					"runId":  p.IDempotencyKey,
					"status": "accepted",
				})
			case MethodAgentWait:
				status := m.waitStatus
				if status == "" {
					status = "ok"
				}
				writeResponse(ctx, ws, req.ID, agentWaitResponse{
					RunID:  "run-1",
					Status: status,
					Error:  m.waitError,
				})
			case MethodSessionsGet:
				writeResponse(ctx, ws, req.ID, sessionsGetResponse{Messages: []sessionMessage{{
					Role:      "assistant",
					Text:      m.assistantText,
					Timestamp: mustJSONRaw(time.Now().Format(time.RFC3339Nano)),
				}}})
			default:
				writeError(ctx, ws, req.ID, "UNKNOWN", "unknown method")
			}
		}
	}
}

func writeResponse(ctx context.Context, ws *websocket.Conn, id string, payload any) {
	_ = ws.Write(ctx, websocket.MessageText, mustJSON(map[string]any{
		"type":    "res",
		"id":      id,
		"ok":      true,
		"payload": payload,
	}))
}

func writeError(ctx context.Context, ws *websocket.Conn, id, code, msg string) {
	_ = ws.Write(ctx, websocket.MessageText, mustJSON(map[string]any{
		"type": "res",
		"id":   id,
		"ok":   false,
		"error": map[string]string{
			"code":    code,
			"message": msg,
		},
	}))
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func mustJSONRaw(v any) json.RawMessage { return json.RawMessage(mustJSON(v)) }

func dialTestConn(t *testing.T, url string) *gateway.Conn {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	id, err := identity.LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := gateway.Dial(ctx, gateway.DialOptions{
		ServerURL: "ws://" + strings.TrimPrefix(url, "http://"),
		Identity:  id,
		Token:     "tok",
		Role:      "operator",
		Scopes:    []string{"operator.read", "operator.write"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func TestDiagnose_UsesOperatorAgentFlow(t *testing.T) {
	ms := &mockDiagnoseServer{assistantText: "建议先检查路径是否存在。"}
	srv := httptest.NewServer(ms.handler(t))
	defer srv.Close()

	conn := dialTestConn(t, srv.URL)
	defer conn.Close()

	dc := NewClient(conn)
	ch, err := dc.Start(context.Background(), &Request{
		Command:    "ls /definitely-not-exist",
		ExitCode:   1,
		OutputTail: "No such file or directory",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	var got strings.Builder
	var sawDone bool
	for ev := range ch {
		if ev.Error != "" {
			t.Fatalf("unexpected error: %s", ev.Error)
		}
		got.WriteString(ev.Delta)
		if ev.Done {
			sawDone = true
		}
	}
	if !sawDone {
		t.Fatal("expected to see done=true")
	}
	if got.String() != "建议先检查路径是否存在。" {
		t.Fatalf("got %q", got.String())
	}
	if len(ms.agentRequests) != 1 {
		t.Fatalf("agentRequests=%d, want 1", len(ms.agentRequests))
	}
	if got := ms.agentRequests[0].SessionKey; got != defaultSessionKey {
		t.Errorf("sessionKey=%q, want %q", got, defaultSessionKey)
	}
	if !strings.Contains(ms.agentRequests[0].Message, "退出码: 1") {
		t.Errorf("prompt missing exit code: %q", ms.agentRequests[0].Message)
	}
	want := []string{MethodAgent, MethodAgentWait, MethodSessionsGet}
	if strings.Join(ms.seenMethods, ",") != strings.Join(want, ",") {
		t.Fatalf("methods=%v, want %v", ms.seenMethods, want)
	}
}

func TestDiagnose_AlertSubmitsTermindLarkSkillPrompt(t *testing.T) {
	t.Setenv("TERMIND_LARK_CHAT_ID", "oc_test")
	ms := &mockDiagnoseServer{assistantText: "ignored"}
	srv := httptest.NewServer(ms.handler(t))
	defer srv.Close()

	conn := dialTestConn(t, srv.URL)
	defer conn.Close()

	dc := NewClient(conn)
	err := dc.Alert(context.Background(), &Request{
		Command:    "go run ./cmd/grade serve",
		ExitCode:   1,
		OutputTail: "panic: runtime error: invalid memory address\nAuthorization: Bearer secret-token",
		Shell:      "/bin/zsh",
		Cwd:        "/repo",
		Lang:       "zh_CN.UTF-8",
	})
	if err != nil {
		t.Fatalf("Alert: %v", err)
	}

	if len(ms.agentRequests) != 1 {
		t.Fatalf("agentRequests=%d, want 1", len(ms.agentRequests))
	}
	req := ms.agentRequests[0]
	if req.SessionKey != alertSessionKey {
		t.Fatalf("sessionKey=%q, want %q", req.SessionKey, alertSessionKey)
	}
	if req.Deliver {
		t.Fatal("alert should hand off to OpenClaw orchestration without direct delivery")
	}
	for _, want := range []string{
		"Use the termind-lark-alert skill.",
		"OpenClaw's `message` tool",
		"termind_lark_card_build",
		"channel: feishu",
		"oc_test",
		"go run ./cmd/grade serve",
		"panic: runtime error: invalid memory address",
	} {
		if !strings.Contains(req.Message, want) {
			t.Fatalf("alert prompt missing %q:\n%s", want, req.Message)
		}
	}
	if strings.Contains(req.Message, "Use lark-cli as the only") {
		t.Fatalf("alert prompt still points at lark-cli:\n%s", req.Message)
	}
	if strings.Contains(req.Message, "termind_lark_cli_send_command_build") {
		t.Fatalf("alert prompt still points at legacy lark-cli helper:\n%s", req.Message)
	}
}

func TestDiagnose_AgentWaitError(t *testing.T) {
	ms := &mockDiagnoseServer{waitStatus: "error", waitError: "model blew up"}
	srv := httptest.NewServer(ms.handler(t))
	defer srv.Close()

	conn := dialTestConn(t, srv.URL)
	defer conn.Close()

	dc := NewClient(conn)
	ch, err := dc.Start(context.Background(), &Request{Command: "x", ExitCode: 1})
	if err != nil {
		t.Fatal(err)
	}
	var sawErr string
	for ev := range ch {
		if ev.Error != "" {
			sawErr = ev.Error
		}
	}
	if !strings.Contains(sawErr, "model blew up") {
		t.Fatalf("sawErr=%q", sawErr)
	}
}

func TestDiagnose_LastAssistantTextReadsContentParts(t *testing.T) {
	rawParts := mustJSONRaw([]map[string]string{
		{"type": "text", "text": "hello "},
		{"type": "text", "text": "world"},
	})
	got := lastAssistantText([]sessionMessage{
		{Role: "user", Text: "ignored"},
		{Role: "assistant", Content: rawParts},
	}, time.Now().Add(-time.Second))
	if got != "hello world" {
		t.Fatalf("got %q", got)
	}
}

func TestDiagnose_CleanTerminalTextRemovesMarkdown(t *testing.T) {
	got := cleanTerminalText("**最可能原因**：路径不存在。\n\n```bash\nls -la /tmp/nope\n```\n- 修正路径即可。")
	want := "最可能原因：路径不存在。\n  ls -la /tmp/nope\n修正路径即可。"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
