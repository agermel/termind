package diagnose

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLarkCLIStatus_UsesAgentFlowAndParsesStatus(t *testing.T) {
	ms := &mockDiagnoseServer{assistantText: `{"installed":true,"ready":true,"profile":"cli_a97d19e27838dcb6","profiles":[{"name":"cli_a97d19e27838dcb6","active":true,"tokenValid":true}],"doctor":{"ok":true,"exitCode":0},"auth":{"userOpenID":"ou_test"},"errors":[]}`}
	srv := httptest.NewServer(ms.handler(t))
	defer srv.Close()

	conn := dialTestConn(t, srv.URL)
	defer conn.Close()

	status, err := NewClient(conn).LarkCLIStatus(context.Background())
	if err != nil {
		t.Fatalf("LarkCLIStatus: %v", err)
	}
	if !status.Ready || status.Profile != "cli_a97d19e27838dcb6" {
		t.Fatalf("status=%+v", status)
	}
	if status.Auth.UserOpenID != "ou_test" {
		t.Fatalf("userOpenID=%q", status.Auth.UserOpenID)
	}
	if len(ms.agentRequests) != 1 {
		t.Fatalf("agentRequests=%d, want 1", len(ms.agentRequests))
	}
	if got := ms.agentRequests[0].SessionKey; !strings.HasPrefix(got, larkCLIStatusSessionKey+":") {
		t.Fatalf("sessionKey=%q, want prefix %q", got, larkCLIStatusSessionKey+":")
	}
	if !strings.Contains(ms.agentRequests[0].Message, "termind_lark_cli_exists") {
		t.Fatalf("prompt did not mention status tool: %s", ms.agentRequests[0].Message)
	}
}

func TestBuildLarkCLIStatusPromptUsesSplitFastChecks(t *testing.T) {
	prompt := buildLarkCLIStatusPrompt()
	for _, want := range []string{
		"termind_lark_cli_exists",
		"termind_lark_cli_login_status",
		"bot app active profile is valid",
		"Stop early",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
	if strings.Contains(prompt, "tools.invoke") {
		t.Fatalf("prompt should not mention tools.invoke: %s", prompt)
	}
}

func TestParseLarkCLIStatusText_FencedJSON(t *testing.T) {
	status := parseLarkCLIStatusText("```json\n{\"installed\":true,\"ready\":true,\"profile\":\"cli_test\"}\n```")
	if status == nil {
		t.Fatal("status=nil")
	}
	if status.Profile != "cli_test" || !status.Ready {
		t.Fatalf("status=%+v", status)
	}
}

func TestSearchLarkTargets_UsesAgentFlowAndParsesChoices(t *testing.T) {
	ms := &mockDiagnoseServer{assistantText: `{"kind":"chat","choices":[{"type":"chat","id":"oc_test","label":"ops"}],"errors":[]}`}
	srv := httptest.NewServer(ms.handler(t))
	defer srv.Close()

	conn := dialTestConn(t, srv.URL)
	defer conn.Close()

	result, err := NewClient(conn).SearchLarkTargets(context.Background(), LarkTargetSearchRequest{Kind: "chat", Sender: "bot", Query: "ops"})
	if err != nil {
		t.Fatalf("SearchLarkTargets: %v", err)
	}
	if len(result.Choices) != 1 || result.Choices[0].ID != "oc_test" {
		t.Fatalf("result=%+v", result)
	}
	if len(ms.agentRequests) != 1 {
		t.Fatalf("agentRequests=%d, want 1", len(ms.agentRequests))
	}
	if !strings.HasPrefix(ms.agentRequests[0].SessionKey, larkCLIDiscoverKey+":") {
		t.Fatalf("sessionKey=%q", ms.agentRequests[0].SessionKey)
	}
	if !strings.Contains(ms.agentRequests[0].Message, "termind_lark_cli_discover") {
		t.Fatalf("prompt did not mention discover tool: %s", ms.agentRequests[0].Message)
	}
	if !strings.Contains(ms.agentRequests[0].Message, `"sender":"bot"`) {
		t.Fatalf("prompt did not include sender: %s", ms.agentRequests[0].Message)
	}
}

func TestUseLarkCLIProfile_UsesAgentFlowAndParsesResult(t *testing.T) {
	ms := &mockDiagnoseServer{assistantText: `{"ok":true,"profile":"cli_test","output":"switched","errors":[]}`}
	srv := httptest.NewServer(ms.handler(t))
	defer srv.Close()

	conn := dialTestConn(t, srv.URL)
	defer conn.Close()

	result, err := NewClient(conn).UseLarkCLIProfile(context.Background(), LarkCLIProfileUseRequest{Profile: "cli_test"})
	if err != nil {
		t.Fatalf("UseLarkCLIProfile: %v", err)
	}
	if !result.OK || result.Profile != "cli_test" {
		t.Fatalf("result=%+v", result)
	}
	if len(ms.agentRequests) != 1 {
		t.Fatalf("agentRequests=%d, want 1", len(ms.agentRequests))
	}
	if !strings.HasPrefix(ms.agentRequests[0].SessionKey, larkCLIProfileUseKey+":") {
		t.Fatalf("sessionKey=%q", ms.agentRequests[0].SessionKey)
	}
	if !strings.Contains(ms.agentRequests[0].Message, "termind_lark_cli_profile_use") {
		t.Fatalf("prompt did not mention profile use tool: %s", ms.agentRequests[0].Message)
	}
}

func TestBindLarkCLIConfig_UsesAgentFlowAndParsesResult(t *testing.T) {
	ms := &mockDiagnoseServer{assistantText: `{"ok":true,"appId":"cli_test","identity":"bot-only","profile":"cli_test","output":"bound","errors":[]}`}
	srv := httptest.NewServer(ms.handler(t))
	defer srv.Close()

	conn := dialTestConn(t, srv.URL)
	defer conn.Close()

	result, err := NewClient(conn).BindLarkCLIConfig(context.Background(), LarkCLIConfigBindRequest{AppID: "cli_test"})
	if err != nil {
		t.Fatalf("BindLarkCLIConfig: %v", err)
	}
	if !result.OK || result.AppID != "cli_test" || result.Identity != "bot-only" {
		t.Fatalf("result=%+v", result)
	}
	if len(ms.agentRequests) != 1 {
		t.Fatalf("agentRequests=%d, want 1", len(ms.agentRequests))
	}
	if !strings.HasPrefix(ms.agentRequests[0].SessionKey, larkCLIConfigBindKey+":") {
		t.Fatalf("sessionKey=%q", ms.agentRequests[0].SessionKey)
	}
	if !strings.Contains(ms.agentRequests[0].Message, "termind_lark_cli_config_bind") || !strings.Contains(ms.agentRequests[0].Message, `"appId":"cli_test"`) {
		t.Fatalf("prompt did not mention config bind tool/appId: %s", ms.agentRequests[0].Message)
	}
}

func TestLarkCLIAuthLogin_UsesAgentFlowAndParsesDeviceFlow(t *testing.T) {
	ms := &mockDiagnoseServer{assistantText: `{"ok":true,"deviceCode":"dev-test","userCode":"ABCD-EFGH","verificationURL":"https://example.com/device","expiresIn":600,"errors":[]}`}
	srv := httptest.NewServer(ms.handler(t))
	defer srv.Close()

	conn := dialTestConn(t, srv.URL)
	defer conn.Close()

	result, err := NewClient(conn).StartLarkCLIAuthLogin(context.Background())
	if err != nil {
		t.Fatalf("StartLarkCLIAuthLogin: %v", err)
	}
	if !result.OK || result.DeviceCode != "dev-test" || result.UserCode != "ABCD-EFGH" {
		t.Fatalf("result=%+v", result)
	}
	if len(ms.agentRequests) != 1 {
		t.Fatalf("agentRequests=%d, want 1", len(ms.agentRequests))
	}
	if !strings.HasPrefix(ms.agentRequests[0].SessionKey, larkCLIAuthLoginKey+":") {
		t.Fatalf("sessionKey=%q", ms.agentRequests[0].SessionKey)
	}
	if !strings.Contains(ms.agentRequests[0].Message, "termind_lark_cli_auth_login") || !strings.Contains(ms.agentRequests[0].Message, `"phase":"start"`) {
		t.Fatalf("prompt did not mention auth start tool/phase: %s", ms.agentRequests[0].Message)
	}
}

func TestLarkCLIAuthLoginComplete_UsesAgentFlowAndParsesResult(t *testing.T) {
	ms := &mockDiagnoseServer{assistantText: `{"ok":true,"output":"authorized","errors":[]}`}
	srv := httptest.NewServer(ms.handler(t))
	defer srv.Close()

	conn := dialTestConn(t, srv.URL)
	defer conn.Close()

	result, err := NewClient(conn).CompleteLarkCLIAuthLogin(context.Background(), "dev-test")
	if err != nil {
		t.Fatalf("CompleteLarkCLIAuthLogin: %v", err)
	}
	if !result.OK {
		t.Fatalf("result=%+v", result)
	}
	if len(ms.agentRequests) != 1 {
		t.Fatalf("agentRequests=%d, want 1", len(ms.agentRequests))
	}
	if !strings.Contains(ms.agentRequests[0].Message, "termind_lark_cli_auth_login") || !strings.Contains(ms.agentRequests[0].Message, `"phase":"complete"`) {
		t.Fatalf("prompt did not mention auth complete tool/phase: %s", ms.agentRequests[0].Message)
	}
}

func TestTestLarkTargets_UsesAgentFlowAndParsesResults(t *testing.T) {
	ms := &mockDiagnoseServer{assistantText: `{"results":[{"target":{"type":"chat","id":"oc_test"},"ok":true,"output":"ok"}],"errors":[]}`}
	srv := httptest.NewServer(ms.handler(t))
	defer srv.Close()

	conn := dialTestConn(t, srv.URL)
	defer conn.Close()

	result, err := NewClient(conn).TestLarkTargets(context.Background(), LarkTargetTestRequest{
		Sender:  "bot",
		Targets: []LarkTarget{{Type: "chat", ID: "oc_test", Enabled: true}},
	})
	if err != nil {
		t.Fatalf("TestLarkTargets: %v", err)
	}
	if len(result.Results) != 1 || !result.Results[0].OK {
		t.Fatalf("result=%+v", result)
	}
	if len(ms.agentRequests) != 1 {
		t.Fatalf("agentRequests=%d, want 1", len(ms.agentRequests))
	}
	if !strings.HasPrefix(ms.agentRequests[0].SessionKey, larkCLITestKey+":") {
		t.Fatalf("sessionKey=%q", ms.agentRequests[0].SessionKey)
	}
	if !strings.Contains(ms.agentRequests[0].Message, "termind_lark_cli_send_command_build") {
		t.Fatalf("prompt did not mention send command tool: %s", ms.agentRequests[0].Message)
	}
}
