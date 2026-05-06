package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"termind/internal/gateway"
	"termind/internal/identity"
	"termind/internal/pairing"
)

var (
	initTUIAccentStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true)
	initTUITitleStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true)
	initTUISubtleStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	initTUISuccessStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	initTUIErrorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	initTUISelectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true)
	initTUICardStyle     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240")).Padding(1, 2)
	initTUICodeStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Background(lipgloss.Color("236")).Padding(0, 1)
)

type initTUIStep int

const (
	initTUIStepMode initTUIStep = iota
	initTUIStepGeneratingLocal
	initTUIStepSetupInput
	initTUIStepApproving
)

type initOpenClawTUIModel struct {
	ctx             context.Context
	cancel          context.CancelFunc
	identity        *identity.Identity
	timeout         time.Duration
	mode            initOpenClawMode
	selectedMode    int
	manualSetupCode bool
	setup           *pairing.SetupCode
	setupInput      textinput.Model
	spinner         spinner.Model
	step            initTUIStep
	approvalLogs    []string
	errorText       string
	result          *initOpenClawConnection
	err             error
	statusCh        <-chan string
	setupRefreshes  int
}

type initLocalSetupResultMsg struct {
	setup *pairing.SetupCode
	err   error
}

type initApprovalStatusMsg string

type initApprovalStatusClosedMsg struct{}

type initApprovalDoneMsg struct {
	path string
	err  error
}

type initExternalCancelMsg struct{}

func runInitOpenClawTUI(ctx context.Context, cmd *cobra.Command, id *identity.Identity, timeout time.Duration) (*initOpenClawConnection, error) {
	if strings.TrimSpace(initSetupCode) != "" {
		setup, err := pairing.ParseSetupCode(initSetupCode)
		if err != nil {
			return nil, err
		}
		mode := initOpenClawRemote
		if isLocalOpenClawGatewayURL(gateway.NormalizeGatewayURL(setup.URL)) {
			mode = initOpenClawLocal
		}
		return runInitOpenClawApprovalTUI(ctx, cmd, id, timeout, setup, mode)
	}

	tuiCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	model := newInitOpenClawTUIModel(tuiCtx, cancel, id, timeout)
	program := tea.NewProgram(model, initTUIProgramOptions(cmd.InOrStdin(), cmd.OutOrStdout())...)
	finalModel, err := program.Run()
	if err != nil {
		return nil, err
	}
	m, ok := finalModel.(*initOpenClawTUIModel)
	if !ok {
		return nil, errors.New("init tui returned unexpected model")
	}
	if m.err != nil {
		return nil, m.err
	}
	if m.result == nil {
		return nil, context.Canceled
	}
	return m.result, nil
}

func runInitOpenClawApprovalTUI(ctx context.Context, cmd *cobra.Command, id *identity.Identity, timeout time.Duration, setup *pairing.SetupCode, mode initOpenClawMode) (*initOpenClawConnection, error) {
	tuiCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	model := newInitOpenClawTUIModel(tuiCtx, cancel, id, timeout)
	model.mode = mode
	model.setup = setup
	model.step = initTUIStepApproving
	program := tea.NewProgram(model, initTUIProgramOptions(cmd.InOrStdin(), cmd.OutOrStdout())...)
	finalModel, err := program.Run()
	if err != nil {
		return nil, err
	}
	m, ok := finalModel.(*initOpenClawTUIModel)
	if !ok {
		return nil, errors.New("init tui returned unexpected model")
	}
	if m.err != nil {
		return nil, m.err
	}
	if m.result == nil {
		return nil, context.Canceled
	}
	return m.result, nil
}

func initTUIProgramOptions(in io.Reader, out io.Writer) []tea.ProgramOption {
	return []tea.ProgramOption{tea.WithInput(in), tea.WithOutput(out), tea.WithAltScreen()}
}

func newInitOpenClawTUIModel(ctx context.Context, cancel context.CancelFunc, id *identity.Identity, timeout time.Duration) *initOpenClawTUIModel {
	selectedMode := 0
	if _, err := exec.LookPath("openclaw"); err != nil {
		selectedMode = 1
	}
	input := textinput.New()
	input.Placeholder = "粘贴 OpenClaw setup code"
	input.CharLimit = 8192
	input.Width = 80
	s := spinner.New()
	s.Spinner = spinner.Dot
	return &initOpenClawTUIModel{
		ctx:             ctx,
		cancel:          cancel,
		identity:        id,
		timeout:         timeout,
		selectedMode:    selectedMode,
		manualSetupCode: initManualSetupCode,
		setupInput:      input,
		spinner:         s,
		step:            initTUIStepMode,
	}
}

func (m *initOpenClawTUIModel) Init() tea.Cmd {
	cmds := []tea.Cmd{initWatchContextCmd(m.ctx)}
	if m.step == initTUIStepApproving && m.setup != nil {
		cmds = append(cmds, m.startApprovalCmd(), m.spinner.Tick)
	}
	return tea.Batch(cmds...)
}

func (m *initOpenClawTUIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case initExternalCancelMsg:
		m.err = context.Canceled
		m.cancel()
		return m, tea.Quit
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" || msg.String() == "esc" {
			m.err = context.Canceled
			m.cancel()
			return m, tea.Quit
		}
		switch m.step {
		case initTUIStepMode:
			return m.updateMode(msg)
		case initTUIStepSetupInput:
			return m.updateSetupInput(msg)
		}
	case spinner.TickMsg:
		if m.step == initTUIStepGeneratingLocal || m.step == initTUIStepApproving {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
	case initLocalSetupResultMsg:
		if msg.err != nil {
			m.errorText = fmt.Sprintf("本机 OpenClaw setup code 自动生成失败: %v", msg.err)
			m.setupInput.Focus()
			m.step = initTUIStepSetupInput
			return m, nil
		}
		m.setup = msg.setup
		m.mode = initOpenClawLocal
		m.step = initTUIStepApproving
		return m, tea.Batch(m.startApprovalCmd(), m.spinner.Tick)
	case initApprovalStatusMsg:
		m.appendApprovalLog(string(msg))
		return m, initApprovalStatusCmd(m.ctx, m.statusCh)
	case initApprovalStatusClosedMsg:
		return m, nil
	case initApprovalDoneMsg:
		if msg.err != nil {
			if isExpiredBootstrapTokenError(msg.err) && m.mode == initOpenClawLocal && !m.manualSetupCode && m.setupRefreshes == 0 {
				m.setupRefreshes++
				m.approvalLogs = []string{"setup code 已过期,正在重新生成 fresh setup code..."}
				m.errorText = ""
				m.step = initTUIStepGeneratingLocal
				return m, tea.Batch(initGenerateLocalSetupCmd(m.ctx), m.spinner.Tick)
			}
			m.err = msg.err
			return m, tea.Quit
		}
		m.result = &initOpenClawConnection{Setup: m.setup, Mode: m.mode, AuthPath: msg.path}
		return m, tea.Quit
	}
	return m, nil
}

func (m *initOpenClawTUIModel) updateMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k", "down", "j", "tab":
		if m.selectedMode == 0 {
			m.selectedMode = 1
		} else {
			m.selectedMode = 0
		}
	case "enter":
		m.errorText = ""
		if m.selectedMode == 0 {
			m.mode = initOpenClawLocal
			if m.manualSetupCode {
				m.setupInput.Focus()
				m.step = initTUIStepSetupInput
				return m, nil
			}
			m.step = initTUIStepGeneratingLocal
			return m, tea.Batch(initGenerateLocalSetupCmd(m.ctx), m.spinner.Tick)
		}
		m.mode = initOpenClawRemote
		m.setupInput.Focus()
		m.step = initTUIStepSetupInput
	}
	return m, nil
}

func (m *initOpenClawTUIModel) updateSetupInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "enter" {
		value := strings.TrimSpace(m.setupInput.Value())
		if value == "" {
			m.errorText = "OpenClaw setup code 不能为空"
			return m, nil
		}
		setup, err := pairing.ParseSetupCode(value)
		if err != nil {
			m.errorText = fmt.Sprintf("setup code 解析失败: %v", err)
			return m, nil
		}
		m.setup = setup
		m.errorText = ""
		m.step = initTUIStepApproving
		return m, tea.Batch(m.startApprovalCmd(), m.spinner.Tick)
	}
	var cmd tea.Cmd
	m.setupInput, cmd = m.setupInput.Update(msg)
	return m, cmd
}

func (m *initOpenClawTUIModel) View() string {
	body := ""
	switch m.step {
	case initTUIStepMode:
		body = m.renderModeStep()
	case initTUIStepGeneratingLocal:
		body = m.renderGenerateStep()
	case initTUIStepSetupInput:
		body = m.renderSetupInputStep()
	case initTUIStepApproving:
		body = m.renderApprovalStep()
	}
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(initTUITitleStyle.Render("Termind 初始化"))
	b.WriteString("\n")
	b.WriteString(initTUISubtleStyle.Render("先把这台机器接入 OpenClaw,之后 shell 才能自动诊断和转发。"))
	b.WriteString("\n\n")
	b.WriteString(m.renderProgress())
	b.WriteString("\n\n")
	b.WriteString(initTUICardStyle.Width(82).Render(body))
	if m.errorText != "" {
		b.WriteString("\n")
		b.WriteString(initTUIErrorStyle.Render("✗ " + m.errorText))
		b.WriteString("\n")
	}
	b.WriteString(m.renderHelp())
	return b.String()
}

func (m *initOpenClawTUIModel) renderModeStep() string {
	var b strings.Builder
	b.WriteString(initTUIAccentStyle.Render("OpenClaw 在哪里运行?"))
	b.WriteString("\n")
	b.WriteString(initTUISubtleStyle.Render("选择后 termind 会准备连接码,并等待你在 OpenClaw 里批准这台设备。"))
	b.WriteString("\n\n")
	b.WriteString(m.renderModeOption(0, "本机 OpenClaw", "自动生成 setup code,适合 OpenClaw 就跑在这台电脑上。"))
	b.WriteString("\n")
	b.WriteString(m.renderModeOption(1, "远程 OpenClaw", "在远程机器生成 setup code,这里粘贴后继续配对。"))
	return b.String()
}

func (m *initOpenClawTUIModel) renderGenerateStep() string {
	return strings.Join([]string{
		initTUIAccentStyle.Render("正在准备连接码"),
		initTUISubtleStyle.Render("termind 正在调用本机 openclaw 生成 setup code。"),
		"",
		fmt.Sprintf("%s %s", m.spinner.View(), "检查 openclaw 并生成 setup code..."),
		"",
		initTUISubtleStyle.Render("如果自动生成失败,下一屏会让你手动粘贴 setup code。"),
	}, "\n")
}

func (m *initOpenClawTUIModel) renderSetupInputStep() string {
	var b strings.Builder
	b.WriteString(initTUIAccentStyle.Render("粘贴 OpenClaw setup code"))
	b.WriteString("\n")
	if m.mode == initOpenClawRemote {
		b.WriteString(initTUISubtleStyle.Render("请先在 OpenClaw 所在机器生成连接码,再粘贴到这里。"))
		b.WriteString("\n\n")
		b.WriteString("推荐命令\n")
		b.WriteString(renderInitCommandLines(
			"openclaw qr --setup-code-only \\",
			"  --url ws://<OpenClaw地址>:18789/v1/gateway",
		))
	} else {
		b.WriteString(initTUISubtleStyle.Render("自动生成没有完成,你也可以手动生成后粘贴。"))
		b.WriteString("\n\n")
		b.WriteString("推荐命令\n")
		b.WriteString(renderInitCommandLines(
			"openclaw qr --setup-code-only \\",
			"  --url ws://127.0.0.1:18789/v1/gateway",
		))
	}
	b.WriteString("\n\n")
	b.WriteString("setup code\n")
	b.WriteString(m.setupInput.View())
	return b.String()
}

func (m *initOpenClawTUIModel) renderApprovalStep() string {
	var b strings.Builder
	b.WriteString(initTUIAccentStyle.Render("等待 OpenClaw 批准"))
	b.WriteString("\n")
	b.WriteString(initTUISubtleStyle.Render("请在 OpenClaw 里批准这台设备,批准后 termind 会自动继续。"))
	b.WriteString("\n\n")
	b.WriteString(m.renderDeviceSummary())
	if m.setup != nil {
		b.WriteString("\n")
		b.WriteString("Server     ")
		b.WriteString(initTUISubtleStyle.Render(gateway.NormalizeGatewayURL(m.setup.URL)))
	}
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("%s 等待批准中...", m.spinner.View()))
	if len(m.approvalLogs) > 0 {
		b.WriteString("\n\n")
		b.WriteString("批准状态\n")
		for _, line := range m.approvalLogs {
			if line == "" {
				continue
			}
			b.WriteString(initTUISubtleStyle.Render("  "+line) + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *initOpenClawTUIModel) renderModeOption(index int, title string, detail string) string {
	prefix := "  ○ "
	titleStyle := lipgloss.NewStyle()
	if m.selectedMode == index {
		prefix = initTUISelectedStyle.Render("  ● ")
		titleStyle = initTUISelectedStyle
	}
	return fmt.Sprintf("%s%s\n     %s", prefix, titleStyle.Render(title), initTUISubtleStyle.Render(detail))
}

func renderInitCommandLines(lines ...string) string {
	rendered := make([]string, 0, len(lines))
	for _, line := range lines {
		rendered = append(rendered, initTUICodeStyle.Render(line))
	}
	return strings.Join(rendered, "\n")
}

func (m *initOpenClawTUIModel) renderProgress() string {
	current := 1
	switch m.step {
	case initTUIStepGeneratingLocal, initTUIStepSetupInput:
		current = 2
	case initTUIStepApproving:
		current = 3
	}
	labels := []string{"选择 OpenClaw", "准备连接码", "批准设备"}
	parts := make([]string, 0, len(labels))
	for i, label := range labels {
		n := i + 1
		switch {
		case n < current:
			parts = append(parts, initTUISuccessStyle.Render("✓ "+label))
		case n == current:
			parts = append(parts, initTUIAccentStyle.Render(fmt.Sprintf("%d %s", n, label)))
		default:
			parts = append(parts, initTUISubtleStyle.Render(fmt.Sprintf("%d %s", n, label)))
		}
	}
	return strings.Join(parts, initTUISubtleStyle.Render("  ──  "))
}

func (m *initOpenClawTUIModel) renderDeviceSummary() string {
	if m.identity == nil {
		return ""
	}
	return strings.Join([]string{
		"Device ID  " + initTUISubtleStyle.Render(m.identity.DeviceID()),
		"Fingerprint " + initTUISubtleStyle.Render(m.identity.Fingerprint()),
	}, "\n")
}

func (m *initOpenClawTUIModel) renderHelp() string {
	switch m.step {
	case initTUIStepMode:
		return "\n" + initTUISubtleStyle.Render("↑/↓ 选择   Enter 继续   Ctrl+C 取消") + "\n"
	case initTUIStepSetupInput:
		return "\n" + initTUISubtleStyle.Render("Enter 继续   Ctrl+C 取消") + "\n"
	default:
		return "\n" + initTUISubtleStyle.Render("Ctrl+C 取消") + "\n"
	}
}

func (m *initOpenClawTUIModel) appendApprovalLog(message string) {
	m.approvalLogs = append(m.approvalLogs, message)
	if len(m.approvalLogs) > 8 {
		m.approvalLogs = m.approvalLogs[len(m.approvalLogs)-8:]
	}
}

func (m *initOpenClawTUIModel) startApprovalCmd() tea.Cmd {
	statusCh := make(chan string, 8)
	m.statusCh = statusCh
	return tea.Batch(initWaitApprovalCmd(m.ctx, m.setup, m.identity, m.timeout, statusCh), initApprovalStatusCmd(m.ctx, statusCh))
}

func initGenerateLocalSetupCmd(ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		localCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		setup, err := generateLocalOpenClawSetupCode(localCtx)
		return initLocalSetupResultMsg{setup: setup, err: err}
	}
}

func initWaitApprovalCmd(ctx context.Context, setup *pairing.SetupCode, id *identity.Identity, timeout time.Duration, statusCh chan<- string) tea.Cmd {
	return func() tea.Msg {
		path, err := waitForDeviceApprovalWithStatus(ctx, setup, id, timeout, func(message string) {
			select {
			case statusCh <- message:
			case <-ctx.Done():
			}
		})
		close(statusCh)
		return initApprovalDoneMsg{path: path, err: err}
	}
}

func initApprovalStatusCmd(ctx context.Context, statusCh <-chan string) tea.Cmd {
	return func() tea.Msg {
		select {
		case message, ok := <-statusCh:
			if !ok {
				return initApprovalStatusClosedMsg{}
			}
			return initApprovalStatusMsg(message)
		case <-ctx.Done():
			return initExternalCancelMsg{}
		}
	}
}

func initWatchContextCmd(ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		<-ctx.Done()
		return initExternalCancelMsg{}
	}
}
