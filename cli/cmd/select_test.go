package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"termind/internal/config"
	"termind/internal/diagnose"
)

func TestRunSelectChat_RejectsWhenServerURLEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetIn(strings.NewReader(""))
	cmd.SetContext(context.Background())

	err := runSelectChat(cmd, nil)
	if err == nil {
		t.Fatal("runSelectChat should error when OpenClaw is not configured")
	}
	if !strings.Contains(err.Error(), "OpenClaw") {
		t.Fatalf("error should mention OpenClaw: %v", err)
	}
}

func TestRunSelectChat_RejectsWhenNoIdentities(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfgDir := filepath.Join(home, ".config", "termind")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfgJSON, _ := json.Marshal(map[string]any{
		"server_url": "ws://127.0.0.1:18789/v1/gateway",
		"role":       "operator",
	})
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), cfgJSON, 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetIn(strings.NewReader(""))
	cmd.SetContext(context.Background())

	err := runSelectChat(cmd, nil)
	if err == nil {
		t.Fatal("runSelectChat should error when no Lark identity is bound")
	}
	if !strings.Contains(err.Error(), "Lark") || !strings.Contains(err.Error(), "身份") {
		t.Fatalf("error should mention missing Lark identity: %v", err)
	}
}

func TestSelectChatPreload_KeepsIdentitiesRoutesAndStartsAtTargetStep(t *testing.T) {
	cfg := &config.Config{
		ServerURL: "ws://127.0.0.1:18789/v1/gateway",
		Lark: config.LarkConfig{
			UserOpenID: "ou_self",
			Sender:     "bot",
			Targets: []config.LarkTarget{
				{Type: "chat", ID: "oc_existing", Label: "old chat", Enabled: true},
			},
			Forwarding: config.LarkForwardingConfig{
				Version: 1,
				Identities: map[string]config.LarkForwardingIdentity{
					"bot:cli_a": {
						ID:      "bot:cli_a",
						Kind:    "bot",
						Label:   "Alice Bot",
						AppID:   "cli_a",
						Profile: "cli_a",
						Source:  "lark-cli",
						Enabled: true,
					},
				},
				Routes: []config.LarkForwardingRoute{
					{
						IdentityID: "bot:cli_a",
						Target:     config.LarkTarget{Type: "chat", ID: "oc_route", Label: "routed chat", Enabled: true},
						Enabled:    true,
					},
				},
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, cfg, cfg.ServerURL, true)

	// 复用 runLarkSelectChatTUI 的预填逻辑(直接调用其行为)
	for id, identity := range cfg.Lark.Forwarding.Identities {
		model.identities[id] = diagnose.LarkForwardingIdentity{
			ID:      identity.ID,
			Kind:    identity.Kind,
			Label:   identity.Label,
			AppID:   identity.AppID,
			Profile: identity.Profile,
			Source:  identity.Source,
			Enabled: identity.Enabled,
		}
		model.identityOrder = append(model.identityOrder, id)
	}
	for _, route := range cfg.Lark.Forwarding.Routes {
		model.routes = append(model.routes, diagnose.LarkForwardingRoute{
			IdentityID: route.IdentityID,
			Target: diagnose.LarkTarget{
				Type:    route.Target.Type,
				ID:      route.Target.ID,
				Label:   route.Target.Label,
				Enabled: route.Target.Enabled,
			},
			Enabled: route.Enabled,
		})
	}
	model.openClawSetupDone = true

	next, _ := model.prepareKeepExisting()
	got := next.(*larkInitModel)

	// 已有 1 个 target,prepareKeepExisting 应该停在 KeepExisting 让用户决定。
	if got.step != larkStepKeepExisting {
		t.Fatalf("step=%d, want larkStepKeepExisting(%d)", got.step, larkStepKeepExisting)
	}
	// 进度条应该显示在第 5 步「目标」。
	if progress := got.stepProgress(); progress != 5 {
		t.Fatalf("stepProgress=%d, want 5(目标)", progress)
	}
	// Identities / Routes 必须保留。
	if len(got.identities) != 1 {
		t.Fatalf("identities=%d, want 1", len(got.identities))
	}
	if _, ok := got.identities["bot:cli_a"]; !ok {
		t.Fatalf("identity bot:cli_a missing: %+v", got.identities)
	}
	if len(got.routes) != 1 {
		t.Fatalf("routes=%d, want 1", len(got.routes))
	}
	if got.routes[0].IdentityID != "bot:cli_a" || got.routes[0].Target.ID != "oc_route" {
		t.Fatalf("unexpected route: %+v", got.routes[0])
	}
}

func TestSelectChatPreload_NoExistingTargets_SkipsToSearch(t *testing.T) {
	cfg := &config.Config{
		ServerURL: "ws://127.0.0.1:18789/v1/gateway",
		Lark: config.LarkConfig{
			Sender: "bot",
			// 注意: 没有 UserOpenID,prepareAddSelf 会跳到 prepareSearchChats。
			Targets: nil,
			Forwarding: config.LarkForwardingConfig{
				Version: 1,
				Identities: map[string]config.LarkForwardingIdentity{
					"bot:cli_a": {ID: "bot:cli_a", Kind: "bot", AppID: "cli_a", Source: "lark-cli", Enabled: true},
				},
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := newLarkInitModel(ctx, cancel, strings.NewReader(""), &bytes.Buffer{}, cfg, cfg.ServerURL, true)
	for id, identity := range cfg.Lark.Forwarding.Identities {
		model.identities[id] = diagnose.LarkForwardingIdentity{
			ID: identity.ID, Kind: identity.Kind, AppID: identity.AppID, Source: identity.Source, Enabled: identity.Enabled,
		}
		model.identityOrder = append(model.identityOrder, id)
	}
	model.openClawSetupDone = true

	next, _ := model.prepareKeepExisting()
	got := next.(*larkInitModel)

	// 没有 targets 也没有 userOpenID,prepareKeepExisting → prepareAddSelf → prepareSearchChats。
	if got.step != larkStepSearchChats {
		t.Fatalf("step=%d, want larkStepSearchChats(%d)", got.step, larkStepSearchChats)
	}
	if progress := got.stepProgress(); progress != 5 {
		t.Fatalf("stepProgress=%d, want 5", progress)
	}
}
