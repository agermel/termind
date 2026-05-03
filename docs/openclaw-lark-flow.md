# Termind OpenClaw Lark Flow Handoff

This document is the team handoff for the current Termind -> OpenClaw ->
Feishu/Lark alert chain.

## Current Architecture

```text
Developer shell
  -> termind shell integration captures command + exit + tail output
  -> termind CLI connects to OpenClaw Gateway as an approved operator device
  -> termind submits an OpenClaw agent request with the failure event
  -> OpenClaw loads the termind-lark-alert skill
  -> OpenClaw calls Termind plugin pure tools
  -> OpenClaw sends the returned card with message(action=send, channel=feishu)
  -> Feishu/Lark group receives the interactive card
```

The Termind plugin is intentionally side-effect free. It does not execute shell
commands, read environment variables, or send network requests. Sending is owned
by OpenClaw's built-in `message` tool.

## Directory Map

- `cli/`: Go Termind CLI.
- `plugin/`: OpenClaw Termind plugin.
- `plugin/skills/termind-lark-alert/`: Skill that tells OpenClaw how to build
  and send Feishu cards.
- `plugin/examples/lark-smoke/`: Repeatable smoke tests for Feishu card sending.
- `docs/openclaw-lark-flow.md`: This handoff.

Removed/retired:

- `poc/lark`: folded into `plugin/examples/lark-smoke`.
- `experiments/openclaw-plugin`: removed because the early lark-cli/exec-based
  plugin shape triggers OpenClaw dangerous plugin scanning and is not the
  product path.

## Termind CLI Responsibilities

Key files:

- `cli/internal/integration/zsh.zsh`: emits OSC 133 command-start metadata.
- `cli/internal/osc133/parser.go`: parses command metadata.
- `cli/internal/cmdbuf/cmdbuf.go`: stores command text, exit code, and output
  tail.
- `cli/internal/shell/diagnose.go`: dispatches local terminal insight and Lark
  alert handoff.
- `cli/internal/diagnose/client.go`: talks to OpenClaw `agent`,
  `agent.wait`, and `sessions.get`.

When a command exits non-zero, Termind now does two things:

1. Runs the normal local diagnosis path and renders a compact terminal insight.
2. Fire-and-forget submits an alert event to OpenClaw session
   `agent:main:termind-lark-alert`.

`TERMIND_LARK_CHAT_ID` is read by the Termind process and copied into the event
as `larkChatId`. This is necessary because the OpenClaw agent runtime cannot be
assumed to see the user's shell environment.

## OpenClaw Plugin Responsibilities

Key files:

- `plugin/src/index.js`: registers safe tools.
- `plugin/src/lib/redact.js`: normalizes and redacts failure events.
- `plugin/src/lib/fingerprint.js`: computes stable fingerprints.
- `plugin/src/lib/classify.js`: chooses severity/routing hints.
- `plugin/src/lib/card.js`: builds Feishu/Lark interactive card JSON.
- `plugin/src/lib/report.js`: builds incident report templates.

Core tools:

- `termind_event_redact`
- `termind_fingerprint_compute`
- `termind_failure_classify`
- `termind_lark_card_build`
- `termind_report_template_build`

## Required OpenClaw Configuration

Install or refresh the plugin:

```bash
openclaw plugins install /Users/matterhorn/work/lark/plugin
openclaw plugins inspect termind
```

Allow the agent to use Termind tools and the OpenClaw message sender:

```bash
openclaw config set tools.alsoAllow '[
  "browser",
  "message",
  "termind_event_redact",
  "termind_fingerprint_compute",
  "termind_failure_classify",
  "termind_lark_card_build",
  "termind_report_template_build"
]' --strict-json
```

Restart OpenClaw Gateway after config changes:

```bash
openclaw gateway restart
```

Set the target chat id in the shell that starts Termind:

```bash
export TERMIND_LARK_CHAT_ID=oc_xxx
```

## Smoke Tests

### 1. Feishu Channel Only

This checks that OpenClaw can send a card without involving the agent:

```bash
export TERMIND_LARK_CHAT_ID=oc_xxx
plugin/examples/lark-smoke/scripts/openclaw-message-card-smoke.sh
```

Expected output includes:

```json
{
  "ok": true,
  "channel": "feishu",
  "action": "send",
  "messageId": "om_..."
}
```

### 2. Agent Orchestration

This checks the skill/tool/message path:

```bash
openclaw agent --local --agent main \
  --session-id termind-lark-message-smoke \
  --message "$(cat plugin/examples/lark-smoke/prompts/agent-message-card-smoke.md)" \
  --timeout 180
```

Expected result: the agent calls Termind tools, then `message`, then reports
`ok: true` with a Feishu `messageId`.

### 3. Full Termind Shell Path

```bash
export TERMIND_LARK_CHAT_ID=oc_xxx
TERMIND_DEBUG=1 termind shell
cnmb
```

Expected result:

- Local terminal insight appears.
- Feishu/Lark receives a card.
- Debug log contains:

```text
alert: start command="cnmb"
alert: submitted
diagnose: done
```

Debug log path is printed at shell startup when `TERMIND_DEBUG=1`.

## Known Good Verification

The current local end-to-end verification produced:

```text
messageId: om_x100b504fe21e7ca4c3afd1ff8d1a3fb
chatId: oc_4bf56e2d154b54e29c4837e44b17433d
```

Tests run:

```bash
cd cli && go test ./...
cd plugin && npm test
```

## Common Failure Modes

### Agent says `tools.alsoAllow must include message`

OpenClaw loaded the skill but did not expose the `message` tool to the agent.
Add `message` to `tools.alsoAllow` and restart the gateway.

### Local insight appears but Feishu receives nothing

Check:

- `TERMIND_LARK_CHAT_ID` is exported before starting `termind shell`.
- `TERMIND_DEBUG=1` log contains `alert: submitted`.
- OpenClaw session `agent:main:termind-lark-alert` has a `message` tool call.
- Gateway was restarted after plugin/config changes.

### `feishu_chat` appears in agent output

That is the wrong sender. In OpenClaw 2026.4.2, `feishu_chat` is for chat/member
information, not card sending. The sender must be `message` with
`channel=feishu`.

### Duplicate plugin id warning

If OpenClaw warns about duplicate `termind` plugins, clean up old installed
copies or stale config entries. It usually does not block runtime, but it can
confuse debugging because OpenClaw may load one copy while the team edits
another.

## Team Work Split

- CLI owner: harden shell capture, command summaries, environment metadata, and
  failure-event shaping.
- Plugin owner: improve redaction, fingerprints, classification, card rendering,
  and report templates.
- OpenClaw integration owner: plugin install/config script, gateway restart
  guidance, Feishu channel validation, and operator troubleshooting.
- Product/knowledge owner: define incident thresholds, report lifecycle,
  knowledge base linking, dedupe/escalation policy, and Feishu card actions.

## Next TODO

- Add a Termind init/check command that validates OpenClaw plugin installed,
  `tools.alsoAllow` contains `message`, Feishu channel is configured, and
  `TERMIND_LARK_CHAT_ID` is set.
- Add a config file field for Lark chat routing instead of relying only on an
  environment variable.
- Add dedupe/throttle so repeated typo commands do not spam Feishu.
- Improve summary extraction so prompts like `%` are not used as alert titles.
- Add project/git metadata enrichment in the CLI before submitting events.
- Add structured report creation/update flow after the card MVP stabilizes.
