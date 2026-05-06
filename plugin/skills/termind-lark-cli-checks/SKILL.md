---
name: termind-lark-cli-checks
description: Fast staged lark-cli readiness checks for Termind init using one OpenClaw exec command per stage and early stop on failure.
---

# Termind lark-cli Checks

Use this skill when Termind init asks whether OpenClaw-side `lark-cli` is ready.

## Flow

1. Call `termind_lark_cli_exists` with `{ "action": "plan" }`.
2. Execute exactly the returned `command` with OpenClaw `exec`.
3. Call `termind_lark_cli_exists` with `{ "action": "parse" }` plus exec `stdout`, `stderr`, and `exitCode`.
4. If `installed` is false, return `status` immediately.
5. Call `termind_lark_cli_login_status` with `{ "action": "plan" }`.
6. Execute exactly the returned `command` with OpenClaw `exec`.
7. Call `termind_lark_cli_login_status` with `{ "action": "parse" }` plus exec output.
8. If `loggedIn` is false, return `status` immediately.
9. Do not require `termind_lark_cli_identity_status` for readiness. A bot app active profile is valid for Termind.
10. For maximum speed, skip doctor unless Termind explicitly asks for doctor detail. If doctor is needed, call `termind_lark_cli_doctor_status`, execute its one command, parse, and merge `statusPatch`.
11. Return exactly one JSON object with `installed`, `ready`, `profile`, `profiles`, `doctor`, `auth`, and `errors`.

## Rules

- Do not run local Termind-side `lark-cli`.
- Do not call `tools.invoke` directly from Termind.
- Do not execute broad diagnostics before the fast checks.
- Each stage may execute at most one `lark-cli` command.
- Stop as soon as a required stage fails.
- Never reveal tokens, app secrets, cookies, credential files, or environment dumps.
