# Termind OpenClaw Lark Flow Handoff

This document is the team handoff for the current Termind -> OpenClaw ->
Feishu/Lark alert chain.

## Current Architecture

```text
Developer shell
  -> termind shell integration captures command + exit + tail output
  -> termind CLI connects to OpenClaw Gateway as an approved operator device
  -> termind submits an OpenClaw agent request with the failure event and larkTargets
  -> OpenClaw loads the termind-lark-alert skill
  -> OpenClaw calls Termind plugin pure tools
  -> Termind plugin builds interactive card JSON and lark-cli argv
  -> OpenClaw exec runs lark-cli im +messages-send
  -> Feishu/Lark group receives the interactive card
```

The Termind plugin is intentionally side-effect free. It does not execute shell
commands, read environment variables, or send network requests. It builds card
JSON and controlled `lark-cli` command arguments. Sending is owned by OpenClaw
exec running `lark-cli`.

## Directory Map

- `cli/`: Go Termind CLI.
- `plugin/`: OpenClaw Termind plugin.
- `plugin/skills/termind-lark-alert/`: Skill that tells OpenClaw how to build
  and send Feishu cards.
- `plugin/examples/lark-smoke/`: Repeatable smoke tests for Feishu card sending.
- `docs/openclaw-lark-flow.md`: This handoff.

Removed/retired:

- `poc/lark`: folded into `plugin/examples/lark-smoke`.
- `experiments/openclaw-plugin`: folded into the safe plugin shape where
  Termind tools only generate data and OpenClaw owns command execution.

## Termind CLI Responsibilities

Key files:

- `cli/internal/integration/zsh.zsh`: emits OSC 133 command-start metadata.
- `cli/internal/osc133/parser.go`: parses command metadata.
- `cli/internal/cmdbuf/cmdbuf.go`: stores command text, exit code, and output
  tail.
- `cli/internal/shell/diagnose.go`: dispatches local terminal insight and Lark
  alert handoff.
- `cli/internal/diagnose/client.go`: talks to OpenClaw `agent`,
  `agent.wait`, and `sessions.get`.

When a command exits non-zero, Termind now does two things:

1. Runs the normal local diagnosis path and renders a compact terminal insight.
2. Fire-and-forget submits an alert event to OpenClaw session
   `agent:main:termind-lark-alert`.

Lark routing is stored in `~/.config/termind/config.json` under `lark.targets`.
Termind copies these targets into the alert event as `larkTargets`, so the agent
does not need to read shell environment variables.

## OpenClaw Plugin Responsibilities

Key files:

- `plugin/src/index.js`: registers safe tools.
- `plugin/src/lib/redact.js`: normalizes and redacts failure events.
- `plugin/src/lib/fingerprint.js`: computes stable fingerprints.
- `plugin/src/lib/classify.js`: chooses severity/routing hints.
- `plugin/src/lib/card.js`: builds Feishu/Lark interactive card JSON.
- `plugin/src/lib/report.js`: builds incident report templates.

Core tools:

- `termind_event_redact`
- `termind_fingerprint_compute`
- `termind_failure_classify`
- `termind_lark_card_build`
- `termind_lark_cli_send_command_build`
- `termind_report_template_build`

## Required OpenClaw Configuration

Install or refresh the plugin:

```bash
openclaw plugins install termind-openclaw-plugin@dev
openclaw plugins inspect termind
```

Allow the agent to use Termind tools and OpenClaw exec:

```bash
openclaw config set tools.alsoAllow '[
  "browser",
  "exec",
  "termind_event_redact",
  "termind_fingerprint_compute",
  "termind_failure_classify",
  "termind_lark_card_build",
  "termind_lark_cli_send_command_build",
  "termind_report_template_build"
]' --strict-json
```

Allow OpenClaw exec to run `lark-cli`:

```bash
openclaw approvals allowlist add "$(command -v lark-cli)"
```

Restart OpenClaw Gateway after config changes:

```bash
openclaw gateway restart
```

Run `termind init` to discover and save Lark targets:

```bash
termind init
```

## Smoke Tests

### 1. Direct lark-cli Card Send

This checks that `lark-cli` can send a card without involving the agent:

```bash
export TERMIND_LARK_TARGET_ID=oc_xxx
export TERMIND_LARK_TARGET_TYPE=chat
export TERMIND_LARK_SENDER=bot
plugin/examples/lark-smoke/scripts/openclaw-message-card-smoke.sh
```

Expected result: `lark-cli` returns a successful message response.

### 2. Agent Orchestration

This checks the skill/tool/lark-cli path:

```bash
openclaw agent --local --agent main \
  --session-id termind-lark-message-smoke \
  --message "$(cat plugin/examples/lark-smoke/prompts/agent-message-card-smoke.md)" \
  --timeout 180
```

Expected result: the agent calls Termind tools, executes generated `lark-cli`
commands, then reports delivery only after every command exits successfully.

### 3. Full Termind Shell Path

```bash
termind init
TERMIND_DEBUG=1 termind shell
cnmb
```

Expected result:

- Local terminal insight appears.
- Feishu/Lark receives a card.
- Debug log contains:

```text
alert: start command="cnmb"
alert: submitted
diagnose: done
```

Debug log path is printed at shell startup when `TERMIND_DEBUG=1`.

## Known Good Verification

Example local end-to-end verification should produce a Feishu/Lark message id:

```text
messageId: om_x100b504fe21e7ca4c3afd1ff8d1a3fb
chatId: oc_4bf56e2d154b54e29c4837e44b17433d
```

Tests run:

```bash
cd cli && go test ./...
cd plugin && npm test
```

## Common Failure Modes

### Agent says `tools.alsoAllow must include exec`

OpenClaw loaded the skill but did not expose exec to the agent. Add `exec` and
the Termind tools to `tools.alsoAllow`, then restart the gateway.

### Local insight appears but Feishu receives nothing

Check:

- `termind status` shows at least one enabled Lark target.
- `TERMIND_DEBUG=1` log contains `alert: submitted`.
- OpenClaw session `agent:main:termind-lark-alert` calls
  `termind_lark_cli_send_command_build`.
- OpenClaw exec approvals allowlist includes `lark-cli`.
- Gateway was restarted after plugin/config changes.

### Agent generated a command but Feishu receives nothing

Check that `lark-cli doctor` passes, the selected `oc_xxx` or `ou_xxx` target
is correct, and the configured sender (`bot` or `user`) has permission to send.

### Duplicate plugin id warning

If OpenClaw warns about duplicate `termind` plugins, clean up old installed
copies or stale config entries. It usually does not block runtime, but it can
confuse debugging because OpenClaw may load one copy while the team edits
another.

## Team Work Split

- CLI owner: harden shell capture, command summaries, environment metadata, and
  failure-event shaping.
- Plugin owner: improve redaction, fingerprints, classification, card rendering,
  and report templates.
- OpenClaw integration owner: plugin install/config script, gateway restart
  guidance, lark-cli exec allowlist, and operator troubleshooting.
- Product/knowledge owner: define incident thresholds, report lifecycle,
  knowledge base linking, dedupe/escalation policy, and Feishu card actions.

## Next TODO

- Replace the current text-form init flow with a richer terminal UI when the
  dependency and UX are finalized.
- Add a future lark-channel inbound integration for Lark -> OpenClaw
  interactions after that channel exists.
- Add dedupe/throttle so repeated typo commands do not spam Feishu.
- Improve summary extraction so prompts like `%` are not used as alert titles.
- Add project/git metadata enrichment in the CLI before submitting events.
- Add structured report creation/update flow after the card MVP stabilizes.
