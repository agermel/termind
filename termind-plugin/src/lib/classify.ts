import type { FailureEvent } from "../schemas/failure-event.js";

export interface ClassificationResult {
  severity: "info" | "warning" | "incident";
  route: "local_only" | "private_or_test_channel" | "team_channel";
  reasons: string[];
  shouldCreateReport: boolean;
  shouldSearchKnowledge: boolean;
}

/** Classify severity and routing hints for a normalized failure event. */
export function classifyFailure(event: FailureEvent): ClassificationResult {
  const summary = `${event.summary}\n${event.tail || ""}`.toLowerCase();
  const occurrences = event.occurrences ?? 0;
  const affectedUsers = event.affectedUsers ?? 0;

  const reasons: string[] = [];
  let severity: ClassificationResult["severity"] = "warning";

  // Rule 1: high frequency / broad impact → incident
  if (occurrences >= 5 || affectedUsers >= 3) {
    severity = "incident";
    reasons.push("repeated fingerprint across users or occurrences");
  }

  // Rule 2: runtime crash signatures → at least warning
  if (
    summary.includes("panic:") ||
    summary.includes("segmentation fault") ||
    summary.includes("fatal error")
  ) {
    if (severity !== "incident") severity = "warning";
    reasons.push("runtime crash signature");
  }

  // Rule 3: main branch failure → incident
  if (
    event.branchKind === "main" &&
    (summary.includes("failed") || summary.includes("panic"))
  ) {
    severity = "incident";
    reasons.push("main branch failure");
  }

  // Rule 4: low evidence → info (downgrade only when still at warning)
  if (
    severity === "warning" &&
    (!event.stackTop || event.stackTop.length === 0) &&
    !event.tail
  ) {
    severity = "info";
    reasons.push("low evidence event");
  }

  return {
    severity,
    route:
      severity === "incident"
        ? "team_channel"
        : severity === "warning"
          ? "private_or_test_channel"
          : "local_only",
    reasons,
    shouldCreateReport: severity !== "info",
    shouldSearchKnowledge: true,
  };
}
