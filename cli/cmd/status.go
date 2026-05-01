package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"termind/internal/config"
	"termind/internal/identity"
	"termind/internal/integration"
	"termind/internal/pairing"
)

// statusCmd 是 M4 ⑥ 模块的 cobra 入口。
//
// 只读地展示当前设备的 termind 状态:
//   - 配置文件路径和 server URL
//   - 本机 device ID 和公钥指纹
//   - token 是否存在(不显示 token 本身)
//   - shell integration 是否装了(看 ~/.zshrc)
//
// 刻意不在这里主动连 ws:一来会卡住命令,二来连不上的原因五花八门,
// 留给未来的 `termind doctor`(M5 可能加)做深度检查。
var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "显示当前 termind 的配对和连线状态",
	Long:  `只读地展示设备身份、配对状态、token 是否存在,用于排错。`,
	RunE:  runStatus,
}

func runStatus(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()

	// 1. 配置
	cfgPath, _ := config.Path()
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(out, "config:       %s (读取失败: %v)\n", cfgPath, err)
	} else {
		fmt.Fprintf(out, "config:       %s\n", cfgPath)
		if cfg.ServerURL == "" {
			fmt.Fprintln(out, "  server:     (未配对,请运行 termind pair --server ...)")
		} else {
			fmt.Fprintf(out, "  server:     %s\n", cfg.ServerURL)
			if !cfg.PairedAt.IsZero() {
				fmt.Fprintf(out, "  paired at:  %s\n", cfg.PairedAt.Format("2006-01-02 15:04:05 MST"))
			}
		}
	}

	// 2. 身份
	if _, err := os.Stat(mustKeyPath()); os.IsNotExist(err) {
		fmt.Fprintln(out, "\nidentity:     (尚未生成,运行 termind pair 时会自动创建)")
	} else {
		id, err := identity.LoadOrCreate()
		if err != nil {
			fmt.Fprintf(out, "\nidentity:     加载失败: %v\n", err)
		} else {
			fmt.Fprintf(out, "\nidentity:\n")
			fmt.Fprintf(out, "  device id:  %s\n", id.DeviceID())
			fmt.Fprintf(out, "  fingerprint:%s\n", id.Fingerprint())
		}
	}

	// 3. token
	tok, err := pairing.LoadToken()
	if err != nil {
		fmt.Fprintf(out, "\ntoken:        读取失败: %v\n", err)
	} else if tok == "" {
		fmt.Fprintln(out, "\ntoken:        (不存在,未配对)")
	} else {
		fmt.Fprintf(out, "\ntoken:        已存在 (%d 字符,内容不显示)\n", len(tok))
	}

	// 4. shell integration
	installed, err := integration.IsInstalled()
	switch {
	case err != nil:
		fmt.Fprintf(out, "\nshell integration: 检查失败: %v\n", err)
	case installed:
		fmt.Fprintln(out, "\nshell integration: 已安装 (~/.zshrc)")
	default:
		fmt.Fprintln(out, "\nshell integration: 未安装 (运行 termind init)")
	}

	return nil
}

func mustKeyPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "termind", "keys", "device.key")
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
