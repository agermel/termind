package shell

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"termind/internal/cmdbuf"
	"termind/internal/config"
	"termind/internal/debuglog"
	"termind/internal/diagnose"
	"termind/internal/gateway"
	"termind/internal/identity"
	"termind/internal/osc133"
	"termind/internal/pairing"
	"termind/internal/render"
)

// dispatcher 是 M5 的粘合层: 把 gateway/diagnose/render 串起来,
// 对 shell.Run 暴露三个方法:
//
//	OnCmdDone(cmd)   — cmdbuf 命令完成时调,按策略触发诊断 goroutine
//	OnShellEvent(ev) — osc133 事件,收到 CommandStart 时取消上一条进行中的诊断
//	Close()          — shell 退出,停所有正在跑的 goroutine + 关 gateway
//
// 设计:
//   - 如果没配对(config.ServerURL 或 token 为空)构造 dispatcher 时就返回 "离线" 状态,
//     所有 OnCmdDone 都 no-op。让 shell 本身继续能跑。
//   - Dial 失败同样降级到 "离线",但打一条 warn 到 stderr。
//   - 同时只允许一条诊断流在活动;新命令开始(OSC 133 C)或新失败命令
//     触发时都会取消上一条。
type dispatcher struct {
	w io.Writer // 诊断渲染的目的地,通常就是 PTY 输出目的地 (os.Stdout)

	conn *gateway.Conn   // 可能为 nil(离线)
	dc   *diagnose.Client

	// 运行时命令环境,传给 server 做诊断
	shellBin string

	mu         sync.Mutex
	activeCtx  context.Context
	activeStop context.CancelFunc
	closed     bool
}

// newDispatcher 尝试连接 gateway;失败或未配对时返回一个"离线"dispatcher,
// 调用者继续用它即可(所有 OnCmdDone 都 no-op)。
//
// stderr 用来打连接状态 warning,不影响正常输出。
func newDispatcher(parentCtx context.Context, w io.Writer, stderr io.Writer, shellBin string) *dispatcher {
	d := &dispatcher{w: w, shellBin: shellBin}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "termind: config load: %v (continuing offline)\n", err)
		return d
	}
	token, err := pairing.LoadToken()
	if err != nil {
		fmt.Fprintf(stderr, "termind: load token: %v (continuing offline)\n", err)
		return d
	}
	if cfg.ServerURL == "" || token == "" {
		// 从未 pair 过,安静降级(不报警告,避免吓用户)
		debuglog.Logf("dispatcher: offline (no pair)")
		return d
	}

	id, err := identity.LoadOrCreate()
	if err != nil {
		fmt.Fprintf(stderr, "termind: load identity: %v (continuing offline)\n", err)
		return d
	}

	// 3 秒硬超时: 连不上就降级,不拖住 shell 启动
	dialCtx, cancel := context.WithTimeout(parentCtx, 3*time.Second)
	defer cancel()
	conn, err := gateway.Dial(dialCtx, gateway.DialOptions{
		ServerURL:     cfg.ServerURL,
		Identity:      id,
		Token:         token,
		ClientVersion: "termind-dev", // M4 里 cmd.Version 有,但 internal 不好反向依赖
	})
	if err != nil {
		fmt.Fprintf(stderr, "termind: gateway dial: %v (continuing offline)\n", err)
		return d
	}
	d.conn = conn
	d.dc = diagnose.NewClient(conn)
	fmt.Fprintf(stderr, "termind: gateway connected (%s)\n", cfg.ServerURL)
	return d
}

// enabled 报告本 dispatcher 是否能真正跑诊断。
func (d *dispatcher) enabled() bool {
	return d != nil && d.dc != nil
}

// OnCmdDone 接 cmdbuf.OnComplete。按策略决定是否触发诊断。
//
// 不触发的情况(保留 0 成功、用户主动信号、shell 退出等):
//   - exit == 0 : 成功
//   - exit == 130: SIGINT (Ctrl-C)
//   - exit == 143: SIGTERM
//   - exit == 148: SIGTSTP (Ctrl-Z)
func (d *dispatcher) OnCmdDone(c cmdbuf.Command) {
	if !d.enabled() {
		return
	}
	if d.shouldSkip(c.Exit) {
		return
	}

	// 取消上一条进行中的诊断;一次只留一条
	d.cancelActive("new failed command")

	ctx, stop := context.WithTimeout(context.Background(), 60*time.Second)
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		stop()
		return
	}
	d.activeCtx, d.activeStop = ctx, stop
	d.mu.Unlock()

	go d.run(ctx, c)
}

// OnShellEvent 接 osc133.Parser 的 onEvent 链路之后;新命令开始(C)时
// 取消上一条进行中的诊断,避免 prompt 和诊断串线。
func (d *dispatcher) OnShellEvent(ev osc133.Event) {
	if ev.Kind == osc133.EventCommandStart {
		d.cancelActive("next command started")
	}
}

// Close 在 shell 退出路径调用;取消进行中的诊断 + 关 gateway。
func (d *dispatcher) Close() {
	d.mu.Lock()
	d.closed = true
	stop := d.activeStop
	d.activeStop = nil
	d.mu.Unlock()
	if stop != nil {
		stop()
	}
	if d.conn != nil {
		_ = d.conn.Close()
	}
}

// ---------- 内部 ----------

func (d *dispatcher) shouldSkip(exit int) bool {
	switch exit {
	case 0, 130, 143, 148:
		return true
	}
	return false
}

func (d *dispatcher) cancelActive(reason string) {
	d.mu.Lock()
	stop := d.activeStop
	d.activeStop = nil
	d.mu.Unlock()
	if stop != nil {
		debuglog.Logf("dispatcher: cancel active diagnose: %s", reason)
		stop()
	}
}

// run 跑一次诊断: 构 Request → Start → renderer 流式消费 token → Done/Fail。
//
// 出错不 panic,只打 log 和 Fail 给用户看。
func (d *dispatcher) run(ctx context.Context, c cmdbuf.Command) {
	// 组装 Request
	cwd, _ := os.Getwd()
	req := &diagnose.Request{
		Command:    "", // M3 cmdbuf 当前没采集命令本身,等 M3 增强后填;先留空
		ExitCode:   c.Exit,
		OutputTail: string(c.Tail),
		Shell:      d.shellBin,
		Cwd:        cwd,
		Lang:       os.Getenv("LANG"),
	}

	r := render.New(d.w)
	r.Start()
	debuglog.Logf("diagnose: start exit=%d tail=%dB", c.Exit, len(c.Tail))

	events, err := d.dc.Start(ctx, req)
	if err != nil {
		r.Fail(fmt.Sprintf("%v", err))
		debuglog.Logf("diagnose: start failed: %v", err)
		return
	}

	for ev := range events {
		if ev.Error != "" {
			r.Fail(ev.Error)
			debuglog.Logf("diagnose: server error: %s", ev.Error)
			return
		}
		if ev.Delta != "" {
			_, _ = r.Write(ev.Delta)
		}
		if ev.Done {
			r.Done()
			debuglog.Logf("diagnose: done")
			return
		}
	}
	// channel 被关但没收到 done: 被 cancel 或 conn 断
	r.Done()
	debuglog.Logf("diagnose: stream closed without done")
}
