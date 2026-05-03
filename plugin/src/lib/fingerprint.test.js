import assert from "node:assert/strict";
import test from "node:test";

import { computeFingerprint } from "./fingerprint.js";

test("computeFingerprint is stable across dynamic values", () => {
  const base = {
    project: "be-grade",
    command: "go run ./cmd/grade serve",
    summary: "panic: runtime error: invalid memory address at 12345",
    stackTop: ["be-grade/cron/rank.go:87 computeRank commit abcdef123456"]
  };
  const changed = {
    ...base,
    summary: "panic: runtime error: invalid memory address at 67890",
    stackTop: ["be-grade/cron/rank.go:87 computeRank commit 987654abcdef"]
  };

  assert.equal(computeFingerprint(base).fingerprint, computeFingerprint(changed).fingerprint);
});
