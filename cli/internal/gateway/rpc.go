// Package gateway 实现 termind 跟 OpenClaw 之间的 WebSocket 长连接
// 以及跑在上面的 JSON-RPC 2.0 消息层。
//
// 两个文件:
//
//	rpc.go   — 消息 DTO 和编解码(纯数据,无 IO)
//	conn.go  — 握手 / 心跳 / 收发循环 / Call/Notify API
//
// 对上层(cmd、shell 集成)暴露的只有 Conn 的 Call/Notify/OnNotify,
// JSON-RPC 的细节不泄漏出去。
package gateway

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
)

// JSON-RPC 2.0 protocol version。我们只发这个值;收到别的直接丢。
const jsonrpcVersion = "2.0"

// rpcMessage 是入站帧的统一外壳。
//
// 为什么用一个类型而不是三个:
//   - Request 有 id + method
//   - Response 有 id + result/error
//   - Notification 有 method + 无 id
//
// 这三种共享同一个 JSON 对象形状,只是字段不全,用 *json.RawMessage 延迟解析
// ID 字段。上层根据 method/id 是否存在来判别。
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`     // null / 缺省 = notification
	Method  string          `json:"method,omitempty"` // 有 method = server 主动发;response 无 method
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError 是 JSON-RPC 2.0 error 对象。
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Error 让 *rpcError 实现 error 接口,方便直接返给上层。
func (e *rpcError) Error() string {
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// RPCError 是 gateway 对外暴露的错误类型,避免把内部 rpcError 泄漏出去。
type RPCError struct {
	Code    int
	Message string
	Data    json.RawMessage
}

// Error 让 *RPCError 实现 error 接口。
func (e *RPCError) Error() string {
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// IsNotification 报告这帧是不是 server 主动 push(没 id)。
func (m *rpcMessage) IsNotification() bool {
	return m.Method != "" && (len(m.ID) == 0 || string(m.ID) == "null")
}

// IsRequest 报告这帧是不是 server 发来的请求(有 id + method)。
// 当前 CLI 不处理 server 主动请求;为空函数但保留 API 便于未来扩展。
func (m *rpcMessage) IsRequest() bool {
	return m.Method != "" && len(m.ID) > 0 && string(m.ID) != "null"
}

// IsResponse 报告这帧是不是 Call 的响应(有 id 但没 method)。
func (m *rpcMessage) IsResponse() bool {
	return m.Method == "" && len(m.ID) > 0 && string(m.ID) != "null"
}

// idGen 是 Conn 内部用来发号的单调递增计数器。
// 用 atomic 保证多 goroutine 同时 Call 不冲突。
type idGen struct{ n atomic.Int64 }

// next 返回下一个 id,从 1 开始。JSON-RPC 允许 id 用 int 或 string,我们用 int。
func (g *idGen) next() int64 { return g.n.Add(1) }

// encodeRequest 构造一个出站 Request 帧的 JSON 字节。
// params 可以是任意能 Marshal 的 Go 值;nil 表示无 params。
func encodeRequest(id int64, method string, params any) ([]byte, error) {
	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal params: %w", err)
		}
		rawParams = b
	}
	// 注意: id 要序列化成 JSON 数字,不能用 json.RawMessage 直接塞字符串
	idBytes, _ := json.Marshal(id)
	m := rpcMessage{
		JSONRPC: jsonrpcVersion,
		ID:      idBytes,
		Method:  method,
		Params:  rawParams,
	}
	return json.Marshal(&m)
}

// encodeNotification 构造一个 Notification(无 id,server 不回)。
func encodeNotification(method string, params any) ([]byte, error) {
	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal params: %w", err)
		}
		rawParams = b
	}
	m := rpcMessage{
		JSONRPC: jsonrpcVersion,
		Method:  method,
		Params:  rawParams,
	}
	return json.Marshal(&m)
}

// decode 反解一条入站帧;返回的 message 字段用 IsXxx 判别类型。
func decode(raw []byte) (*rpcMessage, error) {
	var m rpcMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("decode rpc: %w", err)
	}
	if m.JSONRPC != "" && m.JSONRPC != jsonrpcVersion {
		// 记一声但不拒绝:宽松处理,便于跟非严格的 server 互操作
		return &m, nil
	}
	return &m, nil
}

// idOf 把 rpcMessage.ID 这个 RawMessage 拆回 int64。
// 非 int 或 null 返回 (0, false)。
func idOf(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	var i int64
	if err := json.Unmarshal(raw, &i); err != nil {
		return 0, false
	}
	return i, true
}
