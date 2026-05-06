# Lark Smoke Tests

This directory contains small, repeatable checks for the Termind -> OpenClaw
-> Feishu/Lark notification path.

The production path uses `lark-cli` as the sender:

```text
Termind failure event
  -> OpenClaw agent
  -> Termind plugin pure tools
  -> termind_lark_cli_send_command_build
  -> OpenClaw exec runs lark-cli im +messages-send
  -> Feishu/Lark interactive card
```

## Prerequisites

- OpenClaw 2026.4.2 or compatible.
- `lark-cli` installed and authenticated.
- Termind plugin installed.
- OpenClaw agent profile includes `exec` and Termind plugin tools:

```bash
openclaw config get tools --json
```

`tools.alsoAllow` should include:

```json
[
  "exec",
  "termind_event_redact",
  "termind_fingerprint_compute",
  "termind_failure_classify",
  "termind_lark_card_build",
  "termind_lark_cli_send_command_build"
]
```

The OpenClaw exec approvals allowlist should allow the `lark-cli` binary:

```bash
openclaw approvals allowlist add "$(command -v lark-cli)"
```

Set the target for the direct lark-cli smoke:

```bash
export TERMIND_LARK_TARGET_ID=oc_xxx
export TERMIND_LARK_TARGET_TYPE=chat
export TERMIND_LARK_SENDER=bot
```

## Smoke 1: Direct lark-cli Card Send

This bypasses the agent and verifies `lark-cli` can send an interactive card:

```bash
examples/lark-smoke/scripts/openclaw-message-card-smoke.sh
```

Expected result: `lark-cli` returns a successful message response.

## Smoke 2: OpenClaw Agent Orchestration

This asks the agent to call the Termind plugin tools, build the card, and send
it by executing the generated `lark-cli` command through OpenClaw exec:

```bash
openclaw agent --local --agent main \
  --session-id termind-lark-message-smoke \
  --message "$(cat examples/lark-smoke/prompts/agent-message-card-smoke.md)" \
  --timeout 180
```

Expected result: agent reports delivery only after every `lark-cli` command
exits successfully.

## Smoke 3: Full Termind Shell Path

Run:

```bash
termind init
TERMIND_DEBUG=1 termind shell
cnmb
```

Expected result:

- Local terminal insight appears.
- Feishu/Lark receives an interactive card.
- Debug log contains `alert: submitted`.

## Notes

`termind init` stores selected Lark targets in `~/.config/termind/config.json`.
The alert event sends `larkTargets` to OpenClaw; the Termind plugin builds
`lark-cli im +messages-send` commands for those targets.
