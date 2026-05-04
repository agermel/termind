import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { classifyFailure } from "../src/lib/classify.ts";
import { normalizeFailureEvent } from "../src/lib/redact.ts";

describe("classifyFailure", () => {
  it("defaults to warning for normal failures", () => {
    const event = normalizeFailureEvent({
      summary: "build failed",
      command: "go build",
      tail: "error: undefined reference",
    });
    const result = classifyFailure(event);
    assert.equal(result.severity, "warning");
    assert.equal(result.route, "private_or_test_channel");
  });

  it("upgrades to incident on high occurrences", () => {
    const event = normalizeFailureEvent({
      summary: "build failed",
      command: "go build",
      occurrences: 5,
    });
    const result = classifyFailure(event);
    assert.equal(result.severity, "incident");
  });

  it("upgrades to incident on many affected users", () => {
    const event = normalizeFailureEvent({
      summary: "build failed",
      command: "go build",
      affectedUsers: 3,
    });
    const result = classifyFailure(event);
    assert.equal(result.severity, "incident");
  });

  it("recognizes panic as runtime crash", () => {
    const event = normalizeFailureEvent({
      summary: "panic: nil pointer dereference",
      command: "go run .",
    });
    const result = classifyFailure(event);
    assert.ok(result.reasons.includes("runtime crash signature"));
  });

  it("upgrades main branch failures to incident", () => {
    const event = normalizeFailureEvent({
      summary: "panic: nil pointer",
      command: "go run .",
      branchKind: "main",
    });
    const result = classifyFailure(event);
    assert.equal(result.severity, "incident");
    assert.ok(result.reasons.includes("main branch failure"));
  });

  it("downgrades low-evidence events to info", () => {
    const event = normalizeFailureEvent({
      summary: "something happened",
      command: "go run .",
      tail: "",
      stackTop: [],
    });
    const result = classifyFailure(event);
    assert.equal(result.severity, "info");
    assert.equal(result.route, "local_only");
    assert.equal(result.shouldCreateReport, false);
  });

  it("always sets shouldSearchKnowledge to true", () => {
    const event = normalizeFailureEvent({
      summary: "err",
      command: "cmd",
    });
    assert.equal(classifyFailure(event).shouldSearchKnowledge, true);
  });
});
