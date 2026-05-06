---
name: termind-lark-alert
description: Build and send Lark / Feishu interactive cards for Termind terminal failure events using safe Termind tools plus lark-cli.
---

# Termind Lark Alert

Use this skill when OpenClaw receives a Termind terminal failure event and the
user wants a Lark / Feishu notification.

## Flow

1. Call `termind_event_redact` on the failure event.
2. Call `termind_fingerprint_compute` if the event has no fingerprint.
3. Call `termind_failure_classify` to decide severity and routing.
4. Call `termind_lark_card_build` to produce interactive card JSON.
5. Send the card with `lark-cli`:
   ```bash
   lark-cli message send --channel feishu --target <chatId> --card '<json>'
   ```
   - `<chatId>` is the `event.larkChatId` value.
   - `<json>` is the `card` object from `termind_lark_card_build`, serialized as
     a single-line JSON string.
6. Only claim delivery after `lark-cli` returns a `messageId`.

## Rules

- The Termind plugin tools are pure data tools. They do not send messages.
- The sender is `lark-cli message send --channel feishu`.
- Do not use OpenClaw's `message` tool or `feishu_chat` for sending in this flow.
- Do not assume the agent can read shell environment variables. Termind CLI
  should pass `event.larkChatId`; if it is missing, say the target chat id is
  missing.
- Do not simulate send checks. Only claim delivery when `lark-cli` returns a
  `messageId`.
- Do not rewrite card JSON by hand to debug Lark rendering. Use the
  `termind_lark_card_build` output as the source of truth.
- Never send secrets, cookies, private keys, bearer tokens, or full environment
  dumps.
- Use low-noise routing: `info` stays local, `warning` can go to a private or
  test chat, `incident` can go to a team chat.
