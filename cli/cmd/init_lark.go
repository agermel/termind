package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

type larkTargetChoice struct {
	Type  string
	ID    string
	Label string
}

var termindOpenClawAllowedTools = []string{
	"exec",
	"process",
	"termind_event_redact",
	"termind_fingerprint_compute",
	"termind_failure_classify",
	"termind_lark_card_build",
	"termind_lark_cli_discover",
	"termind_lark_cli_auth_login",
	"termind_lark_cli_config_bind",
	"termind_lark_cli_doctor_status",
	"termind_lark_cli_exists",
	"termind_lark_cli_identity_status",
	"termind_lark_cli_login_status",
	"termind_lark_cli_profile_use",
	"termind_lark_cli_status",
	"termind_lark_cli_send_command_build",
	"termind_report_template_build",
}

const termindOpenClawPluginSpec = "termind-openclaw-plugin@dev"

func runLarkInitTUI(ctx context.Context, in io.Reader, out io.Writer, openClawGatewayURL string, localOpenClaw bool) error {
	return runLarkInitBubbleTea(ctx, in, out, openClawGatewayURL, localOpenClaw)
}

func promptLine(ctx context.Context, reader *bufio.Reader, out io.Writer, label string, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(out, "%s [%s]: ", label, def)
	} else {
		fmt.Fprintf(out, "%s: ", label)
	}
	type promptResult struct {
		line string
		err  error
	}
	done := make(chan promptResult, 1)
	go func() {
		line, err := reader.ReadString('\n')
		done <- promptResult{line: line, err: err}
	}()
	var result promptResult
	select {
	case <-ctx.Done():
		fmt.Fprintln(out)
		return "", ctx.Err()
	case result = <-done:
	}
	if result.err != nil && result.err != io.EOF {
		return "", result.err
	}
	value := strings.TrimSpace(result.line)
	if value == "" {
		return def, nil
	}
	return value, nil
}

func promptConfirm(ctx context.Context, reader *bufio.Reader, out io.Writer, label string, def bool) (bool, error) {
	defaultText := "y/N"
	if def {
		defaultText = "Y/n"
	}
	for {
		answer, err := promptLine(ctx, reader, out, label+" ("+defaultText+")", "")
		if err != nil {
			return false, err
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "":
			return def, nil
		case "y", "yes", "是", "好":
			return true, nil
		case "n", "no", "否", "不":
			return false, nil
		default:
			fmt.Fprintln(out, "请输入 y 或 n。")
		}
	}
}

func appendSelectedTargets(ctx context.Context, reader *bufio.Reader, out io.Writer, targets []larkTargetChoice, choices []larkTargetChoice) ([]larkTargetChoice, error) {
	if len(choices) == 0 {
		fmt.Fprintln(out, "没有找到可选群聊。")
		return targets, nil
	}
	if len(choices) > 20 {
		choices = choices[:20]
	}
	fmt.Fprintln(out, "可选群聊:")
	for i, choice := range choices {
		fmt.Fprintf(out, "  %d. %s %s\n", i+1, choice.ID, choice.Label)
	}
	line, err := promptLine(ctx, reader, out, "选择群聊编号(逗号分隔,留空跳过)", "")
	if err != nil {
		return targets, err
	}
	if line == "" {
		return targets, nil
	}
	for _, part := range strings.Split(line, ",") {
		idx, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || idx <= 0 || idx > len(choices) {
			continue
		}
		targets = appendTarget(targets, choices[idx-1])
	}
	return targets, nil
}

func appendTarget(targets []larkTargetChoice, target larkTargetChoice) []larkTargetChoice {
	target.Type = normalizeTargetType(target.Type)
	target.ID = strings.TrimSpace(target.ID)
	target.Label = strings.TrimSpace(target.Label)
	if target.ID == "" {
		return targets
	}
	for i, existing := range targets {
		if normalizeTargetType(existing.Type) == target.Type && strings.TrimSpace(existing.ID) == target.ID {
			if target.Label != "" {
				targets[i].Label = target.Label
			}
			return targets
		}
	}
	return append(targets, target)
}

func normalizeSender(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), "user") {
		return "user"
	}
	return "bot"
}

func normalizeTargetType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "user", "bot":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "chat"
	}
}

func findChatChoices(v any) []larkTargetChoice {
	seen := map[string]bool{}
	choices := make([]larkTargetChoice, 0)
	var walk func(any)
	walk = func(value any) {
		switch x := value.(type) {
		case map[string]any:
			if id, ok := stringValue(x["chat_id"]); ok && id != "" && !seen[id] {
				seen[id] = true
				label, _ := stringValue(x["name"])
				if label == "" {
					label, _ = stringValue(x["description"])
				}
				choices = append(choices, larkTargetChoice{Type: "chat", ID: id, Label: label})
			}
			keys := make([]string, 0, len(x))
			for key := range x {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				walk(x[key])
			}
		case []any:
			for _, item := range x {
				walk(item)
			}
		}
	}
	walk(v)
	return choices
}

func findUserChoices(v any) []larkTargetChoice {
	seen := map[string]bool{}
	choices := make([]larkTargetChoice, 0)
	var walk func(any)
	walk = func(value any) {
		switch x := value.(type) {
		case map[string]any:
			if id, ok := stringValue(x["open_id"]); ok && id != "" && !seen[id] {
				seen[id] = true
				label, _ := stringValue(x["name"])
				if label == "" {
					label, _ = stringValue(x["localized_name"])
				}
				choices = append(choices, larkTargetChoice{Type: "user", ID: id, Label: label})
			}
			keys := make([]string, 0, len(x))
			for key := range x {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				walk(x[key])
			}
		case []any:
			for _, item := range x {
				walk(item)
			}
		}
	}
	walk(v)
	return choices
}

func firstStringForKey(v any, key string) string {
	switch x := v.(type) {
	case map[string]any:
		if s, ok := stringValue(x[key]); ok && s != "" {
			return s
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if s := firstStringForKey(x[k], key); s != "" {
				return s
			}
		}
	case []any:
		for _, item := range x {
			if s := firstStringForKey(item, key); s != "" {
				return s
			}
		}
	}
	return ""
}

func stringValue(v any) (string, bool) {
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(s), true
}

func testLarkCardContent() string {
	card := map[string]any{
		"config": map[string]any{
			"wide_screen_mode": true,
		},
		"header": map[string]any{
			"template": "green",
			"title": map[string]string{
				"tag":     "plain_text",
				"content": "termind init · Lark card test",
			},
		},
		"elements": []map[string]any{
			{
				"tag": "div",
				"text": map[string]string{
					"tag":     "lark_md",
					"content": "**termind:** Lark/Feishu interactive card test",
				},
			},
		},
	}
	b, _ := json.Marshal(card)
	return string(b)
}

func configureOpenClawForLarkCLI(ctx context.Context, reader *bufio.Reader, out io.Writer) error {
	if ok, err := promptConfirm(ctx, reader, out, "安装/刷新 Termind OpenClaw 插件", true); err != nil {
		return err
	} else if ok {
		pluginSpec := termindOpenClawPluginSpec
		var err error
		pluginSpec, err = promptLine(ctx, reader, out, "Termind OpenClaw 插件 npm spec", pluginSpec)
		if err != nil {
			return err
		}
		if strings.TrimSpace(pluginSpec) != "" {
			runStatusCommand(ctx, out, "openclaw plugins install", "openclaw", "plugins", "install", strings.TrimSpace(pluginSpec))
			runStatusCommand(ctx, out, "openclaw plugins enable termind", "openclaw", "plugins", "enable", "termind")
		}
	}

	if ok, err := promptConfirm(ctx, reader, out, "配置 OpenClaw tools.alsoAllow", true); err != nil {
		return err
	} else if ok {
		if err := configureOpenClawToolAllowlist(ctx, out); err != nil {
			fmt.Fprintf(out, "配置 tools.alsoAllow 失败: %v\n", err)
		}
	}

	if ok, err := promptConfirm(ctx, reader, out, "把 lark-cli 加入 OpenClaw exec approvals allowlist", true); err != nil {
		return err
	} else if ok {
		runStatusCommand(ctx, out, "openclaw approvals allowlist add", "openclaw", "approvals", "allowlist", "add", "lark-cli")
	}
	if ok, err := promptConfirm(ctx, reader, out, "重启 OpenClaw Gateway 让配置生效", true); err != nil {
		return err
	} else if ok {
		runStatusCommand(ctx, out, "openclaw gateway restart", "openclaw", "gateway", "restart")
	}
	return nil
}

func configureOpenClawToolAllowlist(ctx context.Context, out io.Writer) error {
	current := make([]string, 0)
	if output, err := runOutput(ctx, 10*time.Second, "openclaw", "config", "get", "tools", "--json"); err == nil {
		var raw map[string]any
		if json.Unmarshal(output, &raw) == nil {
			if values, ok := raw["alsoAllow"].([]any); ok {
				for _, value := range values {
					if s, ok := value.(string); ok {
						current = append(current, s)
					}
				}
			}
		}
	}
	current = mergeStrings(current, termindOpenClawAllowedTools)
	b, err := json.Marshal(current)
	if err != nil {
		return err
	}
	output, err := runOutput(ctx, 20*time.Second, "openclaw", "config", "set", "tools.alsoAllow", string(b), "--strict-json")
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	fmt.Fprintln(out, "✓ OpenClaw tools.alsoAllow 已包含 exec、process 和 Termind Lark 工具")
	return nil
}

func mergeStrings(values []string, required []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values)+len(required))
	for _, value := range append(values, required...) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func runStatusCommand(ctx context.Context, out io.Writer, label string, name string, args ...string) {
	output, err := runOutput(ctx, 30*time.Second, name, args...)
	if err != nil {
		text := strings.TrimSpace(string(output))
		if label == "openclaw plugins install" && strings.Contains(text, "plugin already exists") {
			fmt.Fprintf(out, "✓ %s 已存在,跳过\n", label)
			fmt.Fprintln(out, "  如需刷新,请先在 OpenClaw 侧删除旧插件目录后重跑。")
			return
		}
		fmt.Fprintf(out, "✗ %s: %v\n", label, err)
		if text != "" {
			fmt.Fprintln(out, text)
		}
		return
	}
	fmt.Fprintf(out, "✓ %s\n", label)
}

func runOutput(ctx context.Context, timeout time.Duration, name string, args ...string) ([]byte, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, name, args...)
	return cmd.CombinedOutput()
}

func isLocalOpenClawGatewayURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func configureRemoteOpenClawForLarkCLI(ctx context.Context, reader *bufio.Reader, in io.Reader, out io.Writer, openClawGatewayURL string) error {
	printRemoteOpenClawLarkInstructions(out, openClawGatewayURL)
	ok, err := promptConfirm(ctx, reader, out, "通过 SSH 辅助配置远程 OpenClaw", false)
	if err != nil || !ok {
		return err
	}
	if _, err := exec.LookPath("ssh"); err != nil {
		fmt.Fprintln(out, "未找到 ssh,无法自动配置远程 OpenClaw。")
		return nil
	}
	target, err := promptLine(ctx, reader, out, "SSH 目标(user@host,留空跳过)", "")
	if err != nil {
		return err
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return nil
	}
	if ok, err := promptConfirm(ctx, reader, out, "安装/刷新远程 Termind OpenClaw 插件", true); err != nil {
		return err
	} else if ok {
		pluginSpec, err := promptLine(ctx, reader, out, "Termind OpenClaw 插件 npm spec", termindOpenClawPluginSpec)
		if err != nil {
			return err
		}
		if strings.TrimSpace(pluginSpec) != "" {
			runRemoteStatusCommand(ctx, out, "remote openclaw plugins install/enable", target, "openclaw plugins install "+shellQuote(strings.TrimSpace(pluginSpec))+"; openclaw plugins enable termind")
		}
	}
	allowJSON, _ := json.Marshal(termindOpenClawAllowedTools)
	if ok, err := promptConfirm(ctx, reader, out, "配置远程 OpenClaw tools.alsoAllow", true); err != nil {
		return err
	} else if ok {
		runRemoteStatusCommand(ctx, out, "remote openclaw config set tools.alsoAllow", target, "openclaw config set tools.alsoAllow "+shellQuote(string(allowJSON))+" --strict-json")
	}
	if ok, err := promptConfirm(ctx, reader, out, "把远程 lark-cli 加入 OpenClaw exec approvals allowlist", true); err != nil {
		return err
	} else if ok {
		runRemoteStatusCommand(ctx, out, "remote openclaw approvals allowlist add", target, "openclaw approvals allowlist add lark-cli")
	}
	if ok, err := promptConfirm(ctx, reader, out, "重启远程 OpenClaw Gateway 让配置生效", true); err != nil {
		return err
	} else if ok {
		runRemoteStatusCommand(ctx, out, "remote openclaw gateway restart", target, "openclaw gateway restart")
	}
	return nil
}

func printRemoteOpenClawLarkInstructions(out io.Writer, openClawGatewayURL string) {
	allowJSON, _ := json.Marshal(termindOpenClawAllowedTools)
	fmt.Fprintln(out, "OpenClaw 不在本机,已跳过本机 OpenClaw plugin/tools 自动配置。")
	fmt.Fprintf(out, "运行时发 Lark 消息发生在 OpenClaw 所在机器: %s\n", openClawGatewayURL)
	fmt.Fprintln(out, "请在那台机器上确认:")
	fmt.Fprintln(out, "  1. lark-cli 已安装,并且 lark-cli doctor 通过")
	fmt.Fprintln(out, "  2. lark-cli 已完成 user/bot 凭证初始化")
	fmt.Fprintln(out, "  3. Termind OpenClaw 插件已安装并启用")
	fmt.Fprintln(out, "  4. OpenClaw tools.alsoAllow 包含 exec、process 和 Termind Lark 工具")
	fmt.Fprintln(out, "  5. OpenClaw exec approvals allowlist 允许 lark-cli")
	fmt.Fprintln(out, "  6. OpenClaw Gateway 已重启加载新配置")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "参考命令:")
	fmt.Fprintf(out, "  openclaw plugins install %s\n", termindOpenClawPluginSpec)
	fmt.Fprintln(out, "  openclaw plugins enable termind")
	fmt.Fprintf(out, "  openclaw config set tools.alsoAllow '%s' --strict-json\n", string(allowJSON))
	fmt.Fprintln(out, "  openclaw approvals allowlist add lark-cli")
	fmt.Fprintln(out, "  openclaw gateway restart")
}

func runRemoteStatusCommand(ctx context.Context, out io.Writer, label string, target string, remoteCommand string) {
	output, err := runOutput(ctx, 60*time.Second, "ssh", target, remoteCommand)
	if err != nil {
		text := strings.TrimSpace(string(output))
		if strings.Contains(label, "plugins install") && strings.Contains(text, "plugin already exists") {
			fmt.Fprintf(out, "✓ %s 已存在,跳过\n", label)
			return
		}
		fmt.Fprintf(out, "✗ %s: %v\n", label, err)
		if text != "" {
			fmt.Fprintln(out, text)
		}
		return
	}
	fmt.Fprintf(out, "✓ %s\n", label)
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("_@%+=:,./~-", r))
	}) == -1 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
