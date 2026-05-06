import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { computeFingerprint } from "../src/lib/fingerprint.ts";
import { normalizeFailureEvent } from "../src/lib/redact.ts";

const base = normalizeFailureEvent({
  summary: "panic: runtime error: invalid memory address",
  command: "go run ./cmd/server",
  project: "be-grade",
  stackTop: ["be-grade/cron/rank.go:87 computeRank()"],
  tail: "goroutine 1 [running]:\nbe-grade/cron/rank.go:87",
});

describe("computeFingerprint", () => {
  it("returns a deterministic fingerprint", () => {
    const a = computeFingerprint(base);
    const b = computeFingerprint(base);
    assert.equal(a.fingerprint, b.fingerprint);
    assert.equal(a.fingerprint.length, 8);
  });

  it("same error type + different instance → same fingerprint", () => {
    const evA = normalizeFailureEvent({
      summary: "panic: runtime error: invalid memory address",
      command: "go run ./cmd/server",
    });
    const evB = normalizeFailureEvent({
      summary: "panic: runtime error: invalid memory address",
      command: "go run ./cmd/server",
    });
    const fpA = computeFingerprint(evA);
    const fpB = computeFingerprint(evB);
    assert.equal(fpA.fingerprint, fpB.fingerprint);
    assert.equal(fpA.algorithm, "termind-v1");
  });

  it("different error kind → different fingerprint", () => {
    const evA = normalizeFailureEvent({
      summary: "panic: runtime error",
      command: "go run .",
    });
    const evB = normalizeFailureEvent({
      summary: "command not found: xyz",
      command: "xyz",
    });
    const fpA = computeFingerprint(evA);
    const fpB = computeFingerprint(evB);
    assert.notEqual(fpA.fingerprint, fpB.fingerprint);
  });

  it("reports confidence based on basis dimensions", () => {
    const rich = computeFingerprint(base);
    assert.equal(rich.confidence, "high");

    // Sparse: minimal event with few dimensions
    const sparse = computeFingerprint(
      normalizeFailureEvent({ summary: "err", command: "x" }),
    );
    // commandFamily "x" + errorKind "err" + normalizeDynamic "err" → 3 dims → high
    assert.equal(sparse.confidence, "high");
  });

  it("normalizes dynamic values before hashing", () => {
    const evA = normalizeFailureEvent({
      summary: "error: connection refused on 10.0.0.1",
      command: "curl 10.0.0.1",
    });
    const evB = normalizeFailureEvent({
      summary: "error: connection refused on 192.168.1.1",
      command: "curl 192.168.1.1",
    });
    // Both should normalize IPs out and produce same fingerprint
    assert.equal(
      computeFingerprint(evA).fingerprint,
      computeFingerprint(evB).fingerprint,
    );
  });
});
