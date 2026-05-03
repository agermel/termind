package pairing

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DeviceAuth 是 OpenClaw hello-ok.auth 中和 deviceToken 相关的持久状态。
type DeviceAuth struct {
	Token     string   `json:"token"`
	Role      string   `json:"role"`
	Scopes    []string `json:"scopes"`
	UpdatedAt string   `json:"updated_at"`
}

type deviceAuthFile struct {
	Version  int                   `json:"version"`
	DeviceID string                `json:"device_id,omitempty"`
	Tokens   map[string]DeviceAuth `json:"tokens"`
}

// SaveToken 把 OpenClaw 颁发的 deviceToken 写到 ~/.config/termind/token,权限 0600。
//
// 简单明了: 现阶段只有一个 server,一个 token。后续如果要支持多 server,
// 再改成 {server_id -> token} 的 map。
func SaveToken(token string) (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	path := filepath.Join(dir, "token")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(token), 0o600); err != nil {
		return "", fmt.Errorf("write token: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", fmt.Errorf("rename token: %w", err)
	}
	return path, nil
}

// SaveDeviceAuth 保存 OpenClaw hello-ok.auth.deviceToken 及其 role/scopes。
// 同时写 legacy token 文件,兼容现有 status 和旧版本配置。
func SaveDeviceAuth(deviceID, role, token string, scopes []string) (string, error) {
	role = strings.TrimSpace(role)
	if role == "" {
		role = DefaultRole
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return "", errors.New("empty device token")
	}
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	path := filepath.Join(dir, "device-auth.json")
	entry := DeviceAuth{
		Token:     token,
		Role:      role,
		Scopes:    normalizeScopesForStore(scopes),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	store := deviceAuthFile{
		Version:  1,
		DeviceID: deviceID,
		Tokens:   map[string]DeviceAuth{role: entry},
	}
	if existing, err := readDeviceAuthFile(path); err == nil && existing != nil && existing.DeviceID == deviceID {
		store.Tokens = existing.Tokens
		if store.Tokens == nil {
			store.Tokens = make(map[string]DeviceAuth)
		}
		store.Tokens[role] = entry
	}
	b, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return "", err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return "", fmt.Errorf("write device auth: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", fmt.Errorf("rename device auth: %w", err)
	}
	if _, err := SaveToken(token); err != nil {
		return "", err
	}
	return path, nil
}

// LoadDeviceAuth 读回指定 role 的 device auth。不存在时返回 nil,nil。
func LoadDeviceAuth(deviceID, role string) (*DeviceAuth, error) {
	role = strings.TrimSpace(role)
	if role == "" {
		role = DefaultRole
	}
	dir, err := configDir()
	if err != nil {
		return nil, err
	}
	store, err := readDeviceAuthFile(filepath.Join(dir, "device-auth.json"))
	if err != nil {
		return nil, err
	}
	if store == nil || (deviceID != "" && store.DeviceID != "" && store.DeviceID != deviceID) {
		return nil, nil
	}
	entry, ok := store.Tokens[role]
	if !ok || strings.TrimSpace(entry.Token) == "" {
		return nil, nil
	}
	entry.Scopes = normalizeScopesForStore(entry.Scopes)
	return &entry, nil
}

// LoadToken 读回之前 SaveToken 存的 token;不存在时返回 ""(不是错误)。
func LoadToken() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "token")
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(bytes.TrimSpace(b)), nil
}

func readDeviceAuthFile(path string) (*deviceAuthFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var store deviceAuthFile
	if err := json.Unmarshal(b, &store); err != nil {
		return nil, fmt.Errorf("decode device auth: %w", err)
	}
	if store.Version != 1 {
		return nil, fmt.Errorf("unsupported device auth version: %d", store.Version)
	}
	if store.Tokens == nil {
		store.Tokens = make(map[string]DeviceAuth)
	}
	return &store, nil
}

func normalizeScopesForStore(scopes []string) []string {
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
	sort.Strings(out)
	return out
}

func configDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "termind"), nil
}
