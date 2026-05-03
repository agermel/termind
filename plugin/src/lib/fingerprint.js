import { createHash } from "node:crypto";

export function computeFingerprint(event) {
  const basisParts = [
    normalize(event.project),
    commandFamily(event.command),
    errorKind(event.summary || event.tail || ""),
    topFrame(event.stackTop),
    normalizeDynamic(event.summary || "")
  ].filter(Boolean);
  const basis = basisParts.join("|");
  const fingerprint = createHash("sha256").update(basis || "unknown").digest("hex").slice(0, 8);
  return {
    fingerprint,
    basis,
    algorithm: "termind-v1",
    confidence: basisParts.length >= 3 ? "high" : basisParts.length >= 2 ? "medium" : "low"
  };
}

function commandFamily(command) {
  const first = normalize(command).split(/\s+/).slice(0, 2).join(" ");
  return first;
}

function errorKind(text) {
  const value = normalize(text);
  if (value.includes("panic: runtime error")) return "go_panic_runtime";
  if (value.includes("no matches for kind")) return "k8s_no_matches_for_kind";
  if (value.includes("command not found")) return "shell_command_not_found";
  if (value.includes("segmentation fault")) return "segfault";
  if (value.includes("permission denied")) return "permission_denied";
  return value.split("\n")[0]?.slice(0, 120) || "unknown_error";
}

function topFrame(stackTop) {
  if (!Array.isArray(stackTop) || stackTop.length === 0) return "";
  return normalizeDynamic(stackTop[0]).slice(0, 180);
}

function normalizeDynamic(value) {
  return normalize(value)
    .replace(/[a-f0-9]{7,40}/g, "<hex>")
    .replace(/\b\d{2,}\b/g, "<num>")
    .replace(/\/Users\/[^/\s]+/g, "/Users/<user>")
    .replace(/\/home\/[^/\s]+/g, "/home/<user>")
    .replace(/\b[0-9a-f]{8}-[0-9a-f-]{27,}\b/g, "<uuid>");
}

function normalize(value) {
  return String(value || "").trim().toLowerCase().replace(/\s+/g, " ");
}
