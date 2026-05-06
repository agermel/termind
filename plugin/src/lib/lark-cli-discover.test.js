import assert from "node:assert/strict";
import test from "node:test";

import { buildLarkCliDiscoverPlan, parseLarkCliDiscover } from "./lark-cli-discover.js";

test("termind_lark_cli_discover plans chat search without side effects", () => {
  const plan = buildLarkCliDiscoverPlan({ kind: "chat", sender: "user", query: "ops", memberOpenID: "ou_me" });

  assert.equal(plan.sideEffects, false);
  assert.equal(plan.execTool, "exec");
  assert.equal(plan.kind, "chat");
  assert.equal(plan.commands[0].command, "lark-cli");
  assert.deepEqual(plan.commands[0].args.slice(0, 2), ["im", "+chat-search"]);
  assert.deepEqual(plan.commands[0].args.slice(2, 4), ["--as", "user"]);
  assert.ok(plan.commands[0].args.includes("--query"));
  assert.ok(plan.commands[0].args.includes("ops"));
  assert.ok(plan.commands[0].args.includes("--member-ids"));
  assert.ok(plan.commands[0].args.includes("ou_me"));
});

test("termind_lark_cli_discover uses chat list for empty bot profile chat discovery", () => {
  const plan = buildLarkCliDiscoverPlan({ kind: "chat", sender: "bot", memberOpenID: "ou_me" });

  assert.equal(plan.commands.length, 1);
  assert.equal(plan.commands[0].optional, false);
  assert.deepEqual(plan.commands[0].args.slice(0, 3), ["im", "chats", "list"]);
  assert.ok(!plan.commands[0].args.includes("--member-ids"));
  assert.deepEqual(plan.commands[0].args.slice(3, 5), ["--as", "bot"]);
  assert.ok(plan.commands[0].args.includes("--page-limit"));
  assert.ok(plan.commands[0].args.includes("50"));
});

test("termind_lark_cli_discover parses chat and user choices", () => {
  const chats = parseLarkCliDiscover({
    kind: "chat",
    chatSearchOutput: JSON.stringify({ data: { items: [{ chat_id: "oc_one", name: "group" }] } })
  });
  const users = parseLarkCliDiscover({
    kind: "user",
    userSearchOutput: JSON.stringify({ data: { users: [{ open_id: "ou_one", localized_name: "Alice" }] } })
  });

  assert.deepEqual(chats.choices, [{ type: "chat", id: "oc_one", label: "group" }]);
  assert.deepEqual(users.choices, [{ type: "user", id: "ou_one", label: "Alice" }]);
});

test("termind_lark_cli_discover parses generic exec stdout with progress lines", () => {
  const chats = parseLarkCliDiscover({
    kind: "chat",
    stdout: '[page 1] fetching...\n{"data":{"items":[{"chat_id":"oc_one","name":"group"}]}}'
  });

  assert.deepEqual(chats.choices, [{ type: "chat", id: "oc_one", label: "group" }]);
});

test("termind_lark_cli_discover preserves lark-cli errors when no choices are found", () => {
  const chats = parseLarkCliDiscover({
    kind: "chat",
    chatListOutput: JSON.stringify({ ok: false, identity: "bot", error: { type: "network", message: "API call failed: TAT API error: [10003] invalid param" } }),
    chatListError: "(Command exited with code 4)"
  });

  assert.deepEqual(chats.choices, []);
  assert.deepEqual(chats.errors, ["API call failed: TAT API error: [10003] invalid param", "(Command exited with code 4)"]);
});
