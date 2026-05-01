// Package cmd 实现 termind 的命令行子命令。
//
// 子命令清单(每个有自己的 .go 文件):
//
//	shell     — 进入 termind 包装的交互式 shell(warp 模式)
//	init      — 首次配置:装 shell integration + 跟 openclaw 配对(后续 M2/M4)
//	pair      — 重新配对 / 切换 openclaw 服务器(后续 M4)
//
// 设计原则:
//   - 每个子命令一个文件,通过 init() 用 rootCmd.AddCommand 注册自己
//   - 子命令本体只做"参数解析 + 调内部包",真实逻辑放 internal/
package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

// Version 由 build 时的 ldflags 注入;手动构建时为 0.0.1-dev。
var Version = "0.0.1-dev"

var rootCmd = &cobra.Command{
	Use:   "termind",
	Short: "Warp 风格的智能 shell 包装",
	Long: `termind 是个透明的交互式 shell 包装。

用 ` + "`termind shell`" + ` 替代 zsh,失败的命令会自动获得 inline AI 诊断,
体验跟原生 shell 完全一样。

子命令:
  shell           进入 PTY 包装的交互式 shell
  init            装 shell integration 到 ~/.zshrc
  pair            跟 OpenClaw 服务器配对
  status          只读展示身份 / 配对 / integration 状态

模块进度:
  [M1] PTY 包装  [M2] shell integration  [M3] OSC 133 解析  [M4] pair + ws 长连
  (后续 M5 inline 流式诊断)`,
	Version:      Version,
	SilenceUsage: true, // RunE 返错时不要重复打印 Usage,我们自己打日志
}

// Execute 是 main 包的唯一入口。
//
// 注: cobra 自己已经把 RunE 返回的 error 用 "Error: ..." 打到 stderr,
// 所以这里不要再打一次,只负责设置退出码。
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
