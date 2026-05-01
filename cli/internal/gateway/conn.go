package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"termind/internal/identity"
	"termind/internal/pairing"
)

// Conn 是一条已经握手完成的 OpenClaw 长连接。
//
// 对上层暴露的能力:
//
//	Call(ctx, method, params, &result) error        同步调用
//	Notify(ctx, method, params) error               发 push 消息
//	OnNotify(handler)                               注册 server push 回调
//	Close() error                                   主动断开
//
// 生命周期: Dial 成功 -> 握手 -> 起一个 readLoop goroutine。
// Close 或 readLoop 报错时,ctx 被取消,所有 pending Call 收到 ErrClosed。
type Conn struct {
	ws      *websocket.Conn
	ids     idGen
	handler NotificationHandler

	mu      sync.Mutex
	pending map[int64]chan *rpcMessage
	closed  bool
	closeCh chan struct{}
	readErr error

	// 写锁: coder/websocket 的 Conn 不支持并发 Write,所以我们自己串行化
	writeMu sync.Mutex

	// pingInterval 心跳间隔,0 = 不发主动心跳(依赖 coder/websocket 自带)
	pingInterval time.Duration
}

// NotificationHandler 是 server 主动 push 时的回调。
// 上层注册一个统一回调,根据 method 分流。
type NotificationHandler func(method string, params json.RawMessage)

// ErrClosed 是连接已关闭时 Call/Notify 返回的固定错误。
var ErrClosed = errors.New("gateway: connection closed")

// DialOptions 是 Dial 的配置。
type DialOptions struct {
	// ServerURL wss://... 或 ws://...。不带路径时自动补 /v1/gateway。
	ServerURL string
	// Identity 本机身份(ed25519)。
	Identity *identity.Identity
	// Token pair 之后拿到的长期 token;必须非空。
	Token string
	// ClientVersion 比如 "0.0.1-dev"。
	ClientVersion string
	// OnNotify server push 的回调,可为 nil。
	OnNotify NotificationHandler
	// HandshakeTimeout 默认 10 秒。
	HandshakeTimeout time.Duration
	// PingInterval 主动发 ping 的间隔,<=0 不发。
	PingInterval time.Duration
	// HTTPClient 用于 ws 握手时的底层 HTTP client;nil 时用默认。
	HTTPClient *http.Client
}

// Dial 建立并完成握手。成功返回的 Conn 内部已启 readLoop,可直接 Call。
func Dial(ctx context.Context, opts DialOptions) (*Conn, error) {
	if opts.ServerURL == "" {
		return nil, errors.New("ServerURL is required")
	}
	if opts.Identity == nil {
		return nil, errors.New("Identity is required")
	}
	if opts.Token == "" {
		return nil, errors.New("Token is required (run `termind pair` first)")
	}
	if opts.HandshakeTimeout <= 0 {
		opts.HandshakeTimeout = 10 * time.Second
	}

	u := normalizeWSURL(opts.ServerURL)

	hsCtx, cancel := context.WithTimeout(ctx, opts.HandshakeTimeout)
	defer cancel()

	ws, _, err := websocket.Dial(hsCtx, u, &websocket.DialOptions{
		HTTPClient: opts.HTTPClient,
	})
	if err != nil {
		return nil, fmt.Errorf("ws dial: %w", err)
	}

	// ---- challenge-response 握手 ----
	_, chBytes, err := ws.Read(hsCtx)
	if err != nil {
		ws.Close(websocket.StatusProtocolError, "read challenge")
		return nil, fmt.Errorf("read challenge: %w", err)
	}
	var ch pairing.ChallengeMessage
	if err := json.Unmarshal(chBytes, &ch); err != nil {
		ws.Close(websocket.StatusProtocolError, "bad challenge json")
		return nil, fmt.Errorf("decode challenge: %w", err)
	}

	auth, err := pairing.SignChallenge(opts.Identity, &ch, opts.Token, opts.ClientVersion)
	if err != nil {
		ws.Close(websocket.StatusPolicyViolation, "sign failed")
		return nil, fmt.Errorf("sign challenge: %w", err)
	}
	authBytes, _ := json.Marshal(auth)
	if err := ws.Write(hsCtx, websocket.MessageText, authBytes); err != nil {
		ws.Close(websocket.StatusAbnormalClosure, "write auth")
		return nil, fmt.Errorf("write auth: %w", err)
	}

	// 等 server 的 auth_ok / auth_fail
	_, resBytes, err := ws.Read(hsCtx)
	if err != nil {
		ws.Close(websocket.StatusAbnormalClosure, "read auth result")
		return nil, fmt.Errorf("read auth result: %w", err)
	}
	var res pairing.AuthResultMessage
	if err := json.Unmarshal(resBytes, &res); err != nil {
		ws.Close(websocket.StatusProtocolError, "bad auth result json")
		return nil, fmt.Errorf("decode auth result: %w", err)
	}
	if res.Type != pairing.MsgTypeAuthOK {
		ws.Close(websocket.StatusPolicyViolation, "auth rejected")
		return nil, fmt.Errorf("auth rejected: %s", res.Reason)
	}

	c := &Conn{
		ws:           ws,
		handler:      opts.OnNotify,
		pending:      make(map[int64]chan *rpcMessage),
		closeCh:      make(chan struct{}),
		pingInterval: opts.PingInterval,
	}
	go c.readLoop()
	if c.pingInterval > 0 {
		go c.pingLoop()
	}
	return c, nil
}

// Call 发一次 Request,阻塞等 Response。result 非 nil 时把 result 字段反序列化进去。
//
// 当 ctx 超时或连接关闭时返回相应错误。
func (c *Conn) Call(ctx context.Context, method string, params, result any) error {
	if c.isClosed() {
		return ErrClosed
	}
	id := c.ids.next()
	raw, err := encodeRequest(id, method, params)
	if err != nil {
		return err
	}
	ch := make(chan *rpcMessage, 1)

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	c.pending[id] = ch
	c.mu.Unlock()

	// 确保 pending 一定被清理(无论哪条路径出去)
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if err := c.writeFrame(ctx, raw); err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.closeCh:
		return ErrClosed
	case resp := <-ch:
		if resp.Error != nil {
			return &RPCError{Code: resp.Error.Code, Message: resp.Error.Message, Data: resp.Error.Data}
		}
		if result != nil && len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, result); err != nil {
				return fmt.Errorf("unmarshal result: %w", err)
			}
		}
		return nil
	}
}

// Notify 发一帧 Notification,不等 server 回。
func (c *Conn) Notify(ctx context.Context, method string, params any) error {
	if c.isClosed() {
		return ErrClosed
	}
	raw, err := encodeNotification(method, params)
	if err != nil {
		return err
	}
	return c.writeFrame(ctx, raw)
}

// SetOnNotify 允许 Dial 之后再改 handler(比如 shell 进入不同场景要换)。
func (c *Conn) SetOnNotify(h NotificationHandler) {
	c.mu.Lock()
	c.handler = h
	c.mu.Unlock()
}

// Close 主动关闭连接。可重入,多次调用只关一次。
func (c *Conn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	close(c.closeCh)
	c.mu.Unlock()
	return c.ws.Close(websocket.StatusNormalClosure, "client closing")
}

// Done 在连接关闭后立刻返回;上层可以 select 监听。
func (c *Conn) Done() <-chan struct{} { return c.closeCh }

// Err 返回 readLoop 挂掉时记录的底层错误(如果有)。Close 正常不返回 err。
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.readErr
}

// ---------- 内部 ----------

func (c *Conn) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// writeFrame 串行化 ws.Write,因为 coder/websocket 不支持并发写。
func (c *Conn) writeFrame(ctx context.Context, raw []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.ws.Write(ctx, websocket.MessageText, raw)
}

// readLoop 独占 ws 的 Read,把入站帧分流到 pending 或 handler。
func (c *Conn) readLoop() {
	defer func() {
		// 读循环出来就意味着连接失效,统一关一次
		c.mu.Lock()
		if !c.closed {
			c.closed = true
			close(c.closeCh)
		}
		// 唤醒所有 pending Call
		for _, ch := range c.pending {
			close(ch)
		}
		c.pending = nil
		c.mu.Unlock()
	}()

	ctx := context.Background()
	for {
		_, raw, err := c.ws.Read(ctx)
		if err != nil {
			c.mu.Lock()
			c.readErr = err
			c.mu.Unlock()
			return
		}
		msg, err := decode(raw)
		if err != nil {
			// 协议异常,断开;上层可以根据 Err() 判定
			c.mu.Lock()
			c.readErr = err
			c.mu.Unlock()
			return
		}
		switch {
		case msg.IsResponse():
			id, ok := idOf(msg.ID)
			if !ok {
				continue
			}
			c.mu.Lock()
			ch := c.pending[id]
			c.mu.Unlock()
			if ch != nil {
				// 非阻塞送:如果 Call 已经 ctx.Done 了,也不卡死 readLoop
				select {
				case ch <- msg:
				default:
				}
			}
		case msg.IsNotification():
			c.mu.Lock()
			h := c.handler
			c.mu.Unlock()
			if h != nil {
				// 不在 readLoop 里直接同步跑 handler,怕用户 handler 阻塞;
				// 但也不想每个 notify 起一个 goroutine,折中:同步跑,
				// 约定 handler 必须快速返回(几 ms 内)或自己 spawn goroutine。
				h(msg.Method, msg.Params)
			}
		case msg.IsRequest():
			// 当前 CLI 不支持被 server 反向 Call;回一个 method not found。
			id, ok := idOf(msg.ID)
			if !ok {
				continue
			}
			errResp := rpcMessage{
				JSONRPC: jsonrpcVersion,
				ID:      msg.ID,
				Error:   &rpcError{Code: -32601, Message: "method not found"},
			}
			b, _ := json.Marshal(&errResp)
			_ = c.writeFrame(context.Background(), b)
			_ = id
		default:
			// 空帧或格式异常,忽略
		}
	}
}

// pingLoop 定期发应用层 ping。coder/websocket 自己已经有底层 ping/pong,
// 这个只是业务层保活(有的代理会杀太久没业务的 ws)。
func (c *Conn) pingLoop() {
	t := time.NewTicker(c.pingInterval)
	defer t.Stop()
	for {
		select {
		case <-c.closeCh:
			return
		case <-t.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = c.ws.Ping(ctx)
			cancel()
		}
	}
}

// normalizeWSURL 补齐默认路径 /v1/gateway,同时 https->wss / http->ws。
func normalizeWSURL(s string) string {
	// 允许用户传 https:// 作为 base,自动转成 wss://
	switch {
	case len(s) >= 8 && s[:8] == "https://":
		s = "wss://" + s[8:]
	case len(s) >= 7 && s[:7] == "http://":
		s = "ws://" + s[7:]
	}
	// 去尾斜杠
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	// 如果没带路径,补 /v1/gateway。简单判断: 有没有第三个 '/'。
	slashes := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			slashes++
			if slashes >= 3 {
				break
			}
		}
	}
	if slashes < 3 {
		return s + "/v1/gateway"
	}
	return s
}
