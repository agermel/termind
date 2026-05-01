// Package identity 管理本机 termind 设备的长期身份:一对 ed25519 密钥。
//
// 密钥文件:
//
//	~/.config/termind/keys/device.key   私钥,PEM 编码,权限 0600
//	~/.config/termind/keys/device.pub   公钥,PEM 编码,权限 0644
//
// 私钥绝不离开本机。公钥在 pair 阶段上报给 OpenClaw,并作为 device_id
// 的衍生来源(device_id = sha256(pubkey)[:16] 的 hex)。
//
// 典型用法:
//
//	id, err := identity.LoadOrCreate()   // 首次会自动创建
//	sig := id.Sign(nonce)                // ws 握手时签 challenge
//	fp := id.Fingerprint()               // pair 时给用户看,便于比对
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
)

// Identity 是一台设备的长期身份。私钥留在内存,外部只读。
type Identity struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

// LoadOrCreate 从标准路径加载密钥对;如果不存在,自动生成并落盘。
//
// 并发安全性: 首次启动的竞争可能导致两个 goroutine 都尝试创建,
// 目前 CLI 场景 single-process,不作保护。
func LoadOrCreate() (*Identity, error) {
	dir, err := keysDir()
	if err != nil {
		return nil, err
	}
	keyPath := filepath.Join(dir, "device.key")
	pubPath := filepath.Join(dir, "device.pub")

	// 1. 尝试加载
	if id, err := load(keyPath, pubPath); err == nil {
		return id, nil
	} else if !os.IsNotExist(err) {
		// 文件存在但读失败(权限/损坏),直接往上报,不要悄悄覆盖
		return nil, fmt.Errorf("read existing identity: %w", err)
	}

	// 2. 不存在: 创建目录 + 生成 + 落盘
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ed25519: %w", err)
	}
	if err := writeKey(keyPath, priv); err != nil {
		return nil, err
	}
	if err := writePub(pubPath, pub); err != nil {
		return nil, err
	}
	return &Identity{priv: priv, pub: pub}, nil
}

// Sign 用私钥对任意字节签名,返回 64 字节 ed25519 签名。
func (id *Identity) Sign(msg []byte) []byte {
	return ed25519.Sign(id.priv, msg)
}

// PublicKey 返回 ed25519 公钥的原始 32 字节。
func (id *Identity) PublicKey() ed25519.PublicKey {
	// 复制,避免外部篡改
	out := make(ed25519.PublicKey, len(id.pub))
	copy(out, id.pub)
	return out
}

// PublicKeyPEM 返回公钥的 PEM 编码,给 pair 阶段 POST 给 server 用。
func (id *Identity) PublicKeyPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "ED25519 PUBLIC KEY", Bytes: id.pub})
}

// DeviceID 是公钥的稳定短标识(sha256 前 16 字节 hex,共 32 字符)。
// 用作 OpenClaw 侧的设备 ID 和本地日志前缀。
func (id *Identity) DeviceID() string {
	h := sha256.Sum256(id.pub)
	return hex.EncodeToString(h[:16])
}

// Fingerprint 给人看的短指纹: SHA256:XXXX-XXXX-XXXX(前 12 字符 base32 友好分组)。
// pair 时打印在屏幕上,让操作员和 OpenClaw 后台显示的一致。
func (id *Identity) Fingerprint() string {
	h := sha256.Sum256(id.pub)
	hx := hex.EncodeToString(h[:6]) // 12 字符
	return fmt.Sprintf("SHA256:%s-%s-%s", hx[0:4], hx[4:8], hx[8:12])
}

// ---------- 内部 ----------

func keysDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, ".config", "termind", "keys"), nil
}

func load(keyPath, pubPath string) (*Identity, error) {
	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	pubBytes, err := os.ReadFile(pubPath)
	if err != nil {
		return nil, err
	}

	kb, _ := pem.Decode(keyBytes)
	if kb == nil || kb.Type != "ED25519 PRIVATE KEY" {
		return nil, fmt.Errorf("invalid private key PEM at %s", keyPath)
	}
	if len(kb.Bytes) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid private key size at %s: got %d, want %d", keyPath, len(kb.Bytes), ed25519.PrivateKeySize)
	}

	pb, _ := pem.Decode(pubBytes)
	if pb == nil || pb.Type != "ED25519 PUBLIC KEY" {
		return nil, fmt.Errorf("invalid public key PEM at %s", pubPath)
	}
	if len(pb.Bytes) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid public key size at %s: got %d, want %d", pubPath, len(pb.Bytes), ed25519.PublicKeySize)
	}

	return &Identity{
		priv: ed25519.PrivateKey(kb.Bytes),
		pub:  ed25519.PublicKey(pb.Bytes),
	}, nil
}

func writeKey(path string, priv ed25519.PrivateKey) error {
	block := &pem.Block{Type: "ED25519 PRIVATE KEY", Bytes: priv}
	// 写临时文件再 rename,避免半写状态
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, pem.EncodeToMemory(block), 0o600); err != nil {
		return fmt.Errorf("write private key: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename private key: %w", err)
	}
	return nil
}

func writePub(path string, pub ed25519.PublicKey) error {
	block := &pem.Block{Type: "ED25519 PUBLIC KEY", Bytes: pub}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, pem.EncodeToMemory(block), 0o644); err != nil {
		return fmt.Errorf("write public key: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename public key: %w", err)
	}
	return nil
}
