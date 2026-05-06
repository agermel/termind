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
3. Look up the fingerprint in the incident registry (skill
   `termind-incident-registry`):
   1. Call `termind_incident_registry_query` with `action: "plan"`.
   2. Execute the lookup using the first available capability:
      - prefer `memory.get` with the returned `key`,
      - fall back to `kv.get` with the namespace/key in `capabilityHints[1]`,
      - if neither is available, pass `missingCapabilityFallback` directly to
        the parse step. Do not invent results.
   3. Call `termind_incident_registry_query` with `action: "parse"`, supplying
      the raw value from the capability and `branchKind` from the event.
   4. **Keep the raw capability value** (the JSON string from `memory.get` /
      `kv.get`) in scope; step 13 reuses it as `priorRaw` for the upsert plan.
   5. Merge the parsed result into the event before classify and card-build:
      - `event.occurrences       = record.occurrences`
      - `event.affectedUsers     = record.affectedUsers.length`
      - `event.windowOccurrences = record.windowOccurrences`
      - `event.windowMinutes     = record.windowMinutes`
      - `event.firstSeen         = record.firstSeen` (only if non-empty)
      - `event.lastSeen          = record.lastSeen`  (only if non-empty)
      - `event.reportUrl         = record.reportUrl` (only if non-empty)
      - `event.registryBranch    = result.branch`
4. Call `termind_failure_classify`. Classify reads the merged `occurrences`
   and `affectedUsers` to upgrade severity.
5. **Owner resolve (step 12)**: invoke skill `termind-owner-resolve` with the
   git author email/name from the failure event. Merge the resulting
   `owner` onto the event. If the skill returns
   `missingCapability: "user_oauth"`, accept the label-only owner; do not
   block delivery.
6. **Knowledge RAG (step 9)**: if classify returned `severity` ≥ `warning`,
   invoke skill `termind-knowledge-rag`, which delegates to
   `termind-lark-knowledge-search` for the Lark/Feishu doc layer. Merge the
   top hits onto `event.knowledgeHits`. Skip on `severity: "info"`.
7. Call `termind_lark_card_build` with the merged event so the card can
   render the 🆕 / 🔁 / ⛔️ branch styling, the history/escalation block,
   the @-mention owner, the report link, and the related-knowledge block.
8. Call `termind_lark_cli_send_command_build` with the redacted event and card.
9. Execute each returned `lark-cli` command with OpenClaw exec.
10. If exec is unavailable, return a concise failure saying
    `tools.alsoAllow must include exec`; do not claim delivery.
11. If any `lark-cli` command exits non-zero, return the exact stderr/stdout and
    do not try another sender.
12. **Registry write-back (step 13)**: only after every enabled `lark-cli`
    command exits successfully:
    1. If `registryBranch === "new_case"`, the new-case report doc is
       handled by skill `termind-incident-report`. Run that skill and use
       its returned `reportUrl` for the upsert.
    2. If `registryBranch` is `recurrence` or `escalation_candidate`, run
       skill `termind-incident-recurrence` (which appends a row to the
       prior report and writes back the registry).
    3. Otherwise (skills above unavailable), call
       `termind_incident_registry_upsert` directly with `action: "plan"`,
       passing the `priorRaw` saved in step 3.4. Execute the first
       available `memory.set` / `kv.set` capability hint, then call
       `action: "parse"` with the ack. A failed write must not be
       reported to the user as a delivery failure; log it instead.

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
