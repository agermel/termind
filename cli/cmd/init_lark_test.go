package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"termind/internal/config"
	"termind/internal/diagnose"
	"termind/internal/gateway"
)

var testANSIRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func TestFindChatChoices(t *testing.T) {
	var input any
	if err := json.Unmarshal([]byte(`{
		"data": {
			"items": [
				{"chat_id": "oc_one", "name": "one"},
				{"chat_id": "oc_two", "description": "two"},
				{"chat_id": "oc_one", "name": "duplicate"}
			]
		}
	}`), &input); err != nil {
		t.Fatal(err)
	}
	choices := findChatChoices(input)
	if len(choices) != 2 {
		t.Fatalf("choices=%d, want 2: %#v", len(choices), choices)
	}
	if choices[0].ID != "oc_one" || choices[0].Label != "one" {
		t.Fatalf("first choice=%#v", choices[0])
	}
	if choices[1].ID != "oc_two" || choices[1].Label != "two" {
		t.Fatalf("second choice=%#v", choices[1])
	}
}

func TestFindUserChoices(t *testing.T) {
	var input any
	if err := json.Unmarshal([]byte(`{
		"data": {
			"users": [
				{"open_id": "ou_one", "name": "Alice"},
				{"open_id": "ou_two", "localized_name": "Bob"}
			]
		}
	}`), &input); err != nil {
		t.Fatal(err)
	}
	choices := findUserChoices(input)
	if len(choices) != 2 {
		t.Fatalf("choices=%d, want 2: %#v", len(choices), choices)
	}
	if choices[0].Type != "user" || choices[0].ID != "ou_one" || choices[0].Label != "Alice" {
		t.Fatalf("first choice=%#v", choices[0])
	}
	if choices[1].ID != "ou_two" || choices[1].Label != "Bob" {
		t.Fatalf("second choice=%#v", choices[1])
	}
}

func TestAppendTargetDeduplicates(t *testing.T) {
	targets := appendTarget(nil, larkTargetChoice{Type: "chat", ID: " oc_one ", Label: "old"})
	targets = appendTarget(targets, larkTargetChoice{Type: "chat", ID: "oc_one", Label: "new"})
	if len(targets) != 1 {
		t.Fatalf("targets=%d, want 1", len(targets))
	}
	if targets[0].Label != "new" {
		t.Fatalf("label=%q, want new", targets[0].Label)
	}
}

func TestLarkInitModelInitialViewIsBubbleTeaWizard(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, &config.Config{}, "ws://127.0.0.1:18789/v1/gateway", true)

	view := m.View()
	if !strings.Contains(view, "Lark/Feishu 转发配置") {
		t.Fatalf("expected lark wizard title, got %q", view)
	}
	if strings.Contains(view, "(Y/n):") {
		t.Fatalf("should not render plain prompt: %q", view)
	}
}

func TestLarkInitModelProgressOrderMatchesFlow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, &config.Config{}, "ws://127.0.0.1:18789/v1/gateway", true)

	progress := plainANSI(m.renderProgress())
	want := "1 启用  ──  2 OpenClaw  ──  3 lark-cli  ──  4 Profile  ──  5 目标  ──  6 测试"
	if progress != want {
		t.Fatalf("progress=%q, want %q", progress, want)
	}
}

func TestLarkInitModelDisableQuits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, &config.Config{}, "ws://127.0.0.1:18789/v1/gateway", true)
	m.yes = false

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(*larkInitModel)
	if !got.done {
		t.Fatal("expected disabled lark init to finish")
	}
}

func TestLarkInitModelManualChatTargetFlow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, &config.Config{}, "ws://127.0.0.1:18789/v1/gateway", true)
	m.prepareManualChatID()
	m.input.SetValue("oc_ops")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(*larkInitModel)
	if got.step != larkStepManualChatLabel {
		t.Fatalf("step=%d, want %d", got.step, larkStepManualChatLabel)
	}
	got.input.SetValue("ops")
	next, _ = got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = next.(*larkInitModel)
	if len(got.targets) != 1 {
		t.Fatalf("targets=%d, want 1", len(got.targets))
	}
	if got.targets[0].Type != "chat" || got.targets[0].ID != "oc_ops" || got.targets[0].Label != "ops" {
		t.Fatalf("target=%+v", got.targets[0])
	}
}

func TestLarkInitModelTestDoneSavesWithoutStuckSavingStep(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, &config.Config{}, "ws://127.0.0.1:18789/v1/gateway", true)
	m.targets = []larkTargetChoice{{Type: "chat", ID: "oc_ops", Label: "ops"}}

	next, _ := m.Update(larkCommandDoneMsg{label: "发送测试消息", text: "✓ 测试发送成功 oc_ops", next: larkStepSaving})
	got := next.(*larkInitModel)
	if got.step == larkStepSaving {
		t.Fatal("model should not stay on saving step")
	}
	if got.savedPath == "" {
		t.Fatal("expected config to be saved")
	}
}

func TestLarkInitModelTestErrorStillSavesConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, &config.Config{}, "ws://127.0.0.1:18789/v1/gateway", true)
	m.targets = []larkTargetChoice{{Type: "chat", ID: "oc_ops", Label: "ops"}}
	m.openClawSetupDone = true
	m.commandRetry = larkStepDoctorFailed
	m.commandRetrySet = true

	next, _ := m.Update(larkCommandDoneMsg{label: "发送测试消息", err: errors.New("send failed"), next: larkStepSaving})
	got := next.(*larkInitModel)
	if got.step == larkStepDoctorFailed {
		t.Fatal("test send error should not jump back to stale command retry step")
	}
	if got.savedPath == "" {
		t.Fatal("expected config to be saved even when test send fails")
	}
}

func TestLarkInitModelOpenClawChangePromptsGatewayRestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, &config.Config{}, "ws://127.0.0.1:18789/v1/gateway", true)

	next, _ := m.Update(larkCommandDoneMsg{label: "openclaw plugins install/enable", text: "ok", next: larkStepLocalGatewayRestart, needsRestart: true})
	got := next.(*larkInitModel)
	if got.step != larkStepLocalGatewayRestart {
		t.Fatalf("step=%d, want %d", got.step, larkStepLocalGatewayRestart)
	}
	if !got.yes {
		t.Fatal("gateway restart should default to yes")
	}
}

func TestLarkInitModelMissingLarkCLIAsksToInstallInsteadOfContinuing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, &config.Config{}, "ws://127.0.0.1:18789/v1/gateway", true)

	next, _ := m.Update(larkDoctorDoneMsg{status: &diagnose.LarkCLIStatus{Installed: false, Ready: false}})
	got := next.(*larkInitModel)
	if got.step != larkStepLocalInstallLarkCLI {
		t.Fatalf("step=%d, want %d", got.step, larkStepLocalInstallLarkCLI)
	}
}

func TestLarkInitModelUnconfiguredLarkCLIDoesNotRunLocalConfig(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, &config.Config{}, "ws://127.0.0.1:18789/v1/gateway", true)

	next, _ := m.Update(larkDoctorDoneMsg{status: &diagnose.LarkCLIStatus{Installed: true, Ready: false}})
	got := next.(*larkInitModel)
	if got.step != larkStepDoctorFailed {
		t.Fatalf("step=%d, want %d", got.step, larkStepDoctorFailed)
	}
	view := got.View()
	if strings.Contains(view, "继续手动填写") {
		t.Fatalf("should not offer to continue to target/test flow: %q", got.View())
	}
	if strings.Contains(view, "termind 带你") {
		t.Fatalf("configure view should not claim termind will run setup: %q", view)
	}
	if strings.Contains(view, "绑定已有 OpenClaw bot") {
		t.Fatalf("configure view should not collect app id in Termind: %q", view)
	}
	if !strings.Contains(view, "OpenClaw 运行端") || !strings.Contains(view, "Termind 只负责后续读取 chat 列表和选择转发目标") {
		t.Fatalf("configure view should guide OpenClaw-side login/bind: %q", view)
	}
	if strings.Contains(view, "app-secret") {
		t.Fatalf("configure view should not ask for app secret: %q", view)
	}
}

func TestLarkInitModelNotReadyLarkCLIDoesNotRunLocalAuthLogin(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, &config.Config{}, "ws://127.0.0.1:18789/v1/gateway", true)

	next, _ := m.Update(larkDoctorDoneMsg{status: &diagnose.LarkCLIStatus{
		Installed: true,
		Ready:     false,
		Profiles:  []diagnose.LarkCLIProfile{{Name: "cli_test", Active: true}},
	}})
	got := next.(*larkInitModel)
	if got.step != larkStepDoctorFailed {
		t.Fatalf("step=%d, want %d", got.step, larkStepDoctorFailed)
	}
	view := got.View()
	if strings.Contains(view, "termind 内登录") || strings.Contains(view, "授权链接") {
		t.Fatalf("auth login view should not start local login flow: %q", view)
	}
	if !strings.Contains(view, "完成 lark-cli 登录/授权") || !strings.Contains(view, "不复制本机凭证") {
		t.Fatalf("auth login view should require OpenClaw-side login: %q", view)
	}
}

func TestLarkInitModelMultipleProfilesShowsOpenClawProfileChoice(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, &config.Config{}, "ws://127.0.0.1:18789/v1/gateway", true)

	next, _ := m.Update(larkDoctorDoneMsg{status: &diagnose.LarkCLIStatus{
		Installed: true,
		Ready:     true,
		Profile:   "cli_active",
		Profiles: []diagnose.LarkCLIProfile{
			{Name: "cli_active", Active: true, TokenValid: true},
			{Name: "cli_other", TokenValid: true},
		},
	}})
	got := next.(*larkInitModel)
	if got.step != larkStepProfileChoice {
		t.Fatalf("step=%d, want %d", got.step, larkStepProfileChoice)
	}
	view := got.View()
	if !strings.Contains(view, "选择 OpenClaw lark-cli profile") {
		t.Fatalf("view should ask user to choose OpenClaw lark-cli profile: %q", view)
	}
	if !strings.Contains(view, "登录/新增授权") {
		t.Fatalf("view should include login option: %q", view)
	}
	if !strings.Contains(view, "OpenClaw 检测到多个 lark-cli profile") {
		t.Fatalf("view should mention OpenClaw multi-profile state: %q", view)
	}
}

func TestSenderForProfileDoesNotReuseUserFallbackForBotProfile(t *testing.T) {
	got := senderForProfile(diagnose.LarkCLIProfile{Name: "cli_bot", Active: true, TokenValid: true}, "user")
	if got != "bot" {
		t.Fatalf("sender=%q, want bot", got)
	}
	got = senderForProfile(diagnose.LarkCLIProfile{Name: "cli_bot", User: "Alice", Active: true, TokenValid: true}, "")
	if got != "bot" {
		t.Fatalf("sender=%q, want bot for profile user metadata", got)
	}
	got = senderForProfile(diagnose.LarkCLIProfile{Name: "cli_user", Identity: "user", Active: true, TokenValid: true}, "")
	if got != "user" {
		t.Fatalf("sender=%q, want user for explicit user identity", got)
	}
}

func TestLarkInitModelRemoteProfileChoiceStartsOpenClawAuth(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, &config.Config{}, "ws://127.0.0.1:18789/v1/gateway", false)

	next, _ := m.Update(larkDoctorDoneMsg{status: &diagnose.LarkCLIStatus{
		Installed: true,
		Ready:     true,
		Profile:   "cli_active",
		Profiles: []diagnose.LarkCLIProfile{
			{Name: "cli_active", Active: true, TokenValid: true},
		},
	}})
	got := next.(*larkInitModel)
	view := got.View()
	if !strings.Contains(view, "登录/新增授权（在 OpenClaw 运行端完成）") {
		t.Fatalf("remote profile choice should include remote login option: %q", view)
	}

	got.selectedIndex = len(got.profileChoices()) - 1
	next, cmd := got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = next.(*larkInitModel)
	if cmd == nil {
		t.Fatalf("remote login option should start OpenClaw auth command")
	}
	if got.step != larkStepLocalAuthLoginStarting {
		t.Fatalf("step=%d, want %d", got.step, larkStepLocalAuthLoginStarting)
	}
}

func TestLarkInitModelLocalProfileLoginStartsOpenClawAuth(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, &config.Config{}, "ws://127.0.0.1:18789/v1/gateway", true)

	next, _ := m.Update(larkDoctorDoneMsg{status: &diagnose.LarkCLIStatus{
		Installed: true,
		Ready:     true,
		Profile:   "cli_active",
		Profiles: []diagnose.LarkCLIProfile{
			{Name: "cli_active", Active: true, TokenValid: true},
		},
	}})
	got := next.(*larkInitModel)
	got.selectedIndex = len(got.profileChoices()) - 1
	next, cmd := got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = next.(*larkInitModel)
	if cmd == nil {
		t.Fatalf("local login option should start OpenClaw lark-cli auth command")
	}
	if got.step != larkStepLocalAuthLoginStarting {
		t.Fatalf("step=%d, want %d", got.step, larkStepLocalAuthLoginStarting)
	}
	view := got.View()
	if strings.Contains(view, "termind 内登录") {
		t.Fatalf("local login option should not render local auth flow: %q", view)
	}
	if !strings.Contains(view, "OpenClaw exec") {
		t.Fatalf("view should explain OpenClaw-side login: %q", view)
	}
}

func TestLarkInitModelAuthLoginStartDisplaysOpenClawDeviceFlow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, &config.Config{}, "ws://127.0.0.1:18789/v1/gateway", true)

	next, cmd := m.Update(larkAuthLoginStartDoneMsg{result: &diagnose.LarkCLIAuthLoginStartResult{
		OK:              true,
		DeviceCode:      "dev-test",
		UserCode:        "ABCD-EFGH",
		VerificationURL: "https://example.com/device?user_code=ABCD-EFGH",
		ExpiresIn:       600,
	}})
	got := next.(*larkInitModel)
	if got.step != larkStepLocalAuthLoginWaiting {
		t.Fatalf("step=%d, want %d", got.step, larkStepLocalAuthLoginWaiting)
	}
	if cmd == nil {
		t.Fatal("expected OpenClaw auth login complete command")
	}
	view := got.View()
	for _, want := range []string{"OpenClaw lark-cli 浏览器授权", "https://example.com/device", "ABCD-EFGH", "等待 OpenClaw 端授权完成"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q: %q", want, view)
		}
	}
}

func TestLarkInitModelAuthLoginCompleteRechecksOpenClawStatus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, &config.Config{}, "ws://127.0.0.1:18789/v1/gateway", true)
	m.step = larkStepLocalAuthLoginWaiting

	next, cmd := m.Update(larkAuthLoginCompleteDoneMsg{result: &diagnose.LarkCLIAuthLoginCompleteResult{OK: true}})
	got := next.(*larkInitModel)
	if got.step != larkStepCheckingDoctor {
		t.Fatalf("step=%d, want %d", got.step, larkStepCheckingDoctor)
	}
	if cmd == nil {
		t.Fatal("expected OpenClaw status recheck command")
	}
}

func TestLarkInitModelConfigBindAppIDStartsOpenClawBind(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, &config.Config{}, "ws://127.0.0.1:18789/v1/gateway", true)
	m.setInput(larkStepLarkConfigBindAppID, "cli_xxx", "cli_test")

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(*larkInitModel)
	if cmd == nil {
		t.Fatalf("config bind app id should start OpenClaw bind command")
	}
	if got.step != larkStepLarkConfigBinding {
		t.Fatalf("step=%d, want %d", got.step, larkStepLarkConfigBinding)
	}
	if got.larkConfigBindAppID != "cli_test" {
		t.Fatalf("appID=%q, want cli_test", got.larkConfigBindAppID)
	}
}

func TestLarkInitModelConfigBindDoneRechecksOpenClawStatus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, &config.Config{}, "ws://127.0.0.1:18789/v1/gateway", true)
	m.step = larkStepLarkConfigBinding

	next, cmd := m.Update(larkConfigBindDoneMsg{appID: "cli_test", result: &diagnose.LarkCLIConfigBindResult{OK: true, AppID: "cli_test", Identity: "bot-only"}})
	got := next.(*larkInitModel)
	if got.step != larkStepCheckingDoctor {
		t.Fatalf("step=%d, want %d", got.step, larkStepCheckingDoctor)
	}
	if cmd == nil {
		t.Fatal("expected OpenClaw status recheck command")
	}
	if got.sender != "bot" {
		t.Fatalf("sender=%q, want bot", got.sender)
	}
}

func TestLarkInitModelActiveProfileChoiceSkipsProfileUse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, &config.Config{}, "ws://127.0.0.1:18789/v1/gateway", true)

	next, _ := m.Update(larkDoctorDoneMsg{status: &diagnose.LarkCLIStatus{
		Installed: true,
		Ready:     true,
		Profile:   "cli_active",
		Profiles: []diagnose.LarkCLIProfile{
			{Name: "cli_active", Active: true, TokenValid: true},
		},
	}})
	got := next.(*larkInitModel)
	got.selectedIndex = 0
	next, cmd := got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = next.(*larkInitModel)
	if cmd != nil {
		t.Fatalf("active profile choice should not call profile use")
	}
	if got.step == larkStepSwitchingProfile {
		t.Fatalf("active profile choice should not enter switching profile step")
	}
	if got.step != larkStepSearchChats {
		t.Fatalf("step=%d, want %d", got.step, larkStepSearchChats)
	}
}

func TestLarkInitModelRemoteUnconfiguredLarkCLIDoesNotRunLocalConfig(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, &config.Config{}, "wss://openclaw.example.com/v1/gateway", false)

	next, _ := m.Update(larkDoctorDoneMsg{status: &diagnose.LarkCLIStatus{Installed: true, Ready: false}})
	got := next.(*larkInitModel)
	if got.step == larkStepLocalConfigureLarkCLI {
		t.Fatal("remote OpenClaw must not run local lark-cli config/login")
	}
	if got.step != larkStepDoctorFailed {
		t.Fatalf("step=%d, want %d", got.step, larkStepDoctorFailed)
	}
	view := got.View()
	if strings.Contains(view, "绑定已有 OpenClaw bot") {
		t.Fatalf("remote view should not collect app id in Termind: %q", view)
	}
	if !strings.Contains(view, "OpenClaw 运行端") || !strings.Contains(view, "Termind 只负责后续读取 chat 列表和选择转发目标") {
		t.Fatalf("remote view should guide OpenClaw-side login/bind: %q", view)
	}
}

func TestLarkDoctorStatusWithRetryRetriesClosedGateway(t *testing.T) {
	attempts := 0
	status, notices, err := larkDoctorStatusWithRetry(context.Background(), "ws://127.0.0.1:18789/v1/gateway", func(_ context.Context, _ string) (*diagnose.LarkCLIStatus, error) {
		attempts++
		if attempts == 1 {
			return nil, fmt.Errorf("sessions.get: %w", gateway.ErrClosed)
		}
		return &diagnose.LarkCLIStatus{Installed: true, Ready: true, Profile: "cli_test"}, nil
	}, []time.Duration{0})
	if err != nil {
		t.Fatalf("status retry should recover, got err=%v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts=%d, want 2", attempts)
	}
	if status == nil || !status.Ready || status.Profile != "cli_test" {
		t.Fatalf("status=%+v", status)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "自动重试") {
		t.Fatalf("notices=%v", notices)
	}
}

func TestLarkDoctorStatusWithRetryDoesNotRetryNonClosedError(t *testing.T) {
	attempts := 0
	_, _, err := larkDoctorStatusWithRetry(context.Background(), "ws://127.0.0.1:18789/v1/gateway", func(context.Context, string) (*diagnose.LarkCLIStatus, error) {
		attempts++
		return nil, errors.New("lark-cli doctor failed")
	}, []time.Duration{0})
	if err == nil {
		t.Fatal("expected error")
	}
	if attempts != 1 {
		t.Fatalf("attempts=%d, want 1", attempts)
	}
}

func TestLarkInitModelOpenClawSetupDoneSkipsRepeatSetup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, &config.Config{}, "ws://127.0.0.1:18789/v1/gateway", true)
	m.openClawSetupDone = true

	next, cmd := m.prepareLocalOpenClawSetup(larkStepCheckingDoctor)
	got := next.(*larkInitModel)
	if got.step != larkStepCheckingDoctor {
		t.Fatalf("step=%d, want %d", got.step, larkStepCheckingDoctor)
	}
	if cmd == nil {
		t.Fatal("expected lark status check command")
	}
}

func TestLarkInitModelGatewayRestartMarksOpenClawSetupDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, &config.Config{}, "ws://127.0.0.1:18789/v1/gateway", true)
	m.openClawSetupNext = larkStepCheckingDoctor
	m.openClawNeedsRestart = true
	m.commandTitle = "重启 OpenClaw Gateway"
	m.commandNext = larkStepCheckingDoctor

	next, cmd := m.Update(larkCommandDoneMsg{label: "openclaw gateway restart", next: larkStepCheckingDoctor})
	got := next.(*larkInitModel)
	if !got.openClawSetupDone {
		t.Fatal("gateway restart should mark OpenClaw setup done")
	}
	if got.openClawNeedsRestart {
		t.Fatal("gateway restart success should clear restart-needed state")
	}
	if got.step != larkStepCheckingDoctor {
		t.Fatalf("step=%d, want %d", got.step, larkStepCheckingDoctor)
	}
	if cmd == nil {
		t.Fatal("expected lark status check command")
	}
}

func TestLarkInitModelOpenClawCommandErrorReturnsToRetryStep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, &config.Config{}, "ws://127.0.0.1:18789/v1/gateway", true)
	m.commandRetry = larkStepLocalToolsAllow
	m.commandRetrySet = true
	m.commandNext = larkStepLocalExecAllow

	next, _ := m.Update(larkCommandDoneMsg{label: "openclaw config set tools.alsoAllow", err: errors.New("boom"), next: larkStepLocalExecAllow})
	got := next.(*larkInitModel)
	if got.step != larkStepLocalToolsAllow {
		t.Fatalf("step=%d, want %d", got.step, larkStepLocalToolsAllow)
	}
}

func TestLarkInstallPluginUnmanagedExistingPluginContinues(t *testing.T) {
	dir := t.TempDir()
	openclaw := filepath.Join(dir, "openclaw")
	if err := os.WriteFile(openclaw, []byte(`#!/bin/sh
if [ "$1" = "plugins" ] && [ "$2" = "install" ]; then
  echo 'plugin already exists'
  exit 1
fi
if [ "$1" = "plugins" ] && [ "$2" = "uninstall" ]; then
  echo 'Plugin "termind" is not managed by plugins config/install records and cannot be uninstalled'
  echo 'loaded without install/load-path provenance; treat as untracked'
  exit 1
fi
exit 99
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cmd := larkInstallPluginCmd(context.Background(), larkStepLocalToolsAllow, "/tmp/termind-plugin")
	msg := cmd().(larkCommandDoneMsg)
	if msg.err != nil {
		t.Fatalf("unmanaged existing plugin should continue, got err=%v text=%q", msg.err, msg.text)
	}
	if msg.next != larkStepLocalToolsAllow {
		t.Fatalf("next=%d, want %d", msg.next, larkStepLocalToolsAllow)
	}
	if !strings.Contains(msg.text, "跳过自动刷新") {
		t.Fatalf("expected skip-refresh notice, got %q", msg.text)
	}
}

func TestLarkInitModelLocalPluginDefaultsToNpmSpec(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, &config.Config{}, "ws://127.0.0.1:18789/v1/gateway", true)
	m.step = larkStepLocalInstallPlugin
	m.yes = true

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(*larkInitModel)
	if got.step != larkStepLocalPluginSpec {
		t.Fatalf("step=%d, want %d", got.step, larkStepLocalPluginSpec)
	}
	if got.input.Value() != termindOpenClawPluginSpec {
		t.Fatalf("plugin spec=%q, want %q", got.input.Value(), termindOpenClawPluginSpec)
	}
}

func TestLarkInitModelRemotePluginDefaultsToNpmSpec(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, &config.Config{}, "wss://openclaw.example.com/v1/gateway", false)
	m.step = larkStepRemotePlugin
	m.remoteTarget = "user@example.com"
	m.yes = true

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(*larkInitModel)
	if got.step != larkStepRemotePluginSpec {
		t.Fatalf("step=%d, want %d", got.step, larkStepRemotePluginSpec)
	}
	if got.input.Value() != termindOpenClawPluginSpec {
		t.Fatalf("plugin spec=%q, want %q", got.input.Value(), termindOpenClawPluginSpec)
	}
}

func TestRemotePluginSpecRunsNpmInstallCommand(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, &config.Config{}, "wss://openclaw.example.com/v1/gateway", false)
	m.step = larkStepRemotePluginSpec
	m.remoteTarget = "user@example.com"

	next, _ := m.advanceInput(termindOpenClawPluginSpec)
	got := next.(*larkInitModel)
	if got.step != larkStepRunningCommand {
		t.Fatalf("step=%d, want %d", got.step, larkStepRunningCommand)
	}
	oldPathHint := "~/" + "termind/plugin"
	if strings.Contains(got.commandDetail, oldPathHint) {
		t.Fatalf("remote install should not mention path install: %q", got.commandDetail)
	}
	if !strings.Contains(got.commandDetail, "npm") {
		t.Fatalf("remote install detail should mention npm: %q", got.commandDetail)
	}
}

func TestLarkInitModelNoRestartSetupContinuesToNextStep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, &config.Config{}, "ws://127.0.0.1:18789/v1/gateway", true)
	m.openClawSetupNext = larkStepCheckingDoctor
	m.openClawNeedsRestart = false

	next, cmd := m.prepareLocalGatewayRestart()
	got := next.(*larkInitModel)
	if !got.openClawSetupDone {
		t.Fatal("setup without restart should still mark OpenClaw setup done")
	}
	if got.openClawNeedsRestart {
		t.Fatal("setup without restart should keep restart-needed state clear")
	}
	if got.step != larkStepCheckingDoctor {
		t.Fatalf("step=%d, want %d", got.step, larkStepCheckingDoctor)
	}
	if cmd == nil {
		t.Fatal("expected lark status check command")
	}
}

func TestLarkInitModelRunningLarkCLIInstallProgressStaysOnLarkCLI(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, &config.Config{}, "ws://127.0.0.1:18789/v1/gateway", true)
	m.step = larkStepRunningCommand
	m.commandTitle = "安装 lark-cli"
	m.commandNext = larkStepLocalGatewayRestart

	progress := plainANSI(m.renderProgress())
	if !strings.Contains(progress, "3 lark-cli") {
		t.Fatalf("progress should stay on lark-cli step: %q", progress)
	}
}

func TestLarkInitModelRunningLarkCLIConfigProgressStaysOnLarkCLI(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, &config.Config{}, "ws://127.0.0.1:18789/v1/gateway", true)
	m.step = larkStepRunningCommand
	m.commandTitle = "配置并登录 lark-cli"
	m.commandNext = larkStepLocalGatewayRestart

	progress := plainANSI(m.renderProgress())
	if !strings.Contains(progress, "3 lark-cli") {
		t.Fatalf("progress should stay on lark-cli step: %q", progress)
	}
}

func TestLarkInitModelGatewayRestartProgressMatchesSourceStep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, &config.Config{}, "ws://127.0.0.1:18789/v1/gateway", true)
	m.step = larkStepLocalGatewayRestart
	m.commandTitle = "配置 OpenClaw tools.alsoAllow"
	if progress := plainANSI(m.renderProgress()); !strings.Contains(progress, "2 OpenClaw") {
		t.Fatalf("OpenClaw setup restart should stay on OpenClaw step: %q", progress)
	}
	m.commandTitle = "安装 lark-cli"
	if progress := plainANSI(m.renderProgress()); !strings.Contains(progress, "3 lark-cli") {
		t.Fatalf("lark-cli setup restart should stay on lark-cli step: %q", progress)
	}
}

func TestLarkInitModelRemoteIntroTextStaysReadable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, &config.Config{}, "wss://openclaw.example.com/v1/gateway", false)

	assertPlainLineMax(t, m.remoteIntroText(), 86)
	if strings.Contains(m.remoteIntroText(), `["exec"`) {
		t.Fatalf("remote intro should not render long JSON inline: %q", m.remoteIntroText())
	}
}

func TestLarkInitModelRunningGatewayRestartKeepsSourceProgress(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, &config.Config{}, "ws://127.0.0.1:18789/v1/gateway", true)
	m.step = larkStepLocalGatewayRestart
	m.commandTitle = "安装 lark-cli"

	next, _ := m.runCommand("重启 OpenClaw Gateway", "openclaw gateway restart", larkStepCheckingDoctor, nil)
	got := next.(*larkInitModel)
	progress := plainANSI(got.renderProgress())
	if !strings.Contains(progress, "3 lark-cli") {
		t.Fatalf("gateway restart launched from lark-cli setup should keep lark-cli progress: %q", progress)
	}
}

func TestLarkInitModelFinishProgressShowsAllStepsComplete(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, &config.Config{}, "ws://127.0.0.1:18789/v1/gateway", true)
	m.prepareFinish()

	progress := plainANSI(m.renderProgress())
	if strings.Contains(progress, "6 测试") {
		t.Fatalf("finish progress should not leave test step active: %q", progress)
	}
	if !strings.Contains(progress, "✓ 测试") {
		t.Fatalf("finish progress should mark test complete: %q", progress)
	}
}

func TestPromptLineReturnsWhenContextCancelled(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var out bytes.Buffer
	started := time.Now()
	_, err := promptLine(ctx, bufio.NewReader(reader), &out, "Name", "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want context.Canceled", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("promptLine did not return promptly after cancellation")
	}
}

func plainANSI(value string) string {
	return testANSIRe.ReplaceAllString(value, "")
}

func assertPlainLineMax(t *testing.T, value string, max int) {
	t.Helper()
	for _, line := range strings.Split(plainANSI(value), "\n") {
		if len([]rune(line)) > max {
			t.Fatalf("line too long: len=%d max=%d line=%q", len([]rune(line)), max, line)
		}
	}
}

func TestIsLocalOpenClawGatewayURL(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"ws://127.0.0.1:18789/v1/gateway", true},
		{"ws://localhost:18789/v1/gateway", true},
		{"ws://[::1]:18789/v1/gateway", true},
		{"wss://openclaw.example.com/v1/gateway", false},
		{"ws://100.64.0.10:18789/v1/gateway", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isLocalOpenClawGatewayURL(tc.raw); got != tc.want {
			t.Fatalf("isLocalOpenClawGatewayURL(%q)=%v, want %v", tc.raw, got, tc.want)
		}
	}
}
