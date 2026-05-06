import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { buildIncidentCard } from "../src/lib/card.ts";
import { normalizeFailureEvent } from "../src/lib/redact.ts";
import { computeFingerprint } from "../src/lib/fingerprint.ts";

const base = normalizeFailureEvent({
  summary: "panic: runtime error: invalid memory address",
  command: "go run ./cmd/server",
  severity: "warning",
  project: "be-grade",
  user: "matterhorn",
  branch: "feat/rank-v2",
  gitCommit: "8e4d21a",
  environment: "go1.22.3 macOS",
  stackTop: [
    "be-grade/cron/rank.go:87 computeRank()",
    "be-grade/cron/rank.go:42 (*RankJob).Run()",
  ],
  tail: "goroutine 1 [running]:",
});
base.fingerprint = computeFingerprint(base).fingerprint;

describe("buildIncidentCard", () => {
  it("returns a card with config and header", () => {
    const card = buildIncidentCard(base);
    const c = card as Record<string, unknown>;
    assert.ok(c.config);
    assert.ok(c.header);
    assert.ok(Array.isArray(c.elements));
  });

  it("sets wide_screen_mode to true", () => {
    const card = buildIncidentCard(base);
    const c = card as Record<string, unknown>;
    assert.equal((c.config as any)?.wide_screen_mode, true);
  });

  it("uses orange tag for warning severity", () => {
    const card = buildIncidentCard(base);
    const header = (card as any).header;
    assert.equal(header.template, "orange");
  });

  it("uses red tag for incident severity", () => {
    const incident = { ...base, severity: "incident" as const };
    const card = buildIncidentCard(incident);
    assert.equal((card as any).header.template, "red");
  });

  it("includes summary text in elements", () => {
    const card = buildIncidentCard(base);
    const json = JSON.stringify(card);
    assert.ok(json.includes(base.summary));
  });

  it("includes fingerprint in header title", () => {
    const card = buildIncidentCard(base);
    const title = (card as any).header.title.content as string;
    assert.ok(title.includes(base.fingerprint!));
  });

  it("always includes the false-positive action button", () => {
    const card = buildIncidentCard(base);
    const json = JSON.stringify(card);
    assert.ok(json.includes("标记误报"));
    assert.ok(json.includes("termind.false_positive"));
  });

  it("includes report button when reportUrl is set", () => {
    const withReport = { ...base, reportUrl: "https://feishu.cn/doc/123" };
    const card = buildIncidentCard(withReport);
    const json = JSON.stringify(card);
    assert.ok(json.includes("打开报告"));
  });
});
