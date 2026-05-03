package diagnose

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"termind/internal/gateway"
)

const (
	agentWaitTimeout = 60 * time.Second
)

// Client 把 OpenClaw Gateway 的 operator 方法封装成一次 shell 诊断。
//
// OpenClaw 的 operator method 权限模型会把未知方法默认视为 operator.admin。
// termind 使用 setup-code handoff 后拿到的是 operator.read/write,所以这里走
// 官方白名单里的 agent -> agent.wait -> sessions.get。
type Client struct {
	conn *gateway.Conn
}

// NewClient 构造 Client。
func NewClient(conn *gateway.Conn) *Client {
	return &Client{conn: conn}
}

// Start 发起一次诊断,返回一个事件 channel。
//
// 为了保持 shell 层稳定,这里仍返回 channel。当前实现会在后台完成一次
// agent run,然后把最后一条 assistant 文本作为单个 Delta 推给渲染器。
func (c *Client) Start(ctx context.Context, req *Request) (<-chan TokenEvent, error) {
	if c.conn == nil {
		return nil, errors.New("diagnose: nil gateway conn")
	}

	out := make(chan TokenEvent, 1)
	go c.run(ctx, req, out)
	return out, nil
}

// Alert 把失败事件交给 OpenClaw 的 termind-lark-alert skill 处理。
//
// 这里不等待 agent 完成,也不在 Termind 本地发送飞书消息。Termind 只负责把
// 结构化失败事件投递给 OpenClaw,后续卡片构建/飞书发送由 OpenClaw 编排。
func (c *Client) Alert(ctx context.Context, req *Request) error {
	if c.conn == nil {
		return errors.New("diagnose: nil gateway conn")
	}
	runID := "termind-alert-" + randomHex(16)
	agentReq := agentRequest{
		Message:        buildAlertPrompt(req),
		SessionKey:     alertSessionKey,
		Deliver:        false,
		Thinking:       "low",
		IDempotencyKey: runID,
		Label:          alertLabel,
	}
	var accepted agentResponse
	if err := c.conn.Call(ctx, MethodAgent, agentReq, &accepted); err != nil {
		return fmt.Errorf("agent: %w", err)
	}
	return nil
}

func (c *Client) run(ctx context.Context, req *Request, out chan<- TokenEvent) {
	defer close(out)

	runID := "termind-" + randomHex(16)
	sessionKey := defaultSessionKey
	startedAt := time.Now()
	agentReq := agentRequest{
		Message:        buildPrompt(req),
		SessionKey:     sessionKey,
		Deliver:        false,
		Thinking:       "low",
		IDempotencyKey: runID,
		Label:          defaultLabel,
	}

	var accepted agentResponse
	if err := c.conn.Call(ctx, MethodAgent, agentReq, &accepted); err != nil {
		sendError(ctx, out, fmt.Sprintf("agent: %v", err))
		return
	}
	if accepted.RunID == "" {
		accepted.RunID = runID
	}

	waitCtx, cancel := context.WithTimeout(ctx, agentWaitTimeout)
	defer cancel()
	var waited agentWaitResponse
	if err := c.conn.Call(waitCtx, MethodAgentWait, agentWaitRequest{
		RunID:     accepted.RunID,
		TimeoutMs: int(agentWaitTimeout / time.Millisecond),
	}, &waited); err != nil {
		sendError(ctx, out, fmt.Sprintf("agent.wait: %v", err))
		return
	}
	switch waited.Status {
	case "ok":
	case "timeout":
		sendError(ctx, out, "agent.wait: OpenClaw 诊断超时")
		return
	case "error":
		if waited.Error != "" {
			sendError(ctx, out, "agent.wait: "+waited.Error)
		} else {
			sendError(ctx, out, "agent.wait: OpenClaw 诊断失败")
		}
		return
	default:
		sendError(ctx, out, "agent.wait: unexpected status "+waited.Status)
		return
	}

	var sess sessionsGetResponse
	if err := c.conn.Call(ctx, MethodSessionsGet, sessionsGetRequest{
		Key:   sessionKey,
		Limit: 8,
	}, &sess); err != nil {
		sendError(ctx, out, fmt.Sprintf("sessions.get: %v", err))
		return
	}
	answer := lastAssistantText(sess.Messages, startedAt)
	if answer == "" {
		sendError(ctx, out, "OpenClaw 诊断完成,但没有返回可显示的回复")
		return
	}
	answer = cleanTerminalText(answer)

	select {
	case out <- TokenEvent{Delta: answer, Done: true}:
	case <-ctx.Done():
	}
}

type agentRequest struct {
	Message        string `json:"message"`
	SessionKey     string `json:"sessionKey"`
	Deliver        bool   `json:"deliver"`
	Thinking       string `json:"thinking,omitempty"`
	IDempotencyKey string `json:"idempotencyKey"`
	Label          string `json:"label,omitempty"`
}

type agentResponse struct {
	RunID  string `json:"runId"`
	Status string `json:"status,omitempty"`
}

type agentWaitRequest struct {
	RunID     string `json:"runId"`
	TimeoutMs int    `json:"timeoutMs,omitempty"`
}

type agentWaitResponse struct {
	RunID  string `json:"runId"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type sessionsGetRequest struct {
	Key   string `json:"key"`
	Limit int    `json:"limit,omitempty"`
}

type sessionsGetResponse struct {
	Messages []sessionMessage `json:"messages"`
}

type sessionMessage struct {
	Role      string          `json:"role"`
	Text      string          `json:"text"`
	Content   json.RawMessage `json:"content"`
	Timestamp json.RawMessage `json:"timestamp"`
	CreatedAt json.RawMessage `json:"createdAt"`
}

func buildPrompt(req *Request) string {
	var b strings.Builder
	b.WriteString("你是 termind 的 shell 错误诊断助手。")
	b.WriteString("用简体中文输出终端友好的纯文本,不要 Markdown/代码围栏/粗体/表格。")
	b.WriteString("最多 3 行: 原因一行,下一步一行,必要提醒一行。每行尽量短。\n\n")
	if req.Command != "" {
		fmt.Fprintf(&b, "命令:\n```sh\n%s\n```\n\n", req.Command)
	}
	fmt.Fprintf(&b, "退出码: %d\n", req.ExitCode)
	if req.Shell != "" {
		fmt.Fprintf(&b, "Shell: %s\n", req.Shell)
	}
	if req.Cwd != "" {
		fmt.Fprintf(&b, "工作目录: %s\n", req.Cwd)
	}
	if req.Lang != "" {
		fmt.Fprintf(&b, "语言环境: %s\n", req.Lang)
	}
	if strings.TrimSpace(req.OutputTail) != "" {
		b.WriteString("\n输出末尾:\n```text\n")
		b.WriteString(trimForPrompt(req.OutputTail, 4000))
		b.WriteString("\n```\n")
	}
	return b.String()
}

func buildAlertPrompt(req *Request) string {
	event := failureEventFromRequest(req)
	b, _ := json.MarshalIndent(event, "", "  ")

	var out strings.Builder
	out.WriteString("Use the termind-lark-alert skill.\n\n")
	out.WriteString("Termind detected a failed terminal command. Treat the following JSON as the source event.\n")
	out.WriteString("Call the Termind plugin tools in this order:\n")
	out.WriteString("1. termind_event_redact\n")
	out.WriteString("2. termind_fingerprint_compute\n")
	out.WriteString("3. termind_failure_classify\n")
	out.WriteString("4. termind_lark_card_build\n\n")
	out.WriteString("Then send the returned card with OpenClaw's `message` tool, using exactly:\n")
	out.WriteString("- action: send\n")
	out.WriteString("- channel: feishu\n")
	out.WriteString("- target: the event.larkChatId value\n")
	out.WriteString("- card: the `card` object returned by termind_lark_card_build\n\n")
	out.WriteString("Do not use feishu_chat for sending; that tool is for chat/member info.\n")
	out.WriteString("Do not use lark-cli for this flow. If the `message` tool is unavailable, say `tools.alsoAllow must include message` and do not claim delivery.\n")
	out.WriteString("Only claim the card was sent after the `message` tool returns ok.\n\n")
	out.WriteString("Failure event:\n")
	out.WriteString("```json\n")
	out.Write(b)
	out.WriteString("\n```\n")
	return out.String()
}

func failureEventFromRequest(req *Request) map[string]any {
	event := map[string]any{
		"summary":     summarizeFailure(req),
		"command":     strings.TrimSpace(req.Command),
		"severity":    "warning",
		"exitCode":    req.ExitCode,
		"cwd":         req.Cwd,
		"environment": req.Lang,
		"shell":       req.Shell,
		"tail":        trimForPrompt(req.OutputTail, 4000),
	}
	if chatID := strings.TrimSpace(os.Getenv("TERMIND_LARK_CHAT_ID")); chatID != "" {
		event["larkChatId"] = chatID
	}
	for k, v := range event {
		if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
			delete(event, k)
		}
	}
	return event
}

func summarizeFailure(req *Request) string {
	tail := strings.TrimSpace(req.OutputTail)
	if tail != "" {
		lines := strings.Split(tail, "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			line := strings.TrimSpace(lines[i])
			if line != "" {
				return line
			}
		}
	}
	if strings.TrimSpace(req.Command) != "" {
		return fmt.Sprintf("command failed: %s", strings.TrimSpace(req.Command))
	}
	return fmt.Sprintf("command failed with exit code %d", req.ExitCode)
}

func cleanTerminalText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	inFence := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			continue
		}
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimPrefix(line, "* ")
		line = strings.ReplaceAll(line, "**", "")
		line = strings.ReplaceAll(line, "__", "")
		line = strings.ReplaceAll(line, "`", "")
		if inFence && line != "" {
			line = "  " + line
		}
		if line != "" {
			out = append(out, line)
		}
		if len(out) >= 4 {
			break
		}
	}
	return strings.Join(out, "\n")
}

func trimForPrompt(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return "...(truncated)\n" + s[len(s)-max:]
}

func lastAssistantText(messages []sessionMessage, after time.Time) string {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if !isAssistantRole(msg.Role) {
			continue
		}
		if !messageAfter(msg, after.Add(-5*time.Second)) {
			continue
		}
		if text := strings.TrimSpace(extractMessageText(msg)); text != "" {
			return text
		}
	}
	return ""
}

func isAssistantRole(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "assistant", "agent", "model":
		return true
	default:
		return false
	}
}

func messageAfter(msg sessionMessage, cutoff time.Time) bool {
	ts, ok := parseMessageTime(msg.Timestamp)
	if !ok {
		ts, ok = parseMessageTime(msg.CreatedAt)
	}
	return !ok || !ts.Before(cutoff)
}

func parseMessageTime(raw json.RawMessage) (time.Time, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return time.Time{}, false
	}
	var n float64
	if err := json.Unmarshal(raw, &n); err == nil && n > 0 {
		if n > 1e12 {
			return time.UnixMilli(int64(n)), true
		}
		return time.Unix(int64(n), 0), true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil && strings.TrimSpace(s) != "" {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func extractMessageText(msg sessionMessage) string {
	if strings.TrimSpace(msg.Text) != "" {
		return msg.Text
	}
	if len(msg.Content) == 0 || string(msg.Content) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(msg.Content, &s); err == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(msg.Content, &parts); err == nil {
		var b strings.Builder
		for _, p := range parts {
			if p.Type == "" || p.Type == "text" {
				b.WriteString(p.Text)
			}
		}
		return b.String()
	}
	var obj map[string]any
	if err := json.Unmarshal(msg.Content, &obj); err == nil {
		for _, key := range []string{"text", "message", "content"} {
			if v, ok := obj[key].(string); ok {
				return v
			}
		}
	}
	return ""
}

func sendError(ctx context.Context, out chan<- TokenEvent, msg string) {
	select {
	case out <- TokenEvent{Done: true, Error: msg}:
	case <-ctx.Done():
	}
}

func randomHex(bytes int) string {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
