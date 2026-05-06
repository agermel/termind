// Package diagnose 是 termind 跟 OpenClaw "AI 诊断"业务的客户端。
//
// 职责:
//
//	protocol.go — 定义跟 OpenClaw plugin 共享的 DTO 和方法名
//	client.go   — 在 gateway.Conn 上封装 Start(ctx, req) -> <-chan Event
//
// 协议语义(OpenClaw Gateway frames over WebSocket):
//
//	client  -->  req/agent         (payload 返回 {runId})
//	client  -->  req/agent.wait    (等待 run 完成)
//	client  -->  req/sessions.get  (读取本轮 assistant 回复)
//
// 设计要点:
//   - agent/agent.wait/sessions.get 是 OpenClaw 官方 operator 方法。
//   - 对 shell 侧仍暴露事件 channel,方便以后换成插件流式 token 协议而不改 UI 层。
package diagnose

// Gateway method 名,集中一处改。
const (
	MethodAgent       = "agent"
	MethodAgentWait   = "agent.wait"
	MethodSessionsGet = "sessions.get"

	defaultSessionKey = "agent:main:termind"
	alertSessionKey   = "agent:main:termind-lark-alert"
	defaultLabel      = "termind"
	alertLabel        = "termind-lark-alert"
)

// Request 是一次 termind shell 诊断的输入。
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
	Lark       LarkRouting
}

type LarkRouting struct {
	UserOpenID string
	Sender     string
	Targets    []LarkTarget
	Forwarding LarkForwardingConfig
}

type LarkTarget struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Label   string `json:"label,omitempty"`
	Enabled bool   `json:"enabled,omitempty"`
}

type LarkForwardingConfig struct {
	Version    int                               `json:"version,omitempty"`
	Identities map[string]LarkForwardingIdentity `json:"identities,omitempty"`
	Routes     []LarkForwardingRoute             `json:"routes,omitempty"`
}

type LarkForwardingIdentity struct {
	ID               string `json:"id,omitempty"`
	Kind             string `json:"kind,omitempty"`
	Label            string `json:"label,omitempty"`
	AppID            string `json:"appId,omitempty"`
	UserOpenID       string `json:"userOpenId,omitempty"`
	Profile          string `json:"profile,omitempty"`
	LarkCLIConfigDir string `json:"larkCliConfigDir,omitempty"`
	Source           string `json:"source,omitempty"`
	Slot             string `json:"slot,omitempty"`
	Enabled          bool   `json:"enabled"`
}

type LarkForwardingRoute struct {
	IdentityID string     `json:"identityId,omitempty"`
	Target     LarkTarget `json:"target,omitempty"`
	Enabled    bool       `json:"enabled"`
}

// TokenEvent 是 shell 层消费的诊断事件。
//
// 当前 agent flow 只有一次 Delta + Done;未来如果 termind OpenClaw plugin 提供
// 流式 gateway method,可以继续复用这个 DTO。
type TokenEvent struct {
	// Delta 本次新增的文本。空字符串表示"只是状态位变更"。
	Delta string
	// Done 标记流终止;true 时此帧之后不会再有同一次诊断的 token。
	Done bool
	// Error 非空表示 server 侧出错终止;client 应当把错误展示给用户。
	Error string `json:"error,omitempty"`
}
