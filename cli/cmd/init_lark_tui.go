package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"termind/internal/config"
	"termind/internal/diagnose"
	"termind/internal/gateway"
)

type larkInitStep int

const (
	larkStepEnable larkInitStep = iota
	larkStepIdentitySources
	larkStepLoadingOpenClawBotAccounts
	larkStepSelectOpenClawBots
	larkStepConfiguringIdentity
	larkStepUserOAuthAppChoice
	larkStepUserOAuthLabel
	larkStepUserOAuthConfiguring
	larkStepForwardIdentitySelect
	larkStepLoadingIdentityChats
	larkStepSelectIdentityChats
	larkStepSavingOpenClawConfig
	larkStepCheckingDoctor
	larkStepDoctorFailed
	larkStepLocalInstallLarkCLI
	larkStepLocalConfigureLarkCLI
	larkStepLarkConfigBindAppID
	larkStepLarkConfigBinding
	larkStepLarkBotLoginInstruction
	larkStepLocalAuthLogin
	larkStepLocalAuthLoginStarting
	larkStepLocalAuthLoginWaiting
	larkStepProfileChoice
	larkStepSwitchingProfile
	larkStepKeepExisting
	larkStepAddSelf
	larkStepSearchChats
	larkStepChatQuery
	larkStepSearchingChats
	larkStepSelectChats
	larkStepSearchUsers
	larkStepUserQuery
	larkStepSearchingUsers
	larkStepSelectUsers
	larkStepManualChatID
	larkStepManualChatLabel
	larkStepManualPersonID
	larkStepManualPersonType
	larkStepManualPersonLabel
	larkStepTestTargets
	larkStepTestingTargets
	larkStepSaving
	larkStepLocalInstallPlugin
	larkStepLocalPluginSpec
	larkStepLocalToolsAllow
	larkStepLocalExecAllow
	larkStepLocalGatewayRestart
	larkStepRemoteIntro
	larkStepRemoteSSH
	larkStepRemoteTarget
	larkStepRemoteLarkConfig
	larkStepRemoteDoctor
	larkStepRemotePlugin
	larkStepRemotePluginSpec
	larkStepRemoteTools
	larkStepRemoteApprovals
	larkStepRemoteGatewayRestart
	larkStepRunningCommand
	larkStepFinish
)

type larkInitModel struct {
	ctx                  context.Context
	cancel               context.CancelFunc
	in                   io.Reader
	out                  io.Writer
	openClawGatewayURL   string
	localOpenClaw        bool
	cfg                  *config.Config
	openClawCLI          string
	sshCLI               string
	sender               string
	userOpenID           string
	profiles             []diagnose.LarkCLIProfile
	selectedProfile      string
	larkConfigBindAppID  string
	selectedSources      map[string]bool
	identities           map[string]diagnose.LarkForwardingIdentity
	identityOrder        []string
	selectedIdentityIDs  []string
	routes               []diagnose.LarkForwardingRoute
	pendingIdentity      diagnose.LarkForwardingIdentity
	pendingOAuthApp      diagnose.LarkForwardingIdentity
	currentIdentityIndex int
	currentIdentityID    string
	secretInput          bool
	targets              []larkTargetChoice
	choices              []larkTargetChoice
	selected             map[int]bool
	selectedIndex        int
	input                textinput.Model
	spinner              spinner.Model
	step                 larkInitStep
	yes                  bool
	errorText            string
	notices              []string
	pendingChatID        string
	pendingPersonID      string
	pendingPersonType    string
	remoteTarget         string
	commandTitle         string
	commandDetail        string
	commandNext          larkInitStep
	commandProgress      int
	commandRetry         larkInitStep
	commandRetrySet      bool
	larkLoginURL         string
	larkLoginUserCode    string
	larkLoginDeviceCode  string
	larkLoginExpiresIn   int
	larkLoginMessage     string
	openClawNeedsRestart bool
	openClawSetupDone    bool
	openClawSetupNext    larkInitStep
	doctorFailureKind    string
	doctorFailureDetail  string
	savedPath            string
	done                 bool
	err                  error
}

type larkExternalCancelMsg struct{}

type larkDoctorDoneMsg struct {
	status *diagnose.LarkCLIStatus
	output []byte
	err    error
}

type larkChoicesDoneMsg struct {
	kind    string
	choices []larkTargetChoice
	err     error
}

type larkCommandDoneMsg struct {
	label        string
	text         string
	err          error
	next         larkInitStep
	needsRestart bool
}

type larkExecDoneMsg struct {
	label string
	next  larkInitStep
	err   error
}

type larkProfileUseDoneMsg struct {
	profile string
	result  *diagnose.LarkCLIProfileUseResult
	err     error
}

type larkConfigBindDoneMsg struct {
	appID  string
	result *diagnose.LarkCLIConfigBindResult
	err    error
}

type larkAuthLoginStartDoneMsg struct {
	identity diagnose.LarkForwardingIdentity
	result   *diagnose.LarkCLIAuthLoginStartResult
	err      error
}

type larkAuthLoginCompleteDoneMsg struct {
	identity diagnose.LarkForwardingIdentity
	result   *diagnose.LarkCLIAuthLoginCompleteResult
	err      error
}

type larkOpenClawBotAccountsDoneMsg struct {
	accounts []larkTargetChoice
	err      error
}

type larkIdentityDoneMsg struct {
	identity diagnose.LarkForwardingIdentity
	err      error
}

type larkIdentitiesDoneMsg struct {
	identities []diagnose.LarkForwardingIdentity
	err        error
}

type larkIdentityChatsDoneMsg struct {
	identityID string
	choices    []larkTargetChoice
	err        error
}

var larkDoctorRetryDelays = []time.Duration{500 * time.Millisecond, 1200 * time.Millisecond}

func runLarkInitBubbleTea(ctx context.Context, in io.Reader, out io.Writer, openClawGatewayURL string, localOpenClaw bool) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	tuiCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	model := newLarkInitModel(tuiCtx, cancel, in, out, cfg, openClawGatewayURL, localOpenClaw)
	program := tea.NewProgram(model, initTUIProgramOptions(in, out)...)
	finalModel, err := program.Run()
	if err != nil {
		return err
	}
	m, ok := finalModel.(*larkInitModel)
	if !ok {
		return errors.New("lark init tui returned unexpected model")
	}
	if m.err != nil {
		return m.err
	}
	return nil
}

func newLarkInitModel(ctx context.Context, cancel context.CancelFunc, in io.Reader, out io.Writer, cfg *config.Config, openClawGatewayURL string, localOpenClaw bool) *larkInitModel {
	input := textinput.New()
	input.Width = 72
	s := spinner.New()
	s.Spinner = spinner.Dot
	openClawCLI, _ := exec.LookPath("openclaw")
	sshCLI, _ := exec.LookPath("ssh")
	sender := cfg.Lark.Sender
	if sender == "" {
		sender = "bot"
	}
	m := &larkInitModel{
		ctx:                ctx,
		cancel:             cancel,
		in:                 in,
		out:                out,
		openClawGatewayURL: openClawGatewayURL,
		localOpenClaw:      localOpenClaw,
		cfg:                cfg,
		openClawCLI:        openClawCLI,
		sshCLI:             sshCLI,
		sender:             normalizeSender(sender),
		userOpenID:         strings.TrimSpace(cfg.Lark.UserOpenID),
		input:              input,
		spinner:            s,
		step:               larkStepEnable,
		yes:                true,
		openClawSetupNext:  larkStepFinish,
		selectedSources:    map[string]bool{},
		identities:         map[string]diagnose.LarkForwardingIdentity{},
	}
	return m
}

func (m *larkInitModel) Init() tea.Cmd {
	return larkWatchContextCmd(m.ctx)
}

func (m *larkInitModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case larkExternalCancelMsg:
		m.err = context.Canceled
		m.cancel()
		return m, tea.Quit
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" || msg.String() == "esc" {
			m.err = context.Canceled
			m.cancel()
			return m, tea.Quit
		}
		if m.isLoadingStep() {
			return m, nil
		}
		switch {
		case m.isConfirmStep():
			return m.updateConfirm(msg)
		case m.isInputStep():
			return m.updateInput(msg)
		case m.isChoiceStep():
			return m.updateChoice(msg)
		case m.isMultiSelectStep():
			return m.updateMultiSelect(msg)
		case m.step == larkStepFinish:
			if msg.String() == "enter" {
				return m, tea.Quit
			}
		}
	case spinner.TickMsg:
		if m.isLoadingStep() {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
	case larkDoctorDoneMsg:
		return m.handleDoctorDone(msg)
	case larkChoicesDoneMsg:
		return m.handleChoicesDone(msg)
	case larkCommandDoneMsg:
		return m.handleCommandDone(msg)
	case larkOpenClawBotAccountsDoneMsg:
		if msg.err != nil {
			m.addNotice(fmt.Sprintf("✗ 读取 OpenClaw lark-cli bot profiles: %v", msg.err))
			return m.afterSourceOpenClawBots()
		}
		if len(msg.accounts) == 0 {
			msg.accounts = larkBotProfileChoices(nil)
		}
		m.choices = msg.accounts
		m.selected = map[int]bool{}
		m.selectedIndex = 0
		m.step = larkStepSelectOpenClawBots
		return m, nil
	case larkIdentityDoneMsg:
		if msg.err != nil {
			m.addNotice(fmt.Sprintf("✗ 配置 Lark 身份: %v", msg.err))
		} else {
			m.addIdentity(msg.identity)
			m.addNotice("✓ Lark 身份: " + msg.identity.Label)
		}
		return m.afterIdentityConfigured()
	case larkIdentitiesDoneMsg:
		if msg.err != nil {
			m.addNotice(fmt.Sprintf("✗ 配置 Lark 身份: %v", msg.err))
		}
		for _, identity := range msg.identities {
			m.addIdentity(identity)
			m.addNotice("✓ Lark 身份: " + identity.Label)
		}
		return m.afterIdentityConfigured()
	case larkIdentityChatsDoneMsg:
		if msg.err != nil {
			m.addNotice(fmt.Sprintf("✗ 读取身份可见群聊: %v", msg.err))
			return m.prepareNextIdentityChats()
		}
		m.currentIdentityID = msg.identityID
		m.choices = msg.choices
		m.selected = map[int]bool{}
		m.selectedIndex = 0
		m.step = larkStepSelectIdentityChats
		return m, nil
	case larkConfigBindDoneMsg:
		if msg.err != nil {
			m.addNotice(fmt.Sprintf("✗ 绑定 OpenClaw lark-cli bot profile: %v", msg.err))
			next, _ := m.prepareLarkConfigBindAppID()
			return next, nil
		}
		if msg.result != nil && !msg.result.OK {
			detail := strings.Join(msg.result.Errors, "; ")
			if detail == "" {
				detail = msg.result.Output
			}
			if detail == "" {
				detail = "lark-cli config bind failed"
			}
			m.addNotice("✗ 绑定 OpenClaw lark-cli bot profile: " + detail)
			next, _ := m.prepareLarkConfigBindAppID()
			return next, nil
		}
		if strings.TrimSpace(msg.appID) != "" {
			m.larkConfigBindAppID = strings.TrimSpace(msg.appID)
			m.selectedProfile = strings.TrimSpace(msg.appID)
		}
		m.sender = "bot"
		m.addNotice("✓ OpenClaw lark-cli config bind")
		return m.goToStep(larkStepCheckingDoctor)
	case larkAuthLoginStartDoneMsg:
		if msg.err != nil {
			m.addNotice(fmt.Sprintf("✗ 启动 OpenClaw lark-cli 登录: %v", msg.err))
			if strings.TrimSpace(msg.identity.ID) != "" {
				return m.afterIdentityConfigured()
			}
			return m.prepareProfileChoice(), nil
		}
		if msg.result == nil || !msg.result.OK || strings.TrimSpace(msg.result.DeviceCode) == "" {
			detail := "OpenClaw lark-cli auth login did not return a device code"
			if msg.result != nil {
				if joined := strings.Join(msg.result.Errors, "; "); joined != "" {
					detail = joined
				} else if msg.result.Output != "" {
					detail = msg.result.Output
				}
			}
			m.addNotice("✗ 启动 OpenClaw lark-cli 登录: " + detail)
			if strings.TrimSpace(msg.identity.ID) != "" {
				return m.afterIdentityConfigured()
			}
			return m.prepareProfileChoice(), nil
		}
		m.pendingIdentity = msg.identity
		m.setLarkLogin(msg.result)
		m.step = larkStepLocalAuthLoginWaiting
		if strings.TrimSpace(msg.identity.ID) != "" {
			return m, tea.Batch(m.spinner.Tick, larkAuthLoginCompleteCmdForIdentity(m.ctx, m.openClawGatewayURL, msg.identity, msg.result.DeviceCode))
		}
		return m, tea.Batch(m.spinner.Tick, larkAuthLoginCompleteCmd(m.ctx, m.openClawGatewayURL, msg.result.DeviceCode))
	case larkAuthLoginCompleteDoneMsg:
		if msg.err != nil {
			m.addNotice(fmt.Sprintf("✗ OpenClaw lark-cli 登录: %v", msg.err))
			if strings.TrimSpace(msg.identity.ID) != "" {
				return m.afterIdentityConfigured()
			}
			return m.prepareProfileChoice(), nil
		}
		if msg.result != nil && !msg.result.OK {
			detail := strings.Join(msg.result.Errors, "; ")
			if detail == "" {
				detail = msg.result.Output
			}
			if detail == "" {
				detail = "OpenClaw lark-cli auth login failed"
			}
			m.addNotice("✗ OpenClaw lark-cli 登录: " + detail)
			if strings.TrimSpace(msg.identity.ID) != "" {
				return m.afterIdentityConfigured()
			}
			return m.prepareProfileChoice(), nil
		}
		m.addNotice("✓ OpenClaw lark-cli auth login")
		m.clearLarkLogin()
		if strings.TrimSpace(msg.identity.ID) != "" {
			m.addIdentity(msg.identity)
			return m.afterIdentityConfigured()
		}
		return m.goToStep(larkStepCheckingDoctor)
	case larkProfileUseDoneMsg:
		if msg.err != nil {
			m.addNotice(fmt.Sprintf("✗ 切换 OpenClaw lark-cli profile: %v", msg.err))
			return m.prepareProfileChoice(), nil
		}
		if msg.result != nil && !msg.result.OK {
			detail := strings.Join(msg.result.Errors, "; ")
			if detail == "" {
				detail = msg.result.Output
			}
			if detail == "" {
				detail = "profile switch failed"
			}
			m.addNotice("✗ 切换 OpenClaw lark-cli profile: " + detail)
			return m.prepareProfileChoice(), nil
		}
		if strings.TrimSpace(msg.profile) != "" {
			m.selectedProfile = strings.TrimSpace(msg.profile)
			m.sender = senderForProfile(m.findProfile(msg.profile), m.sender)
			m.addNotice("✓ OpenClaw lark-cli profile: " + m.selectedProfile)
		}
		return m.prepareKeepExisting()
	case larkExecDoneMsg:
		if msg.err != nil {
			m.addNotice(fmt.Sprintf("✗ %s: %v", msg.label, msg.err))
		} else {
			m.addNotice("✓ " + msg.label)
		}
		return m.goToStep(msg.next)
	}
	return m, nil
}

func (m *larkInitModel) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "left", "right", "up", "down", "h", "l", "j", "k", "tab", " ":
		m.yes = !m.yes
	case "y", "Y", "是", "好":
		m.yes = true
		return m.advanceConfirm()
	case "n", "N", "否", "不":
		m.yes = false
		return m.advanceConfirm()
	case "enter":
		return m.advanceConfirm()
	}
	return m, nil
}

func (m *larkInitModel) updateInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "enter" {
		value := strings.TrimSpace(m.input.Value())
		return m.advanceInput(value)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *larkInitModel) updateChoice(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	max := 2
	if m.step == larkStepProfileChoice {
		max = len(m.profileChoices())
	} else if m.step == larkStepUserOAuthAppChoice {
		max = len(m.identityOrder)
	} else if m.step == larkStepManualPersonType {
		max = 2
	}
	if max <= 0 {
		return m, nil
	}
	switch msg.String() {
	case "up", "k", "left", "h":
		m.selectedIndex--
		if m.selectedIndex < 0 {
			m.selectedIndex = max - 1
		}
	case "down", "j", "right", "l", "tab":
		m.selectedIndex++
		if m.selectedIndex >= max {
			m.selectedIndex = 0
		}
	case "enter":
		return m.advanceChoice()
	}
	return m, nil
}

func (m *larkInitModel) updateMultiSelect(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if len(m.choices) == 0 {
		return m, nil
	}
	switch msg.String() {
	case "up", "k":
		m.selectedIndex--
		if m.selectedIndex < 0 {
			m.selectedIndex = len(m.choices) - 1
		}
	case "down", "j":
		m.selectedIndex++
		if m.selectedIndex >= len(m.choices) {
			m.selectedIndex = 0
		}
	case " ":
		m.selected[m.selectedIndex] = !m.selected[m.selectedIndex]
	case "enter":
		if m.step == larkStepIdentitySources {
			return m.afterIdentitySourceSelect()
		}
		if m.step == larkStepSelectOpenClawBots {
			return m.configureSelectedOpenClawBots()
		}
		if m.step == larkStepForwardIdentitySelect {
			return m.afterForwardIdentitySelect()
		}
		if m.step == larkStepSelectIdentityChats {
			return m.afterIdentityChatSelect()
		}
		for i, choice := range m.choices {
			if m.selected[i] {
				m.targets = appendTarget(m.targets, choice)
			}
		}
		if m.step == larkStepSelectChats {
			return m.prepareSearchUsers()
		}
		return m.prepareManualChatID(), nil
	}
	return m, nil
}

func (m *larkInitModel) advanceConfirm() (tea.Model, tea.Cmd) {
	switch m.step {
	case larkStepEnable:
		if !m.yes {
			m.done = true
			return m, tea.Quit
		}
		m.addNotice("先准备 OpenClaw 插件和工具权限。")
		if m.localOpenClaw {
			return m.prepareLocalOpenClawSetup(larkStepIdentitySources)
		}
		m.openClawSetupNext = larkStepIdentitySources
		return m.prepareRemoteIntro(), nil
	case larkStepDoctorFailed:
		if !m.yes {
			m.prepareFinish()
			return m, nil
		}
		m.addNotice("重新通过 OpenClaw 检查运行端 lark-cli。")
		return m.goToStep(larkStepCheckingDoctor)
	case larkStepLocalInstallLarkCLI:
		if !m.yes {
			m.prepareFinish()
			return m, nil
		}
		m.openClawSetupNext = larkStepCheckingDoctor
		return m.runCommand("安装 lark-cli", "npm install -g @larksuite/cli && npx skills add larksuite/cli -y -g", larkStepLocalGatewayRestart, larkInstallLarkCLICmd(m.ctx, larkStepLocalGatewayRestart, larkStepLocalInstallLarkCLI))
	case larkStepLocalConfigureLarkCLI:
		m.addNotice("请在 OpenClaw 运行端完成 lark-cli profile 登录/绑定；Termind 不在本机直接执行 lark-cli。")
		if !m.yes {
			m.prepareFinish()
			return m, nil
		}
		return m.goToStep(larkStepCheckingDoctor)
	case larkStepLarkBotLoginInstruction:
		if !m.yes {
			return m.afterSourceOpenClawBots()
		}
		m.step = larkStepLoadingOpenClawBotAccounts
		return m, tea.Batch(m.spinner.Tick, larkOpenClawBotAccountsCmd(m.ctx, m.openClawGatewayURL, m.openClawLarkCLIConfigDir()))
	case larkStepLocalAuthLogin:
		if !m.yes {
			m.prepareFinish()
			return m, nil
		}
		m.step = larkStepLocalAuthLoginStarting
		m.clearLarkLogin()
		return m, tea.Batch(m.spinner.Tick, larkAuthLoginStartCmd(m.ctx, m.openClawGatewayURL))
	case larkStepKeepExisting:
		if m.yes {
			for _, target := range m.cfg.Lark.Targets {
				if target.Enabled && strings.TrimSpace(target.ID) != "" {
					m.targets = appendTarget(m.targets, larkTargetChoice{Type: target.Type, ID: target.ID, Label: target.Label})
				}
			}
		}
		return m.prepareAddSelf()
	case larkStepAddSelf:
		if m.yes {
			m.targets = appendTarget(m.targets, larkTargetChoice{Type: "user", ID: m.userOpenID, Label: "me"})
		}
		return m.prepareSearchChats()
	case larkStepSearchChats:
		if !m.yes {
			return m.prepareSearchUsers()
		}
		m.setInput(larkStepChatQuery, "群名关键字,留空列出最近群聊", "")
		return m, nil
	case larkStepSearchUsers:
		if !m.yes {
			return m.prepareManualChatID(), nil
		}
		m.setInput(larkStepUserQuery, "用户名关键字", "")
		return m, nil
	case larkStepTestTargets:
		if !m.yes {
			return m.saveConfig()
		}
		m.step = larkStepTestingTargets
		if len(m.routes) > 0 {
			return m, tea.Batch(m.spinner.Tick, larkTestRoutesCmd(m.ctx, m.openClawGatewayURL, m.identities, m.routes))
		}
		return m, tea.Batch(m.spinner.Tick, larkTestTargetsCmd(m.ctx, m.openClawGatewayURL, m.sender, m.targets))
	case larkStepLocalInstallPlugin:
		if !m.yes {
			return m.prepareLocalToolsAllow(), nil
		}
		m.setInput(larkStepLocalPluginSpec, "Termind OpenClaw 插件 npm spec", termindOpenClawPluginSpec)
		return m, nil
	case larkStepLocalToolsAllow:
		if !m.yes {
			return m.prepareLocalExecAllow(), nil
		}
		return m.runCommand("配置 OpenClaw tools.alsoAllow", "openclaw config set tools.alsoAllow", larkStepLocalExecAllow, larkConfigureToolsCmd(m.ctx, larkStepLocalExecAllow))
	case larkStepLocalExecAllow:
		if !m.yes {
			return m.prepareLocalGatewayRestart()
		}
		return m.runCommand("把 lark-cli 加入 OpenClaw exec approvals allowlist", "openclaw approvals allowlist add lark-cli", larkStepLocalGatewayRestart, larkStatusCmd(m.ctx, "openclaw approvals allowlist add", larkStepLocalGatewayRestart, "openclaw", "approvals", "allowlist", "add", "lark-cli"))
	case larkStepLocalGatewayRestart:
		if !m.yes {
			m.openClawSetupDone = true
			m.openClawNeedsRestart = false
			return m.goToStep(m.openClawSetupNext)
		}
		return m.runCommand("重启 OpenClaw Gateway", "openclaw gateway restart", m.openClawSetupNext, larkStatusCmd(m.ctx, "openclaw gateway restart", m.openClawSetupNext, "openclaw", "gateway", "restart"))
	case larkStepRemoteSSH:
		if !m.yes {
			return m.goToStep(larkStepIdentitySources)
		}
		if m.sshCLI == "" {
			m.addNotice("未找到 ssh,无法自动配置远程 OpenClaw。")
			m.prepareFinish()
			return m, nil
		}
		m.setInput(larkStepRemoteTarget, "SSH 目标(user@host,留空跳过)", "")
		return m, nil
	case larkStepRemoteLarkConfig:
		if !m.yes {
			m.prepareFinish()
			return m, nil
		}
		m.step = larkStepLocalAuthLoginStarting
		m.clearLarkLogin()
		return m, tea.Batch(m.spinner.Tick, larkAuthLoginStartCmd(m.ctx, m.openClawGatewayURL))
	case larkStepRemoteDoctor:
		return m.prepareRemotePlugin()
	case larkStepRemotePlugin:
		if !m.yes {
			return m.prepareRemoteTools()
		}
		m.setInput(larkStepRemotePluginSpec, "Termind OpenClaw 插件 npm spec", termindOpenClawPluginSpec)
		return m, nil
	case larkStepRemoteTools:
		if !m.yes {
			return m.prepareRemoteApprovals()
		}
		allowJSON, _ := jsonMarshalString(termindOpenClawAllowedTools)
		remoteCommand := "openclaw config set tools.alsoAllow " + shellQuote(allowJSON) + " --strict-json"
		return m.runCommand("配置远程 OpenClaw tools.alsoAllow", "正在通过 SSH 写入远程 tools.alsoAllow，完成后会配置 allowlist。", larkStepRemoteApprovals, larkRemoteStatusCmd(m.ctx, "remote openclaw config set tools.alsoAllow", larkStepRemoteApprovals, m.remoteTarget, remoteCommand))
	case larkStepRemoteApprovals:
		if !m.yes {
			return m.prepareRemoteGatewayRestart()
		}
		return m.runCommand("把远程 lark-cli 加入 OpenClaw allowlist", "正在通过 SSH 允许远程 OpenClaw 执行 lark-cli。", larkStepRemoteGatewayRestart, larkRemoteStatusCmd(m.ctx, "remote openclaw approvals allowlist add", larkStepRemoteGatewayRestart, m.remoteTarget, "openclaw approvals allowlist add lark-cli"))
	case larkStepRemoteGatewayRestart:
		if !m.yes {
			m.openClawSetupDone = true
			m.openClawNeedsRestart = false
			return m.goToStep(m.openClawSetupNext)
		}
		return m.runCommand("重启远程 OpenClaw Gateway", "正在通过 SSH 重启远程 Gateway，让插件和权限配置生效。", m.openClawSetupNext, larkRemoteStatusCmd(m.ctx, "remote openclaw gateway restart", m.openClawSetupNext, m.remoteTarget, "openclaw gateway restart"))
	}
	return m, nil
}

func (m *larkInitModel) advanceInput(value string) (tea.Model, tea.Cmd) {
	switch m.step {
	case larkStepUserOAuthLabel:
		label := strings.TrimSpace(value)
		if label == "" {
			label = "User OAuth"
		}
		m.pendingIdentity = m.newIdentity("user", label, m.pendingOAuthApp.AppID, "oauth")
		if m.pendingOAuthApp.Source != "openclaw" {
			m.addNotice("当前版本只支持基于已绑定的 OpenClaw bot/app 创建 User OAuth；不支持在 Termind 中输入 app_secret 初始化 OAuth app。")
			return m.prepareUserOAuthAppChoice()
		}
		m.step = larkStepUserOAuthConfiguring
		return m, tea.Batch(m.spinner.Tick, larkUserOAuthBindStartCmd(m.ctx, m.openClawGatewayURL, m.pendingIdentity))
	case larkStepChatQuery:
		m.step = larkStepSearchingChats
		return m, tea.Batch(m.spinner.Tick, larkSearchChatsCmd(m.ctx, m.openClawGatewayURL, value, m.sender))
	case larkStepUserQuery:
		m.step = larkStepSearchingUsers
		return m, tea.Batch(m.spinner.Tick, larkSearchUsersCmd(m.ctx, m.openClawGatewayURL, value, m.sender))
	case larkStepManualChatID:
		if value == "" {
			return m.prepareManualPersonID(), nil
		}
		m.pendingChatID = value
		m.setInput(larkStepManualChatLabel, "群备注名", "manual chat")
		return m, nil
	case larkStepManualChatLabel:
		m.targets = appendTarget(m.targets, larkTargetChoice{Type: "chat", ID: m.pendingChatID, Label: value})
		m.pendingChatID = ""
		return m.prepareManualChatID(), nil
	case larkStepManualPersonID:
		if value == "" {
			return m.prepareTestTargets()
		}
		m.pendingPersonID = value
		m.selectedIndex = 0
		m.step = larkStepManualPersonType
		return m, nil
	case larkStepManualPersonLabel:
		m.targets = appendTarget(m.targets, larkTargetChoice{Type: m.pendingPersonType, ID: m.pendingPersonID, Label: value})
		m.pendingPersonID = ""
		m.pendingPersonType = ""
		return m.prepareManualPersonID(), nil
	case larkStepLarkConfigBindAppID:
		appID := strings.TrimSpace(value)
		if appID == "" {
			m.addNotice("请输入 Lark/Feishu bot 的 App ID，例如 cli_xxx。")
			return m.prepareLarkConfigBindAppID()
		}
		m.larkConfigBindAppID = appID
		m.setConfirm(larkStepLarkBotLoginInstruction, true)
		return m, nil
	case larkStepLocalPluginSpec:
		if value == "" {
			return m.prepareLocalToolsAllow(), nil
		}
		return m.runCommand("安装并启用 Termind OpenClaw 插件", "openclaw plugins install "+value+" && openclaw plugins enable termind", larkStepLocalToolsAllow, larkInstallPluginCmd(m.ctx, larkStepLocalToolsAllow, value))
	case larkStepRemoteTarget:
		m.remoteTarget = strings.TrimSpace(value)
		if m.remoteTarget == "" {
			return m.goToStep(larkStepCheckingDoctor)
		}
		return m.prepareRemotePlugin()
	case larkStepRemotePluginSpec:
		if value == "" {
			return m.prepareRemoteTools()
		}
		remoteCommand := "openclaw plugins install " + shellQuote(value) + "; openclaw plugins enable termind"
		return m.runCommand("安装并启用远程 Termind OpenClaw 插件", "正在通过 SSH 从 npm 安装远程 Termind 插件。", larkStepRemoteTools, larkRemoteStatusCmd(m.ctx, "remote openclaw plugins install/enable", larkStepRemoteTools, m.remoteTarget, remoteCommand))
	}
	return m, nil
}

func (m *larkInitModel) advanceChoice() (tea.Model, tea.Cmd) {
	switch m.step {
	case larkStepProfileChoice:
		choices := m.profileChoices()
		if len(choices) == 0 || m.selectedIndex >= len(choices) {
			return m.prepareKeepExisting()
		}
		choice := choices[m.selectedIndex]
		if choice.login {
			m.step = larkStepLocalAuthLoginStarting
			m.clearLarkLogin()
			return m, tea.Batch(m.spinner.Tick, larkAuthLoginStartCmd(m.ctx, m.openClawGatewayURL))
		}
		profile := strings.TrimSpace(choice.profile.Name)
		if profile == "" {
			return m.prepareKeepExisting()
		}
		if strings.EqualFold(profile, strings.TrimSpace(m.selectedProfile)) {
			m.sender = senderForProfile(m.findProfile(profile), m.sender)
			m.addNotice("✓ OpenClaw lark-cli profile: " + profile)
			return m.prepareKeepExisting()
		}
		m.step = larkStepSwitchingProfile
		return m, tea.Batch(m.spinner.Tick, larkUseProfileCmd(m.ctx, m.openClawGatewayURL, profile))
	case larkStepManualPersonType:
		if m.selectedIndex == 1 {
			m.pendingPersonType = "bot"
		} else {
			m.pendingPersonType = "user"
		}
		m.setInput(larkStepManualPersonLabel, "目标备注名", m.pendingPersonType)
		return m, nil
	case larkStepUserOAuthAppChoice:
		if m.selectedIndex < 0 || m.selectedIndex >= len(m.identityOrder) {
			return m.afterIdentityConfigured()
		}
		id := m.identityOrder[m.selectedIndex]
		m.pendingOAuthApp = m.identities[id]
		m.setInput(larkStepUserOAuthLabel, "账号备注", "User OAuth")
		return m, nil
	}
	return m, nil
}

func (m *larkInitModel) handleDoctorDone(msg larkDoctorDoneMsg) (tea.Model, tea.Cmd) {
	m.doctorFailureKind = ""
	m.doctorFailureDetail = ""
	text := strings.TrimSpace(string(msg.output))
	if text != "" {
		m.addNotice(text)
	}
	if msg.err != nil {
		m.errorText = fmt.Sprintf("OpenClaw 端 lark-cli 状态检查失败: %v", msg.err)
		m.doctorFailureKind = "error"
		m.doctorFailureDetail = m.errorText
		return m.prepareDoctorFailed()
	}
	if msg.status != nil {
		statusProfile := strings.TrimSpace(msg.status.Profile)
		m.profiles = msg.status.Profiles
		if !msg.status.Installed || !msg.status.Ready {
			for _, line := range msg.status.Errors {
				m.addNotice(line)
			}
			m.errorText = "OpenClaw 端 lark-cli 尚未就绪"
			if !msg.status.Installed {
				m.doctorFailureKind = "missing"
				m.doctorFailureDetail = "OpenClaw 端没有 lark-cli，需要先安装后才能查人、查群、测试发送。"
			} else if len(msg.status.Profiles) == 0 {
				m.doctorFailureKind = "unconfigured"
				m.doctorFailureDetail = "OpenClaw 端已有 lark-cli，但还没有 profile，需要先在 OpenClaw 运行端完成 bot profile 登录/绑定。"
			} else {
				m.doctorFailureKind = "not-ready"
				m.doctorFailureDetail = "OpenClaw 端 lark-cli 已安装，但 doctor/token 未通过，需要先在 OpenClaw 运行端完成登录/授权。"
			}
			return m.prepareDoctorFailed()
		}
		m.addNotice("✓ OpenClaw 端 lark-cli ready")
		if profile := statusProfile; profile != "" {
			m.selectedProfile = profile
			m.sender = senderForProfile(m.findProfile(profile), m.sender)
			m.addNotice("✓ OpenClaw 当前 lark-cli profile: " + profile)
		}
		if len(msg.status.Profiles) > 1 {
			m.addNotice("OpenClaw 检测到多个 lark-cli profile，可选择已有 profile 或新增登录。")
		}
		if openID := strings.TrimSpace(msg.status.Auth.UserOpenID); openID != "" {
			m.userOpenID = openID
			m.addNotice("✓ OpenClaw 当前用户 open_id: " + openID)
		}
		return m.prepareProfileChoice(), nil
	}
	return m.prepareProfileChoice(), nil
}

func (m *larkInitModel) handleChoicesDone(msg larkChoicesDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.addNotice(fmt.Sprintf("搜索%s失败: %v", msg.kind, msg.err))
		if msg.kind == "群聊" {
			return m.prepareSearchUsers()
		}
		return m.prepareManualChatID(), nil
	}
	choices := msg.choices
	if len(choices) > 20 {
		choices = choices[:20]
	}
	if len(choices) == 0 {
		m.addNotice("没有找到可选" + msg.kind + "。")
		if msg.kind == "群聊" {
			return m.prepareSearchUsers()
		}
		return m.prepareManualChatID(), nil
	}
	m.choices = choices
	m.selected = map[int]bool{}
	m.selectedIndex = 0
	if msg.kind == "群聊" {
		m.step = larkStepSelectChats
	} else {
		m.step = larkStepSelectUsers
	}
	return m, nil
}

func (m *larkInitModel) handleCommandDone(msg larkCommandDoneMsg) (tea.Model, tea.Cmd) {
	if msg.text != "" {
		for _, line := range strings.Split(strings.TrimSpace(msg.text), "\n") {
			if strings.TrimSpace(line) != "" {
				m.addNotice(line)
			}
		}
	}
	if msg.err != nil {
		m.addNotice(fmt.Sprintf("✗ %s: %v", msg.label, msg.err))
		if msg.next == larkStepSaving {
			return m.saveConfig()
		}
		if m.commandRetrySet {
			return m.goToStep(m.commandRetry)
		}
		return m.goToStep(m.commandNext)
	} else if msg.text == "" {
		m.addNotice("✓ " + msg.label)
	}
	if msg.err == nil && msg.needsRestart {
		m.openClawNeedsRestart = true
	}
	if msg.err == nil && msg.next == m.openClawSetupNext && m.commandCompletesOpenClawSetup() {
		m.openClawSetupDone = true
		m.openClawNeedsRestart = false
	}
	if msg.next == larkStepSaving {
		return m.saveConfig()
	}
	return m.goToStep(msg.next)
}

func (m *larkInitModel) goToStep(step larkInitStep) (tea.Model, tea.Cmd) {
	switch step {
	case larkStepIdentitySources:
		return m.prepareIdentitySources()
	case larkStepCheckingDoctor:
		m.step = larkStepCheckingDoctor
		m.addNotice("正在通过 OpenClaw 检查运行端 lark-cli。")
		return m, tea.Batch(m.spinner.Tick, larkDoctorCmd(m.ctx, m.openClawGatewayURL, m.openClawLarkCLIConfigDir()))
	case larkStepLocalToolsAllow:
		return m.prepareLocalToolsAllow(), nil
	case larkStepLocalExecAllow:
		return m.prepareLocalExecAllow(), nil
	case larkStepLocalGatewayRestart:
		return m.prepareLocalGatewayRestart()
	case larkStepRemoteDoctor:
		return m.prepareRemoteDoctor()
	case larkStepRemotePlugin:
		return m.prepareRemotePlugin()
	case larkStepRemoteTools:
		return m.prepareRemoteTools()
	case larkStepRemoteApprovals:
		return m.prepareRemoteApprovals()
	case larkStepRemoteGatewayRestart:
		return m.prepareRemoteGatewayRestart()
	case larkStepFinish:
		m.prepareFinish()
		return m, nil
	default:
		m.step = step
		return m, nil
	}
}

type larkProfileChoice struct {
	profile diagnose.LarkCLIProfile
	login   bool
}

func (m *larkInitModel) profileChoices() []larkProfileChoice {
	choices := make([]larkProfileChoice, 0, len(m.profiles)+1)
	for _, profile := range m.profiles {
		if strings.TrimSpace(profile.Name) == "" {
			continue
		}
		choices = append(choices, larkProfileChoice{profile: profile})
	}
	choices = append(choices, larkProfileChoice{login: true})
	return choices
}

func (m *larkInitModel) findProfile(name string) diagnose.LarkCLIProfile {
	name = strings.TrimSpace(name)
	for _, profile := range m.profiles {
		if strings.TrimSpace(profile.Name) == name {
			return profile
		}
	}
	return diagnose.LarkCLIProfile{}
}

func senderForProfile(profile diagnose.LarkCLIProfile, fallback string) string {
	identity := strings.TrimSpace(profile.Identity)
	if strings.EqualFold(identity, "user") {
		return "user"
	}
	if strings.EqualFold(identity, "bot") || strings.TrimSpace(profile.Name) != "" {
		return "bot"
	}
	if fallback = normalizeSender(fallback); fallback != "" {
		return fallback
	}
	return "bot"
}

func larkProfileChoiceLabel(profile diagnose.LarkCLIProfile) string {
	parts := []string{strings.TrimSpace(profile.Name)}
	if profile.Active {
		parts = append(parts, "active")
	}
	if identity := strings.TrimSpace(profile.Identity); identity != "" {
		parts = append(parts, identity)
	} else if strings.TrimSpace(profile.User) != "" {
		parts = append(parts, "user")
	} else {
		parts = append(parts, "bot")
	}
	if user := strings.TrimSpace(profile.User); user != "" {
		parts = append(parts, user)
	}
	if status := strings.TrimSpace(profile.TokenStatus); status != "" {
		parts = append(parts, status)
	}
	return strings.Join(parts, " · ")
}

const larkBotLoginChoiceID = "__lark_bot_login__"

func (m *larkInitModel) openClawLarkCLIConfigDir() string {
	return ""
}

func isLarkBotLoginChoice(choice larkTargetChoice) bool {
	return choice.Type == "action" && choice.ID == larkBotLoginChoiceID
}

func larkBotProfileChoices(status *diagnose.LarkCLIStatus) []larkTargetChoice {
	choices := make([]larkTargetChoice, 0)
	seen := map[string]bool{}
	if status != nil {
		for _, profile := range status.Profiles {
			if strings.EqualFold(strings.TrimSpace(profile.Identity), "user") {
				continue
			}
			name := strings.TrimSpace(profile.Name)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			appID := strings.TrimSpace(profile.AppID)
			choices = append(choices, larkTargetChoice{
				Type:    "bot",
				ID:      firstNonEmpty(appID, name),
				Label:   larkProfileChoiceLabel(profile),
				Profile: name,
				AppID:   appID,
			})
		}
	}
	choices = append(choices, larkTargetChoice{Type: "action", ID: larkBotLoginChoiceID, Label: "--- login new lark-cli bot"})
	return choices
}

func forwardingIdentityFromBotProfileChoice(choice larkTargetChoice, configDir string) diagnose.LarkForwardingIdentity {
	profile := strings.TrimSpace(choice.Profile)
	if profile == "" {
		profile = strings.TrimSpace(choice.ID)
	}
	appID := strings.TrimSpace(choice.AppID)
	if appID == "" && strings.HasPrefix(profile, "cli_") {
		appID = profile
	}
	label := firstNonEmpty(strings.TrimSpace(choice.Label), profile, appID)
	id := larkIdentityID("bot", "lark-cli", firstNonEmpty(appID, profile), label)
	return diagnose.LarkForwardingIdentity{
		ID:               id,
		Kind:             "bot",
		Label:            label,
		AppID:            appID,
		Profile:          profile,
		Source:           "lark-cli",
		LarkCLIConfigDir: strings.TrimSpace(configDir),
		Enabled:          true,
	}
}

func larkBotConfigInitCommand(appID string, configDir string) string {
	configDir = strings.TrimSpace(configDir)
	if configDir == "" {
		return "lark-cli config init --app-id " + shellQuote(strings.TrimSpace(appID)) + " --brand feishu --app-secret-stdin"
	}
	return strings.Join([]string{
		"mkdir -p " + shellEnvValue(configDir),
		"LARKSUITE_CLI_CONFIG_DIR=" + shellEnvValue(configDir) + " lark-cli config init --app-id " + shellQuote(strings.TrimSpace(appID)) + " --brand feishu --app-secret-stdin",
	}, "\n")
}

func larkBotConfigBindCommand(appID string) string {
	return "lark-cli config bind --source openclaw --app-id " + shellQuote(strings.TrimSpace(appID)) + " --identity bot-only"
}

func shellEnvValue(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "$HOME/") {
		return `"$HOME/` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`").Replace(strings.TrimPrefix(value, "$HOME/")) + `"`
	}
	return shellQuote(value)
}

func (m *larkInitModel) prepareLocalOpenClawSetup(next larkInitStep) (tea.Model, tea.Cmd) {
	m.openClawSetupNext = next
	if m.openClawSetupDone {
		return m.goToStep(next)
	}
	if m.openClawCLI == "" {
		m.addNotice("未找到 openclaw,无法自动配置本机 OpenClaw。")
		return m.goToStep(next)
	}
	m.setConfirm(larkStepLocalInstallPlugin, true)
	return m, nil
}

func (m *larkInitModel) prepareIdentitySources() (tea.Model, tea.Cmd) {
	m.errorText = ""
	m.secretInput = false
	m.input.EchoMode = textinput.EchoNormal
	m.choices = []larkTargetChoice{
		{Type: "source", ID: "openclaw", Label: "OpenClaw lark-cli bot profiles"},
	}
	m.selected = map[int]bool{}
	for i, choice := range m.choices {
		if m.selectedSources[choice.ID] {
			m.selected[i] = true
		}
	}
	if len(m.choices) == 1 && len(m.selectedSources) == 0 {
		m.selected[0] = true
	}
	m.selectedIndex = 0
	m.step = larkStepIdentitySources
	return m, nil
}

func (m *larkInitModel) afterIdentitySourceSelect() (tea.Model, tea.Cmd) {
	m.selectedSources = map[string]bool{}
	for i, choice := range m.choices {
		if m.selected[i] {
			m.selectedSources[choice.ID] = true
		}
	}
	if len(m.selectedSources) == 0 {
		m.addNotice("至少选择一个 Lark 转发身份来源。")
		return m.prepareIdentitySources()
	}
	if m.selectedSources["openclaw"] {
		m.step = larkStepLoadingOpenClawBotAccounts
		return m, tea.Batch(m.spinner.Tick, larkOpenClawBotAccountsCmd(m.ctx, m.openClawGatewayURL, m.openClawLarkCLIConfigDir()))
	}
	return m.afterSourceOpenClawBots()
}

func (m *larkInitModel) afterSourceOpenClawBots() (tea.Model, tea.Cmd) {
	return m.afterSourceConfiguredIdentities()
}

func (m *larkInitModel) afterSourceConfiguredIdentities() (tea.Model, tea.Cmd) {
	if m.selectedSources["oauth"] {
		return m.prepareUserOAuthAppChoice()
	}
	return m.afterIdentityConfigured()
}

func (m *larkInitModel) prepareUserOAuthAppChoice() (tea.Model, tea.Cmd) {
	if len(m.identityOrder) == 0 {
		m.addNotice("User OAuth 需要先有一个已配置的 app/bot 作为 OAuth app；当前版本不支持在 Termind 中直接初始化 app_secret。")
		return m.prepareIdentitySources()
	}
	m.selectedIndex = 0
	m.step = larkStepUserOAuthAppChoice
	return m, nil
}

func (m *larkInitModel) configureSelectedOpenClawBots() (tea.Model, tea.Cmd) {
	bots := make([]larkTargetChoice, 0)
	login := false
	for i, choice := range m.choices {
		if m.selected[i] {
			if isLarkBotLoginChoice(choice) {
				login = true
				continue
			}
			bots = append(bots, choice)
		}
	}
	for _, bot := range bots {
		identity := forwardingIdentityFromBotProfileChoice(bot, m.openClawLarkCLIConfigDir())
		m.addIdentity(identity)
		m.addNotice("✓ Lark bot profile: " + firstNonEmpty(identity.Label, identity.Profile, identity.AppID))
	}
	if login {
		return m.prepareLarkConfigBindAppID()
	}
	if len(bots) == 0 {
		return m.afterSourceOpenClawBots()
	}
	return m.afterSourceOpenClawBots()
}

func (m *larkInitModel) addIdentity(identity diagnose.LarkForwardingIdentity) {
	identity.ID = strings.TrimSpace(identity.ID)
	if identity.ID == "" {
		identity.ID = larkIdentityID(identity.Kind, identity.Source, identity.AppID, identity.Label)
	}
	identity.Enabled = true
	if identity.Slot == "" {
		if !strings.EqualFold(identity.Source, "lark-cli") {
			identity.Slot = identity.ID
		}
	}
	if identity.LarkCLIConfigDir == "" && identity.Slot != "" {
		identity.LarkCLIConfigDir = larkSlotConfigDir(identity.Slot)
	}
	if m.identities == nil {
		m.identities = map[string]diagnose.LarkForwardingIdentity{}
	}
	if _, exists := m.identities[identity.ID]; !exists {
		m.identityOrder = append(m.identityOrder, identity.ID)
	}
	m.identities[identity.ID] = identity
}

func (m *larkInitModel) afterIdentityConfigured() (tea.Model, tea.Cmd) {
	if m.selectedSources["oauth"] && strings.TrimSpace(m.pendingIdentity.ID) != "" {
		m.pendingIdentity = diagnose.LarkForwardingIdentity{}
	}
	if len(m.identityOrder) == 0 {
		m.addNotice("没有可用于转发的 Lark 身份。")
		return m.prepareIdentitySources()
	}
	return m.prepareForwardIdentitySelect()
}

func (m *larkInitModel) prepareForwardIdentitySelect() (tea.Model, tea.Cmd) {
	m.choices = make([]larkTargetChoice, 0, len(m.identityOrder))
	m.selected = map[int]bool{}
	for i, id := range m.identityOrder {
		identity := m.identities[id]
		m.choices = append(m.choices, larkTargetChoice{Type: identity.Kind, ID: id, Label: identity.Label})
		m.selected[i] = true
	}
	m.selectedIndex = 0
	m.step = larkStepForwardIdentitySelect
	return m, nil
}

func (m *larkInitModel) afterForwardIdentitySelect() (tea.Model, tea.Cmd) {
	m.selectedIdentityIDs = nil
	for i, choice := range m.choices {
		if m.selected[i] {
			m.selectedIdentityIDs = append(m.selectedIdentityIDs, choice.ID)
		}
	}
	if len(m.selectedIdentityIDs) == 0 {
		m.addNotice("至少选择一个转发身份。")
		return m.prepareForwardIdentitySelect()
	}
	m.routes = nil
	m.currentIdentityIndex = 0
	return m.prepareNextIdentityChats()
}

func (m *larkInitModel) prepareNextIdentityChats() (tea.Model, tea.Cmd) {
	if m.currentIdentityIndex >= len(m.selectedIdentityIDs) {
		return m.prepareTestTargets()
	}
	id := m.selectedIdentityIDs[m.currentIdentityIndex]
	m.currentIdentityID = id
	identity := m.identities[id]
	m.step = larkStepLoadingIdentityChats
	return m, tea.Batch(m.spinner.Tick, larkIdentityChatsCmd(m.ctx, m.openClawGatewayURL, id, identity))
}

func (m *larkInitModel) afterIdentityChatSelect() (tea.Model, tea.Cmd) {
	for i, choice := range m.choices {
		if !m.selected[i] {
			continue
		}
		m.routes = append(m.routes, diagnose.LarkForwardingRoute{
			IdentityID: m.currentIdentityID,
			Target: diagnose.LarkTarget{
				Type:    normalizeTargetType(choice.Type),
				ID:      strings.TrimSpace(choice.ID),
				Label:   strings.TrimSpace(choice.Label),
				Enabled: true,
			},
			Enabled: true,
		})
	}
	m.currentIdentityIndex++
	return m.prepareNextIdentityChats()
}

func (m *larkInitModel) newIdentity(kind string, label string, appID string, source string) diagnose.LarkForwardingIdentity {
	id := larkIdentityID(kind, source, appID, label)
	return diagnose.LarkForwardingIdentity{
		ID:               id,
		Kind:             normalizeSender(kind),
		Label:            strings.TrimSpace(label),
		AppID:            strings.TrimSpace(appID),
		Profile:          strings.TrimSpace(appID),
		Source:           strings.TrimSpace(source),
		Slot:             id,
		LarkCLIConfigDir: larkSlotConfigDir(id),
		Enabled:          true,
	}
}

func (m *larkInitModel) prepareDoctorFailed() (tea.Model, tea.Cmd) {
	if m.doctorFailureKind == "missing" && m.localOpenClaw {
		m.setConfirm(larkStepLocalInstallLarkCLI, true)
		return m, nil
	}
	m.setConfirm(larkStepDoctorFailed, true)
	return m, nil
}

func (m *larkInitModel) prepareProfileChoice() tea.Model {
	m.errorText = ""
	choices := m.profileChoices()
	if len(choices) == 0 {
		m.sender = normalizeSender(m.sender)
		if m.sender == "" {
			m.sender = "bot"
		}
		next, _ := m.prepareKeepExisting()
		return next
	}
	m.step = larkStepProfileChoice
	m.selectedIndex = 0
	return m
}

func (m *larkInitModel) prepareKeepExisting() (tea.Model, tea.Cmd) {
	if len(m.cfg.Lark.Targets) == 0 {
		return m.prepareAddSelf()
	}
	m.setConfirm(larkStepKeepExisting, true)
	return m, nil
}

func (m *larkInitModel) prepareLarkConfigBindAppID() (tea.Model, tea.Cmd) {
	m.setInput(larkStepLarkConfigBindAppID, "cli_xxx", m.larkConfigBindAppID)
	return m, nil
}

func (m *larkInitModel) prepareAddSelf() (tea.Model, tea.Cmd) {
	if strings.TrimSpace(m.userOpenID) == "" {
		return m.prepareSearchChats()
	}
	m.setConfirm(larkStepAddSelf, true)
	return m, nil
}

func (m *larkInitModel) prepareSearchChats() (tea.Model, tea.Cmd) {
	m.setConfirm(larkStepSearchChats, true)
	return m, nil
}

func (m *larkInitModel) prepareSearchUsers() (tea.Model, tea.Cmd) {
	m.setConfirm(larkStepSearchUsers, false)
	return m, nil
}

func (m *larkInitModel) prepareManualChatID() tea.Model {
	m.setInput(larkStepManualChatID, "手动添加群 chat_id(oc_xxx,留空结束)", "")
	return m
}

func (m *larkInitModel) prepareManualPersonID() tea.Model {
	m.setInput(larkStepManualPersonID, "手动添加个人或 bot open_id(ou_xxx,留空结束)", "")
	return m
}

func (m *larkInitModel) prepareTestTargets() (tea.Model, tea.Cmd) {
	if len(m.targets) == 0 && len(m.routes) == 0 {
		return m.saveConfig()
	}
	m.setConfirm(larkStepTestTargets, true)
	return m, nil
}

func (m *larkInitModel) saveConfig() (tea.Model, tea.Cmd) {
	cfg := *m.cfg
	cfg.Lark.UserOpenID = m.userOpenID
	cfg.Lark.Sender = m.sender
	cfg.Lark.Targets = make([]config.LarkTarget, 0, len(m.targets))
	for _, target := range m.targets {
		cfg.Lark.Targets = append(cfg.Lark.Targets, config.LarkTarget{
			Type:    normalizeTargetType(target.Type),
			ID:      strings.TrimSpace(target.ID),
			Label:   strings.TrimSpace(target.Label),
			Enabled: true,
		})
	}
	cfg.Lark.Forwarding = config.LarkForwardingConfig{
		Version:    1,
		Identities: map[string]config.LarkForwardingIdentity{},
		Routes:     make([]config.LarkForwardingRoute, 0, len(m.routes)),
	}
	for id, identity := range m.identities {
		cfg.Lark.Forwarding.Identities[id] = config.LarkForwardingIdentity{
			ID:               identity.ID,
			Kind:             identity.Kind,
			Label:            identity.Label,
			AppID:            identity.AppID,
			UserOpenID:       identity.UserOpenID,
			Profile:          identity.Profile,
			LarkCLIConfigDir: identity.LarkCLIConfigDir,
			Source:           identity.Source,
			Slot:             identity.Slot,
			Enabled:          identity.Enabled,
		}
	}
	for _, route := range m.routes {
		cfg.Lark.Forwarding.Routes = append(cfg.Lark.Forwarding.Routes, config.LarkForwardingRoute{
			IdentityID: route.IdentityID,
			Target: config.LarkTarget{
				Type:    route.Target.Type,
				ID:      route.Target.ID,
				Label:   route.Target.Label,
				Enabled: route.Target.Enabled,
			},
			Enabled: route.Enabled,
		})
	}
	if err := config.Save(&cfg); err != nil {
		m.errorText = fmt.Sprintf("保存 Lark 配置失败: %v", err)
		m.prepareFinish()
		return m, nil
	}
	path, _ := config.Path()
	m.savedPath = path
	m.addNotice("✓ Lark 转发配置已保存: " + path)
	if err := m.saveOpenClawForwardingConfig(cfg.Lark.Forwarding); err != nil {
		m.addNotice("✗ OpenClaw larkForwarding 保存失败: " + err.Error())
	}
	return m.afterSave()
}

func (m *larkInitModel) saveOpenClawForwardingConfig(forwarding config.LarkForwardingConfig) error {
	b, err := json.Marshal(forwarding)
	if err != nil {
		return err
	}
	if !m.localOpenClaw {
		if strings.TrimSpace(m.remoteTarget) == "" {
			m.addNotice("远程 OpenClaw plugin config 需要在 OpenClaw 所在机器写入；本机 Termind 配置已保存。")
			return nil
		}
		remoteCommand := "openclaw config set plugins.entries.termind.config.larkForwarding " + shellQuote(string(b)) + " --strict-json"
		output, err := runOutput(m.ctx, 20*time.Second, "ssh", m.remoteTarget, remoteCommand)
		if err != nil {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
		}
		m.addNotice("✓ 远程 OpenClaw plugin config: plugins.entries.termind.config.larkForwarding")
		return nil
	}
	if m.openClawCLI == "" {
		m.addNotice("未找到 openclaw CLI；本机 Termind 配置已保存。")
		return nil
	}
	output, err := runOutput(m.ctx, 10*time.Second, "openclaw", "config", "set", "plugins.entries.termind.config.larkForwarding", string(b), "--strict-json")
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	m.addNotice("✓ OpenClaw plugin config: plugins.entries.termind.config.larkForwarding")
	return nil
}

func (m *larkInitModel) afterSave() (tea.Model, tea.Cmd) {
	if m.localOpenClaw {
		return m.prepareLocalOpenClawSetup(larkStepFinish)
	}
	m.step = larkStepRemoteIntro
	return m.prepareRemoteIntro(), nil
}

func (m *larkInitModel) prepareLocalToolsAllow() tea.Model {
	m.setConfirm(larkStepLocalToolsAllow, true)
	return m
}

func (m *larkInitModel) prepareLocalExecAllow() tea.Model {
	m.setConfirm(larkStepLocalExecAllow, true)
	return m
}

func (m *larkInitModel) prepareLocalGatewayRestart() (tea.Model, tea.Cmd) {
	if !m.openClawNeedsRestart {
		m.openClawSetupDone = true
		m.openClawNeedsRestart = false
		return m.goToStep(m.openClawSetupNext)
	}
	m.setConfirm(larkStepLocalGatewayRestart, true)
	return m, nil
}

func (m *larkInitModel) prepareRemoteIntro() tea.Model {
	m.setConfirm(larkStepRemoteSSH, false)
	return m
}

func (m *larkInitModel) prepareRemoteLarkConfig() (tea.Model, tea.Cmd) {
	m.setConfirm(larkStepRemoteLarkConfig, true)
	return m, nil
}

func (m *larkInitModel) prepareRemoteDoctor() (tea.Model, tea.Cmd) {
	m.setConfirm(larkStepRemoteDoctor, true)
	return m, nil
}

func (m *larkInitModel) prepareRemotePlugin() (tea.Model, tea.Cmd) {
	m.setConfirm(larkStepRemotePlugin, true)
	return m, nil
}

func (m *larkInitModel) prepareRemoteTools() (tea.Model, tea.Cmd) {
	m.setConfirm(larkStepRemoteTools, true)
	return m, nil
}

func (m *larkInitModel) prepareRemoteApprovals() (tea.Model, tea.Cmd) {
	m.setConfirm(larkStepRemoteApprovals, true)
	return m, nil
}

func (m *larkInitModel) prepareRemoteGatewayRestart() (tea.Model, tea.Cmd) {
	if !m.openClawNeedsRestart {
		m.openClawSetupDone = true
		m.openClawNeedsRestart = false
		return m.goToStep(m.openClawSetupNext)
	}
	m.setConfirm(larkStepRemoteGatewayRestart, true)
	return m, nil
}

func (m *larkInitModel) prepareFinish() {
	m.step = larkStepFinish
	m.done = true
}

func (m *larkInitModel) runCommand(title string, detail string, next larkInitStep, cmd tea.Cmd) (tea.Model, tea.Cmd) {
	m.commandProgress = m.stepProgress()
	m.commandTitle = title
	m.commandDetail = detail
	m.commandNext = next
	m.commandRetry = m.step
	m.commandRetrySet = true
	m.step = larkStepRunningCommand
	return m, tea.Batch(m.spinner.Tick, cmd)
}

func (m *larkInitModel) setConfirm(step larkInitStep, def bool) {
	m.errorText = ""
	m.input.Blur()
	m.step = step
	m.yes = def
}

func (m *larkInitModel) setInput(step larkInitStep, placeholder string, value string) {
	m.errorText = ""
	m.secretInput = false
	m.step = step
	m.input.Placeholder = placeholder
	m.input.EchoMode = textinput.EchoNormal
	m.input.SetValue(value)
	m.input.CursorEnd()
	m.input.Focus()
}

func (m *larkInitModel) setSecretInput(step larkInitStep, placeholder string) {
	m.setInput(step, placeholder, "")
	m.secretInput = true
	m.input.EchoMode = textinput.EchoPassword
	m.input.EchoCharacter = '•'
}

func (m *larkInitModel) setLarkLogin(login *diagnose.LarkCLIAuthLoginStartResult) {
	if login == nil {
		m.clearLarkLogin()
		return
	}
	m.larkLoginDeviceCode = strings.TrimSpace(login.DeviceCode)
	m.larkLoginUserCode = strings.TrimSpace(login.UserCode)
	m.larkLoginURL = strings.TrimSpace(login.VerificationURL)
	m.larkLoginExpiresIn = login.ExpiresIn
	m.larkLoginMessage = strings.TrimSpace(login.Message)
}

func (m *larkInitModel) clearLarkLogin() {
	m.larkLoginDeviceCode = ""
	m.larkLoginUserCode = ""
	m.larkLoginURL = ""
	m.larkLoginExpiresIn = 0
	m.larkLoginMessage = ""
}

func (m *larkInitModel) addNotice(message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	m.notices = append(m.notices, message)
	if len(m.notices) > 8 {
		m.notices = m.notices[len(m.notices)-8:]
	}
}

func (m *larkInitModel) isLoadingStep() bool {
	switch m.step {
	case larkStepLoadingOpenClawBotAccounts, larkStepConfiguringIdentity, larkStepUserOAuthConfiguring, larkStepLoadingIdentityChats, larkStepSavingOpenClawConfig, larkStepCheckingDoctor, larkStepLarkConfigBinding, larkStepSwitchingProfile, larkStepLocalAuthLoginStarting, larkStepLocalAuthLoginWaiting, larkStepSearchingChats, larkStepSearchingUsers, larkStepTestingTargets, larkStepRunningCommand:
		return true
	default:
		return false
	}
}

func (m *larkInitModel) isConfirmStep() bool {
	switch m.step {
	case larkStepEnable, larkStepDoctorFailed, larkStepLocalInstallLarkCLI, larkStepLarkBotLoginInstruction, larkStepKeepExisting, larkStepAddSelf, larkStepSearchChats, larkStepSearchUsers, larkStepTestTargets, larkStepLocalInstallPlugin, larkStepLocalToolsAllow, larkStepLocalExecAllow, larkStepLocalGatewayRestart, larkStepRemoteSSH, larkStepRemoteLarkConfig, larkStepRemoteDoctor, larkStepRemotePlugin, larkStepRemoteTools, larkStepRemoteApprovals, larkStepRemoteGatewayRestart:
		return true
	default:
		return false
	}
}

func (m *larkInitModel) isInputStep() bool {
	switch m.step {
	case larkStepUserOAuthLabel, larkStepChatQuery, larkStepUserQuery, larkStepManualChatID, larkStepManualChatLabel, larkStepManualPersonID, larkStepManualPersonLabel, larkStepLarkConfigBindAppID, larkStepLocalPluginSpec, larkStepRemoteTarget, larkStepRemotePluginSpec:
		return true
	default:
		return false
	}
}

func (m *larkInitModel) isChoiceStep() bool {
	switch m.step {
	case larkStepUserOAuthAppChoice, larkStepProfileChoice, larkStepManualPersonType:
		return true
	default:
		return false
	}
}

func (m *larkInitModel) isMultiSelectStep() bool {
	switch m.step {
	case larkStepIdentitySources, larkStepSelectOpenClawBots, larkStepForwardIdentitySelect, larkStepSelectIdentityChats, larkStepSelectChats, larkStepSelectUsers:
		return true
	default:
		return false
	}
}

func (m *larkInitModel) View() string {
	var body string
	switch m.step {
	case larkStepEnable:
		body = m.renderConfirm("配置 Lark/Feishu 转发?", "使用 lark-cli 获取用户/群聊信息,运行时也由 OpenClaw 执行 lark-cli 发送。")
	case larkStepIdentitySources:
		body = m.renderMultiSelect("Lark 转发身份来源", "Space 勾选,Enter 确认。可以多选。")
	case larkStepLoadingOpenClawBotAccounts:
		body = m.renderLoading("读取 OpenClaw lark-cli bot profiles", "正在通过 OpenClaw agent exec 获取 lark-cli profile list。")
	case larkStepSelectOpenClawBots:
		body = m.renderMultiSelect("选择 OpenClaw lark-cli bot profile", "Space 勾选已有 bot；末尾 --- login 用于在 OpenClaw 端手动新增 bot profile。")
	case larkStepConfiguringIdentity:
		body = m.renderLoading("配置 Lark 身份", "正在创建独立 lark-cli slot。")
	case larkStepUserOAuthAppChoice:
		body = m.renderOAuthAppChoice()
	case larkStepUserOAuthLabel:
		body = m.renderInput("User OAuth 账号备注", "例如 Alice；授权完成后会回到账号列表。")
	case larkStepUserOAuthConfiguring:
		body = m.renderLoading("启动 User OAuth 登录", "正在初始化 user slot 并启动 lark-cli auth login device flow。")
	case larkStepForwardIdentitySelect:
		body = m.renderMultiSelect("选择转发身份", "Space 勾选 bot/user 身份,Enter 后逐个列出可见群聊。")
	case larkStepLoadingIdentityChats:
		body = m.renderLoading("遍历身份可见群聊", m.currentIdentityLabel())
	case larkStepSelectIdentityChats:
		body = m.renderMultiSelect("选择 "+m.currentIdentityLabel()+" 可见群聊", "Space 勾选,Enter 继续下一个身份。")
	case larkStepSavingOpenClawConfig:
		body = m.renderLoading("保存 OpenClaw Lark forwarding routes", "正在写入本地 Termind 配置和 OpenClaw plugin config。")
	case larkStepCheckingDoctor:
		body = m.renderLoading("检查 OpenClaw 端 lark-cli", "正在通过 OpenClaw agent 查询 lark-cli status/profile")
	case larkStepDoctorFailed:
		body = m.renderConfirm("重新检查 lark-cli?", m.doctorFailureText())
	case larkStepLocalInstallLarkCLI:
		body = m.renderConfirm("安装 lark-cli?", "OpenClaw 在本机运行，检测到本机没有 lark-cli。将执行 npm install -g @larksuite/cli 并安装 lark-cli skill。")
	case larkStepLocalConfigureLarkCLI:
		body = m.renderConfirm("重新检查 OpenClaw lark-cli profile?", "请先在 OpenClaw 运行端完成 lark-cli bot profile 登录/绑定；Termind 随后继续获取群聊并在这里选择。")
	case larkStepLarkConfigBindAppID:
		body = m.renderInput("新增 lark-cli bot profile", "输入 Lark/Feishu App ID（cli_xxx）。Termind 只生成 OpenClaw 端登录命令，不接收 app_secret。")
	case larkStepLarkConfigBinding:
		body = m.renderLoading("绑定 OpenClaw lark-cli bot profile", "正在通过 OpenClaw exec 执行 lark-cli config bind --source openclaw --identity bot-only")
	case larkStepLarkBotLoginInstruction:
		body = m.renderLarkBotLoginInstruction()
	case larkStepLocalAuthLogin:
		body = m.renderConfirm("通过 OpenClaw 登录 lark-cli?", "Termind 会通过 OpenClaw exec 启动 lark-cli auth login，并在这里显示授权链接/验证码。")
	case larkStepLocalAuthLoginStarting:
		body = m.renderLoading("启动 OpenClaw lark-cli 登录", "正在通过 OpenClaw exec 执行 lark-cli auth login --recommend --no-wait --json")
	case larkStepLocalAuthLoginWaiting:
		body = m.renderLarkAuthLoginWaiting()
	case larkStepProfileChoice:
		body = m.renderLarkProfileChoice()
	case larkStepSwitchingProfile:
		body = m.renderLoading("切换 OpenClaw lark-cli profile", "正在通过 OpenClaw 执行 lark-cli profile use")
	case larkStepKeepExisting:
		body = m.renderConfirm(fmt.Sprintf("保留已有 %d 个转发目标?", len(m.cfg.Lark.Targets)), "保留后会和本次新增目标合并去重。")
	case larkStepAddSelf:
		body = m.renderConfirm("把自己作为个人转发目标?", "后续命令失败时,Termind 可以直接把消息发给你。")
	case larkStepSearchChats:
		body = m.renderConfirm("搜索并选择群聊目标?", "通过 OpenClaw 端 lark-cli 搜索群聊,可以一次选择多个。")
	case larkStepChatQuery:
		body = m.renderInput("群名关键字", "留空会尝试列出最近群聊。")
	case larkStepSearchingChats:
		body = m.renderLoading("搜索群聊", "正在通过 OpenClaw 查询群聊")
	case larkStepSelectChats:
		body = m.renderMultiSelect("选择群聊目标", "Space 勾选,Enter 确认。")
	case larkStepSearchUsers:
		body = m.renderConfirm("搜索并选择个人目标?", "可以搜索同事 open_id 作为个人通知目标。")
	case larkStepUserQuery:
		body = m.renderInput("用户名关键字", "输入用户名关键字后搜索。")
	case larkStepSearchingUsers:
		body = m.renderLoading("搜索用户", "正在通过 OpenClaw 查询用户")
	case larkStepSelectUsers:
		body = m.renderMultiSelect("选择个人目标", "Space 勾选,Enter 确认。")
	case larkStepManualChatID:
		body = m.renderInput("手动添加群 chat_id", "输入 oc_xxx；留空进入下一步。")
	case larkStepManualChatLabel:
		body = m.renderInput("群备注名", "给这个群目标起一个本地备注。")
	case larkStepManualPersonID:
		body = m.renderInput("手动添加个人或 bot open_id", "输入 ou_xxx；留空结束目标配置。")
	case larkStepManualPersonType:
		body = m.renderChoice("目标类型", "选择这个 open_id 是 user 还是 bot。", []string{"user", "bot"})
	case larkStepManualPersonLabel:
		body = m.renderInput("目标备注名", "给这个个人目标起一个本地备注。")
	case larkStepTestTargets:
		body = m.renderConfirm(m.testTargetTitle(), m.renderTargetsSummary())
	case larkStepTestingTargets:
		body = m.renderLoading("发送测试消息", "正在通过 OpenClaw 端 lark-cli 测试已选目标。")
	case larkStepLocalInstallPlugin:
		body = m.renderConfirm("安装/刷新 Termind OpenClaw 插件?", "默认从 npm dev 包安装，让 OpenClaw 调用 Termind Lark 工具。")
	case larkStepLocalPluginSpec:
		body = m.renderInput("Termind 插件 npm spec", "默认 "+termindOpenClawPluginSpec+"；可改成精确版本。")
	case larkStepLocalToolsAllow:
		body = m.renderConfirm("配置 OpenClaw tools.alsoAllow?", "加入 exec、process 和 Termind Lark 工具。")
	case larkStepLocalExecAllow:
		body = m.renderConfirm("把 lark-cli 加入 OpenClaw exec approvals allowlist?", "允许 OpenClaw 运行时执行 lark-cli 发消息。")
	case larkStepLocalGatewayRestart:
		body = m.renderConfirm("重启 OpenClaw Gateway 让配置生效?", "插件启用和 tools 配置需要 Gateway 重新加载。")
	case larkStepRemoteSSH:
		body = m.renderConfirm("通过 SSH 辅助配置远程 OpenClaw?", m.remoteIntroText())
	case larkStepRemoteTarget:
		body = m.renderInput("SSH 目标", "例如 user@host；留空跳过远程辅助配置。")
	case larkStepRemoteLarkConfig:
		body = m.renderConfirm("通过 OpenClaw 登录 lark-cli?", "Termind 会通过 OpenClaw exec 在 OpenClaw 运行端启动 lark-cli auth login。")
	case larkStepRemoteDoctor:
		body = m.renderConfirm("跳过远程 lark-cli doctor?", "状态检查已通过 OpenClaw Gateway 完成。")
	case larkStepRemotePlugin:
		body = m.renderConfirm("安装/刷新远程 Termind OpenClaw 插件?", "通过 SSH 在远程机器从 npm 安装。")
	case larkStepRemotePluginSpec:
		body = m.renderInput("Termind 插件 npm spec", "默认 "+termindOpenClawPluginSpec+"；远程机器需能访问 npm。")
	case larkStepRemoteTools:
		body = m.renderConfirm("配置远程 OpenClaw tools.alsoAllow?", "远程执行 openclaw config set tools.alsoAllow。")
	case larkStepRemoteApprovals:
		body = m.renderConfirm("把远程 lark-cli 加入 OpenClaw allowlist?", "远程执行 openclaw approvals allowlist add lark-cli。")
	case larkStepRemoteGatewayRestart:
		body = m.renderConfirm("重启远程 OpenClaw Gateway 让配置生效?", "远程插件启用和 tools 配置需要 Gateway 重新加载。")
	case larkStepRunningCommand:
		body = m.renderLoading(m.commandTitle, m.commandDetail)
	case larkStepFinish:
		body = m.renderFinish()
	}
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(initTUITitleStyle.Render("Lark/Feishu 转发配置"))
	b.WriteString("\n")
	b.WriteString(initTUISubtleStyle.Render("用 lark-cli 找人/找群,再把结果写进 Termind 和 OpenClaw 配置。"))
	b.WriteString("\n\n")
	b.WriteString(m.renderProgress())
	b.WriteString("\n\n")
	b.WriteString(initTUICardStyle.Width(86).Render(body))
	if m.errorText != "" && m.step != larkStepDoctorFailed {
		b.WriteString("\n")
		b.WriteString(initTUIErrorStyle.Render("✗ " + m.errorText))
		b.WriteString("\n")
	}
	if len(m.notices) > 0 && m.step != larkStepFinish {
		b.WriteString("\n")
		b.WriteString(initTUISubtleStyle.Render("最近状态"))
		b.WriteString("\n")
		for _, notice := range m.notices {
			b.WriteString(initTUISubtleStyle.Render("  "+notice) + "\n")
		}
	}
	b.WriteString(m.renderHelp())
	return b.String()
}

func (m *larkInitModel) renderConfirm(title string, detail string) string {
	var b strings.Builder
	b.WriteString(initTUIAccentStyle.Render(title))
	b.WriteString("\n")
	if detail != "" {
		b.WriteString(initTUISubtleStyle.Render(detail))
		b.WriteString("\n\n")
	}
	b.WriteString(m.renderYesNo())
	return b.String()
}

func (m *larkInitModel) renderYesNo() string {
	yes := "  是"
	no := "  否"
	if m.yes {
		yes = initTUISelectedStyle.Render("● 是")
		no = initTUISubtleStyle.Render("○ 否")
	} else {
		yes = initTUISubtleStyle.Render("○ 是")
		no = initTUISelectedStyle.Render("● 否")
	}
	return yes + "    " + no
}

func (m *larkInitModel) renderChoice(title string, detail string, options []string) string {
	var b strings.Builder
	b.WriteString(initTUIAccentStyle.Render(title))
	b.WriteString("\n")
	b.WriteString(initTUISubtleStyle.Render(detail))
	b.WriteString("\n\n")
	for i, option := range options {
		prefix := "  ○ "
		text := option
		if i == m.selectedIndex {
			prefix = initTUISelectedStyle.Render("  ● ")
			text = initTUISelectedStyle.Render(option)
		} else {
			text = initTUISubtleStyle.Render(option)
		}
		b.WriteString(prefix + text + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *larkInitModel) renderInput(title string, detail string) string {
	return strings.Join([]string{
		initTUIAccentStyle.Render(title),
		initTUISubtleStyle.Render(detail),
		"",
		m.input.View(),
	}, "\n")
}

func (m *larkInitModel) renderLoading(title string, detail string) string {
	return strings.Join([]string{
		initTUIAccentStyle.Render(title),
		initTUISubtleStyle.Render(detail),
		"",
		fmt.Sprintf("%s %s", m.spinner.View(), "处理中..."),
	}, "\n")
}

func (m *larkInitModel) renderLarkAuthLoginWaiting() string {
	lines := []string{
		initTUIAccentStyle.Render("等待 OpenClaw lark-cli 浏览器授权"),
		initTUISubtleStyle.Render("请打开下面的链接并输入验证码；Termind 会通过 OpenClaw exec 等待授权完成。"),
		"",
	}
	if msg := strings.TrimSpace(m.larkLoginMessage); msg != "" {
		lines = append(lines, msg)
	}
	if url := strings.TrimSpace(m.larkLoginURL); url != "" {
		lines = append(lines, "授权链接: "+url)
	}
	if code := strings.TrimSpace(m.larkLoginUserCode); code != "" {
		lines = append(lines, "验证码: "+initTUISelectedStyle.Render(code))
	}
	if m.larkLoginExpiresIn > 0 {
		lines = append(lines, fmt.Sprintf("有效期: %d 秒", m.larkLoginExpiresIn))
	}
	lines = append(lines, "", fmt.Sprintf("%s %s", m.spinner.View(), "等待 OpenClaw 端授权完成..."))
	return strings.Join(lines, "\n")
}

func (m *larkInitModel) renderLarkBotLoginInstruction() string {
	configDir := m.openClawLarkCLIConfigDir()
	initCmd := larkBotConfigInitCommand(m.larkConfigBindAppID, configDir)
	bindCmd := larkBotConfigBindCommand(m.larkConfigBindAppID)
	var b strings.Builder
	b.WriteString(initTUIAccentStyle.Render("在 OpenClaw 端登录 lark-cli bot"))
	b.WriteString("\n")
	b.WriteString(initTUISubtleStyle.Render("请在 OpenClaw 运行端依次执行下面两条命令；第一条会要求输入 app_secret，请在该终端粘贴，不要发给 Termind 或 agent。"))
	b.WriteString("\n\n")
	b.WriteString(initTUISubtleStyle.Render("1) 写入 lark-cli local workspace（输入 app_secret）："))
	b.WriteString("\n")
	b.WriteString(initCmd)
	b.WriteString("\n\n")
	b.WriteString(initTUISubtleStyle.Render("2) 同步到 OpenClaw workspace（不需要再次输入 secret）："))
	b.WriteString("\n")
	b.WriteString(bindCmd)
	b.WriteString("\n\n")
	if configDir != "" {
		b.WriteString("OpenClaw lark-cli profile 目录: " + configDir)
	} else {
		b.WriteString("OpenClaw lark-cli profile 目录: ~/.lark-cli/openclaw/config.json (lark-cli 1.0.23+ openclaw workspace)")
	}
	b.WriteString("\n\n")
	b.WriteString(initTUISubtleStyle.Render("两条都跑完后选择 是/Enter，Termind 会通过 skill + OpenClaw agent exec 重新获取 lark-cli bot profile 列表。"))
	b.WriteString("\n\n")
	b.WriteString(m.renderYesNo())
	return b.String()
}

func (m *larkInitModel) renderLarkProfileChoice() string {
	var b strings.Builder
	b.WriteString(initTUIAccentStyle.Render("选择 OpenClaw lark-cli profile"))
	b.WriteString("\n")
	b.WriteString(initTUISubtleStyle.Render("选择已有 profile 会在 OpenClaw 端执行 lark-cli profile use；也可以选择登录/新增授权。"))
	b.WriteString("\n\n")
	choices := m.profileChoices()
	for i, choice := range choices {
		cursor := "  "
		if i == m.selectedIndex {
			cursor = initTUISelectedStyle.Render("❯ ")
		}
		var line string
		if choice.login {
			line = "登录/新增授权"
			if !m.localOpenClaw {
				line += "（在 OpenClaw 运行端完成）"
			}
		} else {
			line = larkProfileChoiceLabel(choice.profile)
		}
		if i == m.selectedIndex {
			line = initTUISelectedStyle.Render(line)
		}
		b.WriteString(cursor + line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *larkInitModel) renderOAuthAppChoice() string {
	var b strings.Builder
	b.WriteString(initTUIAccentStyle.Render("选择 User OAuth app/bot"))
	b.WriteString("\n")
	b.WriteString(initTUISubtleStyle.Render("选择一个已配置 bot/app 作为 OAuth app，然后为 user 创建独立 lark-cli slot。"))
	b.WriteString("\n\n")
	for i, id := range m.identityOrder {
		identity := m.identities[id]
		cursor := "  "
		if i == m.selectedIndex {
			cursor = initTUISelectedStyle.Render("❯ ")
		}
		line := fmt.Sprintf("[%s] %s %s", identity.Kind, identity.AppID, identity.Label)
		if i == m.selectedIndex {
			line = initTUISelectedStyle.Render(line)
		} else {
			line = initTUISubtleStyle.Render(line)
		}
		b.WriteString(cursor + line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *larkInitModel) renderMultiSelect(title string, detail string) string {
	var b strings.Builder
	b.WriteString(initTUIAccentStyle.Render(title))
	b.WriteString("\n")
	b.WriteString(initTUISubtleStyle.Render(detail))
	b.WriteString("\n\n")
	for i, choice := range m.choices {
		cursor := "  "
		if i == m.selectedIndex {
			cursor = initTUISelectedStyle.Render("❯ ")
		}
		mark := "☐"
		if m.selected[i] {
			mark = "☑"
		}
		line := fmt.Sprintf("%s %s [%s] %s %s", cursor, mark, choice.Type, choice.ID, choice.Label)
		if isLarkBotLoginChoice(choice) {
			line = fmt.Sprintf("%s %s %s", cursor, mark, choice.Label)
		}
		if i == m.selectedIndex {
			line = initTUISelectedStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *larkInitModel) renderFinish() string {
	var b strings.Builder
	b.WriteString(initTUISuccessStyle.Render("✓ Lark/Feishu 配置完成"))
	if m.savedPath != "" {
		b.WriteString("\n")
		b.WriteString(initTUISubtleStyle.Render("配置文件: " + m.savedPath))
	}
	if len(m.targets) > 0 {
		b.WriteString("\n\n")
		b.WriteString("转发目标\n")
		b.WriteString(m.renderTargetsSummary())
	}
	if len(m.notices) > 0 {
		b.WriteString("\n\n")
		b.WriteString("执行结果\n")
		for _, notice := range m.notices {
			b.WriteString(initTUISubtleStyle.Render("  "+notice) + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *larkInitModel) renderTargetsSummary() string {
	if len(m.targets) == 0 {
		return "当前没有选择转发目标。"
	}
	lines := make([]string, 0, len(m.targets))
	for i, target := range m.targets {
		lines = append(lines, fmt.Sprintf("%d. [%s] %s %s", i+1, target.Type, target.ID, target.Label))
	}
	return strings.Join(lines, "\n")
}

func (m *larkInitModel) testTargetTitle() string {
	return "发送测试消息?"
}

func (m *larkInitModel) currentIdentityLabel() string {
	identity := m.identities[m.currentIdentityID]
	label := strings.TrimSpace(identity.Label)
	if label == "" {
		label = strings.TrimSpace(identity.AppID)
	}
	if label == "" {
		label = strings.TrimSpace(m.currentIdentityID)
	}
	if kind := strings.TrimSpace(identity.Kind); kind != "" {
		return kind + ": " + label
	}
	return label
}

func (m *larkInitModel) doctorFailureText() string {
	detail := strings.TrimSpace(m.doctorFailureDetail)
	if detail == "" {
		detail = "OpenClaw 端 lark-cli 尚未就绪。请先处理后重新检查。"
	}
	switch m.doctorFailureKind {
	case "missing":
		if m.localOpenClaw {
			return detail + "\n下一步会自动安装 lark-cli，然后重新检查。"
		}
		return detail + "\nOpenClaw 不在本机，必须在 OpenClaw 所在机器安装 lark-cli 后再重新检查。"
	case "unconfigured":
		return detail + "\n请在 OpenClaw 运行端执行 lark-cli bot profile 登录/绑定后回到 Termind 重新检查；Termind 只负责后续读取 chat 列表和选择转发目标。"
	case "not-ready":
		return detail + "\n请在 OpenClaw 运行端完成 lark-cli 登录/授权后回到 Termind 重新检查；Termind 不复制本机凭证，也不直接执行本机 lark-cli。"
	default:
		return detail
	}
}

func (m *larkInitModel) remoteIntroText() string {
	toolLines := make([]string, 0, len(termindOpenClawAllowedTools))
	for _, tool := range termindOpenClawAllowedTools {
		toolLines = append(toolLines, "  - "+tool)
	}
	return strings.Join([]string{
		"OpenClaw 不在本机,运行时的 lark-cli 需要装在 OpenClaw 所在机器。",
		"OpenClaw Gateway: " + m.openClawGatewayURL,
		"Termind 插件默认从 npm 安装: " + termindOpenClawPluginSpec,
		"需要确认: lark-cli doctor、Termind 插件、tools.alsoAllow、exec allowlist。",
		"tools.alsoAllow 需要包含:",
		strings.Join(toolLines, "\n"),
	}, "\n")
}

func (m *larkInitModel) renderProgress() string {
	labels := []string{"启用", "OpenClaw", "lark-cli", "Profile", "目标", "测试"}
	current := m.stepProgress()
	parts := make([]string, 0, len(labels))
	for i, label := range labels {
		n := i + 1
		switch {
		case n < current:
			parts = append(parts, initTUISuccessStyle.Render("✓ "+label))
		case n == current:
			parts = append(parts, initTUIAccentStyle.Render(strconv.Itoa(n)+" "+label))
		default:
			parts = append(parts, initTUISubtleStyle.Render(strconv.Itoa(n)+" "+label))
		}
	}
	return strings.Join(parts, initTUISubtleStyle.Render("  ──  "))
}

func (m *larkInitModel) stepProgress() int {
	switch m.step {
	case larkStepLocalInstallPlugin, larkStepLocalPluginSpec, larkStepLocalToolsAllow, larkStepLocalExecAllow, larkStepRemoteIntro, larkStepRemoteSSH, larkStepRemoteTarget, larkStepRemoteLarkConfig, larkStepRemoteDoctor, larkStepRemotePlugin, larkStepRemotePluginSpec, larkStepRemoteTools, larkStepRemoteApprovals:
		return 2
	case larkStepLocalGatewayRestart, larkStepRemoteGatewayRestart:
		return m.gatewayRestartProgressStep()
	case larkStepCheckingDoctor, larkStepDoctorFailed, larkStepLocalInstallLarkCLI, larkStepLocalConfigureLarkCLI, larkStepLarkConfigBindAppID, larkStepLarkConfigBinding, larkStepLarkBotLoginInstruction, larkStepLocalAuthLogin, larkStepLocalAuthLoginStarting, larkStepLocalAuthLoginWaiting:
		return 3
	case larkStepProfileChoice, larkStepSwitchingProfile:
		return 4
	case larkStepKeepExisting, larkStepAddSelf, larkStepSearchChats, larkStepChatQuery, larkStepSearchingChats, larkStepSelectChats, larkStepSearchUsers, larkStepUserQuery, larkStepSearchingUsers, larkStepSelectUsers, larkStepManualChatID, larkStepManualChatLabel, larkStepManualPersonID, larkStepManualPersonType, larkStepManualPersonLabel:
		return 5
	case larkStepTestTargets, larkStepTestingTargets:
		return 6
	case larkStepFinish:
		return 7
	}
	if m.step == larkStepRunningCommand {
		return m.runningCommandProgressStep()
	}
	return 1
}

func (m *larkInitModel) runningCommandProgressStep() int {
	if m.commandProgress > 0 {
		return m.commandProgress
	}
	title := strings.ToLower(m.commandTitle)
	if strings.Contains(title, "openclaw") || strings.Contains(title, "gateway") || strings.Contains(title, "plugin") || strings.Contains(title, "tools") || strings.Contains(title, "allowlist") || strings.Contains(title, "approvals") {
		return 2
	}
	if strings.Contains(title, "lark-cli") {
		return 3
	}
	switch m.commandNext {
	case larkStepLocalGatewayRestart:
		return 2
	case larkStepCheckingDoctor:
		return 3
	case larkStepSaving:
		return 5
	case larkStepFinish:
		return 6
	default:
		if isOpenClawSetupStep(m.commandNext) {
			return 2
		}
		return 2
	}
}

func (m *larkInitModel) gatewayRestartProgressStep() int {
	title := strings.ToLower(m.commandTitle)
	if strings.Contains(title, "openclaw") || strings.Contains(title, "plugin") || strings.Contains(title, "tools") || strings.Contains(title, "allowlist") || strings.Contains(title, "approvals") {
		return 2
	}
	if strings.Contains(title, "lark-cli") {
		return 3
	}
	return 2
}

func (m *larkInitModel) commandCompletesOpenClawSetup() bool {
	if m.commandNext == larkStepLocalGatewayRestart || m.commandNext == larkStepRemoteGatewayRestart {
		return false
	}
	if m.commandNext != m.openClawSetupNext {
		return false
	}
	if strings.Contains(strings.ToLower(m.commandTitle), "gateway") {
		return true
	}
	return isOpenClawSetupStep(m.commandNext)
}

func (m *larkInitModel) renderHelp() string {
	switch {
	case m.isConfirmStep():
		return "\n" + initTUISubtleStyle.Render("←/→ 选择   Y/N 快速选择   Enter 确认   Ctrl+C 取消") + "\n"
	case m.isInputStep():
		return "\n" + initTUISubtleStyle.Render("Enter 确认   Ctrl+C 取消") + "\n"
	case m.isChoiceStep():
		return "\n" + initTUISubtleStyle.Render("↑/↓ 选择   Enter 确认   Ctrl+C 取消") + "\n"
	case m.isMultiSelectStep():
		return "\n" + initTUISubtleStyle.Render("↑/↓ 移动   Space 勾选   Enter 确认   Ctrl+C 取消") + "\n"
	case m.step == larkStepFinish:
		return "\n" + initTUISubtleStyle.Render("Enter 结束") + "\n"
	default:
		return "\n" + initTUISubtleStyle.Render("Ctrl+C 取消") + "\n"
	}
}

func larkWatchContextCmd(ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		<-ctx.Done()
		return larkExternalCancelMsg{}
	}
}

func larkTestRoutesCmd(ctx context.Context, openClawGatewayURL string, identities map[string]diagnose.LarkForwardingIdentity, routes []diagnose.LarkForwardingRoute) tea.Cmd {
	return func() tea.Msg {
		conn, dc, err := initLarkDiagnoseClient(ctx, openClawGatewayURL)
		if err != nil {
			return larkCommandDoneMsg{label: "发送测试消息", err: err, next: larkStepSaving}
		}
		defer conn.Close()
		result, err := dc.TestLarkTargets(ctx, diagnose.LarkTargetTestRequest{
			Identities: identities,
			Routes:     routes,
			Card:       json.RawMessage(testLarkCardContent()),
		})
		if err != nil {
			return larkCommandDoneMsg{label: "发送测试消息", err: err, next: larkStepSaving}
		}
		if result != nil && !result.OK {
			detail := "lark-cli route test failed"
			if len(result.Results) > 0 {
				parts := make([]string, 0, len(result.Results))
				for _, item := range result.Results {
					if strings.TrimSpace(item.Error) != "" {
						parts = append(parts, item.Error)
					}
				}
				if len(parts) > 0 {
					detail = strings.Join(parts, "; ")
				}
			}
			return larkCommandDoneMsg{label: "发送测试消息", err: errors.New(detail), next: larkStepSaving}
		}
		return larkCommandDoneMsg{label: "发送测试消息", text: "✓ lark-cli route test sent", next: larkStepSaving}
	}
}

func larkDoctorCmd(ctx context.Context, openClawGatewayURL string, configDir string) tea.Cmd {
	return func() tea.Msg {
		status, notices, err := larkDoctorStatusWithRetry(ctx, openClawGatewayURL, larkDoctorStatusOnce, larkDoctorRetryDelays, configDir)
		return larkDoctorDoneMsg{status: status, output: []byte(strings.Join(notices, "\n")), err: err}
	}
}

type larkDoctorStatusFunc func(context.Context, string) (*diagnose.LarkCLIStatus, error)

func larkDoctorStatusOnce(ctx context.Context, openClawGatewayURL string) (*diagnose.LarkCLIStatus, error) {
	return larkDoctorStatusOnceWithConfigDir(ctx, openClawGatewayURL, "")
}

func larkDoctorStatusOnceWithConfigDir(ctx context.Context, openClawGatewayURL string, configDir string) (*diagnose.LarkCLIStatus, error) {
	conn, dc, err := initLarkDiagnoseClient(ctx, openClawGatewayURL)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return dc.LarkCLIStatus(ctx, configDir)
}

func larkDoctorStatusWithRetry(ctx context.Context, openClawGatewayURL string, check larkDoctorStatusFunc, delays []time.Duration, configDir ...string) (*diagnose.LarkCLIStatus, []string, error) {
	notices := make([]string, 0, len(delays))
	for attempt := 0; ; attempt++ {
		var status *diagnose.LarkCLIStatus
		var err error
		if strings.TrimSpace(firstNonEmpty(configDir...)) != "" {
			status, err = larkDoctorStatusOnceWithConfigDir(ctx, openClawGatewayURL, firstNonEmpty(configDir...))
		} else {
			status, err = check(ctx, openClawGatewayURL)
		}
		if err == nil {
			return status, notices, nil
		}
		if !isRetryableGatewayClosed(err) || attempt >= len(delays) {
			return nil, notices, err
		}
		notices = append(notices, fmt.Sprintf("OpenClaw Gateway 连接刚断开,正在自动重试 lark-cli 状态检查(%d/%d)。", attempt+1, len(delays)))
		if err := sleepContext(ctx, delays[attempt]); err != nil {
			return nil, notices, err
		}
	}
}

func larkUseProfileCmd(ctx context.Context, openClawGatewayURL string, profile string) tea.Cmd {
	return func() tea.Msg {
		conn, dc, err := initLarkDiagnoseClient(ctx, openClawGatewayURL)
		if err != nil {
			return larkProfileUseDoneMsg{profile: profile, err: err}
		}
		defer conn.Close()
		result, err := dc.UseLarkCLIProfile(ctx, diagnose.LarkCLIProfileUseRequest{Profile: profile})
		return larkProfileUseDoneMsg{profile: profile, result: result, err: err}
	}
}

func larkConfigBindCmd(ctx context.Context, openClawGatewayURL string, appID string) tea.Cmd {
	return func() tea.Msg {
		conn, dc, err := initLarkDiagnoseClient(ctx, openClawGatewayURL)
		if err != nil {
			return larkConfigBindDoneMsg{appID: appID, err: err}
		}
		defer conn.Close()
		result, err := dc.BindLarkCLIConfig(ctx, diagnose.LarkCLIConfigBindRequest{AppID: appID, Identity: "bot-only"})
		return larkConfigBindDoneMsg{appID: appID, result: result, err: err}
	}
}

func larkAuthLoginStartCmd(ctx context.Context, openClawGatewayURL string) tea.Cmd {
	return func() tea.Msg {
		conn, dc, err := initLarkDiagnoseClient(ctx, openClawGatewayURL)
		if err != nil {
			return larkAuthLoginStartDoneMsg{err: err}
		}
		defer conn.Close()
		result, err := dc.StartLarkCLIAuthLogin(ctx)
		return larkAuthLoginStartDoneMsg{result: result, err: err}
	}
}

func larkAuthLoginCompleteCmd(ctx context.Context, openClawGatewayURL string, deviceCode string) tea.Cmd {
	return func() tea.Msg {
		conn, dc, err := initLarkDiagnoseClient(ctx, openClawGatewayURL)
		if err != nil {
			return larkAuthLoginCompleteDoneMsg{err: err}
		}
		defer conn.Close()
		result, err := dc.CompleteLarkCLIAuthLogin(ctx, deviceCode)
		return larkAuthLoginCompleteDoneMsg{result: result, err: err}
	}
}

func larkAuthLoginCompleteCmdForIdentity(ctx context.Context, openClawGatewayURL string, identity diagnose.LarkForwardingIdentity, deviceCode string) tea.Cmd {
	return func() tea.Msg {
		conn, dc, err := initLarkDiagnoseClient(ctx, openClawGatewayURL)
		if err != nil {
			return larkAuthLoginCompleteDoneMsg{identity: identity, err: err}
		}
		defer conn.Close()
		result, err := dc.CompleteLarkCLIAuthLogin(ctx, deviceCode)
		return larkAuthLoginCompleteDoneMsg{identity: identity, result: result, err: err}
	}
}

func larkOpenClawBotAccountsCmd(ctx context.Context, openClawGatewayURL string, configDir string) tea.Cmd {
	return func() tea.Msg {
		conn, dc, err := initLarkDiagnoseClient(ctx, openClawGatewayURL)
		if err != nil {
			return larkOpenClawBotAccountsDoneMsg{err: err}
		}
		defer conn.Close()
		status, err := dc.LarkCLIStatus(ctx, configDir)
		if err != nil {
			return larkOpenClawBotAccountsDoneMsg{err: err}
		}
		return larkOpenClawBotAccountsDoneMsg{accounts: larkBotProfileChoices(status)}
	}
}

func larkOpenClawBotConfigCmd(ctx context.Context, openClawGatewayURL string, bots []larkTargetChoice) tea.Cmd {
	return func() tea.Msg {
		conn, dc, err := initLarkDiagnoseClient(ctx, openClawGatewayURL)
		if err != nil {
			return larkIdentitiesDoneMsg{err: err}
		}
		defer conn.Close()
		identities := make([]diagnose.LarkForwardingIdentity, 0, len(bots))
		for _, bot := range bots {
			appID := strings.TrimSpace(bot.ID)
			if appID == "" {
				continue
			}
			identity := forwardingIdentity("bot", firstNonEmpty(bot.Label, appID), appID, "openclaw")
			result, err := dc.BindLarkCLIConfig(ctx, diagnose.LarkCLIConfigBindRequest{
				AppID:            appID,
				Identity:         "bot-only",
				Profile:          identity.Profile,
				Name:             identity.Profile,
				Slot:             identity.Slot,
				LarkCLIConfigDir: identity.LarkCLIConfigDir,
			})
			if err != nil {
				return larkIdentitiesDoneMsg{identities: identities, err: err}
			}
			if result != nil && !result.OK {
				return larkIdentitiesDoneMsg{identities: identities, err: errors.New(firstNonEmpty(strings.Join(result.Errors, "; "), result.Output, "lark-cli config bind failed"))}
			}
			if result != nil && strings.TrimSpace(result.LarkCLIConfigDir) != "" {
				identity.LarkCLIConfigDir = strings.TrimSpace(result.LarkCLIConfigDir)
			}
			identities = append(identities, identity)
		}
		return larkIdentitiesDoneMsg{identities: identities}
	}
}

func larkUserOAuthBindStartCmd(ctx context.Context, openClawGatewayURL string, identity diagnose.LarkForwardingIdentity) tea.Cmd {
	return func() tea.Msg {
		conn, dc, err := initLarkDiagnoseClient(ctx, openClawGatewayURL)
		if err != nil {
			return larkAuthLoginStartDoneMsg{identity: identity, err: err}
		}
		defer conn.Close()
		result, err := dc.BindLarkCLIConfig(ctx, diagnose.LarkCLIConfigBindRequest{
			AppID:            identity.AppID,
			Identity:         "user-default",
			Profile:          identity.Profile,
			Name:             identity.Profile,
			Slot:             identity.Slot,
			LarkCLIConfigDir: identity.LarkCLIConfigDir,
		})
		if err != nil {
			return larkAuthLoginStartDoneMsg{identity: identity, err: err}
		}
		if result != nil && !result.OK {
			return larkAuthLoginStartDoneMsg{identity: identity, err: errors.New(firstNonEmpty(strings.Join(result.Errors, "; "), result.Output, "lark-cli config bind failed"))}
		}
		login, err := dc.StartLarkCLIAuthLogin(ctx, identity.LarkCLIConfigDir)
		return larkAuthLoginStartDoneMsg{identity: identity, result: login, err: err}
	}
}

func larkIdentityChatsCmd(ctx context.Context, openClawGatewayURL string, identityID string, identity diagnose.LarkForwardingIdentity) tea.Cmd {
	return func() tea.Msg {
		conn, dc, err := initLarkDiagnoseClient(ctx, openClawGatewayURL)
		if err != nil {
			return larkIdentityChatsDoneMsg{identityID: identityID, err: err}
		}
		defer conn.Close()
		result, err := dc.SearchLarkTargets(ctx, diagnose.LarkTargetSearchRequest{
			Kind:             "chat",
			Sender:           normalizeSender(identity.Kind),
			LarkCLIConfigDir: identity.LarkCLIConfigDir,
			Profile:          identity.Profile,
		})
		if err != nil {
			return larkIdentityChatsDoneMsg{identityID: identityID, err: err}
		}
		if len(result.Errors) > 0 && len(result.Choices) == 0 {
			return larkIdentityChatsDoneMsg{identityID: identityID, err: errors.New(strings.Join(result.Errors, "; "))}
		}
		return larkIdentityChatsDoneMsg{identityID: identityID, choices: larkChoicesFromDiagnose(result.Choices)}
	}
}

func isRetryableGatewayClosed(err error) bool {
	return errors.Is(err, gateway.ErrClosed)
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func larkSearchChatsCmd(ctx context.Context, openClawGatewayURL string, query string, sender string) tea.Cmd {
	return func() tea.Msg {
		conn, dc, err := initLarkDiagnoseClient(ctx, openClawGatewayURL)
		if err != nil {
			return larkChoicesDoneMsg{kind: "群聊", err: err}
		}
		defer conn.Close()
		result, err := dc.SearchLarkTargets(ctx, diagnose.LarkTargetSearchRequest{
			Kind:   "chat",
			Sender: normalizeSender(sender),
			Query:  query,
		})
		if err != nil {
			return larkChoicesDoneMsg{kind: "群聊", err: err}
		}
		if len(result.Errors) > 0 && len(result.Choices) == 0 {
			return larkChoicesDoneMsg{kind: "群聊", err: errors.New(strings.Join(result.Errors, "; "))}
		}
		choices := larkChoicesFromDiagnose(result.Choices)
		return larkChoicesDoneMsg{kind: "群聊", choices: choices, err: err}
	}
}

func larkSearchUsersCmd(ctx context.Context, openClawGatewayURL string, query string, sender string) tea.Cmd {
	return func() tea.Msg {
		conn, dc, err := initLarkDiagnoseClient(ctx, openClawGatewayURL)
		if err != nil {
			return larkChoicesDoneMsg{kind: "用户", err: err}
		}
		defer conn.Close()
		result, err := dc.SearchLarkTargets(ctx, diagnose.LarkTargetSearchRequest{
			Kind:   "user",
			Sender: normalizeSender(sender),
			Query:  query,
		})
		if err != nil {
			return larkChoicesDoneMsg{kind: "用户", err: err}
		}
		if len(result.Errors) > 0 && len(result.Choices) == 0 {
			return larkChoicesDoneMsg{kind: "用户", err: errors.New(strings.Join(result.Errors, "; "))}
		}
		choices := larkChoicesFromDiagnose(result.Choices)
		return larkChoicesDoneMsg{kind: "用户", choices: choices, err: err}
	}
}

func larkTestTargetsCmd(ctx context.Context, openClawGatewayURL string, sender string, targets []larkTargetChoice) tea.Cmd {
	return func() tea.Msg {
		var b strings.Builder
		conn, dc, err := initLarkDiagnoseClient(ctx, openClawGatewayURL)
		if err != nil {
			return larkCommandDoneMsg{label: "发送测试消息", err: err, next: larkStepSaving}
		}
		defer conn.Close()
		result, err := dc.TestLarkTargets(ctx, diagnose.LarkTargetTestRequest{
			Sender:  normalizeSender(sender),
			Targets: diagnoseTargetsFromChoices(targets),
			Card:    json.RawMessage(testLarkCardContent()),
		})
		if err != nil {
			return larkCommandDoneMsg{label: "发送测试消息", err: err, next: larkStepSaving}
		}
		for _, line := range result.Errors {
			if strings.TrimSpace(line) != "" {
				fmt.Fprintf(&b, "✗ %s\n", strings.TrimSpace(line))
			}
		}
		for _, entry := range result.Results {
			targetID := strings.TrimSpace(entry.Target.ID)
			if entry.OK {
				fmt.Fprintf(&b, "✓ 测试发送成功 %s\n", targetID)
				continue
			}
			text := strings.TrimSpace(entry.Error)
			if text == "" {
				text = strings.TrimSpace(entry.Output)
			}
			if text == "" {
				text = "OpenClaw exec returned failure"
			}
			fmt.Fprintf(&b, "✗ 测试发送失败 %s: %s\n", targetID, text)
		}
		return larkCommandDoneMsg{label: "发送测试消息", text: strings.TrimSpace(b.String()), next: larkStepSaving}
	}
}

func larkChoicesFromDiagnose(values []diagnose.LarkTarget) []larkTargetChoice {
	out := make([]larkTargetChoice, 0, len(values))
	for _, value := range values {
		out = append(out, larkTargetChoice{Type: value.Type, ID: value.ID, Label: value.Label})
	}
	return out
}

func larkOpenClawBotChoices(output []byte) []larkTargetChoice {
	var value any
	if json.Unmarshal(output, &value) != nil {
		return nil
	}
	choices := make([]larkTargetChoice, 0)
	seen := map[string]bool{}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			appID := firstStringForKeys(x, "appId", "appID", "app_id", "cliAppId", "cli_app_id")
			if appID != "" && !seen[appID] {
				seen[appID] = true
				label := firstNonEmpty(firstStringForKeys(x, "name", "label", "displayName", "display_name"), appID)
				choices = append(choices, larkTargetChoice{Type: "bot", ID: appID, Label: label})
			}
			for _, child := range x {
				walk(child)
			}
		case []any:
			for _, child := range x {
				walk(child)
			}
		}
	}
	walk(value)
	return choices
}

func forwardingIdentity(kind string, label string, appID string, source string) diagnose.LarkForwardingIdentity {
	id := larkIdentityID(kind, source, appID, label)
	return diagnose.LarkForwardingIdentity{
		ID:               id,
		Kind:             normalizeSender(kind),
		Label:            strings.TrimSpace(label),
		AppID:            strings.TrimSpace(appID),
		Profile:          strings.TrimSpace(appID),
		Source:           strings.TrimSpace(source),
		Slot:             id,
		LarkCLIConfigDir: larkSlotConfigDir(id),
		Enabled:          true,
	}
}

func larkIdentityID(kind string, source string, appID string, label string) string {
	base := strings.Join([]string{normalizeSender(kind), strings.TrimSpace(source), strings.TrimSpace(appID), strings.TrimSpace(label)}, "-")
	base = strings.ToLower(base)
	var b strings.Builder
	lastDash := false
	for _, r := range base {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '.' || r == ':'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	id := strings.Trim(b.String(), "-")
	if id == "" {
		id = "lark-identity"
	}
	return id
}

func larkSlotConfigDir(slot string) string {
	slot = larkIdentityID("slot", "", slot, "")
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return filepath.Join(".termind", "openclaw-lark-cli", slot)
	}
	return filepath.Join(home, ".config", "termind", "openclaw-lark-cli", slot)
}

func firstStringForKeys(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if s, ok := stringValue(values[key]); ok && s != "" {
			return s
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func diagnoseTargetsFromChoices(values []larkTargetChoice) []diagnose.LarkTarget {
	out := make([]diagnose.LarkTarget, 0, len(values))
	for _, value := range values {
		out = append(out, diagnose.LarkTarget{Type: normalizeTargetType(value.Type), ID: strings.TrimSpace(value.ID), Label: strings.TrimSpace(value.Label), Enabled: true})
	}
	return out
}

func larkInstallPluginCmd(ctx context.Context, next larkInitStep, pluginSpec string) tea.Cmd {
	return func() tea.Msg {
		var b strings.Builder
		output, err := runOutput(ctx, 30*time.Second, "openclaw", "plugins", "install", pluginSpec)
		text := strings.TrimSpace(string(output))
		if err != nil {
			if strings.Contains(text, "plugin already exists") {
				b.WriteString("✓ openclaw plugins install 已存在,正在刷新\n")
				output, err = runOutput(ctx, 30*time.Second, "openclaw", "plugins", "uninstall", "termind", "--force")
				text = strings.TrimSpace(string(output))
				if err != nil {
					if isUnmanagedOpenClawPluginText(text) {
						b.WriteString("✓ OpenClaw 已加载 untracked termind 插件,跳过自动刷新\n")
						b.WriteString("  如需强制刷新,请先在 OpenClaw 侧移除手动加载项后重跑。\n")
						return larkCommandDoneMsg{label: "openclaw plugins install/enable", text: strings.TrimSpace(b.String()), next: next}
					}
					if text != "" {
						return larkCommandDoneMsg{label: "openclaw plugins uninstall termind", text: b.String() + text, err: err, next: next}
					}
					return larkCommandDoneMsg{label: "openclaw plugins uninstall termind", text: strings.TrimSpace(b.String()), err: err, next: next}
				}
				b.WriteString("✓ openclaw plugins uninstall termind\n")
				output, err = runOutput(ctx, 30*time.Second, "openclaw", "plugins", "install", pluginSpec)
				text = strings.TrimSpace(string(output))
				if err != nil {
					if text != "" {
						return larkCommandDoneMsg{label: "openclaw plugins install", text: b.String() + text, err: err, next: next}
					}
					return larkCommandDoneMsg{label: "openclaw plugins install", text: strings.TrimSpace(b.String()), err: err, next: next}
				}
				b.WriteString("✓ openclaw plugins install\n")
			} else {
				if text != "" {
					return larkCommandDoneMsg{label: "openclaw plugins install", text: text, err: err, next: next}
				}
				return larkCommandDoneMsg{label: "openclaw plugins install", err: err, next: next}
			}
		} else {
			b.WriteString("✓ openclaw plugins install\n")
		}
		output, err = runOutput(ctx, 30*time.Second, "openclaw", "plugins", "enable", "termind")
		text = strings.TrimSpace(string(output))
		if err != nil {
			if text != "" {
				return larkCommandDoneMsg{label: "openclaw plugins enable termind", text: b.String() + text, err: err, next: next}
			}
			return larkCommandDoneMsg{label: "openclaw plugins enable termind", text: strings.TrimSpace(b.String()), err: err, next: next}
		}
		b.WriteString("✓ openclaw plugins enable termind\n")
		return larkCommandDoneMsg{label: "openclaw plugins install/enable", text: strings.TrimSpace(b.String()), next: next, needsRestart: true}
	}
}

func isUnmanagedOpenClawPluginText(text string) bool {
	value := strings.ToLower(text)
	return strings.Contains(value, "untracked") ||
		strings.Contains(value, "not managed by plugins config/install records") ||
		strings.Contains(value, "loaded without install/load-path provenance")
}

func larkStatusCmd(ctx context.Context, label string, next larkInitStep, name string, args ...string) tea.Cmd {
	return func() tea.Msg {
		output, err := runOutput(ctx, 30*time.Second, name, args...)
		text := strings.TrimSpace(string(output))
		if err != nil {
			if label == "openclaw plugins install" && strings.Contains(text, "plugin already exists") {
				return larkCommandDoneMsg{label: label, text: "✓ " + label + " 已存在,跳过\n如需刷新,请先在 OpenClaw 侧删除旧插件目录后重跑。", next: next}
			}
			if text != "" {
				err = fmt.Errorf("%w: %s", err, text)
			}
			return larkCommandDoneMsg{label: label, err: err, next: next}
		}
		return larkCommandDoneMsg{label: label, text: "✓ " + label, next: next, needsRestart: openClawCommandNeedsRestart(label)}
	}
}

func larkConfigureToolsCmd(ctx context.Context, next larkInitStep) tea.Cmd {
	return func() tea.Msg {
		var out bytes.Buffer
		err := configureOpenClawToolAllowlist(ctx, &out)
		return larkCommandDoneMsg{label: "openclaw config set tools.alsoAllow", text: strings.TrimSpace(out.String()), err: err, next: next, needsRestart: err == nil}
	}
}

func larkInstallLarkCLICmd(ctx context.Context, next larkInitStep, retry larkInitStep) tea.Cmd {
	return func() tea.Msg {
		var b strings.Builder
		output, err := runOutput(ctx, 120*time.Second, "npm", "install", "-g", "@larksuite/cli")
		text := strings.TrimSpace(string(output))
		if err != nil {
			if text != "" {
				return larkCommandDoneMsg{label: "npm install -g @larksuite/cli", text: text, err: err, next: retry}
			}
			return larkCommandDoneMsg{label: "npm install -g @larksuite/cli", err: err, next: retry}
		}
		b.WriteString("✓ npm install -g @larksuite/cli\n")
		output, err = runOutput(ctx, 120*time.Second, "npx", "skills", "add", "larksuite/cli", "-y", "-g")
		text = strings.TrimSpace(string(output))
		if err != nil {
			if text != "" {
				return larkCommandDoneMsg{label: "npx skills add larksuite/cli", text: b.String() + text, err: err, next: retry}
			}
			return larkCommandDoneMsg{label: "npx skills add larksuite/cli", text: strings.TrimSpace(b.String()), err: err, next: retry}
		}
		b.WriteString("✓ npx skills add larksuite/cli -y -g\n")
		return larkCommandDoneMsg{label: "安装 lark-cli", text: strings.TrimSpace(b.String()), next: next, needsRestart: true}
	}
}

func larkRemoteStatusCmd(ctx context.Context, label string, next larkInitStep, target string, remoteCommand string) tea.Cmd {
	return func() tea.Msg {
		output, err := runOutput(ctx, 60*time.Second, "ssh", target, remoteCommand)
		text := strings.TrimSpace(string(output))
		if err != nil {
			if strings.Contains(label, "plugins install") && strings.Contains(text, "plugin already exists") {
				return larkCommandDoneMsg{label: label, text: "✓ " + label + " 已存在,跳过", next: next}
			}
			if text != "" {
				err = fmt.Errorf("%w: %s", err, text)
			}
			return larkCommandDoneMsg{label: label, err: err, next: next}
		}
		return larkCommandDoneMsg{label: label, text: "✓ " + label, next: next, needsRestart: openClawCommandNeedsRestart(label)}
	}
}

func openClawCommandNeedsRestart(label string) bool {
	label = strings.ToLower(strings.TrimSpace(label))
	return strings.Contains(label, "plugins install") ||
		strings.Contains(label, "plugins enable") ||
		strings.Contains(label, "config set tools.alsoallow") ||
		strings.Contains(label, "approvals allowlist")
}

func isOpenClawSetupStep(step larkInitStep) bool {
	switch step {
	case larkStepLocalInstallPlugin, larkStepLocalPluginSpec, larkStepLocalToolsAllow, larkStepLocalExecAllow, larkStepLocalGatewayRestart, larkStepRemotePlugin, larkStepRemotePluginSpec, larkStepRemoteTools, larkStepRemoteApprovals, larkStepRemoteGatewayRestart:
		return true
	default:
		return false
	}
}

func jsonMarshalString(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
