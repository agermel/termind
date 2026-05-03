import assert from "node:assert/strict";
import test from "node:test";

import { buildReportTemplate } from "./report.js";

test("buildReportTemplate includes core incident sections", () => {
  const report = buildReportTemplate({
    fingerprint: "a3f9c2d1",
    command: "go run ./cmd/grade serve",
    summary: "panic",
    stackTop: ["rank.go:87"],
    tail: "panic",
    user: "matterhorn"
  });

  assert.match(report, /# \[错误报告\] a3f9c2d1/);
  assert.match(report, /根本原因/);
  assert.match(report, /历史共现/);
});
