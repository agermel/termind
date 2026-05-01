package diagnose

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"termind/internal/gateway"
)

// Client 把 gateway.Conn 封装成"诊断一次就拿一个 token channel"的使用方式。
//
// 典型用法(shell 层):
//
//	dc := diagnose.NewClient(conn)
//	events, err := dc.Start(ctx, req)
//	for ev := range events {
//	    if ev.Error != "" { ... }
//	    render.Write(ev.Delta)
//	    if ev.Done { break }
//	}
//
// 实现要点:
//   - gateway.Conn 只接受一个 NotificationHandler,所以这里做多路复用:
//     按 stream_id 路由 diagnose.token 到对应 channel
//   - 诊断结束(done/error/ctx done)时会清理路由 + 关 channel
//   - 支持同时跑多条诊断(理论上需要时),shell 层实际只会有一条活跃的
type Client struct {
	conn *gateway.Conn

	mu      sync.Mutex
	streams map[string]chan TokenEvent // stream_id -> 下游 channel
	// pending 缓冲 "早到" 的 token: server 有时在 start response 之后立刻推
	// 第一个 token,但此时 Start() 里 Call 还没返回、streams 尚未注册。
	// 这里按 stream_id 把 early events 暂存,Start 注册 channel 时 flush。
	// 容量上限 32/stream,防止恶意 server push 爆内存。
	pending map[string][]TokenEvent
	// chained 是我们 hook 之前 conn 上已经挂的 handler,保证我们不覆盖别人。
	chained gateway.NotificationHandler
	hooked  bool
}

// earlyBufferCap 是单个 stream 在 Start 返回前允许 buffer 的 token 上限。
const earlyBufferCap = 32

// NewClient 构造 Client,不立即 hook conn —— 第一次 Start 时再 hook,
// 避免未用时产生副作用。
func NewClient(conn *gateway.Conn) *Client {
	return &Client{
		conn:    conn,
		streams: make(map[string]chan TokenEvent),
		pending: make(map[string][]TokenEvent),
	}
}

// Start 发起一次诊断,返回一个 token 事件 channel。
//
// channel 关闭时表示本次诊断结束(done/error/ctx done/conn 断开都会关)。
// 调用方不需要手动 close,也不应该自己 close。
//
// 如果 Start 本身返回 error(比如 conn 已关、RPC 失败),channel 不会被创建。
func (c *Client) Start(ctx context.Context, req *Request) (<-chan TokenEvent, error) {
	if c.conn == nil {
		return nil, errors.New("diagnose: nil gateway conn")
	}
	c.ensureHooked()

	var resp StartResponse
	if err := c.conn.Call(ctx, MethodStart, req, &resp); err != nil {
		return nil, fmt.Errorf("diagnose.start: %w", err)
	}
	if resp.StreamID == "" {
		return nil, errors.New("diagnose.start: server returned empty stream_id")
	}

	ch := make(chan TokenEvent, 16)
	c.mu.Lock()
	c.streams[resp.StreamID] = ch
	// flush 任何已经到达但尚未路由的 early events
	early := c.pending[resp.StreamID]
	delete(c.pending, resp.StreamID)
	c.mu.Unlock()
	var finalEarly *TokenEvent
	for i := range early {
		ev := early[i]
		select {
		case ch <- ev:
		default:
		}
		if ev.Done || ev.Error != "" {
			// 流已经在 start 返回前就结束了(很短的诊断),关 channel 直接返
			finalEarly = &ev
		}
	}
	if finalEarly != nil {
		c.closeStream(resp.StreamID, *finalEarly)
		return ch, nil
	}

	// ctx 取消/超时时: 发 cancel 给 server + 关闭 channel 清理路由
	go func() {
		<-ctx.Done()
		c.cancel(resp.StreamID, ctx.Err().Error())
	}()

	// conn 断开时也要关 channel,别让调用方死等
	go func() {
		select {
		case <-c.conn.Done():
			c.closeStream(resp.StreamID, TokenEvent{StreamID: resp.StreamID, Done: true, Error: "gateway closed"})
		case <-ctx.Done():
			// ctx 分支已经被上面那个 goroutine 处理
		}
	}()

	return ch, nil
}

// ensureHooked 在首次 Start 时给 conn 挂 NotificationHandler。
// 已有 handler 被链式保留,保证未来加别的业务不会踩坑。
func (c *Client) ensureHooked() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hooked {
		return
	}
	c.hooked = true
	// 先拿现有 handler(如果有),我们把非 diagnose.token 的消息往下传
	// 但 Conn 没有 getter,只能覆盖;当前 shell 接入时不会有别的 handler
	c.conn.SetOnNotify(c.onNotify)
}

// onNotify 是我们注册给 gateway.Conn 的总入口。
// 只处理 diagnose.token;其他 method 如果之前有 chained handler,
// 也往那儿转(当前 chained 恒为 nil,以后有需要再补)。
func (c *Client) onNotify(method string, params json.RawMessage) {
	if method != MethodToken {
		if c.chained != nil {
			c.chained(method, params)
		}
		return
	}
	var ev TokenEvent
	if err := json.Unmarshal(params, &ev); err != nil {
		return // 协议错,丢
	}
	if ev.StreamID == "" {
		return
	}

	c.mu.Lock()
	ch, hasStream := c.streams[ev.StreamID]
	if !hasStream {
		// 流还没被 Start 注册 —— early event,先缓冲到 pending。
		// 容量到 cap 后丢旧的,保证新的优先(done 帧更重要)。
		buf := c.pending[ev.StreamID]
		if len(buf) >= earlyBufferCap {
			buf = buf[1:]
		}
		c.pending[ev.StreamID] = append(buf, ev)
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	// 非阻塞送: 下游渲染慢也不拖住 readLoop
	select {
	case ch <- ev:
	default:
		// channel 满了,主动丢这一帧。对用户来说 delta 丢失,但避免死锁。
		// 渲染端如果发现 delta 有断裂可以通过 done 帧关 channel。
	}
	if ev.Done || ev.Error != "" {
		c.closeStream(ev.StreamID, ev)
	}
}

// cancel 通知 server 取消并清理本地 channel。
//
// 无论 server 收没收到 cancel,本地都要立刻关 channel,让上层渲染退出。
func (c *Client) cancel(streamID, reason string) {
	c.mu.Lock()
	_, ok := c.streams[streamID]
	c.mu.Unlock()
	if !ok {
		return
	}
	// best-effort 通知 server,不等响应
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 不管超时,Notify 是 fire-and-forget
	_ = ctx  // 显示依赖 ctx
	_ = c.conn.Notify(context.Background(), MethodCancel, &CancelNotification{
		StreamID: streamID,
		Reason:   reason,
	})
	c.closeStream(streamID, TokenEvent{StreamID: streamID, Done: true, Error: "canceled: " + reason})
}

// closeStream 从路由表删 stream_id 并关 channel。多次调用安全(加锁幂等)。
//
// finalEv 会最后送一次给下游,然后 close——让渲染端能知道"我是因为啥结束的"。
func (c *Client) closeStream(streamID string, finalEv TokenEvent) {
	c.mu.Lock()
	ch, ok := c.streams[streamID]
	if ok {
		delete(c.streams, streamID)
	}
	c.mu.Unlock()
	if !ok {
		return
	}
	// 送 final;满了也不等
	select {
	case ch <- finalEv:
	default:
	}
	close(ch)
}
