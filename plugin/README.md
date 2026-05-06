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
- `termind_incident_registry_query`: plan/parse a registry lookup by
  fingerprint and decide `new_case` / `recurrence` / `escalation_candidate`.
- `termind_incident_registry_upsert`: plan/parse the registry write-back
  after a successful alert delivery (occurrences+1, events appended,
  reportUrl/owner merged) via `memory.set` / `kv.set` capabilities.
- `termind_lark_knowledge_search`: plan/parse Lark/Feishu doc + wiki search
  via `lark-cli docs +search --as user`. The Search v2 API is **user-only**;
  bot identities are rejected by lark-cli.
- `termind_owner_resolve`: plan/parse git author -> Lark `open_id`
  resolution via `lark-cli contact +search-user --as user`. Falls back to a
  `label_only` owner when no user-OAuth profile is available.
- `termind_failure_classify`: classify severity and routing hints.
- `termind_lark_card_build`: build Lark/Feishu interactive card JSON
  (renders branch styling, history, owner @-mention, knowledge hits).
- `termind_lark_cli_send_command_build`: build safe `lark-cli` command argv.
- `termind_report_template_build`: build a Markdown incident report template.

All tools are side-effect free. They only transform input JSON into output JSON.

## Skills

- `termind-lark-alert`: end-to-end orchestrator: redact → fingerprint →
  registry query → classify → owner resolve → knowledge RAG → card build →
  `lark-cli` send → registry write-back.
- `termind-knowledge-rag`: capability ladder for knowledge search;
  delegates the Lark/Feishu doc step to `termind-lark-knowledge-search`.
- `termind-lark-knowledge-search`: user-OAuth-only Lark doc/wiki search.
- `termind-owner-resolve`: user-OAuth-only git-author → Lark open_id
  resolution with label-only fallback.
- `termind-incident-registry`: stateless registry lookup by fingerprint;
  decides whether downstream alerts should treat the failure as a new case,
  a recurrence, or an escalation candidate.
- `termind-incident-report`: create a new incident report doc (new-case
  branch, step 11).
- `termind-incident-recurrence`: append a recurrence row to a prior report
  and bump the registry record (recurrence / escalation branches, step 11).

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

See `../examples/lark-smoke/` (at the repo root) for smoke tests. Examples
live outside `plugin/` so the OpenClaw plugin tarball stays free of any
dev-only subprocess-spawning code.
