package enrich

import (
	"context"
	"os/exec"
	"strings"
)

// gitCommand 在 cwd 下跑一个 git 子命令, 带独立超时.
// 返回 stdout trim 后的字符串; 任何错误都降级为 ("", false).
//
// 我们用 `git -C <cwd> ...` 而不是 cmd.Dir, 是因为 cmd.Dir 要求目录存在,
// 而 -C 对不存在目录直接报错退出, 行为更一致, 且某些 shell 场景下 cwd
// 已经被子进程改动, exec 仍需要显式锚定.
func gitCommand(parent context.Context, cwd string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(parent, stepTimeout)
	defer cancel()

	full := append([]string{"-C", cwd}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	// 阻断 git 的交互式 prompt: 某些系统装了 askpass 助手,
	// 如果 credential 需要交互, git 会整个挂住直到超时.
	cmd.Env = append(cmd.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
	)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return "", false
	}
	return s, true
}

// gitRepoRoot 返回 cwd 所属 git 仓库的顶级路径.
// cwd 不在 git 仓库里时返回 ("", false).
func gitRepoRoot(ctx context.Context, cwd string) (string, bool) {
	return gitCommand(ctx, cwd, "rev-parse", "--show-toplevel")
}

// gitBranch 返回当前分支短名, detached HEAD 时返回 short commit.
func gitBranch(ctx context.Context, cwd string) string {
	// --short HEAD detached 时 `symbolic-ref` 会失败, 所以用 rev-parse --abbrev-ref.
	// detached 时 --abbrev-ref HEAD 返回 "HEAD", 我们把它映射成空.
	s, ok := gitCommand(ctx, cwd, "rev-parse", "--abbrev-ref", "HEAD")
	if !ok {
		return ""
	}
	if s == "HEAD" {
		return ""
	}
	return s
}

// gitShortCommit 返回当前 HEAD 的 7 位短哈希.
func gitShortCommit(ctx context.Context, cwd string) string {
	s, _ := gitCommand(ctx, cwd, "rev-parse", "--short", "HEAD")
	return s
}
