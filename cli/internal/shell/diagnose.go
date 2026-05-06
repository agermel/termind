package shell

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
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

	conn *gateway.Conn // 可能为 nil(离线)
	dc   *diagnose.Client

	connectMu sync.Mutex

	// 运行时命令环境,传给 server 做诊断
	shellBin string
	shellCmd *exec.Cmd

	mu         sync.Mutex
	activeCtx  context.Context
	activeStop context.CancelFunc
	activeR    *render.Renderer
	closed     bool
}

var (
	errGatewayNotConfigured = errors.New("gateway not configured")
	errDispatcherClosed     = errors.New("dispatcher closed")
)

// newDispatcher 尝试连接 gateway;失败或未配对时返回一个"离线"dispatcher,
// 调用者继续用它即可(所有 OnCmdDone 都 no-op)。
//
// stderr 用来打连接状态 warning,不影响正常输出。
func newDispatcher(parentCtx context.Context, w io.Writer, stderr io.Writer, shellBin string, shellCmd *exec.Cmd) *dispatcher {
	d := &dispatcher{w: w, shellBin: shellBin, shellCmd: shellCmd}

	conn, dc, serverURL, err := connectDiagnoseClient(parentCtx)
	if err != nil {
		if errors.Is(err, errGatewayNotConfigured) {
			debuglog.Logf("dispatcher: offline (not configured)")
			return d
		}
		fmt.Fprintf(stderr, "termind: gateway dial: %v (continuing offline)\n", err)
		return d
	}
	d.conn = conn
	d.dc = dc
	fmt.Fprintf(stderr, "termind: gateway connected (%s)\n", serverURL)
	return d
}

func connectDiagnoseClient(parentCtx context.Context) (*gateway.Conn, *diagnose.Client, string, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, "", fmt.Errorf("config load: %w", err)
	}
	token, err := pairing.LoadToken()
	if err != nil {
		return nil, nil, "", fmt.Errorf("load token: %w", err)
	}
	if cfg.ServerURL == "" || token == "" {
		return nil, nil, "", errGatewayNotConfigured
	}

	id, err := identity.LoadOrCreate()
	if err != nil {
		return nil, nil, "", fmt.Errorf("load identity: %w", err)
	}
	role := cfg.Role
	if role == "" {
		role = pairing.DefaultRole
	}
	auth, err := pairing.LoadDeviceAuth(id.DeviceID(), role)
	if err != nil {
		return nil, nil, "", fmt.Errorf("load device auth: %w", err)
	}
	scopes := []string(nil)
	if auth != nil {
		token = auth.Token
		scopes = auth.Scopes
	}
	if role == pairing.DefaultRole && len(scopes) == 0 {
		scopes = pairing.DefaultScopes()
	}
	if role == pairing.DefaultRole && !pairing.HasScopes(scopes, pairing.DefaultScopes()) {
		return nil, nil, "", fmt.Errorf("OpenClaw device token has insufficient scopes: got %v, need %v", scopes, pairing.DefaultScopes())
	}

	// 3 秒硬超时: 连不上就降级,不拖住 shell 启动
	dialCtx, cancel := context.WithTimeout(parentCtx, 3*time.Second)
	defer cancel()
	conn, err := gateway.Dial(dialCtx, gateway.DialOptions{
		ServerURL:     cfg.ServerURL,
		Identity:      id,
		Token:         token,
		Role:          role,
		Scopes:        scopes,
		ClientVersion: "termind-dev", // M4 里 cmd.Version 有,但 internal 不好反向依赖
		OnDeviceToken: func(role, deviceToken string, scopes []string) {
			if _, err := pairing.SaveDeviceAuth(id.DeviceID(), role, deviceToken, scopes); err != nil {
				debuglog.Logf("dispatcher: save device auth: %v", err)
			}
		},
	})
	if err != nil {
		return nil, nil, "", err
	}
	return conn, diagnose.NewClient(conn), cfg.ServerURL, nil
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
	if d == nil {
		return
	}
	if d.shouldSkip(c.Exit) {
		return
	}
	req := d.requestFromCommand(c)
	ctx, stop := context.WithTimeout(context.Background(), 60*time.Second)
	dc, err := d.ensureConnected(ctx)
	if err != nil {
		stop()
		if errors.Is(err, errGatewayNotConfigured) || errors.Is(err, errDispatcherClosed) {
			return
		}
		r := render.New(d.w)
		r.StartAtLineStart(true)
		r.Fail(fmt.Sprintf("%v", err))
		d.redrawPrompt()
		debuglog.Logf("diagnose: reconnect failed: %v", err)
		return
	}
	d.dispatchAlert(dc, req)

	// 取消上一条进行中的诊断;一次只留一条
	d.cancelActive("new failed command")

	r := render.New(d.w)
	r.StartAtLineStart(true)

	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		r.Done()
		stop()
		return
	}
	d.activeCtx, d.activeStop = ctx, stop
	d.activeR = r
	d.mu.Unlock()

	go d.run(ctx, dc, req, r)
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
	conn := d.conn
	d.conn = nil
	d.dc = nil
	d.mu.Unlock()
	if stop != nil {
		stop()
	}
	if conn != nil {
		_ = conn.Close()
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

func (d *dispatcher) currentClientIfAlive() (*diagnose.Client, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.conn == nil || d.dc == nil {
		return nil, false
	}
	select {
	case <-d.conn.Done():
		return nil, false
	default:
		return d.dc, true
	}
}

func (d *dispatcher) ensureConnected(ctx context.Context) (*diagnose.Client, error) {
	if dc, ok := d.currentClientIfAlive(); ok {
		return dc, nil
	}

	d.connectMu.Lock()
	defer d.connectMu.Unlock()

	if dc, ok := d.currentClientIfAlive(); ok {
		return dc, nil
	}

	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil, errDispatcherClosed
	}
	oldConn := d.conn
	if oldConn != nil {
		debuglog.Logf("dispatcher: gateway disconnected: %v", oldConn.Err())
	}
	d.mu.Unlock()

	conn, dc, serverURL, err := connectDiagnoseClient(ctx)
	if err != nil {
		if errors.Is(err, errGatewayNotConfigured) {
			debuglog.Logf("dispatcher: offline (not configured)")
			return nil, err
		}
		return nil, fmt.Errorf("OpenClaw 连接断了，自动重连失败: %w", err)
	}

	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		_ = conn.Close()
		return nil, errDispatcherClosed
	}
	oldConn = d.conn
	d.conn = conn
	d.dc = dc
	d.mu.Unlock()

	if oldConn != nil && oldConn != conn {
		_ = oldConn.Close()
	}
	debuglog.Logf("dispatcher: gateway reconnected (%s)", serverURL)
	return dc, nil
}

func (d *dispatcher) cancelActive(reason string) {
	d.mu.Lock()
	stop := d.activeStop
	d.activeStop = nil
	r := d.activeR
	d.activeR = nil
	d.mu.Unlock()
	if stop != nil {
		debuglog.Logf("dispatcher: cancel active diagnose: %s", reason)
		stop()
	}
	if r != nil {
		r.Done()
	}
}

// run 跑一次诊断: 构 Request → Start → renderer 流式消费 token → Done/Fail。
//
// 出错不 panic,只打 log 和 Fail 给用户看。
func (d *dispatcher) requestFromCommand(c cmdbuf.Command) *diagnose.Request {
	cwd, _ := os.Getwd()
	lark := diagnose.LarkRouting{}
	if cfg, err := config.Load(); err == nil && cfg != nil {
		lark.UserOpenID = strings.TrimSpace(cfg.Lark.UserOpenID)
		lark.Sender = strings.TrimSpace(cfg.Lark.Sender)
		lark.Forwarding = diagnose.LarkForwardingConfig{
			Version:    cfg.Lark.Forwarding.Version,
			Identities: map[string]diagnose.LarkForwardingIdentity{},
			Routes:     []diagnose.LarkForwardingRoute{},
		}
		for id, identity := range cfg.Lark.Forwarding.Identities {
			lark.Forwarding.Identities[id] = diagnose.LarkForwardingIdentity{
				Kind:             strings.TrimSpace(identity.Kind),
				Label:            strings.TrimSpace(identity.Label),
				AppID:            strings.TrimSpace(identity.AppID),
				UserOpenID:       strings.TrimSpace(identity.UserOpenID),
				Profile:          strings.TrimSpace(identity.Profile),
				LarkCLIConfigDir: strings.TrimSpace(identity.LarkCLIConfigDir),
				Source:           strings.TrimSpace(identity.Source),
				Slot:             strings.TrimSpace(identity.Slot),
				Enabled:          identity.Enabled,
			}
		}
		for _, route := range cfg.Lark.Forwarding.Routes {
			lark.Forwarding.Routes = append(lark.Forwarding.Routes, diagnose.LarkForwardingRoute{
				IdentityID: strings.TrimSpace(route.IdentityID),
				Target: diagnose.LarkTarget{
					Type:    strings.TrimSpace(route.Target.Type),
					ID:      strings.TrimSpace(route.Target.ID),
					Label:   strings.TrimSpace(route.Target.Label),
					Enabled: route.Target.Enabled,
				},
				Enabled: route.Enabled,
			})
		}
		for _, target := range cfg.Lark.Targets {
			lark.Targets = append(lark.Targets, diagnose.LarkTarget{
				Type:    strings.TrimSpace(target.Type),
				ID:      strings.TrimSpace(target.ID),
				Label:   strings.TrimSpace(target.Label),
				Enabled: target.Enabled,
			})
		}
	}
	return &diagnose.Request{
		Command:    c.Text,
		ExitCode:   c.Exit,
		OutputTail: string(c.Tail),
		Shell:      d.shellBin,
		Cwd:        cwd,
		Lang:       os.Getenv("LANG"),
		Lark:       lark,
	}
}

func (d *dispatcher) dispatchAlert(dc *diagnose.Client, req *diagnose.Request) {
	if dc == nil {
		return
	}
	if !hasLarkAlertTarget(req) {
		debuglog.Logf("alert: skipped (no lark target configured)")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	go func() {
		defer cancel()
		debuglog.Logf("alert: start command=%q exit=%d tail=%dB", req.Command, req.ExitCode, len(req.OutputTail))
		if err := dc.Alert(ctx, req); err != nil {
			debuglog.Logf("alert: failed: %v", err)
			d.notifyAlertFailure(err)
			return
		}
		debuglog.Logf("alert: delivered")
	}()
}

func hasLarkAlertTarget(req *diagnose.Request) bool {
	if req == nil {
		return false
	}
	for _, target := range req.Lark.Targets {
		if target.Enabled && strings.TrimSpace(target.ID) != "" {
			return true
		}
	}
	for _, route := range req.Lark.Forwarding.Routes {
		if route.Enabled && strings.TrimSpace(route.Target.ID) != "" && strings.TrimSpace(route.IdentityID) != "" {
			return true
		}
	}
	return strings.TrimSpace(req.Lark.UserOpenID) != ""
}

func (d *dispatcher) notifyAlertFailure(err error) {
	if err == nil || d == nil || d.w == nil {
		return
	}
	fmt.Fprintf(d.w, "\r\ntermind: Lark alert failed: %v\r\n", err)
	d.redrawPrompt()
}

// run 跑一次诊断: 构 Request → Start → renderer 流式消费 token → Done/Fail。
//
// 出错不 panic,只打 log 和 Fail 给用户看。
func (d *dispatcher) run(ctx context.Context, dc *diagnose.Client, req *diagnose.Request, r *render.Renderer) {
	debuglog.Logf("diagnose: start command=%q exit=%d tail=%dB", req.Command, req.ExitCode, len(req.OutputTail))
	defer d.finishActive(ctx)

	events, err := dc.Start(ctx, req)
	if err != nil {
		r.Fail(fmt.Sprintf("%v", err))
		d.redrawPrompt()
		debuglog.Logf("diagnose: start failed: %v", err)
		return
	}

	for ev := range events {
		if ev.Error != "" {
			r.Fail(ev.Error)
			d.redrawPrompt()
			debuglog.Logf("diagnose: server error: %s", ev.Error)
			return
		}
		if ev.Delta != "" {
			_, _ = r.Write(ev.Delta)
		}
		if ev.Done {
			r.Done()
			d.redrawPrompt()
			debuglog.Logf("diagnose: done")
			return
		}
	}
	// channel 被关但没收到 done: 被 cancel 或 conn 断
	r.Done()
	d.redrawPrompt()
	debuglog.Logf("diagnose: stream closed without done")
}

func (d *dispatcher) finishActive(ctx context.Context) {
	d.mu.Lock()
	if d.activeCtx == ctx {
		d.activeCtx = nil
		d.activeStop = nil
		d.activeR = nil
	}
	d.mu.Unlock()
}

func (d *dispatcher) redrawPrompt() {
	if !isZshShell(d.shellBin) {
		return
	}
	if d.shellCmd == nil || d.shellCmd.Process == nil {
		return
	}
	if err := d.shellCmd.Process.Signal(syscall.SIGUSR1); err != nil {
		debuglog.Logf("diagnose: redraw prompt signal failed: %v", err)
	}
}

func isZshShell(shellBin string) bool {
	name := filepath.Base(strings.TrimSpace(shellBin))
	return name == "zsh" || name == "-zsh"
}
