package pairing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"termind/internal/identity"
)

// Client 是 termind 跟 OpenClaw 对话的 HTTP 配对客户端。
// 只负责 /v1/pair/start 和 /v1/pair/poll 两步。
type Client struct {
	ServerURL  string       // 例如 "https://openclaw.example.com" (无尾斜杠)
	ID         *identity.Identity
	HTTP       *http.Client // nil 时用默认 30s 超时
	ClientVer  string       // termind 版本号
	Hostname   string       // 不传默认 os.Hostname()
}

// NewClient 构造一个带合理默认值的 Client。
func NewClient(serverURL string, id *identity.Identity, clientVer string) *Client {
	host, _ := os.Hostname()
	return &Client{
		ServerURL: normalizeServer(serverURL),
		ID:        id,
		HTTP:      &http.Client{Timeout: 30 * time.Second},
		ClientVer: clientVer,
		Hostname:  host,
	}
}

// Start 发起一次配对,返回 pair_code 和 challenge_id。
func (c *Client) Start(ctx context.Context) (*StartResponse, error) {
	body := StartRequest{
		DeviceID:      c.ID.DeviceID(),
		PublicKey:     string(c.ID.PublicKeyPEM()),
		Hostname:      c.Hostname,
		ClientVersion: c.ClientVer,
	}
	var resp StartResponse
	if err := c.postJSON(ctx, "/v1/pair/start", body, &resp); err != nil {
		return nil, fmt.Errorf("pair start: %w", err)
	}
	if resp.ChallengeID == "" || resp.PairCode == "" {
		return nil, errors.New("pair start: server returned empty pair_code or challenge_id")
	}
	return &resp, nil
}

// Poll 做一次轮询查询。返回的 status 非 pending 时,调用方应当停止轮询。
func (c *Client) Poll(ctx context.Context, challengeID string) (*PollResponse, error) {
	q := url.Values{}
	q.Set("challenge_id", challengeID)

	u := c.ServerURL + "/v1/pair/poll?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	httpResp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pair poll: %w", err)
	}
	defer httpResp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(httpResp.Body, 1<<20)) // 1 MiB 封顶
	if err != nil {
		return nil, fmt.Errorf("pair poll read: %w", err)
	}
	if httpResp.StatusCode >= 400 {
		return nil, decodeErr(httpResp.StatusCode, body)
	}

	var pr PollResponse
	if err := json.Unmarshal(body, &pr); err != nil {
		return nil, fmt.Errorf("pair poll decode: %w (body=%s)", err, truncate(string(body), 200))
	}
	if pr.Status == "" {
		return nil, errors.New("pair poll: server returned empty status")
	}
	return &pr, nil
}

// WaitApproval 是高层封装:反复 Poll 直到 approved/denied/expired 或 ctx done。
// interval<=0 时默认 2 秒。
func (c *Client) WaitApproval(ctx context.Context, challengeID string, interval time.Duration) (*PollResponse, error) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()

	// 立即查一次,别让用户干等第一个 tick
	if pr, err := c.Poll(ctx, challengeID); err != nil {
		return nil, err
	} else if pr.Status != StatusPending {
		return pr, nil
	}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-tick.C:
			pr, err := c.Poll(ctx, challengeID)
			if err != nil {
				return nil, err
			}
			if pr.Status != StatusPending {
				return pr, nil
			}
		}
	}
}

// SaveToken 把 OpenClaw 颁发的 token 写到 ~/.config/termind/token,权限 0600。
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

// ---------- 内部 ----------

func (c *Client) postJSON(ctx context.Context, path string, body, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.ServerURL+path, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return decodeErr(resp.StatusCode, raw)
	}
	return json.Unmarshal(raw, out)
}

func decodeErr(status int, body []byte) error {
	var er ErrorResponse
	if len(body) > 0 && json.Unmarshal(body, &er) == nil && er.Error != "" {
		return fmt.Errorf("server %d: %s — %s", status, er.Error, er.Message)
	}
	return fmt.Errorf("server %d: %s", status, truncate(string(body), 200))
}

func normalizeServer(s string) string {
	if len(s) > 0 && s[len(s)-1] == '/' {
		return s[:len(s)-1]
	}
	return s
}

func configDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "termind"), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
