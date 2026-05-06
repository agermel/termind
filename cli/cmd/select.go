// Package cmd 中的 select 命令组用于在 termind init 完成后,
// 快速调整 Lark 转发目标(群聊/个人),无需重新走 OpenClaw / lark-cli 的完整流程。
package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"

	"github.com/spf13/cobra"

	"termind/internal/config"
	"termind/internal/gateway"
)

var selectCmd = &cobra.Command{
	Use:   "select",
	Short: "快速调整 Lark 转发目标(在 termind init 完成后使用)",
	Long: `select 是已经跑过 termind init 之后的快捷命令组。

子命令:
  chat     重新选择 Lark 转发的群聊/个人目标(跳过 OpenClaw / lark-cli / Profile 配置)
`,
}

var selectChatCmd = &cobra.Command{
	Use:   "chat",
	Short: "直接进入 Lark 群聊/个人目标选择页面",
	Long: `select chat 在 OpenClaw 已配对、Lark 身份已绑定的前提下,跳过

  1. 启用确认
  2. OpenClaw 插件 / tools / approvals
  3. lark-cli doctor / 安装 / 登录
  4. Profile / 身份绑定

直接进入

  5. 目标选择(保留现有 / 加自己 / 搜索群聊 / 搜索用户)
  6. 测试发送

适用场景: 已经跑过 termind init,后面只想加几个群聊或个人目标。`,
	RunE: runSelectChat,
}

func init() {
	selectCmd.AddCommand(selectChatCmd)
	rootCmd.AddCommand(selectCmd)
}

func runSelectChat(cmd *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg == nil || cfg.ServerURL == "" {
		return errors.New("尚未配置 OpenClaw,先运行 termind init 完成首次配对再用 select chat。")
	}
	if len(cfg.Lark.Forwarding.Identities) == 0 {
		return errors.New("尚未绑定任何 Lark 身份,先运行 termind init 完成 Profile 步骤再用 select chat。")
	}

	ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer cancel()

	openClawGatewayURL := gateway.NormalizeGatewayURL(cfg.ServerURL)
	if err := runLarkSelectChatTUI(ctx, cmd.InOrStdin(), cmd.OutOrStdout(), openClawGatewayURL, cfg); err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(cmd.OutOrStdout(), "已取消 select chat。")
			return nil
		}
		return err
	}
	return nil
}
