export function classifyFailure(event) {
  const summary = `${event.summary}\n${event.tail || ""}`.toLowerCase();
  const occurrences = Number(event.occurrences || 0);
  const affectedUsers = Number(event.affectedUsers || 0);

  const reasons = [];
  let severity = "warning";

  if (occurrences >= 5 || affectedUsers >= 3) {
    severity = "incident";
    reasons.push("repeated fingerprint across users or occurrences");
  }
  if (summary.includes("panic:") || summary.includes("segmentation fault") || summary.includes("fatal error")) {
    if (severity !== "incident") severity = "warning";
    reasons.push("runtime crash signature");
  }
  if (event.branchKind === "main" && (summary.includes("failed") || summary.includes("panic"))) {
    severity = "incident";
    reasons.push("main branch failure");
  }
  if (!event.stackTop?.length && !event.tail) {
    severity = "info";
    reasons.push("low evidence event");
  }

  return {
    severity,
    route: severity === "incident" ? "team_channel" : severity === "warning" ? "private_or_test_channel" : "local_only",
    reasons,
    shouldCreateReport: severity !== "info",
    shouldSearchKnowledge: true
  };
}
