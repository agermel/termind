---
description: Polish Termind init Lark/OpenClaw TUI flow
---

Use this workflow when polishing `termind init` Lark/OpenClaw TUI behavior.

1. Re-state the intended user-visible order before editing:
   `启用 → OpenClaw → lark-cli → 身份 → 目标 → 测试`.

2. Inspect the Bubble Tea state machine in `cli/cmd/init_lark_tui.go`:
   - `advanceConfirm`
   - `handleDoctorDone`
   - `prepareDoctorFailed`
   - `goToStep`
   - `renderProgress`
   - command helpers near the bottom of the file

3. Check these invariants:
   - OpenClaw plugin/tools/exec allowlist setup happens before `lark-cli` status check.
   - Missing local OpenClaw `lark-cli` enters an install step.
   - Installed but unconfigured/not logged-in local OpenClaw `lark-cli` enters an interactive configure/login step.
   - Remote OpenClaw never fixes status by running local `lark-cli`.
   - Target search and test sending are blocked until `lark-cli` is ready.
   - The TUI progress labels match the actual order.
   - User-facing copy says what will happen next, not just what failed.

4. Do a 10-round self-polish loop before declaring the TUI done:
   - Round 1: verify the happy path step order and progress highlighting.
   - Round 2: verify local OpenClaw plugin/tools/allowlist visual states.
   - Round 3: verify missing local `lark-cli` install visual states.
   - Round 4: verify local `lark-cli` config/login interactive handoff visual states.
   - Round 5: verify remote OpenClaw copy and that no local `lark-cli` fix is offered.
   - Round 6: verify command failure branches return to a safe retry/skip screen.
   - Round 7: verify test-send failure still saves config and finishes cleanly.
   - Round 8: verify finish page shows all progress steps complete.
   - Round 9: verify long copy, empty choices, manual target entry, and help text are readable.
   - Round 10: re-run the whole state machine mentally and with tests, then fix any issue found.

5. Treat visual polish as required, not optional:
   - The progress bar must never point at the wrong phase.
   - The finish page must not look like work is still running.
   - Loading screens must say what is happening and what comes next.
   - Confirm screens must make the default choice obvious.
   - Error screens must show the recovery action, not only the failure.
   - Remote and local OpenClaw flows must use different wording where behavior differs.
   - If any TUI copy feels confusing, rewrite it immediately.

6. Self-debug rule:
   - Do not wait for the user to point out obvious TUI bugs.
   - Find bugs yourself, fix them yourself, add regression tests yourself, and re-run validation yourself.
   - When uncertain, inspect the state machine and write a small test instead of guessing.

7. Add or update focused tests in `cli/cmd/init_lark_test.go` for every changed branch.

8. Run validation:
   - `gofmt -w cmd/init_lark_tui.go cmd/init_lark_test.go`
   - `go test ./...` from `cli/`
   - `npm test` from `plugin/` if plugin behavior or contracts changed

9. Do a final grep for forbidden local Lark operation calls in CLI:
   - `runOutput(..., "lark-cli")`
   - `exec.Command*(..., "lark-cli")`
   - `exec.LookPath("lark-cli")`

10. Summarize exactly what changed and how to manually verify with `TERMIND_DEBUG=1 go run . init`.
