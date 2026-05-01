package identity

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 用一个临时 HOME 隔离 LoadOrCreate 真实路径。
func withTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	return dir
}

func TestLoadOrCreate_CreatesThenLoads(t *testing.T) {
	home := withTempHome(t)

	id1, err := LoadOrCreate()
	if err != nil {
		t.Fatalf("first LoadOrCreate: %v", err)
	}

	// 文件应落盘在 ~/.config/termind/keys/
	keyPath := filepath.Join(home, ".config", "termind", "keys", "device.key")
	pubPath := filepath.Join(home, ".config", "termind", "keys", "device.pub")
	for _, p := range []string{keyPath, pubPath} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected %s to exist: %v", p, err)
		}
	}

	// 私钥权限必须 0600
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("private key perm = %v, want 0600", mode)
	}

	// 第二次 LoadOrCreate 应拿到同一把钥匙
	id2, err := LoadOrCreate()
	if err != nil {
		t.Fatalf("second LoadOrCreate: %v", err)
	}
	if id1.DeviceID() != id2.DeviceID() {
		t.Fatalf("device ID changed: %s vs %s", id1.DeviceID(), id2.DeviceID())
	}
}

func TestSignVerify(t *testing.T) {
	withTempHome(t)
	id, err := LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}

	msg := []byte("hello-challenge-nonce")
	sig := id.Sign(msg)
	if len(sig) != ed25519.SignatureSize {
		t.Fatalf("sig size = %d, want %d", len(sig), ed25519.SignatureSize)
	}
	if !ed25519.Verify(id.PublicKey(), msg, sig) {
		t.Fatal("signature should verify with own public key")
	}
	// 篡改 msg 验签应失败
	if ed25519.Verify(id.PublicKey(), []byte("tampered"), sig) {
		t.Fatal("signature should NOT verify on tampered message")
	}
}

func TestFingerprintFormat(t *testing.T) {
	withTempHome(t)
	id, err := LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	fp := id.Fingerprint()
	if !strings.HasPrefix(fp, "SHA256:") {
		t.Fatalf("fingerprint should start with SHA256: got %q", fp)
	}
	// SHA256:XXXX-XXXX-XXXX  => 总长 7 + 4*3 + 2 = 21
	if len(fp) != 21 {
		t.Fatalf("fingerprint len = %d, want 21, got %q", len(fp), fp)
	}
}

func TestDeviceIDStable(t *testing.T) {
	withTempHome(t)
	id, err := LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	did := id.DeviceID()
	if len(did) != 32 {
		t.Fatalf("device id len = %d, want 32 (hex of 16 bytes)", len(did))
	}
	// 同 identity 两次调用必须稳定
	if did != id.DeviceID() {
		t.Fatal("DeviceID unstable across calls")
	}
}

func TestLoadOrCreate_CorruptedKey(t *testing.T) {
	home := withTempHome(t)
	// 先正常建一对
	if _, err := LoadOrCreate(); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(home, ".config", "termind", "keys", "device.key")
	// 把私钥文件写成垃圾
	if err := os.WriteFile(keyPath, []byte("not a pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 再 load 应该报错,不能悄悄重建
	_, err := LoadOrCreate()
	if err == nil {
		t.Fatal("expected error when private key is corrupted, got nil")
	}
}
