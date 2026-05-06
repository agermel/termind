package diagnose

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"termind/internal/gateway"
)

const (
	agentWaitTimeout = 60 * time.Second
)

var (
	ansiEscapeRE     = regexp.MustCompile(`\x1b(?:[@-Z\\-_]|\[[0-?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1b\\))`)
	controlCharRE    = regexp.MustCompile(`[\x00-\x08\x0B\x0C\x0E-\x1F\x7F]`)
	promptArtifactRE = regexp.MustCompile(`^[%$#>❯➜]+\s*$`)
)

// Client 把 OpenClaw Gateway 的 operator 方法封装成一次 shell 诊断。
//
// OpenClaw 的 operator method 权限模型会把未知方法默认视为 operator.admin。
// 普通诊断仍走官方 agent -> agent.wait -> sessions.get 链路。
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
	startedAt := time.Now()
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
		return fmt.Errorf("agent.wait: %w", err)
	}
	switch waited.Status {
	case "ok":
		var sess sessionsGetResponse
		if err := c.conn.Call(ctx, MethodSessionsGet, sessionsGetRequest{
			Key:   alertSessionKey,
			Limit: 24,
		}, &sess); err != nil {
			return fmt.Errorf("sessions.get: %w", err)
		}
		if err := alertDeliveryError(sess.Messages, startedAt); err != nil {
			return err
		}
		return nil
	case "timeout":
		return errors.New("agent.wait: OpenClaw Lark alert timed out")
	case "error":
		if waited.Error != "" {
			return errors.New("agent.wait: " + waited.Error)
		}
		return errors.New("agent.wait: OpenClaw Lark alert failed")
	default:
		return fmt.Errorf("agent.wait: unexpected status %s", waited.Status)
	}
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
	ToolName  string          `json:"toolName"`
	Text      string          `json:"text"`
	Content   json.RawMessage `json:"content"`
	Details   json.RawMessage `json:"details"`
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

// buildAlertPrompt 构造发给 OpenClaw agent 的 user message.
//
// 设计原则: skill 文档 (termind-lark-alert/SKILL.md) 拥有 "如何编排 tool" 的
// 知识 (调用顺序、registry 查询、分支决策、card 渲染等). prompt 只负责两件事:
//
//  1. **router hint** — 明确告诉 agent 用哪个 skill, 防止多 skill 误选;
//  2. **hard contract** — 复述 CLI 作为 caller 不可妥协的交付契约
//     (lark-cli 唯一发送、退出码就是验收等). 这些是即使 skill 文档被改动
//     CLI 也不愿意失守的底线, 在 user-message 优先级上压制 LLM, 给客户端
//     一份"独立于 skill 实现"的契约.
//
// 注意: 凡是属于 skill 内部知识 (具体 tool 名、调用顺序、字段名等) 都不应
// 出现在这里 — 那些是 skill 的实现细节, 在 prompt 里 hardcode 会和 skill
// 形成双源真相, 后续新增/重命名 tool 时维护成本极高.
func buildAlertPrompt(req *Request) string {
	event := failureEventFromRequest(req)
	b, _ := json.MarshalIndent(event, "", "  ")

	var out strings.Builder
	out.WriteString("Use the termind-lark-alert skill.\n\n")
	out.WriteString("Termind detected a failed terminal command. Treat the following JSON as the source event ")
	out.WriteString("and follow the skill's flow to redact, dedupe, classify, build the card, and deliver.\n\n")
	out.WriteString("Hard contract (do not violate regardless of skill docs):\n")
	out.WriteString("- lark-cli is the ONLY valid Lark/Feishu sender. Never use OpenClaw Feishu tools, direct Feishu APIs, the message tool, or fallback scripts.\n")
	out.WriteString("- If exec is unavailable, return `tools.alsoAllow must include exec` and stop without claiming delivery.\n")
	out.WriteString("- Claim delivery only after lark-cli exits 0 for every enabled target.\n")
	out.WriteString("- If lark-cli exits non-zero, return its exact stdout/stderr and do not try another sender.\n\n")
	out.WriteString("Failure event:\n")
	out.WriteString("```json\n")
	out.Write(b)
	out.WriteString("\n```\n")
	return out.String()
}

func failureEventFromRequest(req *Request) map[string]any {
	tail := cleanFailureText(req.OutputTail)
	event := map[string]any{
		"summary":  summarizeFailure(req),
		"command":  strings.TrimSpace(req.Command),
		"severity": "warning",
		"exitCode": req.ExitCode,
		"cwd":      req.Cwd,
		"shell":    req.Shell,
		"tail":     trimForPrompt(tail, 4000),
	}
	// enrich 字段按 optional 注入: plugin 侧 schema 允许缺省, 但只要 CLI 探测到了
	// 就传过去, 指纹会自动把 project 纳入归并, 卡片可以展示 branch/commit.
	// environment 是展示向的"主机/工具链"摘要, 与 Lang (用户语言偏好) 不是同一维度,
	// Lang 只在 buildPrompt 里影响输出语种, 不进 event.
	if v := strings.TrimSpace(req.User); v != "" {
		event["user"] = v
	}
	if v := strings.TrimSpace(req.Project); v != "" {
		event["project"] = v
	}
	if v := strings.TrimSpace(req.Branch); v != "" {
		event["branch"] = v
		event["branchKind"] = branchKind(v)
	}
	if v := strings.TrimSpace(req.GitCommit); v != "" {
		event["gitCommit"] = v
	}
	if v := buildEnvironmentDescription(req); v != "" {
		event["environment"] = v
	}
	if sender := strings.TrimSpace(req.Lark.Sender); sender != "" {
		event["larkSender"] = sender
	}
	if userOpenID := strings.TrimSpace(req.Lark.UserOpenID); userOpenID != "" {
		event["larkUserOpenId"] = userOpenID
	}
	targets := make([]map[string]any, 0, len(req.Lark.Targets))
	for _, target := range req.Lark.Targets {
		id := strings.TrimSpace(target.ID)
		if id == "" || !target.Enabled {
			continue
		}
		targets = append(targets, map[string]any{
			"type":  strings.TrimSpace(target.Type),
			"id":    id,
			"label": strings.TrimSpace(target.Label),
		})
	}
	if len(targets) > 0 {
		event["larkTargets"] = targets
	}
	if len(req.Lark.Forwarding.Identities) > 0 {
		event["larkForwardingIdentities"] = req.Lark.Forwarding.Identities
	}
	if len(req.Lark.Forwarding.Routes) > 0 {
		event["larkForwardingRoutes"] = req.Lark.Forwarding.Routes
	}
	for k, v := range event {
		if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
			delete(event, k)
		}
	}
	return event
}

// buildEnvironmentDescription 拼一条"主机/工具链"摘要, 例如:
//
//	"darwin 24.0.0 · go1.22.3"
//	"linux 5.15.0-105"
//
// 字段全部缺失时返回空串, 由 caller 决定是否写进 event.
// 这个描述只做展示用 (卡片右上角), 不参与指纹计算; 指纹走 project 更稳定.
func buildEnvironmentDescription(req *Request) string {
	parts := make([]string, 0, 2)
	if v := strings.TrimSpace(req.OS); v != "" {
		parts = append(parts, v)
	}
	if v := strings.TrimSpace(req.GoVersion); v != "" {
		parts = append(parts, v)
	}
	return strings.Join(parts, " · ")
}

// branchKind 把分支名归类成 plugin classify.js 能识别的类别:
//
//	main / trunk / master         → "main"   (触发 incident 升级路径)
//	release/* hotfix/*            → "release"
//	feat/* feature/* dev/*        → "feature"
//	其他                           → "other"
//
// 返回值会作为 event.branchKind 传给 plugin 侧 classify.
func branchKind(branch string) string {
	b := strings.ToLower(strings.TrimSpace(branch))
	switch b {
	case "main", "master", "trunk":
		return "main"
	}
	switch {
	case strings.HasPrefix(b, "release/"), strings.HasPrefix(b, "hotfix/"):
		return "release"
	case strings.HasPrefix(b, "feat/"), strings.HasPrefix(b, "feature/"),
		strings.HasPrefix(b, "dev/"), strings.HasPrefix(b, "fix/"):
		return "feature"
	}
	return "other"
}

func summarizeFailure(req *Request) string {
	tail := strings.TrimSpace(cleanFailureText(req.OutputTail))
	if tail != "" {
		lines := strings.Split(tail, "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			line := strings.TrimSpace(lines[i])
			if line != "" && !isPromptArtifact(line) {
				return line
			}
		}
	}
	if strings.TrimSpace(req.Command) != "" {
		return fmt.Sprintf("command failed: %s", strings.TrimSpace(req.Command))
	}
	return fmt.Sprintf("command failed with exit code %d", req.ExitCode)
}

func cleanFailureText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = ansiEscapeRE.ReplaceAllString(s, "")
	s = controlCharRE.ReplaceAllString(s, "")
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, strings.TrimRight(line, " \t"))
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func isPromptArtifact(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return true
	}
	return promptArtifactRE.MatchString(line)
}

func alertDeliveryError(messages []sessionMessage, after time.Time) error {
	cutoff := after.Add(-5 * time.Second)
	var lastAssistant string
	for _, msg := range messages {
		if !messageAfter(msg, cutoff) {
			continue
		}
		if strings.EqualFold(msg.ToolName, "exec") {
			if err := execMessageError(msg); err != nil {
				return err
			}
		}
		if isAssistantRole(msg.Role) {
			if text := strings.TrimSpace(extractMessageText(msg)); text != "" {
				lastAssistant = text
			}
		}
	}
	if looksLikeNoDelivery(lastAssistant) {
		return errors.New("OpenClaw Lark alert did not deliver: " + firstLine(lastAssistant))
	}
	return nil
}

func execMessageError(msg sessionMessage) error {
	var details struct {
		ExitCode   *int   `json:"exitCode"`
		Aggregated string `json:"aggregated"`
	}
	if len(msg.Details) > 0 && string(msg.Details) != "null" {
		_ = json.Unmarshal(msg.Details, &details)
	}
	if details.ExitCode == nil || *details.ExitCode == 0 {
		return nil
	}
	text := strings.TrimSpace(details.Aggregated)
	if text == "" {
		text = strings.TrimSpace(extractMessageText(msg))
	}
	if text == "" {
		text = fmt.Sprintf("exit code %d", *details.ExitCode)
	}
	return fmt.Errorf("lark-cli exec failed: %s", firstLine(text))
}

func looksLikeNoDelivery(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return false
	}
	return strings.Contains(text, "no delivery") ||
		strings.Contains(text, "no successful delivery") ||
		strings.Contains(text, "no delivery to claim") ||
		strings.Contains(text, "invalid param") ||
		strings.Contains(text, "lark-cli failed")
}

func firstLine(text string) string {
	for _, line := range strings.Split(cleanFailureText(text), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return strings.TrimSpace(text)
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
