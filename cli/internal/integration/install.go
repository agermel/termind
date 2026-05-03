// Package integration 把 zsh shell integration 脚本装到用户的 zshrc。
//
// 脚本本身落在 ~/.config/termind/integration.zsh,
// ~/.zshrc 末尾追加一段 guarded source(用 marker 包裹,可幂等重复运行)。
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

	// 2. 检查 ~/.zshrc 是否已有 marker。已安装时也刷新 marker block,
	//    这样旧版无 guard 的 source 会被替换掉。
	rcPath := filepath.Join(home, ".zshrc")
	rc, err := os.ReadFile(rcPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Result{}, fmt.Errorf("read %s: %w", rcPath, err)
	}
	block := integrationBlock(scriptPath)
	if strings.Contains(string(rc), beginMarker) {
		updated, changed := replaceMarkedBlock(string(rc), block)
		if changed {
			if err := os.WriteFile(rcPath, []byte(updated), 0o644); err != nil {
				return Result{}, fmt.Errorf("update %s: %w", rcPath, err)
			}
		}
		return Result{
			ScriptPath:       scriptPath,
			RcPath:           rcPath,
			AlreadyInstalled: true,
		}, nil
	}

	// 3. 追加 source 块到 zshrc
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

func integrationBlock(scriptPath string) string {
	return fmt.Sprintf("\n%s\n[[ -f %q ]] && source %q\n%s\n", beginMarker, scriptPath, scriptPath, endMarker)
}

func replaceMarkedBlock(rc, block string) (string, bool) {
	begin := strings.Index(rc, beginMarker)
	end := strings.Index(rc, endMarker)
	if begin < 0 || end < begin {
		return rc, false
	}
	end += len(endMarker)
	for end < len(rc) && (rc[end] == '\n' || rc[end] == '\r') {
		end++
	}
	updated := rc[:begin] + strings.TrimLeft(block, "\n") + rc[end:]
	return updated, updated != rc
}

// IsInstalled 只读地检查 ~/.zshrc 是否已经有 termind integration 标记。
// 给 `termind status` 用,不做任何副作用。
//
// 读不到 zshrc(比如不存在)等同于"未安装",不当作错误。
func IsInstalled() (bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, err
	}
	rc, err := os.ReadFile(filepath.Join(home, ".zshrc"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return strings.Contains(string(rc), beginMarker), nil
}
