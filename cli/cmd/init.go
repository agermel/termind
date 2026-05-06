package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"termind/internal/config"
	"termind/internal/gateway"
	"termind/internal/identity"
	"termind/internal/integration"
	"termind/internal/pairing"
)

var (
	initSetupCode       string
	initTimeout         time.Duration
	initSkipShell       bool
	initSkipLark        bool
	initManualSetupCode bool
	initContinueShell   bool
)

var (
	errOpenClawNotFound = errors.New("openclaw command not found")
	errInitCancelled    = errors.New("init cancelled")
)

type initOpenClawMode string

const (
	initOpenClawLocal  initOpenClawMode = "local"
	initOpenClawRemote initOpenClawMode = "remote"
)

type initOpenClawConnection struct {
	Setup    *pairing.SetupCode
	Mode     initOpenClawMode
	AuthPath string
}

type initApprovalStatusFunc func(string)

// initCmd 是首次配置入口:安装 shell integration,并通过 OpenClaw Gateway
// device pairing request 完成设备批准。
var initCmd = &cobra.Command{
	Use:   "init",
	Short: "首次配置 termind 并连接 OpenClaw",
	Long: `配置 termind 的 shell integration,并用 OpenClaw setup code 完成设备配对。

具体动作:
  1. 把集成脚本写到 ~/.config/termind/integration.zsh
  2. 在 ~/.zshrc 末尾加一行 source(用 marker 包裹,可幂等)
  3. 在 OpenClaw 里生成 setup code
  4. 输入 setup code,termind 用短期 bootstrapToken 发起连接
  5. 等待你在 OpenClaw 中批准这台设备`,
	RunE: runInit,
}

func runInit(cmd *cobra.Command, _ []string) error {
	ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer cancel()

	snap := snapshotInitState()
	if err := runInitWithContext(ctx, cmd); err != nil {
		rolledBack := rollbackInitState(snap)
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(cmd.OutOrStdout(), "已取消 init。")
			if rolledBack {
				fmt.Fprintln(cmd.OutOrStdout(), "已回退本次 init 写入的中间状态;再次运行 termind 时会重新进入 init 流程。")
			}
			return errInitCancelled
		}
		return err
	}
	return nil
}

// initStateSnapshot 记录 init 启动前 ~/.config/termind/ 的关键状态,
// 用于在 init 中途失败/取消时回退,避免遗留半成品配置导致下次启动跳过 init。
type initStateSnapshot struct {
	dir              string
	dirExistedBefore bool
	wasConfigured    bool
}

func snapshotInitState() initStateSnapshot {
	snap := initStateSnapshot{}
	dir, err := config.Dir()
	if err != nil || dir == "" {
		return snap
	}
	snap.dir = dir
	if _, err := os.Stat(dir); err == nil {
		snap.dirExistedBefore = true
	}
	if cfg, err := config.Load(); err == nil && cfg != nil && strings.TrimSpace(cfg.ServerURL) != "" {
		snap.wasConfigured = true
	}
	return snap
}

// rollbackInitState 把 ~/.config/termind/ 还原到 init 启动前。
//
// 仅在 init 启动前用户尚未配置(cfg.ServerURL 为空)时才回退,避免重新跑 init
// 的用户被误删现有配置。返回是否真的执行了回退,便于上层决定是否提示用户。
func rollbackInitState(snap initStateSnapshot) bool {
	if snap.dir == "" || snap.wasConfigured {
		return false
	}
	if !snap.dirExistedBefore {
		if err := os.RemoveAll(snap.dir); err != nil {
			return false
		}
		return true
	}
	rolledBack := false
	for _, name := range []string{"config.json", "device-auth.json", "token"} {
		if err := os.Remove(filepath.Join(snap.dir, name)); err == nil {
			rolledBack = true
		}
	}
	return rolledBack
}

func runInitWithContext(ctx context.Context, cmd *cobra.Command) error {
	if !initSkipShell {
		r, err := integration.Install()
		if err != nil {
			return err
		}
		if r.AlreadyInstalled {
			fmt.Printf("✓ shell integration 已经在 %s\n", r.RcPath)
			fmt.Printf("  脚本本体已刷新: %s\n", r.ScriptPath)
		} else {
			fmt.Printf("✓ shell integration 安装成功\n")
			fmt.Printf("  脚本写入: %s\n", r.ScriptPath)
			fmt.Printf("  注入到:  %s\n", r.RcPath)
		}
		fmt.Println()
	}

	id, err := identity.LoadOrCreate()
	if err != nil {
		return fmt.Errorf("load identity: %w", err)
	}

	openClaw, err := runInitOpenClawTUI(ctx, cmd, id, initTimeout)
	if err != nil {
		return err
	}
	setup := openClaw.Setup

	fmt.Println()
	fmt.Printf("✓ OpenClaw 连接已批准,device token 已存至 %s\n", openClaw.AuthPath)
	fmt.Println()
	if !initSkipLark {
		if err := runLarkInitTUI(ctx, cmd.InOrStdin(), cmd.OutOrStdout(), gateway.NormalizeGatewayURL(setup.URL), openClaw.Mode == initOpenClawLocal); err != nil {
			return err
		}
		fmt.Println()
	}
	printInitNextSteps(os.Stdout, initContinueShell)
	return nil
}

func resolveInitSetupCode(ctx context.Context, cmd *cobra.Command) (*pairing.SetupCode, error) {
	openClaw, err := resolveInitOpenClawConnection(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return openClaw.Setup, nil
}

func resolveInitOpenClawConnection(ctx context.Context, cmd *cobra.Command) (*initOpenClawConnection, error) {
	if strings.TrimSpace(initSetupCode) != "" {
		setup, err := pairing.ParseSetupCode(initSetupCode)
		if err != nil {
			return nil, err
		}
		mode := initOpenClawRemote
		if isLocalOpenClawGatewayURL(gateway.NormalizeGatewayURL(setup.URL)) {
			mode = initOpenClawLocal
		}
		return &initOpenClawConnection{Setup: setup, Mode: mode}, nil
	}
	reader := bufio.NewReader(cmd.InOrStdin())
	mode, err := promptOpenClawMode(ctx, reader, cmd.OutOrStdout())
	if err != nil {
		return nil, err
	}
	if mode == initOpenClawLocal {
		return resolveLocalOpenClawConnection(ctx, reader, cmd.OutOrStdout())
	}
	return resolveRemoteOpenClawConnection(ctx, reader, cmd.OutOrStdout())
}

func promptOpenClawMode(ctx context.Context, reader *bufio.Reader, out io.Writer) (initOpenClawMode, error) {
	defaultChoice := "1"
	if _, err := exec.LookPath("openclaw"); err != nil {
		defaultChoice = "2"
	}
	fmt.Fprintln(out, "OpenClaw 连接配置")
	fmt.Fprintln(out, "  1. 本机 OpenClaw: 自动生成 setup code,并尽量自动配置插件/allowlist")
	fmt.Fprintln(out, "  2. 远程 OpenClaw: 在 OpenClaw 所在机器生成 setup code,本机只粘贴连接码")
	choice, err := promptLine(ctx, reader, out, "OpenClaw 在哪里? 选择 1/2", defaultChoice)
	if err != nil {
		return "", err
	}
	switch strings.TrimSpace(choice) {
	case "1", "local", "本机":
		return initOpenClawLocal, nil
	case "2", "remote", "远程":
		return initOpenClawRemote, nil
	default:
		return "", fmt.Errorf("未知 OpenClaw 位置选择: %s", choice)
	}
}

func resolveLocalOpenClawConnection(ctx context.Context, reader *bufio.Reader, out io.Writer) (*initOpenClawConnection, error) {
	if !initManualSetupCode {
		localCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		setup, err := generateLocalOpenClawSetupCode(localCtx)
		cancel()
		if err == nil {
			fmt.Fprintln(out, "✓ 已从本机 OpenClaw 自动生成 setup code")
			fmt.Fprintln(out)
			return &initOpenClawConnection{Setup: setup, Mode: initOpenClawLocal}, nil
		}
		if errors.Is(err, errOpenClawNotFound) {
			fmt.Fprintln(out, "未找到本机 openclaw 命令,无法自动生成 setup code。")
		} else {
			fmt.Fprintf(out, "本机 OpenClaw setup code 自动生成失败: %v\n", err)
		}
		fmt.Fprintln(out)
		ok, err := promptConfirm(ctx, reader, out, "改为手动粘贴 setup code", true)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("未完成 OpenClaw setup code 配置")
		}
	}
	printLocalSetupCodeInstructions(out)
	setup, err := promptSetupCode(ctx, reader, out)
	if err != nil {
		return nil, err
	}
	return &initOpenClawConnection{Setup: setup, Mode: initOpenClawLocal}, nil
}

func resolveRemoteOpenClawConnection(ctx context.Context, reader *bufio.Reader, out io.Writer) (*initOpenClawConnection, error) {
	printRemoteSetupCodeInstructions(out)
	setup, err := promptSetupCode(ctx, reader, out)
	if err != nil {
		return nil, err
	}
	return &initOpenClawConnection{Setup: setup, Mode: initOpenClawRemote}, nil
}

func promptSetupCode(ctx context.Context, reader *bufio.Reader, out io.Writer) (*pairing.SetupCode, error) {
	value, err := promptLine(ctx, reader, out, "OpenClaw setup code", "")
	if err != nil {
		return nil, err
	}
	if value == "" {
		return nil, fmt.Errorf("OpenClaw setup code 不能为空")
	}
	return pairing.ParseSetupCode(value)
}

func generateLocalOpenClawSetupCode(ctx context.Context) (*pairing.SetupCode, error) {
	if _, err := exec.LookPath("openclaw"); err != nil {
		return nil, errOpenClawNotFound
	}
	out, err := exec.CommandContext(ctx, "openclaw", "qr", "--setup-code-only", "--url", localOpenClawGatewayURL(ctx)).CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(out))
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("%s", message)
	}
	setup, err := parseSetupCodeFromOpenClawOutput(out)
	if err != nil {
		return nil, fmt.Errorf("parse generated setup code: %w", err)
	}
	return setup, nil
}

func parseSetupCodeFromOpenClawOutput(out []byte) (*pairing.SetupCode, error) {
	var setup *pairing.SetupCode
	var lastErr error
	for _, field := range strings.Fields(string(out)) {
		candidate := strings.Trim(field, "`'\"")
		parsed, err := pairing.ParseSetupCode(candidate)
		if err == nil {
			setup = parsed
			continue
		}
		lastErr = err
	}
	if setup != nil {
		return setup, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("setup code output is empty")
}

func localOpenClawGatewayURL(ctx context.Context) string {
	port := "18789"
	out, err := exec.CommandContext(ctx, "openclaw", "config", "get", "gateway.port").Output()
	if err == nil {
		value := strings.TrimSpace(string(out))
		if value != "" {
			port = value
		}
	}
	return "ws://127.0.0.1:" + port + "/v1/gateway"
}

func waitForDeviceApproval(ctx context.Context, setup *pairing.SetupCode, id *identity.Identity, timeout time.Duration) (string, error) {
	return waitForDeviceApprovalWithScopes(ctx, setup, id, timeout, requestedBootstrapScopes())
}

func requestedBootstrapScopes() []string {
	return pairing.DefaultScopes()
}

func waitForDeviceApprovalWithScopes(ctx context.Context, setup *pairing.SetupCode, id *identity.Identity, timeout time.Duration, scopes []string) (string, error) {
	return waitForDeviceApprovalWithScopesAndStatus(ctx, setup, id, timeout, scopes, nil)
}

func waitForDeviceApprovalWithStatus(ctx context.Context, setup *pairing.SetupCode, id *identity.Identity, timeout time.Duration, onStatus initApprovalStatusFunc) (string, error) {
	return waitForDeviceApprovalWithScopesAndStatus(ctx, setup, id, timeout, requestedBootstrapScopes(), onStatus)
}

func waitForDeviceApprovalWithScopesAndStatus(ctx context.Context, setup *pairing.SetupCode, id *identity.Identity, timeout time.Duration, scopes []string, onStatus initApprovalStatusFunc) (string, error) {
	if setup == nil {
		return "", errors.New("setup code is required")
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var requestID string
	nextNotice := time.Now()
	for attempt := 1; ; attempt++ {
		var issuedRole, issuedToken string
		var issuedScopes []string
		conn, err := gateway.Dial(waitCtx, gateway.DialOptions{
			ServerURL:        setup.URL,
			Identity:         id,
			BootstrapToken:   setup.BootstrapToken,
			Role:             pairing.DefaultRole,
			Scopes:           scopes,
			ClientID:         pairing.DefaultClientID,
			ClientMode:       pairing.DefaultClientMode,
			ClientVersion:    Version,
			HandshakeTimeout: 10 * time.Second,
			OnDeviceToken: func(role, token string, scopes []string) {
				issuedRole = role
				issuedToken = token
				issuedScopes = scopes
			},
		})
		if err == nil {
			defer conn.Close()
			if strings.TrimSpace(issuedToken) == "" {
				return "", fmt.Errorf("gateway did not issue hello-ok.auth.deviceToken")
			}
			if issuedRole == "" {
				issuedRole = pairing.DefaultRole
			}
			if issuedRole == pairing.DefaultRole && !pairing.HasScopes(issuedScopes, scopes) {
				return "", fmt.Errorf("OpenClaw issued insufficient scopes: got %v, need %v", issuedScopes, scopes)
			}
			path, err := pairing.SaveDeviceAuth(id.DeviceID(), issuedRole, issuedToken, issuedScopes)
			if err != nil {
				return "", fmt.Errorf("save device auth: %w", err)
			}
			cfg, err := config.Load()
			if err != nil {
				return "", fmt.Errorf("load config: %w", err)
			}
			cfg.ServerURL = gateway.NormalizeGatewayURL(setup.URL)
			cfg.Role = issuedRole
			cfg.PairedAt = time.Now().UTC()
			if err := config.Save(cfg); err != nil {
				return "", fmt.Errorf("save config: %w", err)
			}
			return path, nil
		}

		var ce *gateway.ConnectError
		if !errors.As(err, &ce) || !ce.IsPairingRequired() {
			if errors.As(err, &ce) && (ce.IsAuthTokenMissing() || ce.IsAuthPasswordMissing()) {
				return "", fmt.Errorf("OpenClaw Gateway 要求额外入口认证,当前 setup code 不能通过此 Gateway;请在 OpenClaw 里重新生成 setup code,或检查 Gateway auth 配置: %w", ce)
			}
			return "", err
		}
		if rid := ce.PairingRequestID(); rid != "" && rid != requestID {
			requestID = rid
			emitInitApprovalStatus(func(message string) {
				if message == "" {
					fmt.Println()
					return
				}
				fmt.Println(message)
			}, "已向 OpenClaw 发起设备批准请求。")
			emitInitApprovalStatus(func(message string) {
				if message == "" {
					fmt.Println()
					return
				}
				fmt.Println(message)
			}, fmt.Sprintf("请在 OpenClaw 中批准 request id: %s", requestID))
			if hint := ce.RemediationHint(); hint != "" {
				emitInitApprovalStatus(func(message string) {
					if message == "" {
						fmt.Println()
						return
					}
					fmt.Println(message)
				}, fmt.Sprintf("OpenClaw 提示: %s", hint))
			}
			emitInitApprovalStatus(func(message string) {
				if message == "" {
					fmt.Println()
					return
				}
				fmt.Println(message)
			}, "termind 会自动等待批准...")
			emitInitApprovalStatus(func(message string) {
				if message == "" {
					fmt.Println()
					return
				}
				fmt.Println(message)
			}, "")
		} else if time.Now().After(nextNotice) {
			emitInitApprovalStatus(func(message string) {
				if message == "" {
					fmt.Println()
					return
				}
				fmt.Println(message)
			}, fmt.Sprintf("等待 OpenClaw 批准中... (attempt %d)", attempt))
			nextNotice = time.Now().Add(15 * time.Second)
		}

		timer := time.NewTimer(2 * time.Second)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			return "", waitCtx.Err()
		case <-timer.C:
		}
	}
}

func isExpiredBootstrapTokenError(err error) bool {
	if err == nil {
		return false
	}
	var ce *gateway.ConnectError
	if errors.As(err, &ce) {
		text := strings.ToLower(ce.Error())
		return strings.Contains(text, "bootstrap token") &&
			(strings.Contains(text, "invalid") || strings.Contains(text, "expired"))
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "bootstrap token") &&
		(strings.Contains(text, "invalid") || strings.Contains(text, "expired"))
}

func emitInitApprovalStatus(onStatus initApprovalStatusFunc, message string) {
	if onStatus != nil {
		onStatus(message)
	}
}

func printLocalSetupCodeInstructions(w io.Writer) {
	fmt.Fprintln(w, "请先在本机 OpenClaw 里生成 setup code。")
	fmt.Fprintln(w, "常用方式:")
	fmt.Fprintln(w, "  openclaw qr --setup-code-only --url ws://127.0.0.1:18789/v1/gateway")
	fmt.Fprintln(w)
}

func printRemoteSetupCodeInstructions(w io.Writer) {
	fmt.Fprintln(w, "请在远程 OpenClaw 所在机器生成 setup code。")
	fmt.Fprintln(w, "常用方式:")
	fmt.Fprintln(w, "  openclaw qr --setup-code-only --url ws://<这台OpenClaw可被本机访问的地址>:18789/v1/gateway")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "如果远程 OpenClaw 有 device-pair 插件,也可以在 OpenClaw 里使用 /pair。")
	fmt.Fprintln(w, "生成后把 setup code 粘贴到这里。")
	fmt.Fprintln(w)
}

func printInitNextSteps(w io.Writer, continueShell bool) {
	if continueShell {
		fmt.Fprintln(w, "连接引导完成,正在进入 termind shell...")
		return
	}
	fmt.Fprintln(w, "下一步:")
	fmt.Fprintln(w, "  1. 重启 shell 或运行: source ~/.zshrc")
	fmt.Fprintln(w, "  2. 跑 termind shell 进入交互式 shell")
}

func init() {
	initCmd.Flags().StringVar(&initSetupCode, "setup-code", "", "OpenClaw setup code(base64url JSON,包含 url 和 bootstrapToken)")
	initCmd.Flags().DurationVar(&initTimeout, "timeout", 5*time.Minute, "等待 OpenClaw 批准的最长时间")
	initCmd.Flags().BoolVar(&initSkipShell, "skip-shell-integration", false, "跳过 shell integration 安装,只配置 OpenClaw")
	initCmd.Flags().BoolVar(&initSkipLark, "skip-lark", false, "跳过 Lark/lark-cli 转发配置")
	initCmd.Flags().BoolVar(&initManualSetupCode, "manual-setup-code", false, "跳过本机 openclaw 自动生成,改为手动粘贴 setup code")
	rootCmd.AddCommand(initCmd)
}
