package pairing

import (
	"testing"

	"termind/internal/identity"
)

func TestSignChallenge_RoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	id, err := identity.LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}

	ch := &ChallengeMessage{Type: MsgTypeChallenge, Nonce: "random-nonce-base64", Realm: "openclaw-test"}
	auth, err := SignChallenge(id, ch, "my-token", "0.0.1-test")
	if err != nil {
		t.Fatalf("SignChallenge: %v", err)
	}
	if auth.Type != MsgTypeAuth {
		t.Fatalf("type=%s", auth.Type)
	}
	if auth.DeviceID != id.DeviceID() {
		t.Fatalf("device id mismatch: %s vs %s", auth.DeviceID, id.DeviceID())
	}
	if auth.Token != "my-token" {
		t.Fatalf("token wrong")
	}
	// 自洽验签
	if err := VerifyAuth(id.PublicKey(), auth, ch.Nonce); err != nil {
		t.Fatalf("VerifyAuth: %v", err)
	}
}

func TestSignChallenge_MissingToken(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	id, _ := identity.LoadOrCreate()
	ch := &ChallengeMessage{Type: MsgTypeChallenge, Nonce: "n"}
	if _, err := SignChallenge(id, ch, "", "test"); err == nil {
		t.Fatal("expected error on empty token")
	}
}

func TestSignChallenge_BadType(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	id, _ := identity.LoadOrCreate()
	ch := &ChallengeMessage{Type: "nope", Nonce: "n"}
	if _, err := SignChallenge(id, ch, "tok", "test"); err == nil {
		t.Fatal("expected error on bad type")
	}
}

func TestVerifyAuth_TamperedNonce(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	id, _ := identity.LoadOrCreate()
	ch := &ChallengeMessage{Type: MsgTypeChallenge, Nonce: "nonce-A"}
	auth, err := SignChallenge(id, ch, "tok", "test")
	if err != nil {
		t.Fatal(err)
	}
	// server 试图用错误 nonce 验签,必须失败
	if err := VerifyAuth(id.PublicKey(), auth, "nonce-B"); err == nil {
		t.Fatal("VerifyAuth should fail on nonce mismatch")
	}
}

func TestVerifyAuth_TamperedToken(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	id, _ := identity.LoadOrCreate()
	ch := &ChallengeMessage{Type: MsgTypeChallenge, Nonce: "n"}
	auth, _ := SignChallenge(id, ch, "tok-real", "test")
	// 攻击者改 token 但保留 signature: 验签必须失败
	auth.Token = "tok-fake"
	if err := VerifyAuth(id.PublicKey(), auth, "n"); err == nil {
		t.Fatal("VerifyAuth should fail when token changed after sign")
	}
}
