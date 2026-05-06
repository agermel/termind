import assert from "node:assert/strict";
import test from "node:test";

import { normalizeFailureEvent, redactFailureEvent } from "./redact.js";

test("normalizeFailureEvent validates required fields", () => {
  assert.throws(() => normalizeFailureEvent({}, { requireFingerprint: false }), /summary is required/);
  const event = normalizeFailureEvent({
    summary: "boom",
    command: "go test ./...",
    severity: "bad"
  }, { requireFingerprint: false });
  assert.equal(event.severity, "warning");
});

test("redactFailureEvent removes common secrets", () => {
  const event = redactFailureEvent(
    normalizeFailureEvent({
      fingerprint: "a3f9c2d1",
      summary: "authorization: Bearer secret-token",
      command: "curl -H 'Authorization: Bearer abc.def'",
      severity: "incident",
      tail: "password=super-secret\napi_key: sk-123"
    })
  );

  assert.doesNotMatch(event.summary, /secret-token/);
  assert.doesNotMatch(event.command, /abc\.def/);
  assert.doesNotMatch(event.tail, /super-secret|sk-123/);
});

test("redactFailureEvent removes terminal control characters", () => {
  const event = redactFailureEvent(
    normalizeFailureEvent({
      fingerprint: "a3f9c2d1",
      summary: "\u001b[1m\u001b[7m%\u001b[27m",
      command: "badcmd",
      tail: "zsh: command not found: badcmd\r\n\u001b[1m%\u001b[0m"
    })
  );

  assert.equal(event.summary, "%");
  assert.equal(event.tail, "zsh: command not found: badcmd\n%");
  assert.doesNotMatch(JSON.stringify(event), /\u001b|\r/);
});

// 回归: normalizeFailureEvent 必须保留 CLI 端发过来的 forwarding 路由表.
// 之前这两个字段被静默丢弃, 导致 buildLarkCliCommands 拿不到 routes 时
// 整条链路虽然不报错但发不出消息, 是个隐性故障.
test("normalizeFailureEvent preserves larkForwardingIdentities and Routes", () => {
  const event = normalizeFailureEvent(
    {
      summary: "boom",
      command: "go test ./...",
      larkForwardingIdentities: {
        "bot-a": {
          id: "bot-a",
          kind: "bot",
          label: "main bot",
          appId: "cli_a96595e849fadbdf",
          profile: "cli_a96595e849fadbdf",
          enabled: true,
          source: "lark-cli"
        }
      },
      larkForwardingRoutes: [
        {
          identityId: "bot-a",
          enabled: true,
          target: { type: "chat", id: "oc_test", label: "test chat" }
        }
      ]
    },
    { requireFingerprint: false }
  );

  assert.equal(event.larkForwardingIdentities["bot-a"].appId, "cli_a96595e849fadbdf");
  assert.equal(event.larkForwardingIdentities["bot-a"].kind, "bot");
  assert.equal(event.larkForwardingIdentities["bot-a"].enabled, true);
  assert.equal(event.larkForwardingRoutes.length, 1);
  assert.equal(event.larkForwardingRoutes[0].target.id, "oc_test");
  assert.equal(event.larkForwardingRoutes[0].target.type, "chat");
});

test("normalizeFailureEvent drops malformed forwarding entries instead of failing", () => {
  const event = normalizeFailureEvent(
    {
      summary: "boom",
      command: "go test ./...",
      larkForwardingIdentities: {
        empty: {},
        valid: { id: "bot-x", kind: "bot" },
        nope: null
      },
      larkForwardingRoutes: [
        { identityId: "bot-x" }, // no target
        { identityId: "bot-x", target: {} }, // target with no id
        { identityId: "bot-x", target: { type: "chat", id: "oc_keep" } },
        "not-an-object"
      ]
    },
    { requireFingerprint: false }
  );

  assert.deepEqual(Object.keys(event.larkForwardingIdentities), ["valid"]);
  assert.equal(event.larkForwardingRoutes.length, 1);
  assert.equal(event.larkForwardingRoutes[0].target.id, "oc_keep");
});

test("redactFailureEvent does not strip forwarding fields", () => {
  const event = redactFailureEvent(
    normalizeFailureEvent(
      {
        summary: "boom",
        command: "go test ./...",
        larkForwardingIdentities: {
          "bot-a": { id: "bot-a", kind: "bot", appId: "cli_x" }
        },
        larkForwardingRoutes: [
          { identityId: "bot-a", target: { type: "chat", id: "oc_keep" } }
        ]
      },
      { requireFingerprint: false }
    )
  );

  assert.equal(event.larkForwardingIdentities["bot-a"].appId, "cli_x");
  assert.equal(event.larkForwardingRoutes[0].target.id, "oc_keep");
});

// 回归: termind-incident-registry skill 把 record 字段 merge 进 event 后再
// 调 termind_lark_card_build / classify / report. 工具内部还会再 normalize
// 一次, 必须保留 registryBranch / windowOccurrences / windowMinutes /
// firstSeen / lastSeen, 否则 card 拿不到 branch 一律落到兜底标题.
test("normalizeFailureEvent preserves registry merge fields", () => {
  const event = normalizeFailureEvent(
    {
      summary: "boom",
      command: "go test ./...",
      registryBranch: "recurrence",
      windowOccurrences: 2,
      windowMinutes: 120,
      firstSeen: "2026-04-25T00:58:12+08:00",
      lastSeen: "2026-05-06T20:55:00+08:00",
      occurrences: 4,
      affectedUsers: 1
    },
    { requireFingerprint: false }
  );

  assert.equal(event.registryBranch, "recurrence");
  assert.equal(event.windowOccurrences, 2);
  assert.equal(event.windowMinutes, 120);
  assert.equal(event.firstSeen, "2026-04-25T00:58:12+08:00");
  assert.equal(event.lastSeen, "2026-05-06T20:55:00+08:00");
  assert.equal(event.occurrences, 4);
  assert.equal(event.affectedUsers, 1);
});

test("normalizeFailureEvent rejects unknown registry branch", () => {
  const event = normalizeFailureEvent(
    {
      summary: "boom",
      command: "go test ./...",
      registryBranch: "garbage_branch"
    },
    { requireFingerprint: false }
  );
  assert.equal(event.registryBranch, "");
});

test("normalizeFailureEvent preserves owner with stable identifier", () => {
  const event = normalizeFailureEvent(
    {
      summary: "boom",
      command: "go test ./...",
      owner: {
        kind: "lark_user",
        openId: "ou_abc",
        label: "@zhangsan",
        email: "zhangsan@bytedance.com",
        source: "git_author",
        confidence: "high"
      }
    },
    { requireFingerprint: false }
  );

  assert.equal(event.owner.openId, "ou_abc");
  assert.equal(event.owner.label, "@zhangsan");
  assert.equal(event.owner.email, "zhangsan@bytedance.com");
  assert.equal(event.owner.source, "git_author");
  assert.equal(event.owner.confidence, "high");
});

test("normalizeFailureEvent drops owner without any stable identifier", () => {
  const event = normalizeFailureEvent(
    { summary: "boom", command: "go test ./...", owner: { kind: "lark_user" } },
    { requireFingerprint: false }
  );
  assert.equal(event.owner, null);
});

test("normalizeFailureEvent preserves knowledge hits", () => {
  const event = normalizeFailureEvent(
    {
      summary: "boom",
      command: "go test ./...",
      knowledgeHits: [
        {
          token: "doccnXX",
          type: "doc",
          title: "Go runtime panic playbook",
          url: "https://example.feishu.cn/docs/doccnXX",
          ownerOpenId: "ou_owner",
          ownerName: "alice",
          snippet: "panic: runtime error -> add nil-check",
          score: 0.91
        },
        // 重复 token 应去重
        { token: "doccnXX", type: "doc", title: "dup" },
        // 没 token 应丢弃
        { type: "doc", title: "no-token" }
      ]
    },
    { requireFingerprint: false }
  );

  assert.equal(event.knowledgeHits.length, 1);
  assert.equal(event.knowledgeHits[0].token, "doccnXX");
  assert.equal(event.knowledgeHits[0].title, "Go runtime panic playbook");
  assert.equal(event.knowledgeHits[0].url, "https://example.feishu.cn/docs/doccnXX");
});
