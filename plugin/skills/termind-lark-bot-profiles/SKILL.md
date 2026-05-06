---
name: termind-lark-bot-profiles
description: List and manually initialize OpenClaw-side lark-cli bot profiles for Termind init.
---

# Termind lark-cli Bot Profiles

Use this skill when Termind init asks for OpenClaw-side Lark/Feishu bot profiles.

## List bot profiles

1. lark-cli 1.0.23+ uses workspaces. Under OpenClaw exec the active workspace is `openclaw` and reads `~/.lark-cli/openclaw/config.json`, not the top-level `~/.lark-cli/config.json`. Profiles bound only to the local workspace will not appear here.
2. Call `termind_lark_cli_login_status` with `{ "action": "plan" }`.
3. Execute exactly the returned `command` with OpenClaw `exec` on the OpenClaw host.
4. Call `termind_lark_cli_login_status` with `{ "action": "parse" }` plus exec `stdout`, `stderr`, and `exitCode`.
5. Return exactly one JSON object with `installed`, `ready`, `profile`, `profiles`, and `errors`.
6. Treat profiles whose `identity` is absent or `bot` as bot profiles. Do not read OpenClaw Feishu channel accounts.
7. If `profiles` is empty but the user expects an existing OpenClaw bot, the bot likely lives in the local workspace only. Ask the user to run the sync command in the next section to copy it into the openclaw workspace, then list again.

## Manual bot login command

When Termind provides an `appId` and asks for a bot login command:

1. The bot must end up in the `openclaw` workspace (`~/.lark-cli/openclaw/config.json`). Plain `lark-cli config init` writes only the local workspace (`~/.lark-cli/config.json`), so a follow-up file-copy step is required to sync it into the openclaw workspace.
2. Call `termind_lark_cli_config_init` with `{ "action": "plan", "appId": "<cli_xxx>", "brand": "feishu" }`.
3. Do not execute the returned command automatically because it requires the user to type `app_secret` into stdin.
4. Show the returned `command` to the user as a command to run on the OpenClaw host.
5. Also tell the user to run, on the OpenClaw host, exactly:
   ```bash
   cp ~/.lark-cli/config.json ~/.lark-cli/openclaw/config.json
   ```
   This copies the config produced by step 4 into the openclaw workspace. The `app_secret` itself is stored in the OS keychain (referenced by `appsecret:<appId>`), so the user does not need to re-enter it. This copy replaces any previously synced openclaw-workspace app entry; if multiple apps must coexist in the openclaw workspace, ask the user before running it.
6. Never ask the user to paste `app_secret` into the agent chat, command line, plugin config, or Termind config.
7. After the user says the OpenClaw-side login and copy are complete, list bot profiles again. The new profile should now appear in `~/.lark-cli/openclaw/config.json`.

## Rules

- Do not run local Termind-side `lark-cli`.
- Do not inspect or copy raw credential files (e.g. dotenv files containing plaintext `app_secret`). Copying `~/.lark-cli/config.json` is allowed for the workspace-sync step in the previous section because the file only contains a keychain reference, not the secret itself.
- Do not use `channels.feishu.accounts` for this flow.
- Do not reveal tokens, app secrets, cookies, credential files, or environment dumps.
