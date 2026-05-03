package pairing

import (
	"encoding/base64"
	"testing"
)

func TestParseSetupCode(t *testing.T) {
	payload := `{"url":"ws://127.0.0.1:18789/v1/gateway","bootstrapToken":"boot"}`
	code := base64.RawURLEncoding.EncodeToString([]byte(payload))

	setup, err := ParseSetupCode(code)
	if err != nil {
		t.Fatalf("ParseSetupCode: %v", err)
	}
	if setup.URL != "ws://127.0.0.1:18789/v1/gateway" {
		t.Fatalf("url=%q", setup.URL)
	}
	if setup.BootstrapToken != "boot" {
		t.Fatalf("bootstrapToken=%q", setup.BootstrapToken)
	}
}

func TestParseSetupCode_RejectsMissingFields(t *testing.T) {
	code := base64.RawURLEncoding.EncodeToString([]byte(`{"url":"ws://127.0.0.1:18789/v1/gateway"}`))

	if _, err := ParseSetupCode(code); err == nil {
		t.Fatal("expected missing bootstrapToken error")
	}
}

func TestParseSetupCode_RejectsInvalidBase64(t *testing.T) {
	if _, err := ParseSetupCode("not a setup code"); err == nil {
		t.Fatal("expected invalid setup code error")
	}
}
