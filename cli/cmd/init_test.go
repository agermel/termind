package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/coder/websocket"

	"termind/internal/gateway"
	"termind/internal/identity"
	"termind/internal/pairing"
)

type initPairingServer struct {
	attempts atomic.Int32
	scopes   []string
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
				"auth":     map[string]any{"role": "operator", "scopes": s.issuedScopes(), "deviceToken": "device-token"},
			},
		}))
	}
}

func (s *initPairingServer) issuedScopes() []string {
	if len(s.scopes) > 0 {
		return s.scopes
	}
	return pairing.DefaultScopes()
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
	for _, want := range pairing.DefaultScopes() {
		if !containsString(auth.Scopes, want) {
			t.Fatalf("scopes=%v, want %s", auth.Scopes, want)
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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

func TestPromptOpenClawMode_RemoteChoice(t *testing.T) {
	mode, err := promptOpenClawMode(context.Background(), bufio.NewReader(strings.NewReader("2\n")), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("promptOpenClawMode: %v", err)
	}
	if mode != initOpenClawRemote {
		t.Fatalf("mode=%s, want %s", mode, initOpenClawRemote)
	}
}

func TestResolveRemoteOpenClawConnection(t *testing.T) {
	code := base64.RawURLEncoding.EncodeToString([]byte(`{"url":"wss://openclaw.example.com/v1/gateway","bootstrapToken":"boot"}`))
	var out bytes.Buffer
	openClaw, err := resolveRemoteOpenClawConnection(context.Background(), bufio.NewReader(strings.NewReader(code+"\n")), &out)
	if err != nil {
		t.Fatalf("resolveRemoteOpenClawConnection: %v", err)
	}
	if openClaw.Mode != initOpenClawRemote {
		t.Fatalf("mode=%s, want %s", openClaw.Mode, initOpenClawRemote)
	}
	if openClaw.Setup.URL != "wss://openclaw.example.com/v1/gateway" || openClaw.Setup.BootstrapToken != "boot" {
		t.Fatalf("setup=%+v", openClaw.Setup)
	}
	if !strings.Contains(out.String(), "远程 OpenClaw") {
		t.Fatalf("expected remote instructions, got %q", out.String())
	}
}

func TestInitOpenClawTUIModelRemoteChoiceMovesToSetupInput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newInitOpenClawTUIModel(ctx, cancel, nil, time.Second)
	m.selectedMode = 1

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(*initOpenClawTUIModel)
	if got.mode != initOpenClawRemote {
		t.Fatalf("mode=%s, want %s", got.mode, initOpenClawRemote)
	}
	if got.step != initTUIStepSetupInput {
		t.Fatalf("step=%d, want %d", got.step, initTUIStepSetupInput)
	}
}

func TestInitOpenClawTUIModelSetupInputParsesCode(t *testing.T) {
	code := base64.RawURLEncoding.EncodeToString([]byte(`{"url":"wss://openclaw.example.com/v1/gateway","bootstrapToken":"boot"}`))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newInitOpenClawTUIModel(ctx, cancel, nil, time.Second)
	m.mode = initOpenClawRemote
	m.step = initTUIStepSetupInput
	m.setupInput.SetValue(code)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(*initOpenClawTUIModel)
	if got.step != initTUIStepApproving {
		t.Fatalf("step=%d, want %d", got.step, initTUIStepApproving)
	}
	if got.setup == nil || got.setup.URL != "wss://openclaw.example.com/v1/gateway" || got.setup.BootstrapToken != "boot" {
		t.Fatalf("setup=%+v", got.setup)
	}
}

func TestInitOpenClawTUIRefreshesExpiredLocalSetupCode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newInitOpenClawTUIModel(ctx, cancel, nil, time.Second)
	m.mode = initOpenClawLocal
	m.step = initTUIStepApproving
	m.setup = &pairing.SetupCode{URL: "ws://127.0.0.1:18789/v1/gateway", BootstrapToken: "stale"}

	next, _ := m.Update(initApprovalDoneMsg{err: &gateway.ConnectError{
		Code:    "INVALID_REQUEST",
		Message: "unauthorized: bootstrap token invalid or expired (scan a fresh setup code)",
	}})
	got := next.(*initOpenClawTUIModel)
	if got.err != nil {
		t.Fatalf("err=%v, want refresh instead of quit", got.err)
	}
	if got.step != initTUIStepGeneratingLocal {
		t.Fatalf("step=%d, want %d", got.step, initTUIStepGeneratingLocal)
	}
	if got.setupRefreshes != 1 {
		t.Fatalf("setupRefreshes=%d, want 1", got.setupRefreshes)
	}
}

func TestIsExpiredBootstrapTokenError(t *testing.T) {
	err := &gateway.ConnectError{
		Code:    "INVALID_REQUEST",
		Message: "unauthorized: bootstrap token invalid or expired (scan a fresh setup code)",
	}
	if !isExpiredBootstrapTokenError(err) {
		t.Fatalf("expected expired bootstrap token error")
	}
	if isExpiredBootstrapTokenError(errors.New("pairing required")) {
		t.Fatalf("pairing required should not look like expired bootstrap token")
	}
}

func TestInitOpenClawTUISetupCommandLinesStayReadable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newInitOpenClawTUIModel(ctx, cancel, nil, time.Second)
	m.mode = initOpenClawRemote
	m.step = initTUIStepSetupInput

	body := m.renderSetupInputStep()
	assertPlainLineMax(t, body, 78)
}

func TestInitTUIProgramOptionsUseAltScreen(t *testing.T) {
	var out bytes.Buffer
	p := tea.NewProgram(quitModel{}, initTUIProgramOptions(strings.NewReader(""), &out)...)
	if _, err := p.Run(); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	if !strings.Contains(got, "\x1b[?1049h") || !strings.Contains(got, "\x1b[?1049l") {
		t.Fatalf("program options should enable alt screen, got %q", got)
	}
}

type quitModel struct{}

func (quitModel) Init() tea.Cmd {
	return tea.Quit
}

func (quitModel) Update(tea.Msg) (tea.Model, tea.Cmd) {
	return quitModel{}, tea.Quit
}

func (quitModel) View() string {
	return ""
}

func TestInitOpenClawTUIModelApprovalViewDoesNotShowPreviousStepLogs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newInitOpenClawTUIModel(ctx, cancel, nil, time.Second)
	m.step = initTUIStepApproving
	m.setup = &pairing.SetupCode{URL: "ws://127.0.0.1:18789/v1/gateway", BootstrapToken: "boot"}

	view := m.View()
	if strings.Contains(view, "正在从本机 OpenClaw 自动生成 setup code") {
		t.Fatalf("approval view should not show previous step logs: %q", view)
	}
	if strings.Contains(view, "↑/↓ 选择") || strings.Contains(view, "Enter 继续") {
		t.Fatalf("approval view should only show cancel help: %q", view)
	}
	if !strings.Contains(view, "请在 OpenClaw 里批准这台设备") {
		t.Fatalf("approval view should show approval instruction: %q", view)
	}
}

func TestInitOpenClawTUIModelCtrlCCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newInitOpenClawTUIModel(ctx, cancel, nil, time.Second)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	got := next.(*initOpenClawTUIModel)
	if !errors.Is(got.err, context.Canceled) {
		t.Fatalf("err=%v, want context.Canceled", got.err)
	}
}

func TestGenerateLocalOpenClawSetupCode(t *testing.T) {
	dir := t.TempDir()
	code := base64.RawURLEncoding.EncodeToString([]byte(`{"url":"ws://127.0.0.1:18789/v1/gateway","bootstrapToken":"boot"}`))
	openclaw := filepath.Join(dir, "openclaw")
	script := "#!/bin/sh\nif [ \"$1 $2 $3\" = \"config get gateway.port\" ]; then printf '18789\\n'; exit 0; fi\nprintf '%s\\n' 'Config warnings: duplicate plugin id'\nprintf '%s\\n' " + code + "\n"
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
