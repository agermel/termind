// Package pairing 实现 termind 跟 OpenClaw 的"首次配对"HTTP 握手,
// 以及后续每次 ws 连线时的 challenge-response 签名。
//
// 这个文件只放"协议 DTO"——和 OpenClaw 后端约定好的 JSON 形状。
// 实际的 HTTP 调用在 client.go,CLI 入口在 cmd/pair.go。
//
// 协议概览:
//
//	1) client -> POST {server}/v1/pair/start
//	      body:  StartRequest
//	      resp:  StartResponse (含 pair_code + challenge_id + 过期时间)
//
//	2) 操作员在 OpenClaw 后台看到 pair_code 和公钥指纹,批准这台设备
//
//	3) client -> GET  {server}/v1/pair/poll?challenge_id=...
//	      resp:  PollResponse,直到 status=approved 返回 long-lived token
//
// 注意:
//   - 私钥永不上报,只上报 PublicKeyPEM 给 server
//   - pair_code 只是给人看的,不参与加密;真正身份锚点是公钥
//   - token 是 OpenClaw 颁发的 opaque 字符串,client 只管存着,
//     下次连 ws 时带上 + 用私钥签 nonce 证明是本人
package pairing

// StartRequest 是 POST /v1/pair/start 的 body。
type StartRequest struct {
	// DeviceID 是公钥的稳定短 ID,便于 OpenClaw 侧去重/排序。
	DeviceID string `json:"device_id"`
	// PublicKey 是 ed25519 公钥的 PEM 编码,server 要存起来用来验签。
	PublicKey string `json:"public_key"`
	// Hostname 给操作员看,便于在审批列表里辨认"这台是谁的机器"。
	Hostname string `json:"hostname"`
	// ClientVersion termind 的版本号,便于服务端兼容判断。
	ClientVersion string `json:"client_version"`
}

// StartResponse 是 /v1/pair/start 的返回。
type StartResponse struct {
	// PairCode 是给人看的短码,比如 "ABC-DEF"。client 把它和公钥指纹
	// 一起显示给用户,用户在 OpenClaw 后台肉眼比对确认。
	PairCode string `json:"pair_code"`
	// ChallengeID 是本次配对请求的唯一 ID,client 之后轮询 /poll 时带上。
	ChallengeID string `json:"challenge_id"`
	// ExpiresAt RFC3339 时间戳,过期后 server 会丢掉这次 challenge。
	ExpiresAt string `json:"expires_at"`
	// PollIntervalSec 建议的轮询间隔秒数;0 = 用 client 默认。
	PollIntervalSec int `json:"poll_interval_sec"`
}

// PollStatus 是配对状态机。
type PollStatus string

const (
	// StatusPending 操作员还没批准。
	StatusPending PollStatus = "pending"
	// StatusApproved 操作员已批准,token 字段有效。
	StatusApproved PollStatus = "approved"
	// StatusDenied 操作员拒绝。
	StatusDenied PollStatus = "denied"
	// StatusExpired 超时未批准,需要重新 start。
	StatusExpired PollStatus = "expired"
)

// PollResponse 是 /v1/pair/poll 的返回。
//
// status=approved 时 Token 非空;其他状态 Token 为空。
type PollResponse struct {
	Status PollStatus `json:"status"`
	Token  string     `json:"token,omitempty"`
	// Reason 仅 denied/expired 时可选,给用户看的人话解释。
	Reason string `json:"reason,omitempty"`
}

// ErrorResponse 是所有 pair 接口的错误返回(HTTP 4xx/5xx)。
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}
