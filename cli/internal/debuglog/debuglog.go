// Package debuglog 是 termind 的轻量调试日志。
//
// 默认把日志丢到 io.Discard —— 对用户和 stdout 都不可见。
// 设置 TERMIND_DEBUG=1 环境变量后,会把日志 append 写到
// $TMPDIR/termind/debug.log,方便 `tail -f` 实时看。
//
// 不写 stderr 的原因:termind shell 接管整个 tty,
// 任何 stderr 输出都会污染用户看到的界面。
package debuglog

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	once sync.Once
	w    io.Writer = io.Discard
	path string
)

// Init 初始化日志。只有第一次调用有效,重复调用无害。
// 未启用 TERMIND_DEBUG 时,w 保持 io.Discard,所有 Logf 会被直接丢弃。
func Init() {
	once.Do(func() {
		if os.Getenv("TERMIND_DEBUG") != "1" {
			return
		}
		dir := filepath.Join(os.TempDir(), "termind")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return
		}
		p := filepath.Join(dir, "debug.log")
		f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		w = f
		path = p
	})
}

// Enabled 返回 TERMIND_DEBUG 是否被打开且日志文件成功打开。
func Enabled() bool {
	return w != io.Discard
}

// Path 返回日志文件绝对路径;未启用时返回空串。
func Path() string {
	return path
}

// Logf 写一行日志,自动带 HH:MM:SS.ms 时间戳。
// 未启用 TERMIND_DEBUG 时这是个零开销 no-op(io.Discard)。
func Logf(format string, args ...any) {
	fmt.Fprintf(w, "[%s] "+format+"\n",
		append([]any{time.Now().Format("15:04:05.000")}, args...)...)
}
