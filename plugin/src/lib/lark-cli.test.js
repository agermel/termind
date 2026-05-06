import assert from "node:assert/strict";
import test from "node:test";

import { buildLarkCliCommands } from "./lark-cli.js";

test("termind_lark_cli_send_command_build builds lark-cli commands for chat and user targets", async () => {
  const commands = buildLarkCliCommands({
    summary: "boom",
    command: "go test ./...",
    larkSender: "bot",
    larkTargets: [
      { type: "chat", id: "oc_test", label: "group", enabled: true },
      { type: "user", id: "ou_test", label: "me", enabled: true }
    ]
  }, { config: { wide_screen_mode: true }, elements: [] });

  assert.equal(commands.length, 2);
  assert.equal(commands[0].command, "lark-cli");
  assert.deepEqual(commands[0].args.slice(0, 3), ["im", "+messages-send", "--as"]);
  assert.ok(commands[0].args.includes("--chat-id"));
  assert.ok(commands[0].args.includes("oc_test"));
  assert.ok(commands[1].args.includes("--user-id"));
  assert.ok(commands[1].args.includes("ou_test"));
});
