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
    larkForwardingIdentities: normalizeForwardingIdentities(input.larkForwardingIdentities),
    larkForwardingRoutes: normalizeForwardingRoutes(input.larkForwardingRoutes),
    stackTop: Array.isArray(input.stackTop)
      ? input.stackTop.map(value => optionalString(value, 300)).filter(Boolean).slice(0, 5)
      : [],
    reportUrl: optionalString(input.reportUrl, 500),
    occurrences: numberOrZero(input.occurrences),
    affectedUsers: numberOrZero(input.affectedUsers),
    branchKind: optionalString(input.branchKind, 40),
    // termind-incident-registry skill 把这些字段 merge 回 event 后再传入
    // termind_lark_card_build, classify, report 工具. normalize 必须保留它们,
    // 否则 card 拿不到 registryBranch, 一律渲染成兜底标题, 历史块也失踪.
    registryBranch: normalizeRegistryBranch(input.registryBranch),
    windowOccurrences: numberOrZero(input.windowOccurrences),
    windowMinutes: numberOrZero(input.windowMinutes),
    firstSeen: optionalString(input.firstSeen, 64),
    lastSeen: optionalString(input.lastSeen, 64),
    // 责任人路由: termind-owner-resolve skill 把 git author -> lark open_id
    // 解析的结果 merge 回 event, card / report 据此 @ 责任人.
    owner: normalizeOwner(input.owner),
    // 知识 RAG 命中: termind-lark-knowledge-search skill 把检索到的 doc 命中
    // 作为只读字段挂到 event, card / report 据此渲染参考链接.
    knowledgeHits: normalizeKnowledgeHits(input.knowledgeHits)
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

// CLI 端 (Termind cli/internal/diagnose) 通过 larkForwarding{Identities,Routes}
// 把 ~/.config/termind/config.json 里的 lark-cli 多 identity 路由表传给 plugin.
// buildLarkCliCommands 优先消费这两个字段; 如果 normalize 这里把它们丢掉,
// 真实链路就只能 fallback 到 larkTargets / larkChatId / larkUserOpenId — 但 CLI
// 实际上不一定填 larkTargets, 所以会出现"链路无报错却发不出消息"的隐性故障.
//
// 因此 normalize 必须保留这两个字段, 同时做一层轻度净化, 防止上游传入垃圾.
function normalizeForwardingIdentities(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) return {};
  const out = {};
  for (const [key, identity] of Object.entries(value)) {
    if (!identity || typeof identity !== "object") continue;
    const cleaned = {
      id: optionalString(identity.id, 200),
      kind: identity.kind === "user" ? "user" : "bot",
      label: optionalString(identity.label, 200),
      appId: optionalString(identity.appId, 200),
      userOpenId: optionalString(identity.userOpenId, 200),
      profile: optionalString(identity.profile, 200),
      larkCliConfigDir: optionalString(identity.larkCliConfigDir, 500),
      enabled: identity.enabled !== false,
      source: optionalString(identity.source, 200),
      slot: optionalString(identity.slot, 200)
    };
    // 至少要有一个稳定标识 (id 或 appId), 否则下游无法路由到具体 lark-cli profile.
    if (!cleaned.id && !cleaned.appId) continue;
    out[String(key).slice(0, 200)] = cleaned;
  }
  return out;
}

function normalizeForwardingRoutes(value) {
  if (!Array.isArray(value)) return [];
  return value.map(route => {
    if (!route || typeof route !== "object") return null;
    const target = route.target && typeof route.target === "object"
      ? {
          type: normalizeLarkTargetType(route.target.type),
          id: optionalString(route.target.id, 160),
          label: optionalString(route.target.label, 120)
        }
      : null;
    if (!target?.id) return null;
    return {
      identityId: optionalString(route.identityId, 200),
      target,
      enabled: route.enabled !== false
    };
  }).filter(Boolean).slice(0, 20);
}

function normalizeLarkTargetType(value) {
  if (value === "user" || value === "bot") return value;
  return "chat";
}

function normalizeRegistryBranch(value) {
  const s = String(value ?? "").trim().toLowerCase();
  if (s === "new_case" || s === "recurrence" || s === "escalation_candidate") return s;
  return "";
}

function normalizeOwner(owner) {
  if (!owner || typeof owner !== "object" || Array.isArray(owner)) return null;
  const cleaned = {
    kind: optionalString(owner.kind, 40),
    openId: optionalString(owner.openId ?? owner.open_id, 200),
    label: optionalString(owner.label ?? owner.name, 200),
    email: optionalString(owner.email, 200),
    source: optionalString(owner.source, 80),
    confidence: optionalString(owner.confidence, 40)
  };
  // 没有任何稳定标识时丢弃, 避免 card 渲染成 "@undefined".
  if (!cleaned.openId && !cleaned.label && !cleaned.email) return null;
  return cleaned;
}

function normalizeKnowledgeHits(value) {
  if (!Array.isArray(value)) return [];
  const out = [];
  const seen = new Set();
  for (const raw of value) {
    if (!raw || typeof raw !== "object") continue;
    const token = optionalString(raw.token ?? raw.objToken ?? raw.docToken, 200);
    if (!token) continue;
    const key = token + "|" + optionalString(raw.type, 40);
    if (seen.has(key)) continue;
    seen.add(key);
    out.push({
      token,
      type: optionalString(raw.type, 40),
      title: optionalString(raw.title, 300),
      url: optionalString(raw.url, 500),
      ownerOpenId: optionalString(raw.ownerOpenId ?? raw.owner_open_id, 200),
      ownerName: optionalString(raw.ownerName ?? raw.owner_name, 200),
      lastModified: optionalString(raw.lastModified ?? raw.last_modified, 64),
      snippet: optionalString(raw.snippet, 600),
      score: numberOrZero(raw.score)
    });
    if (out.length >= 10) break;
  }
  return out;
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
