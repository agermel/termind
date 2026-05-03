package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"termind/internal/config"
	"termind/internal/identity"
	"termind/internal/integration"
	"termind/internal/pairing"
	"termind/internal/shell"
)

// shellCmd 是 M1 模块的 cobra 入口。
//
// 启动后 termind 会:
//  1. 用 pty fork 一个 shell 子进程($SHELL 缺省 /bin/zsh)
//  2. 把当前 tty 切到 raw mode,所有按键透传给子 shell
//  3. 双向转发 stdin <-> ptmx,直到子 shell 退出
//
// 当前 M1 阶段只做透明包装,**没有任何 AI 功能**。
// 用户体验跟直接进 zsh 一致,这是后续 M2-M5 模块的地基。
var shellCmd = &cobra.Command{
	Use:   "shell",
	Short: "进入 termind 包装的交互式 shell(M1:透明 PTY 包装)",
	Long: `启动一个 PTY 包装的子 shell,所有键盘输入和 shell 输出双向透传。

当前 M1 阶段是纯透明包装,没有 AI 功能 —— 但它是 M2-M5 所有功能的地基。
失败命令的捕获 / 诊断渲染会在后续模块里加。

退出方式:跟原 shell 一样 (` + "`exit`" + ` / Ctrl+D)。`,
	RunE: runShell,
}

func runShell(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.ServerURL == "" {
		fmt.Println("第一次使用 termind,先完成 OpenClaw 连接引导。")
		fmt.Println()
		if err := runInitFromShell(); err != nil {
			return err
		}
		fmt.Println()
	} else {
		ready, err := hasCurrentDeviceAuth(cfg)
		if err != nil {
			return err
		}
		if !ready {
			fmt.Println("termind 需要刷新 OpenClaw 连接权限。")
			fmt.Println()
			if err := runInitFromShell(); err != nil {
				return err
			}
			fmt.Println()
		}
	}
	if err := refreshShellIntegration(); err != nil {
		return err
	}
	return shell.Run()
}

func runInitFromShell() error {
	initContinueShell = true
	defer func() { initContinueShell = false }()
	return runInit(rootCmd, nil)
}

func hasCurrentDeviceAuth(cfg *config.Config) (bool, error) {
	if cfg == nil || cfg.ServerURL == "" {
		return false, nil
	}
	if cfg.Role != "" && cfg.Role != pairing.DefaultRole {
		return false, nil
	}
	id, err := identity.LoadOrCreate()
	if err != nil {
		return false, fmt.Errorf("load identity: %w", err)
	}
	role := cfg.Role
	if role == "" {
		role = pairing.DefaultRole
	}
	auth, err := pairing.LoadDeviceAuth(id.DeviceID(), role)
	if err != nil {
		return false, fmt.Errorf("load device auth: %w", err)
	}
	return auth != nil && auth.Token != "", nil
}

func refreshShellIntegration() error {
	r, err := integration.Install()
	if err != nil {
		return err
	}
	if r.AlreadyInstalled {
		return nil
	}
	fmt.Printf("✓ shell integration 安装成功: %s\n\n", r.RcPath)
	return nil
}

func init() {
	rootCmd.AddCommand(shellCmd)
}
