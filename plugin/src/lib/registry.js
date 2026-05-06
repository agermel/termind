// Termind Incident Registry — fingerprint 查重 + 分支路由决策的纯数据 tool.
//
// 设计:
//   - 不直接访问 OpenClaw memory / KV / DB. 任何后端的存取都由 OpenClaw agent
//     按 plan 执行, parse 只接受 raw 字符串 / null.
//   - 输出包含明确的 branch 字段 (new_case | recurrence | escalation_candidate),
//     供 lark-alert skill 直接据此渲染卡片样式.
//   - parse 不抛异常: raw 损坏时降级为 new_case + decoded:false, 让链路继续.
//
// Memory record schema (stored as JSON string):
//   {
//     "fingerprint": "a3f9c2d1",
//     "reportUrl": "https://feishu/docs/...",
//     "firstSeen": "2026-04-25T00:58:12+08:00",
//     "lastSeen":  "2026-05-06T21:00:00+08:00",
//     "occurrences": 5,
//     "events": [
//       { "timestamp": "2026-05-06T20:55:00+08:00", "user": "matterhorn",
//         "branch": "feat/x", "commit": "8e4d21a" }
//     ],
//     "branchKind": "main",            // 上次记录时的 branchKind
//     "owner": { "kind":"lark_user", "openId":"ou_xxx", "label":"@zhangsan" } | null,
//     "status": "open" | "resolved" | "false_positive"
//   }

const KEY_PREFIX = "termind:incident:fp:";
const DEFAULT_WINDOW_MINUTES = 120;
const ESCALATION_OCCURRENCES = 5;
const ESCALATION_AFFECTED_USERS = 3;

export function incidentRegistryTool(params = {}) {
  const action = String(params.action ?? "plan").trim().toLowerCase();
  if (action === "parse") return parseRegistryQuery(params);
  if (action === "plan") return buildRegistryQueryPlan(params);
  return {
    version: 1,
    ok: false,
    errors: [`unsupported action: ${action || "(empty)"}`],
    supportedActions: ["plan", "parse"]
  };
}

// ---------------------------------------------------------------------------
// upsert: 写回 step 13. 用 plan/parse 双相, 完全不直接执行 capability.
//   plan  -> 给定 prior raw + delta(新发生时间/用户/branch/commit/reportUrl/
//            owner) + branchKind, 返回:
//              {
//                key, value (string),
//                capabilityHints: [
//                  { capability:"memory.set", args:{ key, value, ttl } },
//                  { capability:"kv.set", args:{ namespace, key, value } }
//                ],
//                missingCapabilityFallback: { ok:false, reason:"no registry write" }
//              }
//            agent 选 capability 执行后再 upsert(action:"parse-ack") 回写
//            execution 结果, parse-ack 只是把 record 校验后回传 (供下游
//            报告链路 / 卡片显示 occurrences 用).
//   parse -> 接收 capability 调用结果 (memory.set 通常返回 { ok:true }), 校验
//            通过即返回 { ok:true, record }; 否则 { ok:false, errors }.
// ---------------------------------------------------------------------------
export function incidentRegistryUpsertTool(params = {}) {
  const action = String(params.action ?? "plan").trim().toLowerCase();
  if (action === "plan") return buildRegistryUpsertPlan(params);
  if (action === "parse") return parseRegistryUpsertAck(params);
  return {
    version: 1,
    ok: false,
    errors: [`unsupported action: ${action || "(empty)"}`],
    supportedActions: ["plan", "parse"]
  };
}

// ---------------------------------------------------------------------------
// plan
// ---------------------------------------------------------------------------

function buildRegistryQueryPlan(params) {
  const fingerprint = sanitizeFingerprint(params.fingerprint);
  const windowMinutes = sanitizeWindow(params.windowMinutes);

  if (!fingerprint) {
    return {
      version: 1,
      ok: false,
      errors: ["fingerprint is required"],
      parse: parseHandle()
    };
  }

  const key = KEY_PREFIX + fingerprint;
  return {
    version: 1,
    ok: true,
    fingerprint,
    key,
    windowMinutes,
    capabilityHints: [
      { capability: "memory.get", args: { key } },
      { capability: "kv.get", args: { namespace: "termind:incident", key: `fp:${fingerprint}` } }
    ],
    // 当 OpenClaw 没有 memory 能力时, agent 应当直接使用这个 fallback
    // 作为 parse 输入, 链路降级为 new_case + missingCapability=true.
    missingCapabilityFallback: {
      action: "parse",
      fingerprint,
      raw: null,
      missingCapability: true,
      windowMinutes
    },
    parse: parseHandle()
  };
}

function parseHandle() {
  return { tool: "termind_incident_registry_query", action: "parse" };
}

// ---------------------------------------------------------------------------
// parse
// ---------------------------------------------------------------------------

function parseRegistryQuery(params) {
  const fingerprint = sanitizeFingerprint(params.fingerprint);
  if (!fingerprint) {
    return {
      version: 1,
      ok: false,
      errors: ["fingerprint is required"],
      found: false,
      branch: "new_case"
    };
  }

  const windowMinutes = sanitizeWindow(params.windowMinutes);
  const now = parseTimestamp(params.now) ?? new Date();
  const branchKindHint = sanitizeBranchKind(params.branchKind);
  const missingCapability = params.missingCapability === true;

  const decode = decodeRecordRaw(params.raw);
  if (!decode.present) {
    return makeNotFoundResult({
      fingerprint,
      branch: "new_case",
      missingCapability,
      decoded: true,
      reasons: missingCapability
        ? ["no registry capability available"]
        : ["no record for this fingerprint"]
    });
  }
  if (!decode.ok) {
    return makeNotFoundResult({
      fingerprint,
      branch: "new_case",
      missingCapability,
      decoded: false,
      reasons: [`registry record decode failed: ${decode.error}`]
    });
  }

  const record = summarizeRecord(decode.record, { windowMinutes, now, fallbackFingerprint: fingerprint, branchKindHint });

  // 已结案 / 误报: 视作 not-found 让链路重新立案, 但仍返回 record 让卡片可注释
  // "之前曾经报告过, 已 resolved 又重现".
  if (record.status === "resolved" || record.status === "false_positive") {
    return {
      version: 1,
      ok: true,
      found: false,
      fingerprint,
      branch: "new_case",
      record,
      reasons: [`previous record status=${record.status}`],
      decoded: true,
      missingCapability
    };
  }

  const escalation = decideEscalation(record, branchKindHint);
  return {
    version: 1,
    ok: true,
    found: true,
    fingerprint,
    branch: escalation.escalate ? "escalation_candidate" : "recurrence",
    record,
    reasons: escalation.reasons,
    decoded: true,
    missingCapability
  };
}

function makeNotFoundResult({ fingerprint, branch, missingCapability, decoded, reasons }) {
  return {
    version: 1,
    ok: true,
    found: false,
    fingerprint,
    branch,
    record: null,
    reasons,
    decoded,
    missingCapability
  };
}

// ---------------------------------------------------------------------------
// summarization & escalation
// ---------------------------------------------------------------------------

function summarizeRecord(rawRecord, { windowMinutes, now, fallbackFingerprint, branchKindHint }) {
  const events = Array.isArray(rawRecord.events) ? rawRecord.events : [];
  const cutoff = new Date(now.getTime() - windowMinutes * 60 * 1000);

  const inWindow = events.filter((ev) => {
    const ts = parseTimestamp(ev?.timestamp);
    return ts && ts >= cutoff;
  });

  const affectedUsers = uniqueNonEmpty(inWindow.map((ev) => stringField(ev?.user)));
  const occurrences = clampOccurrences(rawRecord.occurrences, events.length);
  const branchKind = sanitizeBranchKind(rawRecord.branchKind) || branchKindHint || "";

  return {
    fingerprint: stringField(rawRecord.fingerprint) || fallbackFingerprint,
    reportUrl: stringField(rawRecord.reportUrl),
    firstSeen: stringField(rawRecord.firstSeen),
    lastSeen: stringField(rawRecord.lastSeen),
    occurrences,
    windowOccurrences: inWindow.length,
    windowMinutes,
    affectedUsers,
    branchKind,
    owner: sanitizeOwner(rawRecord.owner),
    status: sanitizeStatus(rawRecord.status)
  };
}

function decideEscalation(record, branchKindHint) {
  const reasons = [];
  let escalate = false;
  const branchKind = record.branchKind || branchKindHint || "";

  // main 分支重现: 立即升级 (即使窗口内只 1 次)
  if (branchKind === "main" && record.windowOccurrences >= 1) {
    escalate = true;
    reasons.push("main branch recurrence within window");
  }
  if (record.windowOccurrences >= ESCALATION_OCCURRENCES) {
    escalate = true;
    reasons.push(`window occurrences >= ${ESCALATION_OCCURRENCES}`);
  }
  if (record.affectedUsers.length >= ESCALATION_AFFECTED_USERS) {
    escalate = true;
    reasons.push(`affected users >= ${ESCALATION_AFFECTED_USERS}`);
  }
  if (!escalate) {
    reasons.push("recurrence within tolerance");
  }
  return { escalate, reasons };
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

function decodeRecordRaw(raw) {
  if (raw == null) return { present: false };
  if (typeof raw === "object") {
    return { present: true, ok: true, record: raw };
  }
  const s = String(raw).trim();
  if (s === "") return { present: false };
  try {
    const parsed = JSON.parse(s);
    if (parsed == null) return { present: false };
    if (typeof parsed !== "object") {
      return { present: true, ok: false, error: "record is not an object" };
    }
    return { present: true, ok: true, record: parsed };
  } catch (e) {
    return { present: true, ok: false, error: e.message ?? "JSON parse failed" };
  }
}

function sanitizeFingerprint(value) {
  return String(value ?? "").trim().toLowerCase().slice(0, 64);
}

function sanitizeWindow(value) {
  const n = Number(value);
  if (!Number.isFinite(n) || n <= 0) return DEFAULT_WINDOW_MINUTES;
  return Math.min(Math.floor(n), 24 * 60);
}

function sanitizeBranchKind(value) {
  const s = String(value ?? "").trim().toLowerCase();
  if (s === "main" || s === "release" || s === "feature" || s === "other") return s;
  return "";
}

function sanitizeOwner(owner) {
  if (!owner || typeof owner !== "object") return null;
  return {
    kind: stringField(owner.kind),
    openId: stringField(owner.openId),
    label: stringField(owner.label)
  };
}

function sanitizeStatus(status) {
  const s = String(status ?? "open").trim().toLowerCase();
  if (s === "open" || s === "resolved" || s === "false_positive") return s;
  return "open";
}

function stringField(value) {
  if (value == null) return "";
  return String(value).trim();
}

function uniqueNonEmpty(list) {
  const out = [];
  const seen = new Set();
  for (const item of list) {
    if (!item) continue;
    if (seen.has(item)) continue;
    seen.add(item);
    out.push(item);
  }
  return out;
}

function clampOccurrences(value, fallback) {
  const n = Number(value);
  if (!Number.isFinite(n) || n < 0) return Math.max(0, fallback | 0);
  return Math.floor(n);
}

function parseTimestamp(value) {
  if (value == null || value === "") return null;
  if (value instanceof Date) return Number.isNaN(value.getTime()) ? null : value;
  if (typeof value === "number") {
    const d = new Date(value);
    return Number.isNaN(d.getTime()) ? null : d;
  }
  const d = new Date(String(value));
  return Number.isNaN(d.getTime()) ? null : d;
}

// ---------------------------------------------------------------------------
// upsert plan & parse-ack
// ---------------------------------------------------------------------------

const REGISTRY_TTL_DAYS = 90;
const MAX_EVENTS_KEPT = 50;

function buildRegistryUpsertPlan(params) {
  const fingerprint = sanitizeFingerprint(params.fingerprint);
  if (!fingerprint) {
    return {
      version: 1,
      ok: false,
      errors: ["fingerprint is required"],
      parse: upsertParseHandle()
    };
  }

  const now = parseTimestamp(params.now) ?? new Date();
  const windowMinutes = sanitizeWindow(params.windowMinutes);

  const decoded = decodeRecordRaw(params.priorRaw);
  let prior;
  if (decoded.present && decoded.ok) {
    prior = decoded.record;
  } else {
    prior = null;
  }

  const next = mergeRecord(prior, fingerprint, params, now);

  let valueString;
  try {
    valueString = JSON.stringify(next);
  } catch (e) {
    return {
      version: 1,
      ok: false,
      errors: [`record serialization failed: ${e.message ?? "unknown"}`],
      parse: upsertParseHandle()
    };
  }

  const key = KEY_PREFIX + fingerprint;
  const summary = summarizeRecord(next, {
    windowMinutes,
    now,
    fallbackFingerprint: fingerprint,
    branchKindHint: sanitizeBranchKind(params.branchKind)
  });

  return {
    version: 1,
    ok: true,
    fingerprint,
    key,
    value: valueString,
    record: next,
    summary,
    capabilityHints: [
      {
        capability: "memory.set",
        args: { key, value: valueString, ttlSeconds: REGISTRY_TTL_DAYS * 86400 }
      },
      {
        capability: "kv.set",
        args: { namespace: "termind:incident", key: `fp:${fingerprint}`, value: valueString }
      }
    ],
    // 当 OpenClaw 没有 memory/KV 写能力时, agent 应当直接返回这个 fallback.
    // 链路不算失败, 但下次 query 仍会查到旧记录 (或没记录), 卡片 / 报告
    // 不会断链.
    missingCapabilityFallback: {
      action: "parse",
      ok: false,
      written: false,
      reason: "no_registry_write_capability",
      fingerprint,
      record: next
    },
    parse: upsertParseHandle()
  };
}

function parseRegistryUpsertAck(params) {
  const fingerprint = sanitizeFingerprint(params.fingerprint);
  if (!fingerprint) {
    return {
      version: 1,
      ok: false,
      written: false,
      errors: ["fingerprint is required"]
    };
  }

  const errors = [];
  let written = params.written;
  if (typeof written !== "boolean") {
    written = inferWriteSuccess(params, errors);
  }

  // Caller 通常会把 plan 输出的 record 一并传回来供卡片 / 报告引用.
  let record = params.record ?? null;
  if (record && typeof record === "string") {
    const decoded = decodeRecordRaw(record);
    record = decoded.ok ? decoded.record : null;
    if (!decoded.ok && decoded.error) errors.push(`record decode failed: ${decoded.error}`);
  }

  return {
    version: 1,
    ok: written && errors.length === 0,
    written,
    fingerprint,
    record,
    errors
  };
}

function upsertParseHandle() {
  return { tool: "termind_incident_registry_upsert", action: "parse" };
}

function mergeRecord(prior, fingerprint, params, now) {
  const branchKind = sanitizeBranchKind(params.branchKind) || sanitizeBranchKind(prior?.branchKind) || "";
  const reportUrlRaw = stringField(params.reportUrl);
  const reportUrl = reportUrlRaw || stringField(prior?.reportUrl);

  // 新事件: 谁(user) / 何时(timestamp) / 哪个分支(branch) / commit / env.
  const newEventInput = params.event ?? {};
  const occurredAt = parseTimestamp(params.occurredAt ?? newEventInput.timestamp) ?? now;
  const newEvent = {
    timestamp: occurredAt.toISOString(),
    user: stringField(newEventInput.user ?? params.user),
    branch: stringField(newEventInput.branch ?? params.branch),
    commit: stringField(newEventInput.commit ?? params.gitCommit ?? newEventInput.gitCommit),
    environment: stringField(newEventInput.environment ?? params.environment)
  };

  const events = Array.isArray(prior?.events) ? prior.events.slice() : [];
  events.push(stripEmptyFields(newEvent));
  // 按时间倒序保留最近 N 条, 避免无限增长.
  events.sort((a, b) => {
    const ta = parseTimestamp(a?.timestamp)?.getTime() ?? 0;
    const tb = parseTimestamp(b?.timestamp)?.getTime() ?? 0;
    return tb - ta;
  });
  events.splice(MAX_EVENTS_KEPT);

  const occurrences = clampOccurrences(prior?.occurrences, 0) + 1;
  const firstSeen = stringField(prior?.firstSeen) || newEvent.timestamp;
  const lastSeen = newEvent.timestamp;
  const owner = sanitizeOwnerForRecord(params.owner ?? prior?.owner);
  const status = sanitizeStatus(params.status ?? prior?.status);

  return {
    fingerprint,
    reportUrl,
    firstSeen,
    lastSeen,
    occurrences,
    branchKind,
    status,
    owner,
    events
  };
}

function stripEmptyFields(obj) {
  const out = {};
  for (const [k, v] of Object.entries(obj)) {
    if (v == null || v === "") continue;
    out[k] = v;
  }
  return out;
}

function sanitizeOwnerForRecord(owner) {
  if (!owner || typeof owner !== "object") return null;
  const cleaned = {
    kind: stringField(owner.kind),
    openId: stringField(owner.openId ?? owner.open_id),
    label: stringField(owner.label ?? owner.name),
    email: stringField(owner.email),
    source: stringField(owner.source),
    confidence: stringField(owner.confidence)
  };
  if (!cleaned.openId && !cleaned.label && !cleaned.email) return null;
  return cleaned;
}

function inferWriteSuccess(params, errors) {
  const exitCode = params.exitCode;
  if (typeof exitCode === "number" && exitCode !== 0) {
    if (params.stderr) errors.push(String(params.stderr).trim().split(/\r?\n/)[0]);
    return false;
  }
  // 常见 capability ack 形态: { ok:true } / { code:0 } / { success:true } /
  // memory.set 直接回 string "ok" 之类.
  const raw = params.ack ?? params.result ?? params.output ?? params.stdout;
  if (raw == null || raw === "") return true;
  if (typeof raw === "object") {
    if (raw.ok === false || raw.success === false) {
      const message = stringField(raw.error?.message ?? raw.message);
      if (message) errors.push(message);
      return false;
    }
    return true;
  }
  if (typeof raw === "string") {
    if (/error|fail|denied/i.test(raw)) {
      errors.push(raw.split(/\r?\n/)[0]);
      return false;
    }
    return true;
  }
  return true;
}
