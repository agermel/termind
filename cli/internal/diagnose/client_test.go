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
	assistantText   string
	waitStatus      string
	waitError       string
	sessionMessages []sessionMessage
	seenMethods     []string
	agentRequests   []agentRequest
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
				messages := m.sessionMessages
				if len(messages) == 0 {
					messages = []sessionMessage{{
						Role:      "assistant",
						Text:      m.assistantText,
						Timestamp: mustJSONRaw(time.Now().Format(time.RFC3339Nano)),
					}}
				}
				writeResponse(ctx, ws, req.ID, sessionsGetResponse{Messages: messages})
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
		Lark: LarkRouting{
			Sender: "bot",
			Targets: []LarkTarget{
				{Type: "chat", ID: "oc_test", Label: "test group", Enabled: true},
				{Type: "user", ID: "ou_test", Label: "me", Enabled: true},
			},
		},
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
	if strings.Join(ms.seenMethods, ",") != strings.Join([]string{MethodAgent, MethodAgentWait, MethodSessionsGet}, ",") {
		t.Fatalf("methods=%v, want agent then agent.wait then sessions.get", ms.seenMethods)
	}
	// Required pieces of the prompt:
	//   - skill router hint
	//   - the four hard-contract rules (lark-cli only, exec must be allowed,
	//     exit-0 verification, exact stdout/stderr on failure)
	//   - the literal targets and command/tail so plugin tools have data to chew on
	for _, want := range []string{
		"Use the termind-lark-alert skill.",
		"Hard contract",
		"lark-cli is the ONLY valid Lark/Feishu sender",
		"tools.alsoAllow must include exec",
		"Claim delivery only after lark-cli exits 0 for every enabled target",
		"do not try another sender",
		"oc_test",
		"ou_test",
		"go run ./cmd/grade serve",
		"panic: runtime error: invalid memory address",
	} {
		if !strings.Contains(req.Message, want) {
			t.Fatalf("alert prompt missing %q:\n%s", want, req.Message)
		}
	}
	// Skill-implementation details that must not be re-asserted in the prompt
	// (they live in SKILL.md only, to avoid double-source maintenance).
	for _, banned := range []string{
		"termind_lark_card_build",
		"termind_lark_cli_send_command_build",
		"termind_event_redact",
		"termind_fingerprint_compute",
		"OpenClaw's `" + "message" + "` tool",
		"channel" + ": " + "feishu",
		"message" + "(action=send",
	} {
		if strings.Contains(req.Message, banned) {
			t.Fatalf("alert prompt contains banned text %q:\n%s", banned, req.Message)
		}
	}
}

func TestDiagnose_AlertReturnsAgentWaitError(t *testing.T) {
	ms := &mockDiagnoseServer{waitStatus: "error", waitError: "lark-cli failed"}
	srv := httptest.NewServer(ms.handler(t))
	defer srv.Close()

	conn := dialTestConn(t, srv.URL)
	defer conn.Close()

	dc := NewClient(conn)
	err := dc.Alert(context.Background(), &Request{
		Command:  "false",
		ExitCode: 1,
		Lark: LarkRouting{
			Targets: []LarkTarget{{Type: "chat", ID: "oc_test", Enabled: true}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "lark-cli failed") {
		t.Fatalf("err=%v, want lark-cli failed", err)
	}
}

func TestDiagnose_AlertReturnsSessionExecError(t *testing.T) {
	ms := &mockDiagnoseServer{sessionMessages: []sessionMessage{{
		Role:      "toolResult",
		ToolName:  "exec",
		Content:   mustJSONRaw([]map[string]string{{"type": "text", "text": "Error: TAT API error: [10003] invalid param"}}),
		Details:   mustJSONRaw(map[string]any{"exitCode": 1, "aggregated": "Error: TAT API error: [10003] invalid param"}),
		Timestamp: mustJSONRaw(time.Now().Format(time.RFC3339Nano)),
	}}}
	srv := httptest.NewServer(ms.handler(t))
	defer srv.Close()

	conn := dialTestConn(t, srv.URL)
	defer conn.Close()

	dc := NewClient(conn)
	err := dc.Alert(context.Background(), &Request{
		Command:  "false",
		ExitCode: 1,
		Lark: LarkRouting{
			Targets: []LarkTarget{{Type: "chat", ID: "oc_test", Enabled: true}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid param") {
		t.Fatalf("err=%v, want invalid param", err)
	}
}

func TestDiagnose_SummarizeFailureSkipsPromptArtifacts(t *testing.T) {
	got := summarizeFailure(&Request{
		Command:    "kkk",
		ExitCode:   127,
		OutputTail: "zsh: command not found: kkk\r\n\u001b[1m\u001b[7m%\u001b[27m\u001b[1m\u001b[0m",
	})
	if got != "zsh: command not found: kkk" {
		t.Fatalf("summary=%q", got)
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

func TestFailureEventFromRequestMapsEnrichFields(t *testing.T) {
	req := &Request{
		Command:    "go test ./...",
		ExitCode:   1,
		OutputTail: "FAIL\nauthorization: Bearer super-secret\n",
		Shell:      "/bin/zsh",
		Cwd:        "/repo/be-grade",
		Lang:       "zh_CN.UTF-8",
		User:       "matterhorn",
		Project:    "be-grade",
		Branch:     "feat/rank-v2",
		GitCommit:  "8e4d21a",
		OS:         "darwin 24.0.0",
		GoVersion:  "go1.22.3",
	}

	ev := failureEventFromRequest(req)

	if got, want := ev["user"], "matterhorn"; got != want {
		t.Errorf("user=%v, want %v", got, want)
	}
	if got, want := ev["project"], "be-grade"; got != want {
		t.Errorf("project=%v, want %v", got, want)
	}
	if got, want := ev["branch"], "feat/rank-v2"; got != want {
		t.Errorf("branch=%v, want %v", got, want)
	}
	if got, want := ev["branchKind"], "feature"; got != want {
		t.Errorf("branchKind=%v, want %v", got, want)
	}
	if got, want := ev["gitCommit"], "8e4d21a"; got != want {
		t.Errorf("gitCommit=%v, want %v", got, want)
	}
	if got, want := ev["environment"], "darwin 24.0.0 · go1.22.3"; got != want {
		t.Errorf("environment=%v, want %v", got, want)
	}
	// Lang 是输出语种偏好, 不该污染 environment
	if _, exists := ev["lang"]; exists {
		t.Errorf("event should not carry lang; got %v", ev["lang"])
	}
}

func TestFailureEventFromRequestOmitsEmptyEnrichFields(t *testing.T) {
	req := &Request{
		Command:    "false",
		ExitCode:   1,
		OutputTail: "",
		// 所有 enrich 字段都空
	}
	ev := failureEventFromRequest(req)

	for _, k := range []string{"user", "project", "branch", "branchKind", "gitCommit", "environment"} {
		if _, exists := ev[k]; exists {
			t.Errorf("event should omit %q when enrich missing, got %v", k, ev[k])
		}
	}
}

func TestBranchKindClassifies(t *testing.T) {
	cases := map[string]string{
		"main":             "main",
		"master":           "main",
		"trunk":            "main",
		"release/2026-05":  "release",
		"hotfix/rank":      "release",
		"feat/rank-v2":     "feature",
		"feature/foo":      "feature",
		"fix/rank-nil":     "feature",
		"dev/matterhorn":   "feature",
		"topic/experiment": "other",
		"":                 "other",
	}
	for branch, want := range cases {
		if got := branchKind(branch); got != want {
			t.Errorf("branchKind(%q)=%q, want %q", branch, got, want)
		}
	}
}

func TestDiagnose_CleanTerminalTextRemovesMarkdown(t *testing.T) {
	got := cleanTerminalText("**最可能原因**：路径不存在。\n\n```bash\nls -la /tmp/nope\n```\n- 修正路径即可。")
	want := "最可能原因：路径不存在。\n  ls -la /tmp/nope\n修正路径即可。"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
