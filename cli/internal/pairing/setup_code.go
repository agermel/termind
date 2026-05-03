package pairing

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// SetupCode 是 OpenClaw setup code 解出的最小配对信息。
// 官方 setup code 是 base64url(JSON),JSON 中包含 url 和 bootstrapToken。
type SetupCode struct {
	URL            string `json:"url"`
	BootstrapToken string `json:"bootstrapToken"`
}

// ParseSetupCode 解析 OpenClaw CLI / plugin 生成的 setup code。
// setup code 是敏感短期凭证,调用方不要记录原文。
func ParseSetupCode(code string) (*SetupCode, error) {
	value := strings.TrimSpace(code)
	if value == "" {
		return nil, errors.New("setup code is required")
	}

	raw, err := decodeSetupCodePayload(value)
	if err != nil {
		return nil, err
	}
	var setup SetupCode
	if err := json.Unmarshal(raw, &setup); err != nil {
		return nil, fmt.Errorf("decode setup code json: %w", err)
	}
	setup.URL = strings.TrimSpace(setup.URL)
	setup.BootstrapToken = strings.TrimSpace(setup.BootstrapToken)
	if setup.URL == "" {
		return nil, errors.New("setup code missing url")
	}
	if setup.BootstrapToken == "" {
		return nil, errors.New("setup code missing bootstrapToken")
	}
	return &setup, nil
}

func decodeSetupCodePayload(value string) ([]byte, error) {
	decoders := []*base64.Encoding{
		base64.RawURLEncoding,
		base64.URLEncoding,
		base64.RawStdEncoding,
		base64.StdEncoding,
	}
	var lastErr error
	for _, enc := range decoders {
		raw, err := enc.DecodeString(value)
		if err == nil {
			return raw, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("decode setup code: %w", lastErr)
}
