import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { buildReportTemplate } from "../src/lib/report.ts";
import { normalizeFailureEvent } from "../src/lib/redact.ts";
import { computeFingerprint } from "../src/lib/fingerprint.ts";

const base = normalizeFailureEvent({
  summary: "panic: runtime error",
  command: "go run ./cmd/server",
  severity: "warning",
  project: "be-grade",
  user: "matterhorn",
  branch: "feat/rank-v2",
  gitCommit: "8e4d21a",
  environment: "go1.22.3 macOS",
  stackTop: ["rank.go:87 computeRank()"],
  tail: "goroutine 1 [running]:",
});
base.fingerprint = computeFingerprint(base).fingerprint;

describe("buildReportTemplate", () => {
  it("returns a non-empty string", () => {
    const md = buildReportTemplate(base);
    assert.ok(md.length > 0);
  });

  it("includes fingerprint in title", () => {
    const md = buildReportTemplate(base);
    assert.ok(md.includes(base.fingerprint!));
  });

  it("includes all required sections", () => {
    const md = buildReportTemplate(base);
    assert.ok(md.includes("## 元数据"));
    assert.ok(md.includes("## 现场快照"));
    assert.ok(md.includes("## 根本原因"));
    assert.ok(md.includes("## 解决方案"));
    assert.ok(md.includes("## 预防措施"));
    assert.ok(md.includes("## 历史共现"));
  });

  it("includes the command text", () => {
    const md = buildReportTemplate(base);
    assert.ok(md.includes("go run ./cmd/server"));
  });

  it("handles missing optional fields gracefully", () => {
    const minimal = normalizeFailureEvent({ summary: "err", command: "cmd" });
    const md = buildReportTemplate(minimal);
    assert.ok(md.includes("(unknown)"));
    assert.ok(md.includes("(none captured)"));
  });
});
