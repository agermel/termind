Use the termind-lark-alert skill.

Build a Lark/Feishu interactive card for this Termind failure event, then send
it with OpenClaw's message tool. Do not use lark-cli and do not use feishu_chat
for sending.

Call the Termind plugin tools in this order:
1. `termind_event_redact`
2. `termind_fingerprint_compute`
3. `termind_failure_classify`
4. `termind_lark_card_build`
5. `message` with:
   - `action`: `send`
   - `channel`: `feishu`
   - `target`: `event.larkChatId`
   - `card`: the card object returned by `termind_lark_card_build`

Only claim delivery after the `message` tool returns `ok: true`.

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
  "larkChatId": "oc_4bf56e2d154b54e29c4837e44b17433d",
  "stackTop": [
    "be-grade/cron/rank.go:87 computeRank()",
    "be-grade/cron/rank.go:42 (*RankJob).Run()"
  ],
  "tail": "panic: runtime error: invalid memory address\nAuthorization: Bearer secret-token"
}
```
