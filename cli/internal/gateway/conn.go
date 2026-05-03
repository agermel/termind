package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"termind/internal/identity"
	"termind/internal/pairing"
)

const protocolVersion = 3

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
	pending map[string]chan *frame
	closed  bool
	closeCh chan struct{}
	readErr error

	// 写锁: coder/websocket 的 Conn 不支持并发 Write,所以我们自己串行化
	writeMu sync.Mutex

	// pingInterval 心跳间隔,0 = 不发主动心跳(依赖 coder/websocket 自带)
	pingInterval time.Duration
}

type idGen struct{ n atomic.Int64 }

func (g *idGen) next() string {
	return fmt.Sprintf("termind-%d", g.n.Add(1))
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
	// Token 是 OpenClaw hello-ok.auth.deviceToken。
	Token string
	// SharedToken 是 Gateway auth.token,用于网关要求 shared token 才允许 connect 的部署。
	SharedToken string
	// BootstrapToken 是 OpenClaw setup code 里的短期 bootstrapToken。
	// 首次 pair 时使用;成功后 server 会返回 deviceToken。
	BootstrapToken string
	// Password 是 Gateway auth.password,用于网关配置 password auth 的部署。
	Password string
	// Role 是 OpenClaw device token 绑定的 role。空值默认 node。
	Role string
	// Scopes 是 device token 绑定的 scopes。重连时要和 token 绑定值一致。
	Scopes []string
	// ClientID 必须是 OpenClaw GatewayClientId 枚举值。空值默认 node-host。
	ClientID string
	// ClientMode 必须是 OpenClaw GatewayClientMode 枚举值。空值默认 node。
	ClientMode string
	// Platform/deviceFamily 参与 v3 签名 payload;远程 OpenClaw 会做 metadata pinning。
	Platform     string
	DeviceFamily string
	// ClientVersion 比如 "0.0.1-dev"。
	ClientVersion string
	// OnDeviceToken 收到 hello-ok.auth.deviceToken 时调用,用于持久化轮换后的 token。
	OnDeviceToken func(role, token string, scopes []string)
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
	if opts.HandshakeTimeout <= 0 {
		opts.HandshakeTimeout = 10 * time.Second
	}

	u := NormalizeGatewayURL(opts.ServerURL)
	if err := validateSecureWSURL(u); err != nil {
		return nil, err
	}

	hsCtx, cancel := context.WithTimeout(ctx, opts.HandshakeTimeout)
	defer cancel()

	ws, _, err := websocket.Dial(hsCtx, u, &websocket.DialOptions{
		HTTPClient: opts.HTTPClient,
	})
	if err != nil {
		return nil, fmt.Errorf("ws dial: %w", err)
	}

	hello, err := performConnect(hsCtx, ws, opts)
	if err != nil {
		return nil, err
	}

	c := &Conn{
		ws:           ws,
		handler:      opts.OnNotify,
		pending:      make(map[string]chan *frame),
		closeCh:      make(chan struct{}),
		pingInterval: opts.PingInterval,
	}
	if opts.OnDeviceToken != nil && hello.Auth.DeviceToken != "" {
		opts.OnDeviceToken(hello.Auth.Role, hello.Auth.DeviceToken, hello.Auth.Scopes)
	}
	go c.readLoop()
	if c.pingInterval > 0 {
		go c.pingLoop()
	}
	return c, nil
}

// Call 发一次 req,阻塞等 res。result 非 nil 时把 payload 字段反序列化进去。
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
	ch := make(chan *frame, 1)

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
		if resp == nil {
			return ErrClosed
		}
		if resp.Error != nil {
			return &RPCError{
				Code:         resp.Error.Code,
				Message:      resp.Error.Message,
				Details:      resp.Error.Details,
				Retryable:    resp.Error.Retryable,
				RetryAfterMS: resp.Error.RetryAfterMS,
			}
		}
		if result != nil && len(resp.Payload) > 0 {
			if err := json.Unmarshal(resp.Payload, result); err != nil {
				return fmt.Errorf("unmarshal payload: %w", err)
			}
		}
		return nil
	}
}

// Notify 发一帧 req,不等 server 回。
func (c *Conn) Notify(ctx context.Context, method string, params any) error {
	if c.isClosed() {
		return ErrClosed
	}
	raw, err := encodeNotification(c.ids.next(), method, params)
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
			c.mu.Lock()
			ch := c.pending[msg.ID]
			c.mu.Unlock()
			if ch != nil {
				// 非阻塞送:如果 Call 已经 ctx.Done 了,也不卡死 readLoop
				select {
				case ch <- msg:
				default:
				}
			}
		case msg.IsEvent():
			c.mu.Lock()
			h := c.handler
			c.mu.Unlock()
			if h != nil {
				// 不在 readLoop 里直接同步跑 handler,怕用户 handler 阻塞;
				// 但也不想每个 notify 起一个 goroutine,折中:同步跑,
				// 约定 handler 必须快速返回(几 ms 内)或自己 spawn goroutine。
				h(msg.Event, msg.Payload)
			}
		case msg.IsRequest():
			// 当前 CLI 不支持被 server 反向 Call;回一个 method not found。
			errResp := frame{
				Type:  frameTypeResponse,
				ID:    msg.ID,
				OK:    false,
				Error: &frameError{Code: "METHOD_NOT_FOUND", Message: "method not found"},
			}
			b, _ := json.Marshal(&errResp)
			_ = c.writeFrame(context.Background(), b)
		default:
			// 空帧或格式异常,忽略
		}
	}
}

type connectParams struct {
	MinProtocol int                       `json:"minProtocol"`
	MaxProtocol int                       `json:"maxProtocol"`
	Client      connectClientInfo         `json:"client"`
	Caps        []string                  `json:"caps,omitempty"`
	Auth        connectAuth               `json:"auth"`
	Role        string                    `json:"role"`
	Scopes      []string                  `json:"scopes,omitempty"`
	Device      *pairing.DeviceDescriptor `json:"device"`
}

type connectClientInfo struct {
	ID           string `json:"id"`
	DisplayName  string `json:"displayName,omitempty"`
	Version      string `json:"version"`
	Platform     string `json:"platform"`
	DeviceFamily string `json:"deviceFamily,omitempty"`
	Mode         string `json:"mode"`
	InstanceID   string `json:"instanceId,omitempty"`
}

type connectAuth struct {
	Token          string `json:"token,omitempty"`
	BootstrapToken string `json:"bootstrapToken,omitempty"`
	DeviceToken    string `json:"deviceToken,omitempty"`
	Password       string `json:"password,omitempty"`
}

type helloOK struct {
	Type     string `json:"type"`
	Protocol int    `json:"protocol"`
	Server   struct {
		Version string `json:"version"`
		ConnID  string `json:"connId"`
	} `json:"server"`
	Auth struct {
		DeviceToken string   `json:"deviceToken,omitempty"`
		Role        string   `json:"role"`
		Scopes      []string `json:"scopes"`
		IssuedAtMS  int64    `json:"issuedAtMs,omitempty"`
	} `json:"auth"`
}

func performConnect(ctx context.Context, ws *websocket.Conn, opts DialOptions) (*helloOK, error) {
	_, chBytes, err := ws.Read(ctx)
	if err != nil {
		ws.Close(websocket.StatusProtocolError, "read connect challenge")
		return nil, fmt.Errorf("read connect challenge: %w", err)
	}
	var ch pairing.ConnectChallenge
	if err := json.Unmarshal(chBytes, &ch); err != nil {
		ws.Close(websocket.StatusProtocolError, "bad connect challenge json")
		return nil, fmt.Errorf("decode connect challenge: %w", err)
	}
	if ch.Type != frameTypeEvent || ch.Event != "connect.challenge" || ch.Payload.Nonce == "" {
		ws.Close(websocket.StatusProtocolError, "bad connect challenge")
		return nil, errors.New("bad connect challenge")
	}

	role := defaultString(opts.Role, pairing.DefaultRole)
	clientID := defaultString(opts.ClientID, pairing.DefaultClientID)
	clientMode := defaultString(opts.ClientMode, pairing.DefaultClientMode)
	platform := defaultString(opts.Platform, pairing.DefaultPlatform())
	deviceFamily := defaultString(opts.DeviceFamily, pairing.DefaultDeviceFamily)
	scopes := pairing.NormalizeScopes(opts.Scopes)
	if role == pairing.DefaultRole && len(scopes) == 0 {
		scopes = pairing.DefaultScopes()
	}
	signedAt := time.Now().UnixMilli()

	device, err := pairing.BuildDeviceDescriptor(pairing.DeviceAuthInput{
		Identity:     opts.Identity,
		Token:        signatureToken(opts),
		Nonce:        ch.Payload.Nonce,
		SignedAtMs:   signedAt,
		ClientID:     clientID,
		ClientMode:   clientMode,
		Role:         role,
		Scopes:       scopes,
		Platform:     platform,
		DeviceFamily: deviceFamily,
	})
	if err != nil {
		ws.Close(websocket.StatusPolicyViolation, "sign connect")
		return nil, fmt.Errorf("sign connect: %w", err)
	}

	params := connectParams{
		MinProtocol: protocolVersion,
		MaxProtocol: protocolVersion,
		Client: connectClientInfo{
			ID:           clientID,
			DisplayName:  "termind",
			Version:      defaultString(opts.ClientVersion, "termind-dev"),
			Platform:     platform,
			DeviceFamily: deviceFamily,
			Mode:         clientMode,
			InstanceID:   opts.Identity.DeviceID(),
		},
		Caps:   []string{},
		Auth:   connectAuthFor(opts),
		Role:   role,
		Scopes: scopes,
		Device: device,
	}
	reqBytes, err := encodeRequest("connect-1", "connect", params)
	if err != nil {
		ws.Close(websocket.StatusProtocolError, "encode connect")
		return nil, err
	}
	if err := ws.Write(ctx, websocket.MessageText, reqBytes); err != nil {
		ws.Close(websocket.StatusAbnormalClosure, "write connect")
		return nil, fmt.Errorf("write connect: %w", err)
	}

	_, resBytes, err := ws.Read(ctx)
	if err != nil {
		ws.Close(websocket.StatusAbnormalClosure, "read connect result")
		return nil, fmt.Errorf("read connect result: %w", err)
	}
	res, err := decode(resBytes)
	if err != nil {
		ws.Close(websocket.StatusProtocolError, "bad connect response")
		return nil, err
	}
	if !res.IsResponse() || res.ID != "connect-1" {
		ws.Close(websocket.StatusProtocolError, "invalid connect response")
		return nil, errors.New("invalid connect response")
	}
	if !res.OK {
		ws.Close(websocket.StatusPolicyViolation, "connect rejected")
		if res.Error != nil {
			return nil, &ConnectError{
				Code:         res.Error.Code,
				Message:      res.Error.Message,
				Details:      res.Error.Details,
				Retryable:    res.Error.Retryable,
				RetryAfterMS: res.Error.RetryAfterMS,
			}
		}
		return nil, errors.New("connect rejected")
	}
	var hello helloOK
	if err := json.Unmarshal(res.Payload, &hello); err != nil {
		ws.Close(websocket.StatusProtocolError, "bad hello-ok")
		return nil, fmt.Errorf("decode hello-ok: %w", err)
	}
	if hello.Type != "hello-ok" {
		ws.Close(websocket.StatusProtocolError, "bad hello-ok type")
		return nil, fmt.Errorf("expected hello-ok, got %q", hello.Type)
	}
	if hello.Protocol != protocolVersion {
		ws.Close(websocket.StatusProtocolError, "protocol mismatch")
		return nil, fmt.Errorf("protocol mismatch: server=%d client=%d", hello.Protocol, protocolVersion)
	}
	return &hello, nil
}

func connectAuthFor(opts DialOptions) connectAuth {
	if opts.BootstrapToken != "" {
		return connectAuth{BootstrapToken: opts.BootstrapToken}
	}
	if opts.SharedToken != "" {
		return connectAuth{Token: opts.SharedToken}
	}
	if opts.Password != "" {
		return connectAuth{Password: opts.Password}
	}
	if opts.Token == "" {
		return connectAuth{}
	}
	return connectAuth{Token: opts.Token, DeviceToken: opts.Token}
}

func signatureToken(opts DialOptions) string {
	if opts.BootstrapToken != "" {
		return opts.BootstrapToken
	}
	if opts.SharedToken != "" {
		return opts.SharedToken
	}
	return opts.Token
}

func defaultString(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
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

// NormalizeGatewayURL 补齐默认路径 /v1/gateway,同时把用户友好的 host:port
// 或 http(s) URL 规范化为 OpenClaw Gateway WebSocket URL。
func NormalizeGatewayURL(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		s = defaultGatewayScheme(s) + "://" + s
	}
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

func defaultGatewayScheme(hostPort string) string {
	u, err := url.Parse("ws://" + strings.TrimSpace(hostPort))
	if err != nil {
		return "wss"
	}
	host := u.Hostname()
	if isLoopbackHost(host) || host == "10.0.2.2" || isPrivateLANHost(host) {
		return "ws"
	}
	return "wss"
}

func validateSecureWSURL(s string) error {
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("parse gateway url: %w", err)
	}
	if u.Scheme != "ws" {
		return nil
	}
	host := u.Hostname()
	if isLoopbackHost(host) || host == "10.0.2.2" || isPrivateLANHost(host) {
		return nil
	}
	return fmt.Errorf("refusing plaintext ws:// gateway for remote host %q; use wss://", host)
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.IsLoopback()
}

func isPrivateLANHost(host string) bool {
	addr, err := netip.ParseAddr(host)
	return err == nil && (addr.IsPrivate() || addr.IsLinkLocalUnicast())
}
