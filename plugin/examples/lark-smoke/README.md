# Lark Smoke Tests

This directory contains small, repeatable checks for the Termind -> OpenClaw
-> Feishu/Lark notification path.

The production path does not use `lark-cli` as the sender:

```text
Termind failure event
  -> OpenClaw agent
  -> Termind plugin pure tools
  -> OpenClaw message tool (channel=feishu, card=...)
  -> Feishu/Lark interactive card
```

## Prerequisites

- OpenClaw 2026.4.2 or compatible.
- Feishu channel configured in OpenClaw.
- Termind plugin installed.
- OpenClaw agent profile includes the `message` tool:

```bash
openclaw config get tools --json
```

`tools.alsoAllow` should include:

```json
[
  "message",
  "termind_event_redact",
  "termind_fingerprint_compute",
  "termind_failure_classify",
  "termind_lark_card_build"
]
```

Set the target chat id:

```bash
export TERMIND_LARK_CHAT_ID=oc_xxx
```

## Smoke 1: OpenClaw Feishu Channel

This bypasses the agent and verifies OpenClaw can send an interactive card to
Feishu:

```bash
plugin/examples/lark-smoke/scripts/openclaw-message-card-smoke.sh
```

Expected result: JSON output with `ok: true`, `channel: "feishu"`, and a
`messageId`.

## Smoke 2: OpenClaw Agent Orchestration

This asks the agent to call the Termind plugin tools, build the card, and send
it with OpenClaw's `message` tool:

```bash
openclaw agent --local --agent main \
  --session-id termind-lark-message-smoke \
  --message "$(cat plugin/examples/lark-smoke/prompts/agent-message-card-smoke.md)" \
  --timeout 180
```

Expected result: agent reports delivery only after the `message` tool returns
`ok: true`.

## Smoke 3: Full Termind Shell Path

Run:

```bash
export TERMIND_LARK_CHAT_ID=oc_xxx
TERMIND_DEBUG=1 termind shell
cnmb
```

Expected result:

- Local terminal insight appears.
- Feishu/Lark receives an interactive card.
- Debug log contains `alert: submitted`.

## Notes

`feishu_chat` is useful for chat/member information, but in OpenClaw 2026.4.2
it is not the send-card path. Card delivery should go through `message` with
`channel=feishu`.
