import assert from "node:assert/strict";
import test from "node:test";

import {
  buildOwnerResolvePlan,
  ownerResolveTool,
  parseOwnerResolve
} from "./owner-resolve.js";

test("buildOwnerResolvePlan emits a user-only +search-user command preferring email", () => {
  const plan = buildOwnerResolvePlan({
    sender: "user",
    email: "alice@bytedance.com",
    name: "Alice",
    profile: "u_alice",
    larkCliConfigDir: "$HOME/.lark-cli/openclaw-user-alice"
  });

  assert.equal(plan.ok, true);
  assert.equal(plan.sender, "user");
  assert.equal(plan.queryUsed, "alice@bytedance.com");
  assert.equal(plan.queryKind, "email");
  const cmd = plan.commands[0];
  assert.deepEqual(cmd.args.slice(0, 2), ["--profile", "u_alice"]);
  assert.deepEqual(cmd.args.slice(2, 6), ["contact", "+search-user", "--as", "user"]);
  assert.ok(cmd.args.includes("alice@bytedance.com"));
  assert.equal(cmd.env.LARKSUITE_CLI_CONFIG_DIR, "$HOME/.lark-cli/openclaw-user-alice");
});

test("buildOwnerResolvePlan falls back to name when email is missing", () => {
  const plan = buildOwnerResolvePlan({ sender: "user", name: "Alice" });
  assert.equal(plan.ok, true);
  assert.equal(plan.queryUsed, "Alice");
  assert.equal(plan.queryKind, "name");
});

test("buildOwnerResolvePlan refuses when sender=bot, returns label-only owner fallback", () => {
  const plan = buildOwnerResolvePlan({ sender: "bot", email: "alice@b.com", name: "Alice" });
  assert.equal(plan.ok, false);
  assert.equal(plan.missingCapability, "user_oauth");
  assert.deepEqual(plan.commands, []);
  assert.ok(plan.labelOnlyOwner);
  assert.equal(plan.labelOnlyOwner.kind, "git_author");
  assert.equal(plan.labelOnlyOwner.confidence, "label_only");
  assert.equal(plan.labelOnlyOwner.label, "Alice");
});

test("buildOwnerResolvePlan errors when no email and no name", () => {
  const plan = buildOwnerResolvePlan({ sender: "user" });
  assert.equal(plan.ok, false);
  assert.match(plan.errors.join("|"), /email or name is required/);
});

test("parseOwnerResolve picks high-confidence email match", () => {
  const stdout = JSON.stringify({
    ok: true,
    identity: "user",
    data: {
      users: [
        { open_id: "ou_other", name: "Bob", enterprise_email: "bob@b.com" },
        { open_id: "ou_alice", name: "Alice", enterprise_email: "alice@b.com" }
      ]
    }
  });
  const parsed = parseOwnerResolve({
    stdout,
    exitCode: 0,
    email: "alice@b.com",
    name: "Alice",
    queryKind: "email"
  });

  assert.equal(parsed.ok, true);
  assert.equal(parsed.owner.kind, "lark_user");
  assert.equal(parsed.owner.openId, "ou_alice");
  assert.equal(parsed.owner.confidence, "high");
  assert.equal(parsed.candidates.length, 2);
});

test("parseOwnerResolve picks medium-confidence name match when query was name", () => {
  const stdout = JSON.stringify({
    ok: true,
    data: { users: [{ open_id: "ou_alice", name: "Alice" }] }
  });
  const parsed = parseOwnerResolve({
    stdout,
    exitCode: 0,
    name: "Alice",
    queryKind: "name"
  });

  assert.equal(parsed.owner.openId, "ou_alice");
  assert.equal(parsed.owner.confidence, "medium");
});

test("parseOwnerResolve falls back to label-only when no exact match", () => {
  const stdout = JSON.stringify({
    ok: true,
    data: {
      users: [
        { open_id: "ou_other", name: "Different Person" }
      ]
    }
  });
  const parsed = parseOwnerResolve({
    stdout,
    exitCode: 0,
    email: "alice@b.com",
    name: "Alice",
    queryKind: "email"
  });

  assert.equal(parsed.owner.confidence, "label_only");
  assert.equal(parsed.owner.kind, "git_author");
  assert.equal(parsed.owner.label, "Alice");
  assert.equal(parsed.candidates.length, 1);
});

test("parseOwnerResolve flags need_user_authorization and degrades to label-only", () => {
  const stdout = JSON.stringify({
    ok: false,
    error: { type: "api_error", message: "API call failed: need_user_authorization (user: )" }
  });
  const parsed = parseOwnerResolve({
    stdout,
    exitCode: 0,
    name: "Alice",
    email: "alice@b.com",
    queryKind: "email"
  });

  assert.equal(parsed.ok, false);
  assert.equal(parsed.needsUserAuthorization, true);
  assert.equal(parsed.missingCapability, "user_oauth");
  assert.equal(parsed.owner.confidence, "label_only");
  assert.equal(parsed.owner.label, "Alice");
});

test("ownerResolveTool dispatches plan/parse", () => {
  const plan = ownerResolveTool({ action: "plan", sender: "user", email: "x@y.com" });
  assert.equal(plan.ok, true);
  const parsed = ownerResolveTool({
    action: "parse",
    stdout: JSON.stringify({ ok: true, data: { users: [] } }),
    exitCode: 0,
    name: "Alice"
  });
  assert.equal(parsed.owner?.confidence, "label_only");
});
