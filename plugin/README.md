# Termind OpenClaw Plugin POC

This plugin is the safe OpenClaw-side POC for Termind terminal intelligence.
It does **not** execute shell commands, read environment variables, or send
network requests.

Instead, it provides pure tools and workflow skills:

```text
Termind FailureEvent
  -> OpenClaw agent
  -> Termind plugin pure tools
       redact / fingerprint / classify / build card / build lark-cli argv
  -> OpenClaw exec runs lark-cli im +messages-send
  -> Lark/Feishu interactive card
```

## Tools

- `termind_event_redact`: normalize and redact a failure event.
- `termind_fingerprint_compute`: compute deterministic fingerprint metadata.
- `termind_failure_classify`: classify severity and routing hints.
- `termind_lark_card_build`: build Lark/Feishu interactive card JSON.
- `termind_lark_cli_send_command_build`: build safe `lark-cli` command argv.
- `termind_report_template_build`: build a Markdown incident report template.

All tools are side-effect free. They only transform input JSON into output JSON.

## Skills

- `termind-lark-alert`: use Termind tools to produce a safe Lark/Feishu card
  and send it by executing `lark-cli im +messages-send` through OpenClaw exec.
- `termind-knowledge-rag`: progressively search OpenClaw memory/wiki/Feishu
  docs, with graceful fallback when capabilities are missing.
- `termind-incident-report`: create or update incident report templates.

## Install For Testing

```bash
openclaw plugins install termind-openclaw-plugin@dev
openclaw plugins inspect termind
```

This safe POC should not require `--dangerously-force-unsafe-install`.

The agent profile must allow the pure Termind tools plus OpenClaw exec:

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

Restart the gateway after changing OpenClaw config.

## Send Flow

This plugin does not execute shell commands or network requests directly. It
builds the card and controlled `lark-cli` argv; OpenClaw executes the command:

```bash
lark-cli im +messages-send --as bot --chat-id oc_xxx --content '<interactive-card-json>' --msg-type interactive
```

The OpenClaw agent profile must allow `exec`, and the exec approvals allowlist
must allow the `lark-cli` binary.

See `examples/lark-smoke/` for smoke tests.
