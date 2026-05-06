import assert from "node:assert/strict";
import test from "node:test";

import { incidentRegistryTool, incidentRegistryUpsertTool } from "./registry.js";

test("plan rejects missing fingerprint", () => {
  const out = incidentRegistryTool({ action: "plan" });
  assert.equal(out.ok, false);
  assert.deepEqual(out.errors, ["fingerprint is required"]);
});

test("plan returns capability hints and a missing-capability fallback", () => {
  const out = incidentRegistryTool({ action: "plan", fingerprint: "A3F9C2D1" });
  assert.equal(out.ok, true);
  assert.equal(out.fingerprint, "a3f9c2d1");
  assert.equal(out.key, "termind:incident:fp:a3f9c2d1");
  assert.equal(out.windowMinutes, 120);
  assert.ok(out.capabilityHints.find((h) => h.capability === "memory.get"));
  assert.equal(out.parse.tool, "termind_incident_registry_query");
  assert.equal(out.parse.action, "parse");
  assert.equal(out.missingCapabilityFallback.action, "parse");
  assert.equal(out.missingCapabilityFallback.missingCapability, true);
});

test("parse with raw=null returns new_case", () => {
  const out = incidentRegistryTool({ action: "parse", fingerprint: "abc", raw: null });
  assert.equal(out.found, false);
  assert.equal(out.branch, "new_case");
  assert.equal(out.record, null);
  assert.equal(out.decoded, true);
  assert.equal(out.missingCapability, false);
});

test("parse with missingCapability=true preserves the flag", () => {
  const out = incidentRegistryTool({
    action: "parse",
    fingerprint: "abc",
    raw: null,
    missingCapability: true
  });
  assert.equal(out.found, false);
  assert.equal(out.branch, "new_case");
  assert.equal(out.missingCapability, true);
  assert.ok(out.reasons.some((r) => r.includes("no registry capability")));
});

test("parse with malformed JSON degrades to new_case + decoded:false", () => {
  const out = incidentRegistryTool({
    action: "parse",
    fingerprint: "abc",
    raw: "{not json"
  });
  assert.equal(out.found, false);
  assert.equal(out.branch, "new_case");
  assert.equal(out.decoded, false);
  assert.ok(out.reasons.some((r) => r.startsWith("registry record decode failed")));
});

test("parse with feature-branch recurrence under threshold returns recurrence", () => {
  const now = "2026-05-06T21:00:00+08:00";
  const record = {
    fingerprint: "abc",
    reportUrl: "https://feishu/reports/abc",
    firstSeen: "2026-05-01T10:00:00+08:00",
    lastSeen: "2026-05-06T20:55:00+08:00",
    occurrences: 2,
    branchKind: "feature",
    status: "open",
    events: [
      { timestamp: "2026-05-06T20:55:00+08:00", user: "matterhorn" },
      { timestamp: "2026-05-06T20:00:00+08:00", user: "matterhorn" }
    ]
  };

  const out = incidentRegistryTool({
    action: "parse",
    fingerprint: "abc",
    raw: JSON.stringify(record),
    now,
    branchKind: "feature"
  });

  assert.equal(out.found, true);
  assert.equal(out.branch, "recurrence");
  assert.equal(out.record.windowOccurrences, 2);
  assert.deepEqual(out.record.affectedUsers, ["matterhorn"]);
  assert.equal(out.record.reportUrl, "https://feishu/reports/abc");
});

test("parse escalates when window occurrences hit the threshold", () => {
  const now = "2026-05-06T21:00:00+08:00";
  const events = Array.from({ length: 5 }, (_, i) => ({
    timestamp: `2026-05-06T20:5${i}:00+08:00`,
    user: `user${i}`
  }));
  const record = { fingerprint: "abc", branchKind: "feature", status: "open", events };

  const out = incidentRegistryTool({
    action: "parse",
    fingerprint: "abc",
    raw: JSON.stringify(record),
    now,
    branchKind: "feature"
  });

  assert.equal(out.found, true);
  assert.equal(out.branch, "escalation_candidate");
  assert.equal(out.record.windowOccurrences, 5);
  assert.equal(out.record.affectedUsers.length, 5);
  assert.ok(out.reasons.some((r) => r.includes("window occurrences")));
});

test("parse escalates when affected users hit the threshold", () => {
  const now = "2026-05-06T21:00:00+08:00";
  const events = [
    { timestamp: "2026-05-06T20:30:00+08:00", user: "alice" },
    { timestamp: "2026-05-06T20:35:00+08:00", user: "bob" },
    { timestamp: "2026-05-06T20:40:00+08:00", user: "carol" }
  ];
  const record = { fingerprint: "abc", branchKind: "feature", status: "open", events };

  const out = incidentRegistryTool({
    action: "parse",
    fingerprint: "abc",
    raw: JSON.stringify(record),
    now,
    branchKind: "feature"
  });

  assert.equal(out.branch, "escalation_candidate");
  assert.deepEqual(out.record.affectedUsers, ["alice", "bob", "carol"]);
  assert.ok(out.reasons.some((r) => r.includes("affected users")));
});

test("parse escalates immediately on a single main-branch recurrence", () => {
  const now = "2026-05-06T21:00:00+08:00";
  const record = {
    fingerprint: "abc",
    branchKind: "main",
    status: "open",
    events: [{ timestamp: "2026-05-06T20:55:00+08:00", user: "matterhorn" }]
  };

  const out = incidentRegistryTool({
    action: "parse",
    fingerprint: "abc",
    raw: JSON.stringify(record),
    now,
    branchKind: "main"
  });

  assert.equal(out.branch, "escalation_candidate");
  assert.ok(out.reasons.some((r) => r.includes("main branch recurrence")));
});

test("parse with resolved status treats as new_case but keeps record", () => {
  const now = "2026-05-06T21:00:00+08:00";
  const record = {
    fingerprint: "abc",
    branchKind: "feature",
    status: "resolved",
    events: [{ timestamp: "2026-05-06T20:55:00+08:00", user: "matterhorn" }]
  };

  const out = incidentRegistryTool({
    action: "parse",
    fingerprint: "abc",
    raw: JSON.stringify(record),
    now
  });

  assert.equal(out.found, false);
  assert.equal(out.branch, "new_case");
  assert.equal(out.record.status, "resolved");
  assert.ok(out.reasons.some((r) => r.includes("status=resolved")));
});

test("parse drops events outside the window", () => {
  const now = "2026-05-06T21:00:00+08:00";
  const record = {
    fingerprint: "abc",
    branchKind: "feature",
    status: "open",
    events: [
      { timestamp: "2026-05-06T20:55:00+08:00", user: "matterhorn" },  // within 120 min
      { timestamp: "2026-05-06T18:00:00+08:00", user: "old-user" },    // outside
      { timestamp: "not-a-timestamp", user: "broken" }                 // unparseable
    ]
  };

  const out = incidentRegistryTool({
    action: "parse",
    fingerprint: "abc",
    raw: JSON.stringify(record),
    now,
    windowMinutes: 120
  });

  assert.equal(out.record.windowOccurrences, 1);
  assert.deepEqual(out.record.affectedUsers, ["matterhorn"]);
});

test("parse accepts a pre-decoded object (no JSON layer)", () => {
  const now = "2026-05-06T21:00:00+08:00";
  const out = incidentRegistryTool({
    action: "parse",
    fingerprint: "abc",
    raw: { fingerprint: "abc", branchKind: "feature", status: "open", events: [] },
    now
  });
  assert.equal(out.found, true);
  assert.equal(out.branch, "recurrence");
});

test("plan and parse round-trip: plan output is shaped for parse callers", () => {
  const plan = incidentRegistryTool({ action: "plan", fingerprint: "abc", windowMinutes: 60 });
  assert.equal(plan.parse.tool, "termind_incident_registry_query");
  assert.equal(plan.parse.action, "parse");
  // missingCapabilityFallback should itself be a valid parse input
  const out = incidentRegistryTool(plan.missingCapabilityFallback);
  assert.equal(out.found, false);
  assert.equal(out.branch, "new_case");
  assert.equal(out.missingCapability, true);
});

test("unsupported action returns errors with supported list", () => {
  const out = incidentRegistryTool({ action: "noop" });
  assert.equal(out.ok, false);
  assert.deepEqual(out.supportedActions, ["plan", "parse"]);
});

// --- upsert ---

test("upsert plan emits memory.set + kv.set hints with serialized record", () => {
  const out = incidentRegistryUpsertTool({
    action: "plan",
    fingerprint: "abc12345",
    branchKind: "feature",
    reportUrl: "https://example.feishu.cn/docs/new",
    user: "matterhorn",
    branch: "feat/x",
    gitCommit: "8e4d21a",
    environment: "go1.22 macOS",
    owner: { kind: "lark_user", openId: "ou_owner", label: "@alice", confidence: "high" },
    occurredAt: "2026-05-06T21:00:00+08:00",
    priorRaw: null
  });

  assert.equal(out.ok, true);
  assert.equal(out.fingerprint, "abc12345");
  assert.equal(out.key, "termind:incident:fp:abc12345");
  const decoded = JSON.parse(out.value);
  assert.equal(decoded.fingerprint, "abc12345");
  assert.equal(decoded.occurrences, 1);
  assert.equal(decoded.firstSeen, "2026-05-06T13:00:00.000Z");
  assert.equal(decoded.lastSeen, "2026-05-06T13:00:00.000Z");
  assert.equal(decoded.reportUrl, "https://example.feishu.cn/docs/new");
  assert.equal(decoded.owner.openId, "ou_owner");
  assert.equal(decoded.events.length, 1);
  assert.equal(decoded.events[0].user, "matterhorn");

  const memHint = out.capabilityHints.find(h => h.capability === "memory.set");
  const kvHint = out.capabilityHints.find(h => h.capability === "kv.set");
  assert.ok(memHint);
  assert.equal(memHint.args.key, "termind:incident:fp:abc12345");
  assert.equal(memHint.args.value, out.value);
  assert.equal(memHint.args.ttlSeconds, 90 * 86400);
  assert.equal(kvHint.args.namespace, "termind:incident");
  assert.equal(kvHint.args.key, "fp:abc12345");

  // missingCapabilityFallback 必须是合法的 parse 输入, 调一遍 parse 不会崩
  const ack = incidentRegistryUpsertTool(out.missingCapabilityFallback);
  assert.equal(ack.ok, false);
  assert.equal(ack.written, false);
});

test("upsert plan increments occurrences and preserves firstSeen across calls", () => {
  const first = incidentRegistryUpsertTool({
    action: "plan",
    fingerprint: "abc",
    user: "matterhorn",
    occurredAt: "2026-05-01T00:00:00Z",
    priorRaw: null
  });
  const decodedFirst = JSON.parse(first.value);
  assert.equal(decodedFirst.occurrences, 1);
  assert.equal(decodedFirst.firstSeen, "2026-05-01T00:00:00.000Z");

  const second = incidentRegistryUpsertTool({
    action: "plan",
    fingerprint: "abc",
    user: "matterhorn",
    occurredAt: "2026-05-06T00:00:00Z",
    priorRaw: first.value
  });
  const decodedSecond = JSON.parse(second.value);
  assert.equal(decodedSecond.occurrences, 2);
  assert.equal(decodedSecond.firstSeen, "2026-05-01T00:00:00.000Z");
  assert.equal(decodedSecond.lastSeen, "2026-05-06T00:00:00.000Z");
  assert.equal(decodedSecond.events.length, 2);
});

test("upsert parse infers success from ack.ok=true", () => {
  const ack = incidentRegistryUpsertTool({
    action: "parse",
    fingerprint: "abc",
    ack: { ok: true },
    record: { fingerprint: "abc", occurrences: 2 }
  });
  assert.equal(ack.ok, true);
  assert.equal(ack.written, true);
  assert.equal(ack.record.occurrences, 2);
});

test("upsert parse infers failure from non-zero exitCode + stderr", () => {
  const ack = incidentRegistryUpsertTool({
    action: "parse",
    fingerprint: "abc",
    exitCode: 1,
    stderr: "memory.set: permission denied\nstack..."
  });
  assert.equal(ack.ok, false);
  assert.equal(ack.written, false);
  assert.match(ack.errors.join("|"), /permission denied/);
});

test("upsert parse fingerprint required", () => {
  const ack = incidentRegistryUpsertTool({ action: "parse" });
  assert.equal(ack.ok, false);
  assert.deepEqual(ack.errors, ["fingerprint is required"]);
});
