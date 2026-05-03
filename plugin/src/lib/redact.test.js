import assert from "node:assert/strict";
import test from "node:test";

import { normalizeFailureEvent, redactFailureEvent } from "./redact.js";

test("normalizeFailureEvent validates required fields", () => {
  assert.throws(() => normalizeFailureEvent({}, { requireFingerprint: false }), /summary is required/);
  const event = normalizeFailureEvent({
    summary: "boom",
    command: "go test ./...",
    severity: "bad"
  }, { requireFingerprint: false });
  assert.equal(event.severity, "warning");
});

test("redactFailureEvent removes common secrets", () => {
  const event = redactFailureEvent(
    normalizeFailureEvent({
      fingerprint: "a3f9c2d1",
      summary: "authorization: Bearer secret-token",
      command: "curl -H 'Authorization: Bearer abc.def'",
      severity: "incident",
      tail: "password=super-secret\napi_key: sk-123"
    })
  );

  assert.doesNotMatch(event.summary, /secret-token/);
  assert.doesNotMatch(event.command, /abc\.def/);
  assert.doesNotMatch(event.tail, /super-secret|sk-123/);
});
