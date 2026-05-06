import { createHash } from "node:crypto";
import type { FailureEvent } from "../schemas/failure-event.js";

export interface FingerprintResult {
  fingerprint: string;
  basis: string;
  algorithm: "termind-v1";
  confidence: "high" | "medium" | "low";
}

/** Compute a stable deterministic fingerprint for a normalized failure event. */
export function computeFingerprint(event: FailureEvent): FingerprintResult {
  const basisParts = [
    normalize(event.project),
    commandFamily(event.command),
    errorKind(event.summary || event.tail || ""),
    topFrame(event.stackTop ?? []),
    normalize(event.summary || ""),
  ]
    .map(normalizeDynamic)
    .filter(Boolean);

  const basis = basisParts.join("|");
  const fingerprint = createHash("sha256")
    .update(basis || "unknown")
    .digest("hex")
    .slice(0, 8);

  return {
    fingerprint,
    basis,
    algorithm: "termind-v1",
    confidence:
      basisParts.length >= 3
        ? "high"
        : basisParts.length >= 2
          ? "medium"
          : "low",
  };
}

// ── dimension extractors ─────────────────────────────────────────────

function commandFamily(command: string): string {
  return normalize(command).split(/\s+/).slice(0, 2).join(" ");
}

function errorKind(text: string): string {
  const value = normalize(text);
  if (value.includes("panic: runtime error")) return "go_panic_runtime";
  if (value.includes("no matches for kind")) return "k8s_no_matches_for_kind";
  if (value.includes("command not found")) return "shell_command_not_found";
  if (value.includes("segmentation fault")) return "segfault";
  if (value.includes("permission denied")) return "permission_denied";
  if (value.includes("panic:")) return "go_panic";
  if (value.includes("fatal error")) return "go_fatal";
  if (value.includes("syntaxerror") || value.includes("syntax error"))
    return "syntax_error";
  return value.split("\n")[0]?.slice(0, 120) || "unknown_error";
}

function topFrame(stackTop: string[]): string {
  if (!stackTop.length) return "";
  return normalizeDynamic(stackTop[0]).slice(0, 180);
}

// ── normalizers ───────────────────────────────────────────────────────

/** Normalize dynamic values in text so same-class errors produce the same
 *  fingerprint: hex hashes → `<hex>`, numbers → `<num>`,
 *  user home paths → `/Users/<user>` or `/home/<user>`, UUIDs → `<uuid>`. */
function normalizeDynamic(value: string): string {
  return normalize(value)
    .replace(/[a-f0-9]{7,40}/g, "<hex>")
    .replace(/\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b/g, "<ip>")
    .replace(/\b\d{2,}\b/g, "<num>")
    .replace(/\/Users\/[^/\s]+/g, "/Users/<user>")
    .replace(/\/home\/[^/\s]+/g, "/home/<user>")
    .replace(/\b[0-9a-f]{8}-[0-9a-f-]{27,}\b/g, "<uuid>");
}

function normalize(value: string): string {
  return String(value || "").trim().toLowerCase().replace(/\s+/g, " ");
}
