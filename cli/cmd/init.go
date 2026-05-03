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
	initManualSetupCode bool
	initContinueShell   bool
)

var errOpenClawNotFound = errors.New("openclaw command not found")

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

	setup, err := resolveInitSetupCode(ctx, cmd)
	if err != nil {
		return err
	}

	id, err := identity.LoadOrCreate()
	if err != nil {
		return fmt.Errorf("load identity: %w", err)
	}

	fmt.Printf("device id:   %s\n", id.DeviceID())
	fmt.Printf("fingerprint: %s\n", id.Fingerprint())
	fmt.Printf("server:      %s\n", gateway.NormalizeGatewayURL(setup.URL))
	fmt.Println()

	path, err := waitForDeviceApproval(ctx, setup, id, initTimeout)
	if err != nil {
		return err
	}

	fmt.Println()
	fmt.Printf("✓ OpenClaw 连接已批准,device token 已存至 %s\n", path)
	fmt.Println()
	printInitNextSteps(os.Stdout, initContinueShell)
	return nil
}

func resolveInitSetupCode(ctx context.Context, cmd *cobra.Command) (*pairing.SetupCode, error) {
	if strings.TrimSpace(initSetupCode) != "" {
		return pairing.ParseSetupCode(initSetupCode)
	}
	if !initManualSetupCode {
		localCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		setup, err := generateLocalOpenClawSetupCode(localCtx)
		cancel()
		if err == nil {
			fmt.Println("✓ 已从本机 OpenClaw 自动生成 setup code")
			fmt.Println()
			return setup, nil
		}
		if !errors.Is(err, errOpenClawNotFound) {
			fmt.Printf("本机 OpenClaw setup code 自动生成失败: %v\n", err)
			fmt.Println()
		}
	}
	printSetupCodeInstructions()
	fmt.Print("OpenClaw setup code: ")
	reader := bufio.NewReader(cmd.InOrStdin())
	line, err := reader.ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return nil, fmt.Errorf("read setup code: %w", err)
	}
	value := strings.TrimSpace(line)
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
	code := strings.TrimSpace(string(out))
	setup, err := pairing.ParseSetupCode(code)
	if err != nil {
		return nil, fmt.Errorf("parse generated setup code: %w", err)
	}
	return setup, nil
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
			Scopes:           pairing.DefaultScopes(),
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
			path, err := pairing.SaveDeviceAuth(id.DeviceID(), issuedRole, issuedToken, issuedScopes)
			if err != nil {
				return "", fmt.Errorf("save device auth: %w", err)
			}
			if err := config.Save(&config.Config{
				ServerURL: gateway.NormalizeGatewayURL(setup.URL),
				Role:      issuedRole,
				PairedAt:  time.Now().UTC(),
			}); err != nil {
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
			fmt.Println("已向 OpenClaw 发起设备批准请求。")
			fmt.Printf("请在 OpenClaw 中批准 request id: %s\n", requestID)
			if hint := ce.RemediationHint(); hint != "" {
				fmt.Printf("OpenClaw 提示: %s\n", hint)
			}
			fmt.Println("termind 会自动等待批准...")
			fmt.Println()
		} else if time.Now().After(nextNotice) {
			fmt.Printf("等待 OpenClaw 批准中... (attempt %d)\n", attempt)
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

func printSetupCodeInstructions() {
	fmt.Println("请先在 OpenClaw 里生成 setup code。")
	fmt.Println("常用方式:")
	fmt.Println("  openclaw qr --setup-code-only --url ws://127.0.0.1:18789/v1/gateway")
	fmt.Println()
	fmt.Println("如果 OpenClaw 不在本机,请在 OpenClaw 侧用 public/tailscale URL 生成 setup code,")
	fmt.Println("或在 OpenClaw 的 device-pair 插件里使用 /pair。")
	fmt.Println()
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
	initCmd.Flags().BoolVar(&initManualSetupCode, "manual-setup-code", false, "跳过本机 openclaw 自动生成,改为手动粘贴 setup code")
	rootCmd.AddCommand(initCmd)
}
