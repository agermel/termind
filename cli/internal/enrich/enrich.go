// Package enrich 负责给失败命令事件补齐进程侧现场元数据:
//
//	- User       当前 shell 用户名 (来自 $USER)
//	- Project    cwd 所属仓库名 (git repo basename, 否则 cwd basename)
//	- Branch     当前 git 分支短名
//	- GitCommit  当前 git HEAD 短哈希
//	- OS         系统描述, e.g. "darwin 24.0.0" / "linux 5.15.0-105"
//	- GoVersion  如果 cwd 能解析出 go.mod 的 go 行, 返回 "go1.22.3" 否则空
//
// 设计要点:
//   - 纯读操作, 不写磁盘/不发网络
//   - 每次 git / uname 子进程都有独立硬超时, 整体 Collect 控在 ~500ms 以内;
//     enrich 是 shell 诊断链路的一环, 不能拖慢终端回显
//   - 任何子步骤失败都静默降级为空串, 不向上抛错 (enrich 是可选增强)
//   - 不依赖外部 go 模块, 只用标准库
package enrich

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Context 是补齐后的现场元数据集合.
// 字段全部为值类型, 空串表示该项未能探测到.
type Context struct {
	User      string
	Project   string
	Branch    string
	GitCommit string
	OS        string
	GoVersion string
}

// stepTimeout 是单次子进程调用的硬超时. git 偶尔会在大仓或磁盘慢时卡顿,
// 我们宁可返回空串, 也不愿意让 shell 诊断一路 hang 住.
const stepTimeout = 150 * time.Millisecond

// Collect 并发收集 cwd 下的现场元数据, 返回 Context.
//
// parent 传入的 ctx 会被叠加一个更短的子超时; 即使 parent 长期不取消,
// Collect 也保证在 ~ stepTimeout 内返回.
//
// cwd 为空时跳过所有和 cwd 相关的探测 (Project/Branch/GitCommit).
func Collect(parent context.Context, cwd string) Context {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 2*stepTimeout)
	defer cancel()

	out := Context{
		User: currentUser(),
		OS:   osDescription(ctx),
	}

	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return out
	}

	if root, ok := gitRepoRoot(ctx, cwd); ok {
		out.Project = filepath.Base(root)
		out.Branch = gitBranch(ctx, cwd)
		out.GitCommit = gitShortCommit(ctx, cwd)
	} else {
		// 不是 git 仓库时, 用 cwd basename 作为 Project 兜底.
		// 这个值仍然对 plugin 侧指纹分组有用 (同一个目录下重复命令会命中同一指纹).
		out.Project = filepath.Base(cwd)
	}

	out.GoVersion = goModVersion(cwd)
	return out
}

// currentUser 返回当前 shell 用户名. 依次尝试 USER, LOGNAME, os.UserHomeDir basename.
func currentUser() string {
	for _, key := range []string{"USER", "LOGNAME"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		return filepath.Base(home)
	}
	return ""
}

// osDescription 返回 "<goos> <kernel-release>" 形式, 例如 "darwin 24.0.0".
// uname 失败时回退为仅 runtime.GOOS.
func osDescription(ctx context.Context) string {
	goos := runtime.GOOS
	if release, ok := kernelRelease(ctx); ok {
		return goos + " " + release
	}
	return goos
}
