// Package render 把诊断 token 流渲染成终端上的 inline 文字。
//
// 当前只有一个 Renderer(最简 inline 模式):
//
//	Start()            打一行 header 占位("💭 thinking..."),不换行
//	Write(delta)       写 token; 第一次写时先擦 header 行再打 "💡" 前缀
//	Fail(msg)          打一行错误,换行结束
//	Done()             换行结束
//
// 设计原则:
//   - 用 ANSI 控制码但不假设终端支持复杂能力(只用 \r + \x1b[K + SGR 颜色)
//   - 所有方法可重入,并发用 mutex 串行;单次诊断通常只有一个 goroutine 在写,
//     但 timeout/cancel 路径可能跟 token handler 竞争,所以还是要锁
//   - 不主动跑 spinner goroutine;第一个 token 到来前 header 是静态的,
//     够给用户一个"在跑"的感觉。后续 UX 迭代可在外层包 spinner
package render

import (
	"fmt"
	"io"
	"sync"
)

// ANSI 控制码常量。不引 termbox 之类的库,省依赖。
const (
	ansiEraseLine = "\r\x1b[K"         // 回到行首 + 清到行尾
	ansiDim       = "\x1b[2m"          // dim (灰字)
	ansiCyan      = "\x1b[36m"         // 前缀颜色
	ansiRed       = "\x1b[31m"         // 错误
	ansiReset     = "\x1b[0m"          // 还原
	headerText    = "💭 termind is thinking..."
	prefixText    = "💡 "
)

// Renderer 是一次诊断的 inline 渲染器。每条命令的诊断应当新 new 一个。
//
// 生命周期(典型):
//
//	r := render.New(os.Stdout)
//	r.Start()                              // 打 header
//	for ev := range events {
//	    if ev.Error != "" { r.Fail(ev.Error); break }
//	    r.Write(ev.Delta)
//	    if ev.Done { r.Done(); break }
//	}
type Renderer struct {
	w io.Writer

	mu       sync.Mutex
	started  bool // Start 已调用
	gotToken bool // 至少收到了一个有内容的 token
	closed   bool // Done 或 Fail 已调用,再写会 no-op
}

// New 构造一个 Renderer,w 通常是 os.Stdout。
func New(w io.Writer) *Renderer {
	return &Renderer{w: w}
}

// Start 打 header 占位行。多次调用只生效一次(幂等)。
// 调用后光标停在 header 行末,不换行——等第一个 token 来了再决定怎么擦。
func (r *Renderer) Start() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started || r.closed {
		return
	}
	r.started = true
	// 前置 \n 保证 header 独占一行,不会接在上一条命令的尾部
	_, _ = fmt.Fprintf(r.w, "\n%s%s%s", ansiDim, headerText, ansiReset)
}

// Write 追加一段 token 文本。delta 可以跨行。
//
// 第一次收到非空 delta 时,擦掉 header 行并打上 "💡 " 前缀,之后直接追加。
// 空字符串(心跳帧)不输出,返回 (0, nil)。
func (r *Renderer) Write(delta string) (int, error) {
	if delta == "" {
		return 0, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return 0, nil
	}
	if !r.started {
		// 没调 Start 就 Write 也允许;直接开始
		r.started = true
	}
	if !r.gotToken {
		r.gotToken = true
		if _, err := fmt.Fprintf(r.w, "%s%s%s%s", ansiEraseLine, ansiCyan, prefixText, ansiReset); err != nil {
			return 0, err
		}
	}
	return r.w.Write([]byte(delta))
}

// Fail 终止当前渲染,打一个红色错误行并换行。
//
// 规则:
//   - 如果还没写过任何 token: 擦 header 行,重写成红色错误
//   - 已经写过 token: 先换一行,再打红色错误(保留已流出的文字)
func (r *Renderer) Fail(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	if r.gotToken {
		_, _ = fmt.Fprintf(r.w, "\n%s✗ termind diagnose: %s%s\n", ansiRed, msg, ansiReset)
	} else {
		prefix := ""
		if r.started {
			prefix = ansiEraseLine
		}
		_, _ = fmt.Fprintf(r.w, "%s%s✗ termind diagnose: %s%s\n", prefix, ansiRed, msg, ansiReset)
	}
}

// Done 正常结束渲染: 换行让 shell prompt 从新一行开始。
//
// 没写过任何 token 时(start 发出但诊断空回复)擦掉 header 不留痕迹。
func (r *Renderer) Done() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	if r.gotToken {
		_, _ = fmt.Fprintln(r.w)
	} else if r.started {
		// header 已打但没 token: 擦掉
		_, _ = fmt.Fprint(r.w, ansiEraseLine)
	}
}
