// Package integration 把 zsh shell integration 脚本装到用户的 zshrc。
//
// 脚本本身落在 ~/.config/termind/integration.zsh,
// ~/.zshrc 末尾追加一行 source(用 marker 包裹,可幂等重复运行)。
//
// 核心设计:脚本自己只在 TERMIND_SHELL 环境变量被设置时激活,
// 所以普通 zsh 加载 zshrc 时不会有任何 OSC 133 输出 —— 零开销。
package integration

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed zsh.zsh
var zshScript []byte

const (
	// beginMarker / endMarker 包裹我们注入到 zshrc 的那一段,
	// 用来检测"已装过"并支持以后的 uninstall。
	beginMarker = "# >>> termind integration >>>"
	endMarker   = "# <<< termind integration <<<"
)

// Result 是 Install 的回执,给 cmd/init.go 用来给用户打清晰反馈。
type Result struct {
	ScriptPath       string // 集成脚本落盘位置
	RcPath           string // 注入的 zshrc 路径
	AlreadyInstalled bool   // true = 这次只刷新了脚本,没动 zshrc
}

// Install 落盘 integration 脚本并往 ~/.zshrc 加 source 行。
//
// 行为:
//   - 始终覆盖写 ~/.config/termind/integration.zsh(让 termind 升级时自动刷新脚本)
//   - 检测到 zshrc 里已有 beginMarker → AlreadyInstalled=true,不再追加
//   - 否则在 zshrc 末尾追加 begin/end 包裹的 source 块
//
// 不会启动新 shell,不会 source rc,这些事用户自己做。
func Install() (Result, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Result{}, fmt.Errorf("user home dir: %w", err)
	}

	// 1. 写脚本本体(覆盖式,升级时跟着新 binary 走)
	cfgDir := filepath.Join(home, ".config", "termind")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("create %s: %w", cfgDir, err)
	}
	scriptPath := filepath.Join(cfgDir, "integration.zsh")
	if err := os.WriteFile(scriptPath, zshScript, 0o644); err != nil {
		return Result{}, fmt.Errorf("write %s: %w", scriptPath, err)
	}

	// 2. 检查 ~/.zshrc 是否已有 marker
	rcPath := filepath.Join(home, ".zshrc")
	rc, err := os.ReadFile(rcPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Result{}, fmt.Errorf("read %s: %w", rcPath, err)
	}
	if strings.Contains(string(rc), beginMarker) {
		return Result{
			ScriptPath:       scriptPath,
			RcPath:           rcPath,
			AlreadyInstalled: true,
		}, nil
	}

	// 3. 追加 source 块到 zshrc
	block := fmt.Sprintf("\n%s\nsource %q\n%s\n", beginMarker, scriptPath, endMarker)
	f, err := os.OpenFile(rcPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return Result{}, fmt.Errorf("open %s: %w", rcPath, err)
	}
	defer f.Close()
	if _, err := f.WriteString(block); err != nil {
		return Result{}, fmt.Errorf("append %s: %w", rcPath, err)
	}

	return Result{ScriptPath: scriptPath, RcPath: rcPath}, nil
}
