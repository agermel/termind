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
5. Call `termind_lark_cli_send_command_build` with the redacted event and card.
6. Execute each returned `lark-cli` command with OpenClaw exec.
7. If exec is unavailable, return a concise failure saying
   `tools.alsoAllow must include exec`; do not claim delivery.
8. If any `lark-cli` command exits non-zero, return the exact stderr/stdout and
   do not try another sender.

## Rules

- The Termind plugin tools are pure data tools. They do not send messages.
- The primary sender is `lark-cli im +messages-send` executed by OpenClaw.
- Never send with OpenClaw Feishu tools, the `message` tool, direct Feishu APIs,
  or hand-written fallback scripts. This skill's runtime sender is only
  `lark-cli`.
- Termind should pass `event.larkTargets`; if it is missing, the command builder
  may fall back to `event.larkChatId` or `event.larkUserOpenId`.
- Do not simulate send checks. Only claim delivery after `lark-cli` exits
  successfully for every enabled target.
- Preserve the full event object when moving between tools. Do not drop
  `larkTargets`, `larkSender`, `larkChatId`, or `larkUserOpenId`.
- Do not rewrite card JSON by hand to debug Lark rendering. Use the
  `termind_lark_card_build` output as the source of truth.
- Never send secrets, cookies, private keys, bearer tokens, or full environment
  dumps.
- Use low-noise routing: `info` stays local, `warning` can go to a private or
  test chat, `incident` can go to a team chat.
- Do not use lower-capability fallbacks for delivery. A fallback may explain the
  failure, but it must not send the Lark/Feishu notification.

## lark-cli Pattern

```bash
lark-cli im +messages-send --as bot --chat-id oc_xxx --content '<interactive-card-json>' --msg-type interactive
```

This requires OpenClaw's agent tool allowlist to include `exec` and the exec
approval policy to allow `lark-cli`.
