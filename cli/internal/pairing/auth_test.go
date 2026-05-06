package pairing

import (
	"reflect"
	"testing"

	"termind/internal/identity"
)

func TestDefaultScopesMatchQRBootstrapAllowlist(t *testing.T) {
	if got, want := DefaultScopes(), []string{"operator.read", "operator.write"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("DefaultScopes=%v want %v", got, want)
	}
}

func TestBuildDeviceDescriptorV3Shape(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	id, err := identity.LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	in := DeviceAuthInput{
		Identity:     id,
		Token:        "my-token",
		Nonce:        "random-nonce-base64",
		SignedAtMs:   1234567890,
		ClientID:     "gateway-client",
		ClientMode:   "backend",
		Role:         "operator",
		Scopes:       []string{"operator.write"},
		Platform:     "Darwin",
		DeviceFamily: "Desktop",
	}
	dev, err := BuildDeviceDescriptor(in)
	if err != nil {
		t.Fatalf("BuildDeviceDescriptor: %v", err)
	}
	if dev.ID != id.DeviceID() {
		t.Fatalf("device id mismatch: %s vs %s", dev.ID, id.DeviceID())
	}
	if dev.PublicKey != id.PublicKeyBase64URL() {
		t.Fatalf("public key mismatch")
	}
	if dev.Signature == "" {
		t.Fatal("signature should be set")
	}
	if err := VerifyDeviceDescriptor(id.PublicKey(), dev, in); err != nil {
		t.Fatalf("VerifyDeviceDescriptor: %v", err)
	}
}

func TestBuildDeviceAuthPayloadV3_MatchesOpenClawShape(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	id, err := identity.LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	got := BuildDeviceAuthPayloadV3(DeviceAuthInput{
		Identity:     id,
		Token:        "tok",
		Nonce:        "nonce",
		SignedAtMs:   42,
		ClientID:     "gateway-client",
		ClientMode:   "backend",
		Role:         "operator",
		Scopes:       []string{"operator.write"},
		Platform:     "Darwin",
		DeviceFamily: "Desktop",
	})
	want := "v3|" + id.DeviceID() + "|gateway-client|backend|operator|operator.read,operator.write|42|tok|nonce|darwin|desktop"
	if got != want {
		t.Fatalf("payload mismatch:\ngot  %q\nwant %q", got, want)
	}
}

func TestBuildDeviceAuthPayloadV3_DefaultsToOpenClawOperator(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	id, err := identity.LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	got := BuildDeviceAuthPayloadV3(DeviceAuthInput{
		Identity:   id,
		Token:      "tok",
		Nonce:      "nonce",
		SignedAtMs: 42,
	})
	want := "v3|" + id.DeviceID() + "|cli|cli|operator||42|tok|nonce|" + DefaultPlatform() + "|"
	if got != want {
		t.Fatalf("payload mismatch:\ngot  %q\nwant %q", got, want)
	}
}

func TestBuildDeviceDescriptor_MissingNonce(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	id, _ := identity.LoadOrCreate()
	if _, err := BuildDeviceDescriptor(DeviceAuthInput{
		Identity:   id,
		Token:      "tok",
		SignedAtMs: 1,
	}); err == nil {
		t.Fatal("expected error on empty nonce")
	}
}

func TestVerifyDeviceDescriptor_TamperedNonce(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	id, _ := identity.LoadOrCreate()
	in := DeviceAuthInput{
		Identity:   id,
		Token:      "tok",
		Nonce:      "nonce-A",
		SignedAtMs: 1,
	}
	dev, err := BuildDeviceDescriptor(in)
	if err != nil {
		t.Fatal(err)
	}
	dev.Nonce = "nonce-B"
	if err := VerifyDeviceDescriptor(id.PublicKey(), dev, in); err == nil {
		t.Fatal("VerifyDeviceDescriptor should fail on nonce mismatch")
	}
}

func TestVerifyDeviceDescriptor_TamperedToken(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	id, _ := identity.LoadOrCreate()
	in := DeviceAuthInput{
		Identity:   id,
		Token:      "tok-real",
		Nonce:      "n",
		SignedAtMs: 1,
	}
	dev, err := BuildDeviceDescriptor(in)
	if err != nil {
		t.Fatal(err)
	}
	in.Token = "tok-fake"
	if err := VerifyDeviceDescriptor(id.PublicKey(), dev, in); err == nil {
		t.Fatal("VerifyDeviceDescriptor should fail when token changed after sign")
	}
}
