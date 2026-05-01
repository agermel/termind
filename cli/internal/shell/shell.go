// Package shell 实现 termind 的 PTY 透明包装。
//
// M1:用 creack/pty 启动一个子 shell,把当前 tty 切到 raw mode,
// 双向转发 stdin <-> ptmx,直到子 shell 退出。
//
// M3:在 ptmx -> stdout 那条链路上插入 OSC 133 parser 和命令组装器,
// 识别命令边界并组装 Command 对象(TERMIND_DEBUG=1 时会写调试日志)。
// Command 目前只被 debug log 消费;M4 会把它发给 openclaw。
package shell

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/term"

	"termind/internal/cmdbuf"
	"termind/internal/debuglog"
	"termind/internal/osc133"
)

// Run 启动子 shell 并阻塞,直到 shell 退出。
//
// 流程:
//  1. 检测 stdin 是 tty(termind shell 不能在管道里跑)
//  2. 决定子 shell 二进制($SHELL,缺省 /bin/zsh)
//  3. fork shell 进程并挂上一个新 PTY
//  4. 监听 SIGWINCH,保持子 shell 的窗口尺寸跟当前终端一致
//  5. 把当前 tty 切到 raw mode(让 Ctrl+C 等控制键直接透传)
//  6. 双向 IO 转发:stdin <-> ptmx
//  7. 等子 shell 退出,清理资源
//
// 任何步骤的错误都会被原样返回,由调用方决定打印形态。
// 子 shell 用非 0 退出码退出不视为本函数错误。
func Run() error {
	stdinFd := int(os.Stdin.Fd())
	if !term.IsTerminal(stdinFd) {
		return errors.New("termind shell 需要在交互式终端中运行")
	}

	shellBin := os.Getenv("SHELL")
	if shellBin == "" {
		shellBin = "/bin/zsh"
	}

	c := exec.Command(shellBin)
	// TERMIND_SHELL=1 是给 ~/.config/termind/integration.zsh 看的开关:
	// 它在被 zshrc 无条件 source 时,只有看到这个变量才会真正注册 OSC 133 hook。
	// 这样普通 zsh(用户没进 termind shell 时)零开销。
	c.Env = append(os.Environ(), "TERMIND_SHELL=1")

	ptmx, err := pty.Start(c)
	if err != nil {
		return fmt.Errorf("pty.Start: %w", err)
	}
	// 即使后面出错,也要确保 ptmx 关闭,避免 goroutine 永久阻塞
	defer func() { _ = ptmx.Close() }()

	// 窗口尺寸同步:监听 SIGWINCH 把宿主终端尺寸传给子 shell
	winchCh := make(chan os.Signal, 1)
	signal.Notify(winchCh, syscall.SIGWINCH)
	defer signal.Stop(winchCh)
	go func() {
		for range winchCh {
			if err := pty.InheritSize(os.Stdin, ptmx); err != nil {
				// resize 失败不致命,打 log 继续
				fmt.Fprintf(os.Stderr, "termind: resize pty: %v\n", err)
			}
		}
	}()
	// 启动时主动同步一次,避免子 shell 沿用默认 80x24
	if err := pty.InheritSize(os.Stdin, ptmx); err != nil {
		fmt.Fprintf(os.Stderr, "termind: initial resize pty: %v\n", err)
	}

	// 切到 raw mode:Ctrl+C / Ctrl+Z / Ctrl+D 等控制键直接传给子 shell,
	// 避免 termind 自己截走信号
	oldState, err := term.MakeRaw(stdinFd)
	if err != nil {
		return fmt.Errorf("term.MakeRaw: %w", err)
	}
	defer func() { _ = term.Restore(stdinFd, oldState) }()

	// M3: 初始化调试日志(只在 TERMIND_DEBUG=1 时真正写文件)
	debuglog.Init()
	if debuglog.Enabled() {
		debuglog.Logf("termind shell start: shell=%s", shellBin)
		fmt.Fprintf(os.Stderr, "termind: debug log -> %s\n", debuglog.Path())
	}

	// M3: 命令组装器。每条命令完成时触发回调 —— 目前只写 debug log,
	// M4 会把 Command 推到 ws 客户端发给 openclaw。
	buf := cmdbuf.NewBuffer(4*1024, func(c cmdbuf.Command) {
		debuglog.Logf("cmd done: exit=%d dur=%s tail(%d): %q",
			c.Exit, c.Duration().Round(1e6), len(c.Tail), truncate(c.Tail, 200))
	})

	// M3: parser downstream 是"先写屏幕,再累积进 Buffer 的 tail ring"。
	// 这样用户看到的字节流跟 M2 一样,同时 Buffer 在背后组装 Command。
	dual := cmdbuf.WriteAlongside{Primary: os.Stdout, Buffer: buf}
	parser := osc133.NewParser(dual, buf.OnEvent)

	// 双向 IO 转发:
	//   stdin -> ptmx:用户键盘 → 子 shell(这一路不需要解析)
	//   ptmx -> parser -> (stdout + buffer):子 shell 输出分发
	//
	// stdin goroutine 我们不主动等它退出 —— ptmx.Close() 会让它 unblock。
	go func() { _, _ = io.Copy(ptmx, os.Stdin) }()
	_, _ = io.Copy(parser, ptmx)

	// io.Copy(parser, ptmx) 返回 = 子 shell 已退出(ptmx 收到 EOF)。
	// 显式 Wait() 收割 zombie 进程,顺便拿子 shell 的退出码。
	if err := c.Wait(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return fmt.Errorf("shell wait: %w", err)
		}
		// 子 shell 用非 0 退出码退出是合理情况(比如 exit 1),不当错误返回。
	}
	debuglog.Logf("termind shell exit")
	return nil
}

// truncate 返回 b 的前 n 字节的字符串形态,超出部分用 "..." 代替。
// 只给 debug log 用,不保证 UTF-8 边界。
func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
