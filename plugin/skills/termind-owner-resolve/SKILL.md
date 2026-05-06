---
name: termind-owner-resolve
description: Resolve a git author -> Lark open_id for Termind incident routing using lark-cli contact +search-user under a user-OAuth identity, with graceful label-only fallback.
---

# Termind Owner Resolve

Use this skill when the `termind-lark-alert` or `termind-incident-report`
skill needs to @-mention a responsible engineer.

## Hard fact

`lark-cli contact +search-user` requires `--as user`. Bot identities cannot
search the contact directory. If no user-OAuth profile is available, this
skill must fall back to a `label_only` owner that displays the git author
name without an @-mention.

## Inputs

- `authorEmail` and/or `authorName` from `git log -1 --pretty=...` on the
  failing commit.
- Optional `profile` and `larkCliConfigDir` for the user-OAuth identity slot.

## Flow

1. If `authorEmail` is empty and `authorName` is empty, return `owner: null`
   and stop.
2. Plan the search:
   ```
   termind_owner_resolve {
     action: "plan",
     sender: "user",
     email: "<authorEmail>",
     name: "<authorName>",
     profile: "<u_xxx>",
     larkCliConfigDir: "<…>"
   }
   ```
   The tool prefers `email` (precision high) and falls back to `name`.
3. If plan returns `missingCapability: "user_oauth"`, take the
   `labelOnlyOwner` field from the plan response as the result and stop.
4. Otherwise execute the returned `commands[0]` via OpenClaw `exec`.
5. Parse:
   ```
   termind_owner_resolve {
     action: "parse",
     stdout: "<exec stdout>",
     stderr: "<exec stderr>",
     exitCode: <exec exit code>,
     email: "<authorEmail>",
     name: "<authorName>",
     queryKind: "<email|name from plan output>"
   }
   ```
6. The parse result contains `owner` with one of three confidences:
   - `high` — email-based exact match. Card may @-mention.
   - `medium` — name-based exact match. Card may @-mention with caution.
   - `label_only` — no exact match. Card must not @-mention; show name only.
7. Merge `owner` onto the failure event before invoking
   `termind_lark_card_build`.

## Rules

- Never plan with `sender: "bot"`; the plan tool will refuse and return a
  `labelOnlyOwner` fallback.
- Never @-mention an open_id whose match confidence is `label_only` or
  whose owner came from a stale registry record. Fall back to the git
  author label.
- Never reveal user emails, phone numbers, or department info in the card.
  Only `openId` and `label` may appear; `email` is shown only when the
  owner is `label_only` and the user's identity cannot be confirmed.
- One search call per alert. Do not retry with broader queries if the
  precise email returns no hit; surface ambiguous candidates in the
  report draft instead.
