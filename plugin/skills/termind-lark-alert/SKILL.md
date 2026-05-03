---
name: termind-lark-alert
description: Build and send Lark/Feishu cards for Termind terminal failure events using safe Termind tools plus available OpenClaw/Lark capabilities.
---

# Termind Lark Alert

Use this skill when OpenClaw receives a Termind terminal failure event and the
user wants a Lark/Feishu notification.

## Flow

1. Call `termind_event_redact` on the failure event.
2. Call `termind_fingerprint_compute` if the event has no fingerprint.
3. Call `termind_failure_classify` to decide severity and routing.
4. Call `termind_lark_card_build` to produce interactive card JSON.
5. Send the returned `card` object with OpenClaw's `message` tool:
   - `action`: `send`
   - `channel`: `feishu`
   - `target`: `event.larkChatId`
   - `card`: the `card` object returned by `termind_lark_card_build`
6. If `message` is not available in the current tool list, return a concise
   failure saying `tools.alsoAllow must include message`; do not claim delivery.

## Rules

- The Termind plugin tools are pure data tools. They do not send messages.
- The specified sender is OpenClaw's `message` tool with `channel=feishu`.
- Do not assume the agent can read shell environment variables. Termind should
  pass `event.larkChatId`; if it is missing, say the target chat id is missing.
- Do not simulate send checks. Only claim delivery when `message` returns ok.
- Do not rewrite card JSON by hand to debug Lark rendering. Use the
  `termind_lark_card_build` output as the source of truth.
- Do not claim `feishu_chat` can send messages. In OpenClaw 2026.4.2 that tool
  is for chat information and members, not IM sending.
- Do not use `lark-cli` in this flow. It remains a manual POC fallback, not the
  Termind -> OpenClaw -> Feishu production path.
- Never send secrets, cookies, private keys, bearer tokens, or full environment
  dumps.
- Use low-noise routing: `info` stays local, `warning` can go to a private or
  test chat, `incident` can go to a team chat.
- Do not ask the user to enable a capability if a lower-capability fallback can
  still produce useful output.

## Message Tool Pattern

```json
{
  "action": "send",
  "channel": "feishu",
  "target": "oc_xxx",
  "card": { "config": { "wide_screen_mode": true }, "header": {}, "elements": [] }
}
```

This requires OpenClaw's agent tool allowlist to include `message`.
