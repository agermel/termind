package diagnose

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	larkCLIStatusSessionKey = "agent:main:termind-lark-cli-status"
	larkCLIStatusLabel      = "termind-lark-cli-status"
	larkCLIProfileUseKey    = "agent:main:termind-lark-cli-profile-use"
	larkCLIProfileUseLabel  = "termind-lark-cli-profile-use"
	larkCLIConfigBindKey    = "agent:main:termind-lark-cli-config-bind"
	larkCLIConfigBindLabel  = "termind-lark-cli-config-bind"
	larkCLIAuthLoginKey     = "agent:main:termind-lark-cli-auth-login"
	larkCLIAuthLoginLabel   = "termind-lark-cli-auth-login"
	larkCLIDiscoverKey      = "agent:main:termind-lark-cli-discover"
	larkCLIDiscoverLabel    = "termind-lark-cli-discover"
	larkCLITestKey          = "agent:main:termind-lark-cli-test"
	larkCLITestLabel        = "termind-lark-cli-test"
)

type LarkCLIStatus struct {
	Version   int              `json:"version,omitempty"`
	Installed bool             `json:"installed"`
	Ready     bool             `json:"ready"`
	Profile   string           `json:"profile,omitempty"`
	Profiles  []LarkCLIProfile `json:"profiles,omitempty"`
	Doctor    LarkCLIDoctor    `json:"doctor,omitempty"`
	Auth      LarkCLIAuth      `json:"auth,omitempty"`
	Errors    []string         `json:"errors,omitempty"`
}

type LarkCLIProfile struct {
	Name        string `json:"name"`
	AppID       string `json:"appId,omitempty"`
	Brand       string `json:"brand,omitempty"`
	User        string `json:"user,omitempty"`
	Identity    string `json:"identity,omitempty"`
	Active      bool   `json:"active,omitempty"`
	TokenStatus string `json:"tokenStatus,omitempty"`
	TokenValid  bool   `json:"tokenValid,omitempty"`
}

type LarkCLIProfileUseRequest struct {
	Profile string `json:"profile"`
}

type LarkCLIProfileUseResult struct {
	Version int      `json:"version,omitempty"`
	OK      bool     `json:"ok"`
	Profile string   `json:"profile,omitempty"`
	Output  string   `json:"output,omitempty"`
	Errors  []string `json:"errors,omitempty"`
}

type LarkCLIConfigBindRequest struct {
	AppID            string `json:"appId"`
	Identity         string `json:"identity,omitempty"`
	Profile          string `json:"profile,omitempty"`
	Name             string `json:"name,omitempty"`
	Slot             string `json:"slot,omitempty"`
	LarkCLIConfigDir string `json:"larkCliConfigDir,omitempty"`
}

type LarkCLIConfigBindResult struct {
	Version          int      `json:"version,omitempty"`
	OK               bool     `json:"ok"`
	AppID            string   `json:"appId,omitempty"`
	Identity         string   `json:"identity,omitempty"`
	Profile          string   `json:"profile,omitempty"`
	LarkCLIConfigDir string   `json:"larkCliConfigDir,omitempty"`
	Output           string   `json:"output,omitempty"`
	Errors           []string `json:"errors,omitempty"`
}

type LarkCLIAuthLoginStartResult struct {
	Version         int      `json:"version,omitempty"`
	OK              bool     `json:"ok"`
	DeviceCode      string   `json:"deviceCode,omitempty"`
	UserCode        string   `json:"userCode,omitempty"`
	VerificationURL string   `json:"verificationURL,omitempty"`
	ExpiresIn       int      `json:"expiresIn,omitempty"`
	Interval        int      `json:"interval,omitempty"`
	Message         string   `json:"message,omitempty"`
	Output          string   `json:"output,omitempty"`
	Errors          []string `json:"errors,omitempty"`
}

type LarkCLIAuthLoginCompleteResult struct {
	Version int      `json:"version,omitempty"`
	OK      bool     `json:"ok"`
	Output  string   `json:"output,omitempty"`
	Errors  []string `json:"errors,omitempty"`
}

type LarkCLIDoctor struct {
	OK       *bool  `json:"ok,omitempty"`
	ExitCode *int   `json:"exitCode,omitempty"`
	Output   string `json:"output,omitempty"`
}

type LarkCLIAuth struct {
	UserOpenID string `json:"userOpenID,omitempty"`
}

type LarkTargetSearchRequest struct {
	Kind             string `json:"kind"`
	Sender           string `json:"sender,omitempty"`
	Query            string `json:"query,omitempty"`
	MemberOpenID     string `json:"memberOpenID,omitempty"`
	LarkCLIConfigDir string `json:"larkCliConfigDir,omitempty"`
}

type LarkTargetSearchResult struct {
	Version int          `json:"version,omitempty"`
	Kind    string       `json:"kind"`
	Choices []LarkTarget `json:"choices"`
	Errors  []string     `json:"errors,omitempty"`
}

type LarkTargetTestRequest struct {
	Sender     string                            `json:"sender,omitempty"`
	Targets    []LarkTarget                      `json:"targets,omitempty"`
	Card       json.RawMessage                   `json:"card"`
	Identities map[string]LarkForwardingIdentity `json:"identities,omitempty"`
	Routes     []LarkForwardingRoute             `json:"routes,omitempty"`
}

type LarkTargetTestResult struct {
	Version int                   `json:"version,omitempty"`
	OK      bool                  `json:"ok,omitempty"`
	Results []LarkTargetTestEntry `json:"results"`
	Errors  []string              `json:"errors,omitempty"`
}

type LarkTargetTestEntry struct {
	Target LarkTarget `json:"target"`
	OK     bool       `json:"ok"`
	Output string     `json:"output,omitempty"`
	Error  string     `json:"error,omitempty"`
}

func (c *Client) LarkCLIStatus(ctx context.Context) (*LarkCLIStatus, error) {
	sessionKey := larkCLIStatusSessionKey + ":" + randomHex(8)
	messages, startedAt, err := c.runLarkAgentSession(ctx, sessionKey, larkCLIStatusLabel, buildLarkCLIStatusPrompt(), 32)
	if err != nil {
		return nil, err
	}
	if status := larkCLIStatusFromMessages(messages, startedAt); status != nil {
		return status, nil
	}
	return nil, errors.New("OpenClaw lark-cli status completed, but no status JSON was returned")
}

func (c *Client) SearchLarkTargets(ctx context.Context, req LarkTargetSearchRequest) (*LarkTargetSearchResult, error) {
	sessionKey := larkCLIDiscoverKey + ":" + randomHex(8)
	messages, startedAt, err := c.runLarkAgentSession(ctx, sessionKey, larkCLIDiscoverLabel, buildLarkTargetSearchPrompt(req), 32)
	if err != nil {
		return nil, err
	}
	if result := larkTargetSearchFromMessages(messages, startedAt); result != nil {
		return result, nil
	}
	return nil, errors.New("OpenClaw lark-cli target discovery completed, but no result JSON was returned")
}

func (c *Client) UseLarkCLIProfile(ctx context.Context, req LarkCLIProfileUseRequest) (*LarkCLIProfileUseResult, error) {
	sessionKey := larkCLIProfileUseKey + ":" + randomHex(8)
	messages, startedAt, err := c.runLarkAgentSession(ctx, sessionKey, larkCLIProfileUseLabel, buildLarkCLIProfileUsePrompt(req), 32)
	if err != nil {
		return nil, err
	}
	if result := larkCLIProfileUseFromMessages(messages, startedAt); result != nil {
		return result, nil
	}
	return nil, errors.New("OpenClaw lark-cli profile switch completed, but no result JSON was returned")
}

func (c *Client) BindLarkCLIConfig(ctx context.Context, req LarkCLIConfigBindRequest) (*LarkCLIConfigBindResult, error) {
	req.AppID = strings.TrimSpace(req.AppID)
	if req.AppID == "" {
		return nil, errors.New("app id is required")
	}
	req.Identity = strings.TrimSpace(req.Identity)
	if req.Identity == "" {
		req.Identity = "bot-only"
	}
	sessionKey := larkCLIConfigBindKey + ":" + randomHex(8)
	messages, startedAt, err := c.runLarkAgentSession(ctx, sessionKey, larkCLIConfigBindLabel, buildLarkCLIConfigBindPrompt(req), 32)
	if err != nil {
		return nil, err
	}
	if result := larkCLIConfigBindFromMessages(messages, startedAt); result != nil {
		return result, nil
	}
	return nil, errors.New("OpenClaw lark-cli config bind completed, but no result JSON was returned")
}

func (c *Client) StartLarkCLIAuthLogin(ctx context.Context, configDir ...string) (*LarkCLIAuthLoginStartResult, error) {
	sessionKey := larkCLIAuthLoginKey + ":" + randomHex(8)
	messages, startedAt, err := c.runLarkAgentSessionWithTimeout(ctx, sessionKey, larkCLIAuthLoginLabel, buildLarkCLIAuthLoginStartPrompt(firstVariadicString(configDir)), 32, 2*time.Minute)
	if err != nil {
		return nil, err
	}
	if result := larkCLIAuthLoginStartFromMessages(messages, startedAt); result != nil {
		return result, nil
	}
	return nil, errors.New("OpenClaw lark-cli auth login start completed, but no result JSON was returned")
}

func (c *Client) CompleteLarkCLIAuthLogin(ctx context.Context, deviceCode string, configDir ...string) (*LarkCLIAuthLoginCompleteResult, error) {
	deviceCode = strings.TrimSpace(deviceCode)
	if deviceCode == "" {
		return nil, errors.New("device code is required")
	}
	sessionKey := larkCLIAuthLoginKey + ":" + randomHex(8)
	messages, startedAt, err := c.runLarkAgentSessionWithTimeout(ctx, sessionKey, larkCLIAuthLoginLabel, buildLarkCLIAuthLoginCompletePrompt(deviceCode, firstVariadicString(configDir)), 32, 15*time.Minute)
	if err != nil {
		return nil, err
	}
	if result := larkCLIAuthLoginCompleteFromMessages(messages, startedAt); result != nil {
		return result, nil
	}
	return nil, errors.New("OpenClaw lark-cli auth login complete completed, but no result JSON was returned")
}

func (c *Client) TestLarkTargets(ctx context.Context, req LarkTargetTestRequest) (*LarkTargetTestResult, error) {
	sessionKey := larkCLITestKey + ":" + randomHex(8)
	messages, startedAt, err := c.runLarkAgentSession(ctx, sessionKey, larkCLITestLabel, buildLarkTargetTestPrompt(req), 32)
	if err != nil {
		return nil, err
	}
	if result := larkTargetTestFromMessages(messages, startedAt); result != nil {
		return result, nil
	}
	return nil, errors.New("OpenClaw lark-cli target test completed, but no result JSON was returned")
}

func (c *Client) runLarkAgentSession(ctx context.Context, sessionKey string, label string, prompt string, limit int) ([]sessionMessage, time.Time, error) {
	return c.runLarkAgentSessionWithTimeout(ctx, sessionKey, label, prompt, limit, agentWaitTimeout)
}

func firstVariadicString(values []string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (c *Client) runLarkAgentSessionWithTimeout(ctx context.Context, sessionKey string, label string, prompt string, limit int, timeout time.Duration) ([]sessionMessage, time.Time, error) {
	if c.conn == nil {
		return nil, time.Time{}, errors.New("diagnose: nil gateway conn")
	}
	if timeout <= 0 {
		timeout = agentWaitTimeout
	}
	runID := label + "-" + randomHex(16)
	startedAt := time.Now()
	var accepted agentResponse
	if err := c.conn.Call(ctx, MethodAgent, agentRequest{
		Message:        prompt,
		SessionKey:     sessionKey,
		Deliver:        false,
		Thinking:       "low",
		IDempotencyKey: runID,
		Label:          label,
	}, &accepted); err != nil {
		return nil, time.Time{}, fmt.Errorf("agent: %w", err)
	}
	if accepted.RunID == "" {
		accepted.RunID = runID
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var waited agentWaitResponse
	if err := c.conn.Call(waitCtx, MethodAgentWait, agentWaitRequest{
		RunID:     accepted.RunID,
		TimeoutMs: int(timeout / time.Millisecond),
	}, &waited); err != nil {
		return nil, time.Time{}, fmt.Errorf("agent.wait: %w", err)
	}
	if waited.Status != "ok" {
		if waited.Status == "error" && waited.Error != "" {
			return nil, time.Time{}, errors.New("agent.wait: " + waited.Error)
		}
		return nil, time.Time{}, fmt.Errorf("agent.wait: %s", waited.Status)
	}
	var sess sessionsGetResponse
	if err := c.conn.Call(ctx, MethodSessionsGet, sessionsGetRequest{Key: sessionKey, Limit: limit}, &sess); err != nil {
		return nil, time.Time{}, fmt.Errorf("sessions.get: %w", err)
	}
	return sess.Messages, startedAt, nil
}

func buildLarkCLIStatusPrompt() string {
	return strings.Join([]string{
		"Use the Termind split lark-cli check tools to inspect lark-cli on the OpenClaw host quickly.",
		"Do not use local Termind-side lark-cli. Do not reveal tokens, app secrets, cookies, or credential file contents.",
		"Run each check only if the previous check says to continue. Stop early when a required check fails.",
		"Let OpenClaw-side lark-cli use its active profile. Do not pass or persist a Termind-side profile.",
		"A bot app active profile is valid for Termind readiness. Do not require user contact API authorization for readiness.",
		"Steps:",
		"1. Call termind_lark_cli_exists with {\"action\":\"plan\"}. Execute exactly its returned command with OpenClaw exec. Call termind_lark_cli_exists with {\"action\":\"parse\"} and the exec stdout/stderr/exitCode. If installed is false, reply immediately with its status JSON.",
		"2. Call termind_lark_cli_login_status with {\"action\":\"plan\"}. Execute exactly its returned command with OpenClaw exec. Call termind_lark_cli_login_status with {\"action\":\"parse\"} and the exec stdout/stderr/exitCode. If loggedIn is false, reply immediately with its status JSON.",
		"3. Optionally call termind_lark_cli_doctor_status with {\"action\":\"plan\"} only after the previous checks pass. Execute exactly its returned command with OpenClaw exec and parse it.",
		"4. Reply with exactly one JSON object and no Markdown. Include installed, ready, profile, profiles, doctor, auth, and errors fields.",
	}, "\n")
}

func buildLarkTargetSearchPrompt(req LarkTargetSearchRequest) string {
	payload, _ := json.Marshal(req)
	return strings.Join([]string{
		"Use the Termind plugin tool termind_lark_cli_discover to search Lark/Feishu targets on the OpenClaw host.",
		"Do not use local Termind-side lark-cli. Do not reveal tokens, app secrets, cookies, or credential file contents.",
		"Input JSON: " + string(payload),
		"Steps:",
		"1. Call termind_lark_cli_discover with {\"action\":\"plan\"} plus the input JSON.",
		"2. Execute the returned lark-cli commands with OpenClaw exec on this OpenClaw host.",
		"3. Call termind_lark_cli_discover with {\"action\":\"parse\"} and pass only stdout/stderr/exit-code metadata from exec results.",
		"4. Reply with exactly one JSON object and no Markdown. Include kind, choices, and errors fields.",
	}, "\n")
}

func buildLarkCLIProfileUsePrompt(req LarkCLIProfileUseRequest) string {
	payload, _ := json.Marshal(req)
	return strings.Join([]string{
		"Use the Termind plugin tool termind_lark_cli_profile_use to switch the active lark-cli profile on the OpenClaw host.",
		"Do not use local Termind-side lark-cli. Do not reveal tokens, app secrets, cookies, or credential file contents.",
		"Input JSON: " + string(payload),
		"Steps:",
		"1. Call termind_lark_cli_profile_use with {\"action\":\"plan\"} plus the input JSON.",
		"2. Execute exactly the returned lark-cli command with OpenClaw exec on this OpenClaw host.",
		"3. Call termind_lark_cli_profile_use with {\"action\":\"parse\"} and pass stdout/stderr/exitCode from exec.",
		"4. Reply with exactly one JSON object and no Markdown. Include ok, profile, output, and errors fields.",
	}, "\n")
}

func buildLarkCLIConfigBindPrompt(req LarkCLIConfigBindRequest) string {
	payload, _ := json.Marshal(req)
	return strings.Join([]string{
		"Use the Termind plugin tool termind_lark_cli_config_bind to bind an existing OpenClaw Feishu/Lark bot app into the OpenClaw-side lark-cli workspace.",
		"Do not use local Termind-side lark-cli. Do not reveal tokens, app secrets, cookies, or credential file contents.",
		"Do not ask for or pass app secrets. Do not use lark-cli config init or --app-secret-stdin for this flow.",
		"Input JSON: " + string(payload),
		"Steps:",
		"1. Call termind_lark_cli_config_bind with {\"action\":\"plan\"} plus the input JSON.",
		"2. Execute exactly the returned lark-cli command with OpenClaw exec on this OpenClaw host.",
		"3. Call termind_lark_cli_config_bind with {\"action\":\"parse\"} and pass stdout/stderr/exitCode from exec.",
		"4. Reply with exactly one JSON object and no Markdown. Include ok, appId, identity, profile, output, and errors fields.",
	}, "\n")
}

func buildLarkCLIAuthLoginStartPrompt(configDir string) string {
	payload, _ := json.Marshal(map[string]string{"larkCliConfigDir": strings.TrimSpace(configDir)})
	return strings.Join([]string{
		"Use the Termind plugin tool termind_lark_cli_auth_login to start lark-cli auth login on the OpenClaw host.",
		"Do not use local Termind-side lark-cli. Do not reveal tokens, app secrets, cookies, or credential file contents.",
		"Input JSON: " + string(payload),
		"Steps:",
		"1. Call termind_lark_cli_auth_login with {\"action\":\"plan\",\"phase\":\"start\"} plus the input JSON.",
		"2. Execute exactly the returned lark-cli command with OpenClaw exec on this OpenClaw host.",
		"3. Call termind_lark_cli_auth_login with {\"action\":\"parse\",\"phase\":\"start\"} and pass stdout/stderr/exitCode from exec.",
		"4. Reply with exactly one JSON object and no Markdown. Include ok, deviceCode, userCode, verificationURL, expiresIn, interval, message, output, and errors fields.",
	}, "\n")
}

func buildLarkCLIAuthLoginCompletePrompt(deviceCode string, configDir string) string {
	payload, _ := json.Marshal(map[string]string{"deviceCode": strings.TrimSpace(deviceCode), "larkCliConfigDir": strings.TrimSpace(configDir)})
	return strings.Join([]string{
		"Use the Termind plugin tool termind_lark_cli_auth_login to complete lark-cli auth login on the OpenClaw host.",
		"Do not use local Termind-side lark-cli. Do not reveal tokens, app secrets, cookies, or credential file contents.",
		"Input JSON: " + string(payload),
		"Steps:",
		"1. Call termind_lark_cli_auth_login with {\"action\":\"plan\",\"phase\":\"complete\"} plus the input JSON.",
		"2. Execute exactly the returned lark-cli command with OpenClaw exec on this OpenClaw host. It may block until browser authorization is completed.",
		"3. Call termind_lark_cli_auth_login with {\"action\":\"parse\",\"phase\":\"complete\"} and pass stdout/stderr/exitCode from exec.",
		"4. Reply with exactly one JSON object and no Markdown. Include ok, output, and errors fields.",
	}, "\n")
}

func buildLarkTargetTestPrompt(req LarkTargetTestRequest) string {
	payload, _ := json.Marshal(req)
	return strings.Join([]string{
		"Use the Termind plugin tool termind_lark_cli_send_command_build to test sending a Lark/Feishu card from the OpenClaw host.",
		"Do not use local Termind-side lark-cli. Do not reveal tokens, app secrets, cookies, or credential file contents.",
		"Input JSON: " + string(payload),
		"Steps:",
		"1. Build an event object with summary and command from the input JSON.",
		"   If input JSON contains routes/identities, put them on event.larkForwardingRoutes and event.larkForwardingIdentities.",
		"   Otherwise put sender/targets on event.larkSender and event.larkTargets.",
		"2. Call termind_lark_cli_send_command_build with the event object and card from the input JSON.",
		"3. Execute each returned lark-cli command with OpenClaw exec on this OpenClaw host.",
		"4. Reply with exactly one JSON object and no Markdown. Include results and errors fields. Each result must include target, ok, output, and error.",
	}, "\n")
}

func larkCLIStatusFromMessages(messages []sessionMessage, after time.Time) *LarkCLIStatus {
	cutoff := after.Add(-5 * time.Second)
	var fallback *LarkCLIStatus
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if !messageAfter(msg, cutoff) {
			continue
		}
		text := extractMessageText(msg)
		if strings.EqualFold(msg.ToolName, "termind_lark_cli_status") {
			if status := parseLarkCLIStatusText(text); status != nil {
				return status
			}
		}
		if isAssistantRole(msg.Role) {
			if status := parseLarkCLIStatusText(text); status != nil {
				fallback = status
			}
		}
	}
	return fallback
}

func parseLarkCLIStatusText(text string) *LarkCLIStatus {
	for _, candidate := range jsonCandidates(text) {
		var status LarkCLIStatus
		if err := json.Unmarshal([]byte(candidate), &status); err == nil && larkCLIStatusUseful(&status) {
			return &status
		}
		var wrapper map[string]json.RawMessage
		if err := json.Unmarshal([]byte(candidate), &wrapper); err != nil {
			continue
		}
		for _, key := range []string{"status", "larkCLIStatus", "result"} {
			var nested LarkCLIStatus
			if raw := wrapper[key]; len(raw) > 0 && json.Unmarshal(raw, &nested) == nil && larkCLIStatusUseful(&nested) {
				return &nested
			}
		}
	}
	return nil
}

func larkCLIStatusUseful(status *LarkCLIStatus) bool {
	return status != nil && (status.Installed || status.Ready || status.Profile != "" || len(status.Profiles) > 0 || len(status.Errors) > 0)
}

func larkTargetSearchFromMessages(messages []sessionMessage, after time.Time) *LarkTargetSearchResult {
	cutoff := after.Add(-5 * time.Second)
	var fallback *LarkTargetSearchResult
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if !messageAfter(msg, cutoff) {
			continue
		}
		text := extractMessageText(msg)
		if strings.EqualFold(msg.ToolName, "termind_lark_cli_discover") {
			if result := parseLarkTargetSearchText(text); result != nil {
				return result
			}
		}
		if isAssistantRole(msg.Role) {
			if result := parseLarkTargetSearchText(text); result != nil {
				fallback = result
			}
		}
	}
	return fallback
}

func larkCLIProfileUseFromMessages(messages []sessionMessage, after time.Time) *LarkCLIProfileUseResult {
	cutoff := after.Add(-5 * time.Second)
	var fallback *LarkCLIProfileUseResult
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if !messageAfter(msg, cutoff) {
			continue
		}
		text := extractMessageText(msg)
		if strings.EqualFold(msg.ToolName, "termind_lark_cli_profile_use") {
			if result := parseLarkCLIProfileUseText(text); result != nil {
				return result
			}
		}
		if isAssistantRole(msg.Role) {
			if result := parseLarkCLIProfileUseText(text); result != nil {
				fallback = result
			}
		}
	}
	return fallback
}

func parseLarkCLIProfileUseText(text string) *LarkCLIProfileUseResult {
	for _, candidate := range jsonCandidates(text) {
		var result LarkCLIProfileUseResult
		if err := json.Unmarshal([]byte(candidate), &result); err == nil && (result.OK || result.Profile != "" || len(result.Errors) > 0) {
			return &result
		}
		var wrapper map[string]json.RawMessage
		if err := json.Unmarshal([]byte(candidate), &wrapper); err != nil {
			continue
		}
		for _, key := range []string{"profileUse", "result"} {
			var nested LarkCLIProfileUseResult
			if raw := wrapper[key]; len(raw) > 0 && json.Unmarshal(raw, &nested) == nil && (nested.OK || nested.Profile != "" || len(nested.Errors) > 0) {
				return &nested
			}
		}
	}
	return nil
}

func larkCLIConfigBindFromMessages(messages []sessionMessage, after time.Time) *LarkCLIConfigBindResult {
	cutoff := after.Add(-5 * time.Second)
	var fallback *LarkCLIConfigBindResult
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if !messageAfter(msg, cutoff) {
			continue
		}
		text := extractMessageText(msg)
		if strings.EqualFold(msg.ToolName, "termind_lark_cli_config_bind") {
			if result := parseLarkCLIConfigBindText(text); result != nil {
				return result
			}
		}
		if isAssistantRole(msg.Role) {
			if result := parseLarkCLIConfigBindText(text); result != nil {
				fallback = result
			}
		}
	}
	return fallback
}

func parseLarkCLIConfigBindText(text string) *LarkCLIConfigBindResult {
	for _, candidate := range jsonCandidates(text) {
		var result LarkCLIConfigBindResult
		if err := json.Unmarshal([]byte(candidate), &result); err == nil && (result.OK || result.AppID != "" || result.Profile != "" || len(result.Errors) > 0) {
			return &result
		}
		var wrapper map[string]json.RawMessage
		if err := json.Unmarshal([]byte(candidate), &wrapper); err != nil {
			continue
		}
		for _, key := range []string{"configBind", "result"} {
			var nested LarkCLIConfigBindResult
			if raw := wrapper[key]; len(raw) > 0 && json.Unmarshal(raw, &nested) == nil && (nested.OK || nested.AppID != "" || nested.Profile != "" || len(nested.Errors) > 0) {
				return &nested
			}
		}
	}
	return nil
}

func larkCLIAuthLoginStartFromMessages(messages []sessionMessage, after time.Time) *LarkCLIAuthLoginStartResult {
	cutoff := after.Add(-5 * time.Second)
	var fallback *LarkCLIAuthLoginStartResult
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if !messageAfter(msg, cutoff) {
			continue
		}
		text := extractMessageText(msg)
		if strings.EqualFold(msg.ToolName, "termind_lark_cli_auth_login") {
			if result := parseLarkCLIAuthLoginStartText(text); result != nil {
				return result
			}
		}
		if isAssistantRole(msg.Role) {
			if result := parseLarkCLIAuthLoginStartText(text); result != nil {
				fallback = result
			}
		}
	}
	return fallback
}

func parseLarkCLIAuthLoginStartText(text string) *LarkCLIAuthLoginStartResult {
	for _, candidate := range jsonCandidates(text) {
		var result LarkCLIAuthLoginStartResult
		if err := json.Unmarshal([]byte(candidate), &result); err == nil && (result.OK || result.DeviceCode != "" || len(result.Errors) > 0) {
			return &result
		}
		var wrapper map[string]json.RawMessage
		if err := json.Unmarshal([]byte(candidate), &wrapper); err != nil {
			continue
		}
		for _, key := range []string{"authLogin", "result"} {
			var nested LarkCLIAuthLoginStartResult
			if raw := wrapper[key]; len(raw) > 0 && json.Unmarshal(raw, &nested) == nil && (nested.OK || nested.DeviceCode != "" || len(nested.Errors) > 0) {
				return &nested
			}
		}
	}
	return nil
}

func larkCLIAuthLoginCompleteFromMessages(messages []sessionMessage, after time.Time) *LarkCLIAuthLoginCompleteResult {
	cutoff := after.Add(-5 * time.Second)
	var fallback *LarkCLIAuthLoginCompleteResult
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if !messageAfter(msg, cutoff) {
			continue
		}
		text := extractMessageText(msg)
		if strings.EqualFold(msg.ToolName, "termind_lark_cli_auth_login") {
			if result := parseLarkCLIAuthLoginCompleteText(text); result != nil {
				return result
			}
		}
		if isAssistantRole(msg.Role) {
			if result := parseLarkCLIAuthLoginCompleteText(text); result != nil {
				fallback = result
			}
		}
	}
	return fallback
}

func parseLarkCLIAuthLoginCompleteText(text string) *LarkCLIAuthLoginCompleteResult {
	for _, candidate := range jsonCandidates(text) {
		var result LarkCLIAuthLoginCompleteResult
		if err := json.Unmarshal([]byte(candidate), &result); err == nil && (result.OK || result.Output != "" || len(result.Errors) > 0) {
			return &result
		}
		var wrapper map[string]json.RawMessage
		if err := json.Unmarshal([]byte(candidate), &wrapper); err != nil {
			continue
		}
		for _, key := range []string{"authLogin", "result"} {
			var nested LarkCLIAuthLoginCompleteResult
			if raw := wrapper[key]; len(raw) > 0 && json.Unmarshal(raw, &nested) == nil && (nested.OK || nested.Output != "" || len(nested.Errors) > 0) {
				return &nested
			}
		}
	}
	return nil
}

func parseLarkTargetSearchText(text string) *LarkTargetSearchResult {
	for _, candidate := range jsonCandidates(text) {
		if result := parseLarkTargetSearchJSON([]byte(candidate)); result != nil {
			return result
		}
	}
	return nil
}

func parseLarkTargetSearchJSON(raw []byte) *LarkTargetSearchResult {
	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil
	}
	if _, ok := wrapper["choices"]; ok {
		var result LarkTargetSearchResult
		if json.Unmarshal(raw, &result) == nil {
			return &result
		}
	}
	for _, key := range []string{"search", "targetSearch", "result"} {
		if nestedRaw := wrapper[key]; len(nestedRaw) > 0 {
			if result := parseLarkTargetSearchJSON(nestedRaw); result != nil {
				return result
			}
		}
	}
	return nil
}

func larkTargetTestFromMessages(messages []sessionMessage, after time.Time) *LarkTargetTestResult {
	cutoff := after.Add(-5 * time.Second)
	var fallback *LarkTargetTestResult
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if !messageAfter(msg, cutoff) {
			continue
		}
		if isAssistantRole(msg.Role) {
			if result := parseLarkTargetTestText(extractMessageText(msg)); result != nil {
				fallback = result
			}
		}
	}
	return fallback
}

func parseLarkTargetTestText(text string) *LarkTargetTestResult {
	for _, candidate := range jsonCandidates(text) {
		if result := parseLarkTargetTestJSON([]byte(candidate)); result != nil {
			return result
		}
	}
	return nil
}

func parseLarkTargetTestJSON(raw []byte) *LarkTargetTestResult {
	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil
	}
	if _, ok := wrapper["results"]; ok {
		var result LarkTargetTestResult
		if json.Unmarshal(raw, &result) == nil {
			return &result
		}
	}
	for _, key := range []string{"test", "targetTest", "result"} {
		if nestedRaw := wrapper[key]; len(nestedRaw) > 0 {
			if result := parseLarkTargetTestJSON(nestedRaw); result != nil {
				return result
			}
		}
	}
	return nil
}

func jsonCandidates(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	out := []string{text}
	if fenced := fencedJSON(text); fenced != "" {
		out = append(out, fenced)
	}
	if object := firstJSONObject(text); object != "" {
		out = append(out, object)
	}
	return out
}

func fencedJSON(text string) string {
	start := strings.Index(text, "```")
	if start < 0 {
		return ""
	}
	body := text[start+3:]
	if i := strings.Index(body, "\n"); i >= 0 {
		body = body[i+1:]
	}
	end := strings.Index(body, "```")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(body[:end])
}

func firstJSONObject(text string) string {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return ""
	}
	return strings.TrimSpace(text[start : end+1])
}
