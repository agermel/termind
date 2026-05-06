import assert from "node:assert/strict";
import test from "node:test";

import {
  buildKnowledgeQueries,
  buildLarkKnowledgeSearchPlan,
  larkKnowledgeSearchTool,
  parseLarkKnowledgeSearch
} from "./lark-knowledge-search.js";

test("buildKnowledgeQueries derives queries from event in specificity order", () => {
  const result = buildKnowledgeQueries({
    event: {
      summary: "panic: runtime error: invalid memory address",
      command: "go run ./cmd/grade serve",
      project: "be-grade",
      stackTop: ["  at be-grade/cron/rank.go:87 computeRank()"]
    }
  });

  assert.equal(result.ok, true);
  assert.equal(result.sender, "user");
  // 第一个应是 project + summary 头, 最特异
  assert.match(result.queries[0].query, /be-grade.*panic/i);
  // stack frame 单独成 query
  const stack = result.queries.find(q => q.source === "stack");
  assert.ok(stack, "should produce a stack query");
  assert.match(stack.query, /computeRank/);
  // 不重复
  const set = new Set(result.queries.map(q => q.query.toLowerCase()));
  assert.equal(set.size, result.queries.length);
});

test("buildLarkKnowledgeSearchPlan refuses bot sender with missing_capability=user_oauth", () => {
  const plan = buildLarkKnowledgeSearchPlan({ sender: "bot", query: "panic" });
  assert.equal(plan.ok, false);
  assert.equal(plan.missingCapability, "user_oauth");
  assert.deepEqual(plan.commands, []);
  assert.match(plan.errors.join("|"), /docs \+search only supports --as user/);
});

test("buildLarkKnowledgeSearchPlan emits user-only docs +search command", () => {
  const plan = buildLarkKnowledgeSearchPlan({
    query: "panic: runtime error",
    pageSize: 5,
    profile: "u_alice",
    larkCliConfigDir: "$HOME/.lark-cli/openclaw-user-alice",
    filter: { docTypes: ["doc", "wiki"] }
  });

  assert.equal(plan.ok, true);
  assert.equal(plan.sender, "user");
  assert.equal(plan.execTool, "exec");
  assert.equal(plan.commands.length, 1);
  const cmd = plan.commands[0];
  assert.equal(cmd.command, "lark-cli");
  assert.deepEqual(cmd.args.slice(0, 2), ["--profile", "u_alice"]);
  assert.deepEqual(cmd.args.slice(2, 6), ["docs", "+search", "--as", "user"]);
  assert.ok(cmd.args.includes("--query"));
  assert.ok(cmd.args.includes("panic: runtime error"));
  assert.ok(cmd.args.includes("--page-size"));
  assert.ok(cmd.args.includes("5"));
  assert.ok(cmd.args.includes("--format"));
  assert.ok(cmd.args.includes("json"));
  assert.ok(cmd.args.includes("--filter"));
  // env 必须带上 LARKSUITE_CLI_CONFIG_DIR
  assert.equal(cmd.env.LARKSUITE_CLI_CONFIG_DIR, "$HOME/.lark-cli/openclaw-user-alice");
});

test("buildLarkKnowledgeSearchPlan rejects empty query", () => {
  const plan = buildLarkKnowledgeSearchPlan({ query: "" });
  assert.equal(plan.ok, false);
  assert.match(plan.errors.join("|"), /query is required/);
});

test("parseLarkKnowledgeSearch extracts hits from lark-cli style envelope", () => {
  const stdout = JSON.stringify({
    ok: true,
    identity: "user",
    data: {
      page_token: "next-1",
      has_more: true,
      docs: [
        {
          docs_token: "doccnXX",
          docs_type: "doc",
          docs_title: "Go panic playbook",
          docs_url: "https://example.feishu.cn/docs/doccnXX",
          owner_id: "ou_owner",
          owner_name: "alice",
          update_time: "2026-05-01T00:00:00+08:00",
          score: 0.91,
          snippet: "panic: runtime error -> nil-check"
        },
        {
          obj_token: "wikicnYY",
          obj_type: "wiki",
          name: "Wiki 应急手册",
          url: "https://example.feishu.cn/wiki/wikicnYY"
        }
      ]
    }
  });
  const parsed = parseLarkKnowledgeSearch({ stdout, exitCode: 0 });

  assert.equal(parsed.ok, true);
  assert.equal(parsed.needsUserAuthorization, false);
  assert.equal(parsed.hits.length, 2);
  assert.equal(parsed.hits[0].token, "doccnXX");
  assert.equal(parsed.hits[0].type, "doc");
  assert.equal(parsed.hits[0].title, "Go panic playbook");
  assert.equal(parsed.hits[0].url, "https://example.feishu.cn/docs/doccnXX");
  assert.equal(parsed.hits[0].ownerOpenId, "ou_owner");
  assert.equal(parsed.hits[1].token, "wikicnYY");
  assert.equal(parsed.hits[1].type, "wiki");
  assert.equal(parsed.pageToken, "next-1");
  assert.equal(parsed.hasMore, true);
});

test("parseLarkKnowledgeSearch flags need_user_authorization gracefully", () => {
  const stdout = JSON.stringify({
    ok: false,
    identity: "user",
    error: { type: "api_error", message: "API call failed: need_user_authorization (user: )" }
  });
  const parsed = parseLarkKnowledgeSearch({ stdout, exitCode: 0 });

  assert.equal(parsed.ok, false);
  assert.equal(parsed.needsUserAuthorization, true);
  assert.equal(parsed.missingCapability, "user_oauth");
  assert.deepEqual(parsed.hits, []);
  assert.match(parsed.errors.join("|"), /need_user_authorization/);
});

test("parseLarkKnowledgeSearch handles stderr-only --as bot rejection", () => {
  const parsed = parseLarkKnowledgeSearch({
    stdout: "",
    stderr: "Error: --as bot is not supported, this command only supports: user",
    exitCode: 1
  });

  assert.equal(parsed.ok, false);
  assert.equal(parsed.needsUserAuthorization, true);
  assert.equal(parsed.missingCapability, "user_oauth");
});

test("larkKnowledgeSearchTool dispatches actions queries/plan/parse", () => {
  const queries = larkKnowledgeSearchTool({
    action: "queries",
    event: { summary: "panic", command: "go test", project: "be-grade" }
  });
  assert.equal(queries.ok, true);
  assert.ok(queries.queries.length > 0);

  const plan = larkKnowledgeSearchTool({ action: "plan", query: "panic" });
  assert.equal(plan.ok, true);
  assert.equal(plan.commands.length, 1);

  const parsed = larkKnowledgeSearchTool({
    action: "parse",
    stdout: JSON.stringify({ ok: true, data: { docs: [] } }),
    exitCode: 0
  });
  assert.equal(parsed.ok, true);
  assert.deepEqual(parsed.hits, []);
});
