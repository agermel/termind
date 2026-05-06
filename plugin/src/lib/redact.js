const secretPatterns = [
  /\b(Bearer\s+)[A-Za-z0-9._~+/=-]+/g,
  /\b(token|password|passwd|secret|api[_-]?key|authorization|cookie)\s*[:=]\s*("[^"]*"|'[^']*'|[^\s]+)/gi,
  /-----BEGIN [A-Z ]+PRIVATE KEY-----[\s\S]*?-----END [A-Z ]+PRIVATE KEY-----/g
];

export function normalizeFailureEvent(input, options = {}) {
  const requireFingerprint = options.requireFingerprint ?? true;
  return {
    fingerprint: requireFingerprint ? requiredString(input.fingerprint, "fingerprint").slice(0, 64) : optionalString(input.fingerprint, 64),
    summary: requiredString(input.summary, "summary").slice(0, 300),
    command: requiredString(input.command, "command").slice(0, 1000),
    severity: normalizeSeverity(input.severity),
    cwd: optionalString(input.cwd, 500),
    project: optionalString(input.project, 120),
    user: optionalString(input.user, 120),
    branch: optionalString(input.branch, 120),
    gitCommit: optionalString(input.gitCommit, 80),
    environment: optionalString(input.environment, 200),
    tail: optionalString(input.tail, 4000),
    larkChatId: optionalString(input.larkChatId ?? input.chatId, 160),
    larkUserOpenId: optionalString(input.larkUserOpenId ?? input.userOpenId, 160),
    larkSender: normalizeLarkSender(input.larkSender ?? input.sender),
    larkTargets: normalizeLarkTargets(input.larkTargets ?? input.targets),
    stackTop: Array.isArray(input.stackTop)
      ? input.stackTop.map(value => optionalString(value, 300)).filter(Boolean).slice(0, 5)
      : [],
    reportUrl: optionalString(input.reportUrl, 500),
    occurrences: numberOrZero(input.occurrences),
    affectedUsers: numberOrZero(input.affectedUsers),
    branchKind: optionalString(input.branchKind, 40)
  };
}

export function redactFailureEvent(event) {
  return {
    ...event,
    summary: redact(event.summary),
    command: redact(event.command),
    cwd: redact(event.cwd),
    environment: redact(event.environment),
    tail: redact(event.tail),
    stackTop: event.stackTop.map(redact)
  };
}

function requiredString(value, name) {
  const out = optionalString(value, 0);
  if (!out) throw new Error(`${name} is required`);
  return out;
}

function optionalString(value, max) {
  if (value == null) return "";
  const out = cleanTerminalText(String(value)).trim();
  if (max > 0) return out.slice(0, max);
  return out;
}

function normalizeSeverity(value) {
  if (value === "info" || value === "warning" || value === "incident") {
    return value;
  }
  return "warning";
}

function normalizeLarkTargets(value) {
  if (!Array.isArray(value)) return [];
  return value.map(target => ({
    type: normalizeLarkTargetType(target?.type),
    id: optionalString(target?.id ?? target?.target ?? target?.chatId ?? target?.userId, 160),
    label: optionalString(target?.label ?? target?.name, 120),
    enabled: target?.enabled !== false
  })).filter(target => target.id).slice(0, 10);
}

function normalizeLarkTargetType(value) {
  if (value === "user" || value === "bot") return value;
  return "chat";
}

function normalizeLarkSender(value) {
  if (value === "user") return "user";
  return "bot";
}

function numberOrZero(value) {
  const n = Number(value);
  return Number.isFinite(n) && n > 0 ? n : 0;
}

function redact(value) {
  if (!value) return "";
  let out = String(value);
  for (const pattern of secretPatterns) {
    out = out.replace(pattern, (_match, prefix = "") => `${prefix}[REDACTED]`);
  }
  return cleanTerminalText(out);
}

function cleanTerminalText(value) {
  return String(value)
    .replace(/\r\n/g, "\n")
    .replace(/\r/g, "\n")
    .replace(/\x1B(?:[@-Z\\-_]|\[[0-?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1B\\))/g, "")
    .replace(/[\x00-\x08\x0B\x0C\x0E-\x1F\x7F]/g, "");
}
