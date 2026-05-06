import type { FailureEvent } from "../schemas/failure-event.js";

const secretPatterns: RegExp[] = [
  /\b(Bearer\s+)[A-Za-z0-9._~+/=-]+/g,
  /\b(token|password|passwd|secret|api[_-]?key|authorization|cookie)\s*[:=]\s*("[^"]*"|'[^']*'|[^\s]+)/gi,
  /-----BEGIN [A-Z ]+PRIVATE KEY-----[\s\S]*?-----END [A-Z ]+PRIVATE KEY-----/g,
];

/** Normalize a raw failure event into a canonical FailureEvent.
 *  - Validates required fields (`summary`, `command`).
 *  - Truncates all string fields to their declared max lengths.
 *  - Fills defaults for missing optional fields. */
export function normalizeFailureEvent(
  input: Record<string, unknown>,
): FailureEvent {
  return {
    fingerprint: optionalString(input.fingerprint, 64),
    summary: requiredString(input.summary, "summary").slice(0, 300),
    command: requiredString(input.command, "command").slice(0, 1000),
    severity: normalizeSeverity(input.severity),
    exitCode: numberOrUndefined(input.exitCode),
    cwd: optionalString(input.cwd, 500),
    project: optionalString(input.project, 120),
    user: optionalString(input.user, 120),
    branch: optionalString(input.branch, 120),
    gitCommit: optionalString(input.gitCommit, 80),
    environment: optionalString(input.environment, 200),
    shell: optionalString(input.shell, 40),
    tail: optionalString(input.tail, 4000),
    larkChatId: optionalString(
      (input as any).larkChatId ?? (input as any).chatId,
      160,
    ),
    stackTop: Array.isArray(input.stackTop)
      ? (input.stackTop as unknown[])
          .map((v) => optionalString(v, 300))
          .filter(Boolean)
          .slice(0, 5)
      : [],
    reportUrl: optionalString(input.reportUrl, 500),
    occurrences: safeNonNegativeInt(input.occurrences),
    affectedUsers: safeNonNegativeInt(input.affectedUsers),
    branchKind: optionalString(input.branchKind, 40),
  };
}

/** Redact secrets from every text field in a normalized failure event.
 *  Returns a shallow copy; does not mutate the input. */
export function redactFailureEvent(event: FailureEvent): FailureEvent {
  return {
    ...event,
    summary: redact(event.summary),
    command: redact(event.command),
    cwd: redact(event.cwd),
    environment: redact(event.environment),
    tail: redact(event.tail),
    stackTop: event.stackTop?.map(redact) ?? [],
  };
}

// ── helpers ──────────────────────────────────────────────────────────

function requiredString(value: unknown, name: string): string {
  const out = optionalString(value, 0);
  if (!out) {
    throw new Error(`${name} is required`);
  }
  return out;
}

function optionalString(value: unknown, max: number): string {
  if (value == null) return "";
  const out = String(value).trim();
  if (max > 0 && out.length > max) return out.slice(0, max);
  return out;
}

function normalizeSeverity(value: unknown): FailureEvent["severity"] {
  if (value === "info" || value === "warning" || value === "incident") {
    return value;
  }
  return "warning";
}

function numberOrUndefined(value: unknown): number | undefined {
  const n = Number(value);
  return Number.isFinite(n) ? n : undefined;
}

function safeNonNegativeInt(value: unknown): number {
  const n = Number(value);
  return Number.isFinite(n) && n > 0 ? Math.floor(n) : 0;
}

function redact(value: string | undefined): string {
  if (!value) return "";
  let out = String(value);
  for (const pattern of secretPatterns) {
    out = out.replace(pattern, (_match, prefix = "") =>
      prefix ? `${prefix}[REDACTED]` : "[REDACTED]",
    );
  }
  return out;
}
