// Package cmdbuf 维护当前正在执行的命令的输出缓冲,
// 根据 osc133 事件组装成完整的 Command 对象。
//
// 使用方式:
//  1. NewBuffer 创建(tail 容量 + 完成回调)
//  2. 把 osc133.Parser 的 onEvent 接到 b.OnEvent
//  3. 把"parser downstream"包装成 WriteAlongside{Primary: stdout, Buffer: b},
//     让字节既写屏幕也累积到 tail ring
//  4. 每条命令完成(收到 D)时,onComplete 回调带完整 Command
//
// 设计要点:
//   - Tail 用环形缓冲,最多保留最后 N 字节(默认 4KB);
//     防爆内存,又保证错误末尾能抓到 —— 诊断最需要末尾几行
//   - 还没进入命令阶段时(只收到 A,未收到 C)进来的字节全部丢弃;
//     那些是 prompt 文字、用户输入回显等,不是"命令输出"
package cmdbuf

import (
	"io"
	"time"

	"termind/internal/osc133"
)

// Command 是一条完整的命令记录。
type Command struct {
	StartedAt time.Time
	EndedAt   time.Time

	// Exit 来自 OSC 133;D;<exit> 暗号
	Exit int

	// Tail 是命令输出的"尾部 N 字节"(N = Buffer 的 tailCap)。
	// 不是完整输出 —— 过长内容会被环形缓冲截掉前面部分,
	// 保证末尾错误信息一定在里面。
	Tail []byte
}

// Duration 返回命令执行耗时。StartedAt 和 EndedAt 分别是收到 C / D 事件的时间。
func (c Command) Duration() time.Duration {
	return c.EndedAt.Sub(c.StartedAt)
}

// OnComplete 是命令完成回调。Buffer 内部同步调用 handler,handler 应当尽快返回。
type OnComplete func(Command)

// Buffer 是命令组装器。
//
// Buffer 本身实现 io.Writer,用于接收"parser downstream"清理后的字节流
// (上层应通过 WriteAlongside 把字节同时发给屏幕和 Buffer)。
type Buffer struct {
	tailCap    int
	onComplete OnComplete

	inCommand bool
	startedAt time.Time
	tail      *ring
}

// NewBuffer 创建一个命令组装器。tailCap <= 0 时回退到 4KB。
func NewBuffer(tailCap int, onComplete OnComplete) *Buffer {
	if tailCap <= 0 {
		tailCap = 4 * 1024
	}
	return &Buffer{
		tailCap:    tailCap,
		onComplete: onComplete,
		tail:       newRing(tailCap),
	}
}

// OnEvent 应被挂到 osc133.Parser 的 onEvent 回调上。
func (b *Buffer) OnEvent(ev osc133.Event) {
	now := time.Now()
	switch ev.Kind {
	case osc133.EventCommandStart:
		b.inCommand = true
		b.startedAt = now
		b.tail.reset()
	case osc133.EventCommandEnd:
		if !b.inCommand {
			// 没 C 就 D —— 比如刚 source integration 时,第一次 precmd 没有前置命令,
			// 我们的 zsh.zsh 已经用 _TERMIND_RAN_ONCE 做了守卫,但保险起见这里也吞掉。
			return
		}
		c := Command{
			StartedAt: b.startedAt,
			EndedAt:   now,
			Exit:      ev.Exit,
			Tail:      b.tail.snapshot(),
		}
		b.inCommand = false
		b.tail.reset()
		if b.onComplete != nil {
			b.onComplete(c)
		}
		// A / B 事件不影响 tail 累积,忽略
	}
}

// Write 实现 io.Writer。
// inCommand=true 时字节累积到 tail;否则丢弃。
// 总是返回 (len(p), nil) —— 这是内存操作,不会失败。
func (b *Buffer) Write(p []byte) (int, error) {
	if b.inCommand {
		b.tail.write(p)
	}
	return len(p), nil
}

// WriteAlongside 是个双路 io.Writer 适配器:
// 把同一批字节先写给 Primary(通常是 os.Stdout 给屏幕),
// 再同时累积到 Buffer(给命令缓冲器)。
//
// 让 parser.downstream 用 WriteAlongside 就能"用户能看见 + 我们也记下来"。
type WriteAlongside struct {
	Primary io.Writer
	Buffer  *Buffer
}

// Write 实现 io.Writer。
//
// 返回 Primary.Write 的结果。Buffer.Write 不会失败,我们忽略它的返回值。
func (w WriteAlongside) Write(p []byte) (int, error) {
	n, err := w.Primary.Write(p)
	if err != nil {
		return n, err
	}
	_, _ = w.Buffer.Write(p)
	return n, nil
}

// ring 是一个简单的字节环形缓冲。
//
// total 记录累计写入字节数;当 total > cap 时,
// 新字节覆盖 buf[total%cap] 的旧字节,读取时从 total%cap 开始绕回。
type ring struct {
	buf   []byte
	cap   int
	total int
}

func newRing(cap int) *ring {
	return &ring{
		buf: make([]byte, 0, cap),
		cap: cap,
	}
}

func (r *ring) reset() {
	r.buf = r.buf[:0]
	r.total = 0
}

func (r *ring) write(p []byte) {
	for _, b := range p {
		if len(r.buf) < r.cap {
			r.buf = append(r.buf, b)
		} else {
			r.buf[r.total%r.cap] = b
		}
		r.total++
	}
}

// snapshot 按写入顺序返回当前 buffer 内容的新副本。
func (r *ring) snapshot() []byte {
	if r.total <= r.cap {
		out := make([]byte, len(r.buf))
		copy(out, r.buf)
		return out
	}
	// 已绕回,从 total%cap 位置读到尾,再从头读到 total%cap
	start := r.total % r.cap
	out := make([]byte, r.cap)
	copy(out, r.buf[start:])
	copy(out[r.cap-start:], r.buf[:start])
	return out
}
