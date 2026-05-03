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
  assert.equal(JSON.stringify(card).includes("```"), false);
  assert.equal(card.elements[1].text.tag, "plain_text");
});
