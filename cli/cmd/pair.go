package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/spf13/cobra"

	"termind/internal/config"
	"termind/internal/identity"
	"termind/internal/pairing"
)

// pair 命令的 flag。
var (
	pairServer  string
	pairTimeout time.Duration
)

// pairCmd 是 M4 ② 模块的 cobra 入口。
//
// 流程:
//
//  1. 加载(或首次生成)本机 ed25519 密钥
//  2. 向 server 发 /v1/pair/start,拿到 pair_code
//  3. 打印 pair_code + 公钥指纹,让操作员去 OpenClaw 批准
//  4. 轮询 /v1/pair/poll,收到 approved 时把 token 存 ~/.config/termind/token
//
// 退出码:
//   - 0 批准成功
//   - 非 0 超时/拒绝/网络错误
var pairCmd = &cobra.Command{
	Use:   "pair",
	Short: "跟 OpenClaw 服务器配对,拿到长期 token(M4)",
	Long: `termind pair 向指定的 OpenClaw 服务器发起一次设备配对。

工作方式:
  1. 本机生成(或加载已有)ed25519 密钥对,私钥永不离开本机
  2. 向 server 发 POST /v1/pair/start,拿到 6 位配对码
  3. 屏幕显示配对码 + 公钥指纹
  4. 操作员在 OpenClaw 后台核对指纹后点"批准"
  5. termind 轮询到 approved 时把颁发的 token 存到 ~/.config/termind/token

典型用法:

  termind pair --server https://openclaw.example.com

再次配对(比如换服务器或 token 被吊销):

  termind pair --server https://new-server.example.com`,
	RunE: runPair,
}

func runPair(cmd *cobra.Command, _ []string) error {
	if pairServer == "" {
		return fmt.Errorf("--server is required (e.g. --server https://openclaw.example.com)")
	}

	// Ctrl-C 友好退出
	ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer cancel()

	id, err := identity.LoadOrCreate()
	if err != nil {
		return fmt.Errorf("load identity: %w", err)
	}

	client := pairing.NewClient(pairServer, id, Version)

	fmt.Printf("device id:   %s\n", id.DeviceID())
	fmt.Printf("fingerprint: %s\n", id.Fingerprint())
	fmt.Printf("server:      %s\n", pairServer)
	fmt.Println()
	fmt.Println("向服务器发起配对请求...")

	startCtx, startCancel := context.WithTimeout(ctx, 30*time.Second)
	start, err := client.Start(startCtx)
	startCancel()
	if err != nil {
		return err
	}

	fmt.Printf("\n  配对码: %s\n", start.PairCode)
	fmt.Printf("  有效期至: %s\n\n", start.ExpiresAt)
	fmt.Println("请到 OpenClaw 后台核对指纹并批准这台设备。")
	fmt.Printf("等待批准中 (超时 %s) ... 按 Ctrl-C 取消\n", pairTimeout)

	waitCtx, waitCancel := context.WithTimeout(ctx, pairTimeout)
	defer waitCancel()

	interval := time.Duration(start.PollIntervalSec) * time.Second
	res, err := client.WaitApproval(waitCtx, start.ChallengeID, interval)
	if err != nil {
		if waitCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("等待批准超时(%s)", pairTimeout)
		}
		return err
	}

	switch res.Status {
	case pairing.StatusApproved:
		path, err := pairing.SaveToken(res.Token)
		if err != nil {
			return fmt.Errorf("save token: %w", err)
		}
		// 同步写一份 config,status 命令能看到 server + 配对时间。
		// 这里出错只 warn,不让 pair 整体失败——token 已经存下,主干成功了。
		if err := config.Save(&config.Config{
			ServerURL: pairServer,
			PairedAt:  time.Now().UTC(),
		}); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warn: save config: %v\n", err)
		}
		fmt.Println()
		fmt.Printf("✓ 配对成功,token 已存至 %s\n", path)
		fmt.Println()
		fmt.Println("下一步: termind shell 进入智能 shell")
		return nil
	case pairing.StatusDenied:
		return fmt.Errorf("配对被拒绝: %s", res.Reason)
	case pairing.StatusExpired:
		return fmt.Errorf("配对超时过期: %s", res.Reason)
	default:
		return fmt.Errorf("未知状态: %s", res.Status)
	}
}

func init() {
	pairCmd.Flags().StringVar(&pairServer, "server", "", "OpenClaw 服务器 URL (例: https://openclaw.example.com)")
	pairCmd.Flags().DurationVar(&pairTimeout, "timeout", 10*time.Minute, "等待批准的最长时间")
	rootCmd.AddCommand(pairCmd)
}
