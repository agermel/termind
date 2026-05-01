package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"termind/internal/integration"
)

// initCmd 是 M2 模块的 cobra 入口。
//
// 把 integration 脚本写到 ~/.config/termind/,然后在 ~/.zshrc 末尾
// 加一行 source。可幂等执行,marker 已存在时只刷新脚本本体。
//
// 后续 M4 阶段会扩展这个命令,把 openclaw pairing 也并进来,
// 变成完整的 "首次配置一条龙"。当前 M2 只做 shell integration。
var initCmd = &cobra.Command{
	Use:   "init",
	Short: "首次配置:装 shell integration(M2)",
	Long: `把 termind 的 shell integration 装到 ~/.zshrc。

具体动作:
  1. 把集成脚本写到 ~/.config/termind/integration.zsh
  2. 在 ~/.zshrc 末尾加一行 source(用 marker 包裹,可幂等)

设计要点:
  - 脚本自己只在 TERMIND_SHELL 环境变量被设置时激活
  - 普通 zsh 加载 zshrc 时不会有任何 OSC 133 输出,零开销
  - 只有 termind shell 启动的子 zsh 里才会上报命令边界

完成后:
  - 重启 shell,或运行 source ~/.zshrc
  - 跑 termind shell 进入交互式 shell
  - M3 会消费这些 OSC 133 暗号(后续模块)`,
	RunE: runInit,
}

func runInit(_ *cobra.Command, _ []string) error {
	r, err := integration.Install()
	if err != nil {
		return err
	}

	if r.AlreadyInstalled {
		fmt.Printf("✓ shell integration 已经在 %s\n", r.RcPath)
		fmt.Printf("  脚本本体已刷新: %s\n", r.ScriptPath)
		return nil
	}

	fmt.Printf("✓ shell integration 安装成功\n")
	fmt.Printf("  脚本写入: %s\n", r.ScriptPath)
	fmt.Printf("  注入到:  %s\n", r.RcPath)
	fmt.Println()
	fmt.Println("下一步:")
	fmt.Println("  1. 重启 shell 或运行: source ~/.zshrc")
	fmt.Println("  2. 跑 termind shell 进入交互式 shell")
	return nil
}

func init() {
	rootCmd.AddCommand(initCmd)
}
