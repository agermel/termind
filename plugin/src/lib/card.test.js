import assert from "node:assert/strict";
import test from "node:test";

import { buildIncidentCard } from "./card.js";

test("buildIncidentCard renders an interactive card", () => {
  const card = buildIncidentCard({
    fingerprint: "a3f9c2d1",
    summary: "panic: runtime error: invalid memory address",
    command: "go run ./cmd/grade serve",
    severity: "warning",
    project: "be-grade",
    user: "matterhorn",
    gitCommit: "8e4d21a",
    branch: "feat/rank-v2",
    environment: "go1.22.3",
    stackTop: ["be-grade/cron/rank.go:87 computeRank()"],
    tail: "panic: runtime error",
    reportUrl: "https://example.com/report"
  });

  assert.equal(card.header.template, "orange");
  assert.match(card.header.title.content, /a3f9c2d1/);
  assert.equal(card.elements.at(-1).tag, "action");
  assert.equal(JSON.stringify(card).includes("标记误报"), false);
  assert.equal(JSON.stringify(card).includes("```"), false);
  assert.equal(card.elements[1].text.tag, "plain_text");
});

test("buildIncidentCard omits action block without reportUrl", () => {
  const card = buildIncidentCard({
    fingerprint: "a3f9c2d1",
    summary: "zsh: command not found: badcmd",
    command: "badcmd",
    severity: "warning",
    tail: "zsh: command not found: badcmd"
  });

  assert.equal(card.elements.some(element => element.tag === "action"), false);
});

test("buildIncidentCard renders new_case branch with 🆕 title and no history block", () => {
  const card = buildIncidentCard({
    fingerprint: "a3f9c2d1",
    summary: "panic: runtime error",
    command: "go run ./cmd/grade serve",
    severity: "warning",
    registryBranch: "new_case",
    occurrences: 0
  });

  assert.match(card.header.title.content, /🆕/);
  assert.match(card.header.title.content, /新错误已立案/);
  assert.equal(card.header.template, "orange");
  // history block must be absent for new_case
  const hasHistory = card.elements.some(
    (e) => e?.text?.content?.startsWith?.("历史\n") || e?.text?.content?.startsWith?.("升级原因\n")
  );
  assert.equal(hasHistory, false);
});

test("buildIncidentCard renders recurrence branch with 🔁 title and history line", () => {
  const card = buildIncidentCard({
    fingerprint: "a3f9c2d1",
    summary: "panic: runtime error",
    command: "go run ./cmd/grade serve",
    severity: "warning",
    registryBranch: "recurrence",
    occurrences: 4,
    affectedUsers: 2,
    windowOccurrences: 3,
    windowMinutes: 120,
    firstSeen: "2026-04-25T00:58:12+08:00",
    lastSeen: "2026-05-06T20:55:00+08:00",
    reportUrl: "https://feishu/reports/abc"
  });

  assert.match(card.header.title.content, /🔁/);
  assert.match(card.header.title.content, /历史同款/);

  const history = card.elements.find((e) => e?.text?.content?.startsWith?.("历史\n"));
  assert.ok(history, "history block missing");
  assert.match(history.text.content, /此前发生 4 次/);
  assert.match(history.text.content, /受影响 2 人/);
  assert.match(history.text.content, /近 120 分钟内 3 次/);
  assert.match(history.text.content, /首次发现 2026-04-25/);
  assert.match(history.text.content, /最近发现 2026-05-06/);

  const action = card.elements.find((e) => e.tag === "action");
  assert.ok(action);
  assert.equal(action.actions[0].text.content, "打开历史报告");
});

test("buildIncidentCard renders escalation_candidate branch with ⛔️ and 升级原因", () => {
  const card = buildIncidentCard({
    fingerprint: "a3f9c2d1",
    summary: "panic: runtime error",
    command: "go run ./cmd/grade serve",
    severity: "incident",
    registryBranch: "escalation_candidate",
    occurrences: 7,
    affectedUsers: 4,
    windowOccurrences: 5,
    windowMinutes: 120,
    reportUrl: "https://feishu/reports/abc"
  });

  assert.match(card.header.title.content, /⛔️/);
  assert.match(card.header.title.content, /重复发生升级/);
  assert.equal(card.header.template, "red");

  const history = card.elements.find((e) => e?.text?.content?.startsWith?.("升级原因\n"));
  assert.ok(history, "escalation reason block missing");
  assert.match(history.text.content, /此前发生 7 次/);
  assert.match(history.text.content, /受影响 4 人/);

  const action = card.elements.find((e) => e.tag === "action");
  assert.equal(action.actions[0].text.content, "查看升级报告");
});

test("buildIncidentCard ignores unknown registryBranch and falls back to severity title", () => {
  const card = buildIncidentCard({
    fingerprint: "a3f9c2d1",
    summary: "x",
    command: "y",
    severity: "warning",
    registryBranch: "garbage_value",
    occurrences: 0
  });

  // unknown branch -> severity fallback title (no emoji prefix)
  assert.equal(card.header.title.content.startsWith("termind · "), true);
  assert.equal(/🆕|🔁|⛔️/.test(card.header.title.content), false);
});

test("buildIncidentCard history block degrades when only occurrences is known", () => {
  const card = buildIncidentCard({
    fingerprint: "a3f9c2d1",
    summary: "x",
    command: "y",
    severity: "warning",
    registryBranch: "recurrence",
    occurrences: 2
    // no affectedUsers / window / firstSeen / lastSeen
  });

  const history = card.elements.find((e) => e?.text?.content?.startsWith?.("历史\n"));
  assert.ok(history);
  assert.match(history.text.content, /此前发生 2 次/);
  assert.equal(/受影响/.test(history.text.content), false);
  assert.equal(/分钟内/.test(history.text.content), false);
  assert.equal(/首次发现|最近发现/.test(history.text.content), false);
});

test("buildIncidentCard renders @open_id when owner has high confidence", () => {
  const card = buildIncidentCard({
    fingerprint: "a3f9c2d1",
    summary: "panic",
    command: "go run",
    severity: "warning",
    owner: { kind: "lark_user", openId: "ou_alice", label: "@alice", confidence: "high" }
  });
  const ownerEl = card.elements.find(e => /\*\*责任人:\*\*/.test(e?.text?.content || ""));
  assert.ok(ownerEl);
  assert.match(ownerEl.text.content, /<at id=ou_alice><\/at>/);
  assert.match(ownerEl.text.content, /@alice/);
});

test("buildIncidentCard avoids @-mention when owner is label_only", () => {
  const card = buildIncidentCard({
    fingerprint: "a3f9c2d1",
    summary: "panic",
    command: "go run",
    severity: "warning",
    owner: {
      kind: "git_author",
      openId: "",
      label: "Alice",
      email: "alice@b.com",
      confidence: "label_only"
    }
  });
  const ownerEl = card.elements.find(e => /\*\*责任人:\*\*/.test(e?.text?.content || ""));
  assert.ok(ownerEl);
  assert.equal(/<at /.test(ownerEl.text.content), false);
  assert.match(ownerEl.text.content, /Alice/);
  assert.match(ownerEl.text.content, /alice@b\.com/);
});

test("buildIncidentCard renders knowledge hits as a bulleted list with links", () => {
  const card = buildIncidentCard({
    fingerprint: "a3f9c2d1",
    summary: "panic",
    command: "go run",
    severity: "warning",
    knowledgeHits: [
      { token: "doccnXX", type: "doc", title: "Go panic playbook", url: "https://feishu/docs/doccnXX" },
      { token: "wikicnYY", type: "wiki", title: "应急手册", url: "https://feishu/wiki/wikicnYY" }
    ]
  });
  const knowEl = card.elements.find(e => /相关知识/.test(e?.text?.content || ""));
  assert.ok(knowEl);
  assert.match(knowEl.text.content, /Go panic playbook/);
  assert.match(knowEl.text.content, /https:\/\/feishu\/docs\/doccnXX/);
  assert.match(knowEl.text.content, /应急手册/);
});

test("buildIncidentCard omits owner block when owner is null", () => {
  const card = buildIncidentCard({
    fingerprint: "a3f9c2d1",
    summary: "panic",
    command: "go run",
    severity: "warning"
  });
  const ownerEl = card.elements.find(e => /\*\*责任人:\*\*/.test(e?.text?.content || ""));
  assert.equal(ownerEl, undefined);
});
