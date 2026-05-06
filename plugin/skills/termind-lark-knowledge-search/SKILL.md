---
name: termind-lark-knowledge-search
description: Search Lark/Feishu docs and wiki via lark-cli docs +search using a user-OAuth identity, with graceful fallback when no user identity is available.
---

# Termind Lark Knowledge Search

Use this skill when `termind-knowledge-rag` reaches step 3 (Feishu/Lark docs)
and a Lark user-OAuth profile is available on the OpenClaw side.

## Hard fact

`lark-cli docs +search` is **user-only**. Bot identities are explicitly
rejected by lark-cli:

```text
$ lark-cli docs +search --as bot --query x --dry-run
Error: --as bot is not supported, this command only supports: user
```

Therefore this skill must run under a user-OAuth profile (a profile created
by `lark-cli auth login` and whose `identity` is `user`). If no such profile
exists, return `missingCapability: "user_oauth"` and let the caller degrade.

## Inputs

- The (already redacted) failure event.
- Optional `profile` and `larkCliConfigDir` to pin a specific user identity
  slot (e.g. `LARKSUITE_CLI_CONFIG_DIR=$HOME/.lark-cli/openclaw-user-alice`).

## Flow

1. Verify a user-OAuth profile is available:
   1. Call `termind_lark_cli_login_status` with `{ "action": "plan" }`,
      execute the returned command via OpenClaw `exec`, then call with
      `{ "action": "parse" }` plus the exec output.
   2. If no profile has `identity === "user"`, return
      `{ ok: false, missingCapability: "user_oauth", hits: [] }` and stop.
2. Derive ordered candidate queries from the failure event:
   ```
   termind_lark_knowledge_search { action: "queries", event }
   ```
   Returns `queries[]` ranked from most specific (project + summary) to
   broadest (raw summary).
3. For each query (stop after the first one that returns `hits.length >= 1`,
   or after at most 3 queries):
   1. ```
      termind_lark_knowledge_search {
        action: "plan",
        sender: "user",
        query: "<query>",
        pageSize: 10,
        profile: "<u_xxx>",
        larkCliConfigDir: "<…>"
      }
      ```
   2. Execute the returned `commands[0]` via OpenClaw `exec`.
   3. ```
      termind_lark_knowledge_search {
        action: "parse",
        stdout: "<exec stdout>",
        stderr: "<exec stderr>",
        exitCode: <exec exit code>
      }
      ```
   4. If parse returns `needsUserAuthorization: true`, stop and degrade.
4. Merge the top hits onto `event.knowledgeHits` (max 5). The card and the
   report templates read this field directly.

## Rules

- Never plan with `sender: "bot"` for `docs +search`. The plan tool will
  refuse and emit `missingCapability: "user_oauth"`.
- Never reveal tokens, app secrets, cookies, credential files, or
  environment dumps in the search query.
- Never fabricate hits. If parse returns `hits.length === 0`, leave
  `event.knowledgeHits` empty and let the card render without a knowledge
  block.
- Do not call this skill from the `lark-alert` flow if classify returned
  `severity: "info"` — info events should not block on knowledge search.
- One query at a time. Do not parallelize plans against the same profile to
  avoid rate-limit penalties from Search v2.
- Stop after 3 queries even if none return hits.
