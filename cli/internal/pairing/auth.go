package pairing

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"termind/internal/identity"
)

// 这个文件实现 OpenClaw Gateway connect 阶段的 device identity 签名。
//
// 官方 Gateway Protocol 的 WebSocket 握手:
//  1. server 先推 event: connect.challenge, payload.nonce 是本次握手 nonce
//  2. client 发 req/connect,其中 auth 带 shared token / bootstrapToken / deviceToken,
//     device 带 id、raw publicKey(base64url)、signature、signedAt、nonce
//  3. server 用 v3 payload 验签,同时校验 token/bootstrap/deviceToken 及 role/scopes
//
// 签名 payload 必须和 OpenClaw buildDeviceAuthPayloadV3 保持字节级一致。

const (
	DefaultRole         = "operator"
	DefaultClientID     = "cli"
	DefaultClientMode   = "cli"
	DefaultDeviceFamily = "desktop"
	OperatorScopeRead   = "operator.read"
	OperatorScopeWrite  = "operator.write"
)

func DefaultScopes() []string {
	return []string{OperatorScopeRead, OperatorScopeWrite}
}

func HasScopes(scopes []string, required []string) bool {
	if len(required) == 0 {
		return true
	}
	normalized := NormalizeScopes(scopes)
	set := make(map[string]struct{}, len(normalized))
	for _, scope := range normalized {
		set[scope] = struct{}{}
	}
	for _, scope := range required {
		if _, ok := set[scope]; !ok {
			return false
		}
	}
	return true
}

func DefaultPlatform() string {
	switch runtime.GOOS {
	case "windows":
		return "win32"
	case "darwin":
		return "darwin"
	case "linux":
		return "linux"
	default:
		return runtime.GOOS
	}
}

// ConnectChallenge 是 server 在 WebSocket 建立后推送的第一帧。
type ConnectChallenge struct {
	Type    string `json:"type"`
	Event   string `json:"event"`
	Payload struct {
		Nonce string `json:"nonce"`
	} `json:"payload"`
}

// DeviceDescriptor 是 connect.params.device。
type DeviceDescriptor struct {
	ID        string `json:"id"`
	PublicKey string `json:"publicKey"`
	Signature string `json:"signature"`
	SignedAt  int64  `json:"signedAt"`
	Nonce     string `json:"nonce"`
}

// DeviceAuthInput 是 BuildDeviceDescriptor 的输入。
type DeviceAuthInput struct {
	Identity     *identity.Identity
	Token        string
	Nonce        string
	SignedAtMs   int64
	ClientID     string
	ClientMode   string
	Role         string
	Scopes       []string
	Platform     string
	DeviceFamily string
}

// BuildDeviceDescriptor 构造 OpenClaw connect.params.device。
func BuildDeviceDescriptor(in DeviceAuthInput) (*DeviceDescriptor, error) {
	if in.Identity == nil {
		return nil, errors.New("nil identity")
	}
	if strings.TrimSpace(in.Nonce) == "" {
		return nil, errors.New("empty connect nonce")
	}
	if in.SignedAtMs <= 0 {
		return nil, errors.New("signedAtMs must be positive")
	}

	payload := BuildDeviceAuthPayloadV3(in)
	sig := in.Identity.Sign([]byte(payload))
	return &DeviceDescriptor{
		ID:        in.Identity.DeviceID(),
		PublicKey: in.Identity.PublicKeyBase64URL(),
		Signature: base64.RawURLEncoding.EncodeToString(sig),
		SignedAt:  in.SignedAtMs,
		Nonce:     strings.TrimSpace(in.Nonce),
	}, nil
}

// BuildDeviceAuthPayloadV3 mirrors OpenClaw's buildDeviceAuthPayloadV3 exactly:
// ["v3", deviceId, clientId, clientMode, role, scopesCSV, signedAtMs, token,
//
//	nonce, normalizedPlatform, normalizedDeviceFamily].join("|")
func BuildDeviceAuthPayloadV3(in DeviceAuthInput) string {
	clientID := defaultString(in.ClientID, DefaultClientID)
	clientMode := defaultString(in.ClientMode, DefaultClientMode)
	role := defaultString(in.Role, DefaultRole)
	platform := normalizeDeviceMetadataForAuth(defaultString(in.Platform, DefaultPlatform()))
	deviceFamily := normalizeDeviceMetadataForAuth(in.DeviceFamily)
	scopes := strings.Join(NormalizeScopes(in.Scopes), ",")
	token := in.Token
	nonce := strings.TrimSpace(in.Nonce)

	deviceID := ""
	if in.Identity != nil {
		deviceID = in.Identity.DeviceID()
	}

	return strings.Join([]string{
		"v3",
		deviceID,
		clientID,
		clientMode,
		role,
		scopes,
		fmt.Sprintf("%d", in.SignedAtMs),
		token,
		nonce,
		platform,
		deviceFamily,
	}, "|")
}

// VerifyDeviceDescriptor 是 server/mock 视角的验签参考实现。
func VerifyDeviceDescriptor(pub ed25519.PublicKey, device *DeviceDescriptor, in DeviceAuthInput) error {
	if device == nil {
		return errors.New("nil device")
	}
	sig, err := base64.RawURLEncoding.DecodeString(device.Signature)
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}
	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("bad signature size: %d", len(sig))
	}
	in.SignedAtMs = device.SignedAt
	in.Nonce = device.Nonce
	payload := BuildDeviceAuthPayloadV3(in)
	if !ed25519.Verify(pub, []byte(payload), sig) {
		return errors.New("signature mismatch")
	}
	return nil
}

// NormalizeScopes follows OpenClaw shared/device-auth.ts.
func NormalizeScopes(scopes []string) []string {
	if len(scopes) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(scopes)+2)
	for _, scope := range scopes {
		trimmed := strings.TrimSpace(scope)
		if trimmed != "" {
			set[trimmed] = struct{}{}
		}
	}
	if _, ok := set["operator.admin"]; ok {
		set["operator.read"] = struct{}{}
		set["operator.write"] = struct{}{}
	} else if _, ok := set["operator.write"]; ok {
		set["operator.read"] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for scope := range set {
		out = append(out, scope)
	}
	sortStrings(out)
	return out
}

func defaultString(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func normalizeDeviceMetadataForAuth(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(trimmed))
	for i := 0; i < len(trimmed); i++ {
		ch := trimmed[i]
		if ch >= 'A' && ch <= 'Z' {
			ch += 'a' - 'A'
		}
		b.WriteByte(ch)
	}
	return b.String()
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		v := values[i]
		j := i - 1
		for ; j >= 0 && values[j] > v; j-- {
			values[j+1] = values[j]
		}
		values[j+1] = v
	}
}
