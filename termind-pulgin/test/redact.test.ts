import { describe, it } from "node:test";
import assert from "node:assert/strict";
import {
  normalizeFailureEvent,
  redactFailureEvent,
} from "../src/lib/redact.ts";

describe("normalizeFailureEvent", () => {
  it("throws when summary is missing", () => {
    assert.throws(() => normalizeFailureEvent({ command: "go build" }), {
      message: "summary is required",
    });
  });

  it("throws when command is missing", () => {
    assert.throws(() => normalizeFailureEvent({ summary: "panic" }), {
      message: "command is required",
    });
  });

  it("fills defaults for missing optional fields", () => {
    const result = normalizeFailureEvent({
      summary: "panic: nil pointer",
      command: "go run .",
    });
    assert.equal(result.fingerprint, "");
    assert.equal(result.project, "");
    assert.equal(result.user, "");
    assert.equal(result.branch, "");
    assert.equal(result.tail, "");
    assert.equal(result.stackTop.length, 0);
    assert.equal(result.occurrences, 0);
    assert.equal(result.severity, "warning");
  });

  it("truncates fields to max lengths", () => {
    const long = "x".repeat(2000);
    const result = normalizeFailureEvent({
      summary: long,
      command: long,
    });
    assert.ok(result.summary.length <= 300);
    assert.ok(result.command.length <= 1000);
  });

  it("preserves valid severity values", () => {
    const base = { summary: "err", command: "cmd" };
    assert.equal(
      normalizeFailureEvent({ ...base, severity: "info" }).severity,
      "info",
    );
    assert.equal(
      normalizeFailureEvent({ ...base, severity: "incident" }).severity,
      "incident",
    );
    assert.equal(
      normalizeFailureEvent({ ...base, severity: "unknown" }).severity,
      "warning",
    );
  });

  it("normalizes stackTop to string array (max 5)", () => {
    const result = normalizeFailureEvent({
      summary: "err",
      command: "cmd",
      stackTop: [1, null, "frame3", "f4", "f5", "f6", "f7"],
    });
    assert.equal(result.stackTop.length, 5);
    assert.equal(result.stackTop[0], "1");
  });

  it("reads larkChatId from chatId as fallback", () => {
    const result = normalizeFailureEvent({
      summary: "err",
      command: "cmd",
      chatId: "oc_test123",
    });
    assert.equal(result.larkChatId, "oc_test123");
  });
});

describe("redactFailureEvent", () => {
  const base = normalizeFailureEvent({ summary: "ok", command: "ls" });

  it("redacts Bearer tokens", () => {
    const event = { ...base, summary: "Authorization: Bearer abc123xyz" };
    const result = redactFailureEvent(event);
    assert.ok(result.summary.includes("[REDACTED]"));
    assert.ok(!result.summary.includes("abc123xyz"));
  });

  it("redacts key=value secrets", () => {
    const event = {
      ...base,
      tail: "token=sk-abc123\npassword: secret123",
    };
    const result = redactFailureEvent(event);
    assert.ok(result.tail.includes("[REDACTED]"));
    assert.ok(!result.tail.includes("sk-abc123"));
    assert.ok(!result.tail.includes("secret123"));
  });

  it("redacts PEM private keys", () => {
    const event = {
      ...base,
      tail:
        "-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKC\n-----END RSA PRIVATE KEY-----",
    };
    const result = redactFailureEvent(event);
    assert.ok(result.tail.includes("[REDACTED]"));
    assert.ok(!result.tail.includes("RSA PRIVATE KEY"));
  });

  it("does not mutate the input event", () => {
    const event = { ...base, summary: "Authorization: Bearer xyz" };
    const original = event.summary;
    redactFailureEvent(event);
    assert.equal(event.summary, original);
  });
});
