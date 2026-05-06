Use the termind-lark-alert skill.

Build a Lark/Feishu interactive card for this Termind failure event, then send
it with `lark-cli im +messages-send` through OpenClaw exec.

Call the Termind plugin tools in this order:
1. `termind_event_redact`
2. `termind_fingerprint_compute`
3. `termind_failure_classify`
4. `termind_lark_card_build`
5. `termind_lark_cli_send_command_build` with the redacted event and card
6. OpenClaw exec with every returned `lark-cli` command and args

Only claim delivery after every `lark-cli` command exits successfully.
Do not use OpenClaw Feishu tools, direct Feishu APIs, the `message` tool, or
hand-written fallback scripts. If `lark-cli` fails, report the exact failure.
Preserve `larkTargets`, `larkSender`, `larkChatId`, and `larkUserOpenId` when
passing the event between tools.

Failure event:

```json
{
  "fingerprint": "a3f9c2d1",
  "summary": "panic: runtime error: invalid memory address",
  "command": "go run ./cmd/grade serve",
  "severity": "warning",
  "project": "be-grade",
  "user": "matterhorn",
  "gitCommit": "8e4d21a",
  "branch": "feat/rank-v2",
  "environment": "go1.22.3 macOS dev",
  "larkSender": "bot",
  "larkTargets": [
    {
      "type": "chat",
      "id": "oc_4bf56e2d154b54e29c4837e44b17433d",
      "label": "smoke group"
    }
  ],
  "stackTop": [
    "be-grade/cron/rank.go:87 computeRank()",
    "be-grade/cron/rank.go:42 (*RankJob).Run()"
  ],
  "tail": "panic: runtime error: invalid memory address\nAuthorization: Bearer secret-token"
}
```
