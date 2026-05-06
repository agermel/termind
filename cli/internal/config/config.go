// Package config 管理 termind 的全局配置文件: ~/.config/termind/config.json。
//
// 当前字段很少,只存"已批准的 server URL / role"和"最近批准时间"。
// 随着 M4/M5 推进可以继续加字段(日志级别、默认超时等)。
//
// 之所以用 JSON 而不是 TOML:
//   - 标准库自带,0 依赖
//   - 文件小,人眼可读也够用
//   - 需要更复杂配置时再换
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Config 是本机持久化的配置。
//
// 注意: token 和 keys 不在这里,分别落 ~/.config/termind/token 和
// ~/.config/termind/keys/;一来它们的权限敏感度不同,二来单独存便于
// 吊销(删 token 即可)。
type Config struct {
	// ServerURL 是最近一次 device approval 成功的 OpenClaw server。shell 启动时自动连这个。
	ServerURL string `json:"server_url,omitempty"`
	// Role 是最近一次 device approval 成功的 OpenClaw device role。termind 默认作为 node。
	Role string `json:"role,omitempty"`
	// PairedAt 记录设备批准成功的时间,仅展示用。
	PairedAt time.Time  `json:"paired_at,omitempty"`
	Lark     LarkConfig `json:"lark,omitempty"`
}

type LarkConfig struct {
	UserOpenID string               `json:"user_open_id,omitempty"`
	Sender     string               `json:"sender,omitempty"`
	Targets    []LarkTarget         `json:"targets,omitempty"`
	Forwarding LarkForwardingConfig `json:"forwarding,omitempty"`
}

type LarkTarget struct {
	Type    string `json:"type,omitempty"`
	ID      string `json:"id,omitempty"`
	Label   string `json:"label,omitempty"`
	Enabled bool   `json:"enabled"`
}

type LarkForwardingConfig struct {
	Version    int                               `json:"version,omitempty"`
	Identities map[string]LarkForwardingIdentity `json:"identities,omitempty"`
	Routes     []LarkForwardingRoute             `json:"routes,omitempty"`
}

type LarkForwardingIdentity struct {
	ID               string `json:"id,omitempty"`
	Kind             string `json:"kind,omitempty"`
	Label            string `json:"label,omitempty"`
	AppID            string `json:"appId,omitempty"`
	UserOpenID       string `json:"userOpenId,omitempty"`
	Profile          string `json:"profile,omitempty"`
	LarkCLIConfigDir string `json:"larkCliConfigDir,omitempty"`
	Source           string `json:"source,omitempty"`
	Slot             string `json:"slot,omitempty"`
	Enabled          bool   `json:"enabled"`
}

type LarkForwardingRoute struct {
	IdentityID string     `json:"identityId,omitempty"`
	Target     LarkTarget `json:"target,omitempty"`
	Enabled    bool       `json:"enabled"`
}

// Load 从默认路径加载;文件不存在时返回空 Config 和 nil error。
//
// 这个"不存在不算错"的设计是为了让 `termind status` 在从未配对过的
// 设备上也能跑,只是各字段为空。
func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("decode config %s: %w", path, err)
	}
	return &c, nil
}

// Save 原子写入配置(temp + rename),0600 权限。
func Save(c *Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Path 返回 config.json 的绝对路径。暴露给 status/doctor 展示用。
func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "termind", "config.json"), nil
}

// Dir 返回 ~/.config/termind,给其他子包统一用。
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "termind"), nil
}
