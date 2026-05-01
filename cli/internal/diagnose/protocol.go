// Package diagnose 是 termind 跟 OpenClaw "AI 诊断"业务的客户端。
//
// 职责:
//
//	protocol.go — 定义跟 OpenClaw plugin 共享的 DTO 和方法名
//	client.go   — 在 gateway.Conn 上封装 Start(ctx, req) -> <-chan Event
//
// 协议语义(JSON-RPC over WebSocket):
//
//	client  -->  diagnose.start    (Request, 返回 {stream_id})
//	server  -->  diagnose.token    (Notification, 带 stream_id + delta,
//	                                最后一帧 done=true 可带 final 摘要)
//	client  -->  diagnose.cancel   (Notification, 带 stream_id,取消进行中的诊断)
//
// 设计要点:
//   - stream_id 由 server 分配,client 不要自己生:不同 server 实现策略可能不同
//   - token 流是 Notification 不是 Response,server 可以随时推、数量不定
//   - cancel 是 Notification 不是 Call:client 按下就走,不等 server 回
package diagnose

// JSON-RPC method 名,集中一处改。
const (
	MethodStart  = "diagnose.start"
	MethodToken  = "diagnose.token"
	MethodCancel = "diagnose.cancel"
)

// Request 是 diagnose.start 的 params。
//
// 字段:
//   - Command      用户实际输入的命令(zsh 里从 history 拿)
//   - ExitCode     命令退出码,非 0 才触发 ——plugin 可据此判断严重度
//   - OutputTail   命令输出的末尾几 KB(cmdbuf 的 ring 给的就是这个)
//   - Shell        子 shell 名,比如 "zsh"/"bash"——影响提示/修复建议语法
//   - Cwd          命令执行时的工作目录,用来做 repo 上下文
//   - Lang         用户语言环境,"zh-CN"/"en-US",让 plugin 出对应语种
type Request struct {
	Command    string `json:"command"`
	ExitCode   int    `json:"exit_code"`
	OutputTail string `json:"output_tail,omitempty"`
	Shell      string `json:"shell,omitempty"`
	Cwd        string `json:"cwd,omitempty"`
	Lang       string `json:"lang,omitempty"`
}

// StartResponse 是 diagnose.start 的 result。
//
// 收到后 client 立刻进入"等 token notification"的流式阶段。
type StartResponse struct {
	StreamID string `json:"stream_id"`
}

// TokenEvent 是 diagnose.token notification 的 params。
//
// 一次诊断会收到多次 TokenEvent,顺序有意义。
// 最后一帧 Done=true,可能附加 Final(结构化摘要,M6 plugin 实现时定具体字段)。
type TokenEvent struct {
	StreamID string `json:"stream_id"`
	// Delta 本次新增的文本(增量,非全量)。空字符串表示"只是心跳/状态位变更"。
	Delta string `json:"delta,omitempty"`
	// Done 标记流的终止;true 时此帧之后不会再有同 stream_id 的 token
	Done bool `json:"done,omitempty"`
	// Error 非空表示 server 侧出错终止;client 应当把错误展示给用户
	Error string `json:"error,omitempty"`
}

// CancelNotification 是 diagnose.cancel 的 params。
type CancelNotification struct {
	StreamID string `json:"stream_id"`
	// Reason 可选,给 server 日志用
	Reason string `json:"reason,omitempty"`
}
