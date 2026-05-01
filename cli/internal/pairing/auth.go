package pairing

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"

	"termind/internal/identity"
)

// 这个文件实现每次 WebSocket 连线时的"challenge-response"握手。
//
// 流程(client 视角):
//
//  1. client 连上 wss://...,server 发一帧 ChallengeMessage(含随机 nonce)
//  2. client 用自己的 ed25519 私钥给 nonce 签名,回一帧 AuthMessage
//     (含 device_id + token + base64(signature))
//  3. server 从库里查到这个 device_id 对应的公钥,验签 + 对 token;
//     都通过才允许后续业务消息
//
// 注意:
//   - token 是一次 pair 之后的长期凭证,丢了也没关系(再 pair 一次重拿)
//   - 签名的对象必须是 server 发过来的 nonce 原值,不能是 client 自己随机生的
//   - 我们签 nonce 时也拼进 token 前缀,防止"把在 server A 拿到的签名
//     重放到 server B"这种跨域重放

const (
	// MsgTypeChallenge server -> client 的第一帧 type 值。
	MsgTypeChallenge = "challenge"
	// MsgTypeAuth     client -> server 的回复 type 值。
	MsgTypeAuth = "auth"
	// MsgTypeAuthOK   server 确认握手成功后的回复 type 值。
	MsgTypeAuthOK = "auth_ok"
	// MsgTypeAuthFail server 拒绝握手的 type 值,附 reason。
	MsgTypeAuthFail = "auth_fail"
)

// ChallengeMessage 是 server 发给 client 的第一帧。
type ChallengeMessage struct {
	Type  string `json:"type"`           // 必须 == MsgTypeChallenge
	Nonce string `json:"nonce"`          // server 随机生成的 base64 串,至少 32 字节熵
	Realm string `json:"realm,omitempty"` // server 标识,便于 client 确认连对了地方
}

// AuthMessage 是 client 对 challenge 的回答。
type AuthMessage struct {
	Type      string `json:"type"`      // 必须 == MsgTypeAuth
	DeviceID  string `json:"device_id"` // 对应 pair 时上报的 device_id
	Token     string `json:"token"`     // pair 得到的长期 token
	Signature string `json:"signature"` // base64(ed25519_sign(priv, [token ":" nonce]))
	ClientVer string `json:"client_ver,omitempty"`
}

// AuthResultMessage 是 server 最终的裁决。
type AuthResultMessage struct {
	Type   string `json:"type"`             // MsgTypeAuthOK 或 MsgTypeAuthFail
	Reason string `json:"reason,omitempty"` // Fail 时的人话原因
}

// signedPayload 是我们实际签名的 byte 数组。
// 把 token 拼进去是为了避免"同一 nonce 在不同 token 下复用"之类的边角攻击。
// 分隔符用 '\x00' 保证 token 和 nonce 之间不可歧义。
func signedPayload(token, nonce string) []byte {
	out := make([]byte, 0, len(token)+1+len(nonce))
	out = append(out, token...)
	out = append(out, 0)
	out = append(out, nonce...)
	return out
}

// SignChallenge 用本机 identity 回答一个 ChallengeMessage,构造 AuthMessage。
//
// 失败条件:
//   - ch 的 type 不对
//   - ch 的 nonce 为空
//   - token 为空(说明没 pair 过)
func SignChallenge(id *identity.Identity, ch *ChallengeMessage, token, clientVer string) (*AuthMessage, error) {
	if ch == nil {
		return nil, errors.New("nil challenge")
	}
	if ch.Type != MsgTypeChallenge {
		return nil, fmt.Errorf("expected type=%q, got %q", MsgTypeChallenge, ch.Type)
	}
	if ch.Nonce == "" {
		return nil, errors.New("empty nonce")
	}
	if token == "" {
		return nil, errors.New("empty token (run `termind pair` first)")
	}

	sig := id.Sign(signedPayload(token, ch.Nonce))
	return &AuthMessage{
		Type:      MsgTypeAuth,
		DeviceID:  id.DeviceID(),
		Token:     token,
		Signature: base64.StdEncoding.EncodeToString(sig),
		ClientVer: clientVer,
	}, nil
}

// VerifyAuth 是 server 视角的验签(给 test / mock server 用,生产 server
// 自己实现一份就行)。CLI 端其实用不到,放这里是为了让 SignChallenge
// 有个可对称验证的参考实现,单测时闭环。
func VerifyAuth(pub ed25519.PublicKey, auth *AuthMessage, nonce string) error {
	if auth == nil {
		return errors.New("nil auth")
	}
	if auth.Type != MsgTypeAuth {
		return fmt.Errorf("expected type=%q, got %q", MsgTypeAuth, auth.Type)
	}
	sig, err := base64.StdEncoding.DecodeString(auth.Signature)
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}
	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("bad signature size: %d", len(sig))
	}
	if !ed25519.Verify(pub, signedPayload(auth.Token, nonce), sig) {
		return errors.New("signature mismatch")
	}
	return nil
}
