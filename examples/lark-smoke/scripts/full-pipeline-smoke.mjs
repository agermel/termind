#!/usr/bin/env node
// full-pipeline-smoke — Termind plugin end-to-end pipeline smoke (no OpenClaw needed).
//
// 跑通 plugin 内部完整链路:
//
//   redactFailureEvent
//     -> computeFingerprint
//     -> incidentRegistryTool (plan)
//     -> [模拟 OpenClaw memory.get 的回值]
//     -> incidentRegistryTool (parse)
//     -> merge record into event
//     -> classifyFailure
//     -> buildIncidentCard
//     -> buildLarkCliCommands
//     -> spawn lark-cli (--dry-run by default; --send to actually deliver)
//
// 使用:
//   node examples/lark-smoke/scripts/full-pipeline-smoke.mjs           # dry-run all
//   node examples/lark-smoke/scripts/full-pipeline-smoke.mjs --send    # 实发 4 张到 termind config 中的 target
//   node examples/lark-smoke/scripts/full-pipeline-smoke.mjs --case=new_case --send  # 只发一种
//
// 4 种 case:
//   new_case             —— registry 没记录,标题 🆕
//   recurrence           —— registry 有记录,窗口内 2 次,标题 🔁
//   escalation_candidate —— registry 有记录,窗口内 5 次/3 用户,标题 ⛔️
//   missing_capability   —— registry 能力缺失, fallback 到 new_case
//
// 安全约束:
//   - 默认 dry-run, lark-cli 不会真发, 只打印 request body
//   - --send 时也用 idempotency-key 防止重复发送
//   - 不读取或写入 OpenClaw memory; record 由脚本顶部 STUB_RECORDS 模拟

import { spawn } from "node:child_process";
import { readFileSync } from "node:fs";
import { homedir } from "node:os";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const __dirname = dirname(fileURLToPath(import.meta.url));
// examples/lark-smoke/scripts/ -> repo root -> plugin/src/lib
const PLUGIN_LIB = resolve(__dirname, "../../../plugin/src/lib");

const { normalizeFailureEvent, redactFailureEvent } = await import(`${PLUGIN_LIB}/redact.js`);
const { computeFingerprint } = await import(`${PLUGIN_LIB}/fingerprint.js`);
const { incidentRegistryTool, incidentRegistryUpsertTool } = await import(`${PLUGIN_LIB}/registry.js`);
const { classifyFailure } = await import(`${PLUGIN_LIB}/classify.js`);
const { buildIncidentCard } = await import(`${PLUGIN_LIB}/card.js`);
const { buildLarkCliCommands } = await import(`${PLUGIN_LIB}/lark-cli.js`);
const { larkKnowledgeSearchTool } = await import(`${PLUGIN_LIB}/lark-knowledge-search.js`);
const { ownerResolveTool, parseOwnerResolve } = await import(`${PLUGIN_LIB}/owner-resolve.js`);

// ---------------------------------------------------------------------------
// CLI flags
// ---------------------------------------------------------------------------
const args = process.argv.slice(2);
const SEND = args.includes("--send");
const ONLY = (args.find((a) => a.startsWith("--case=")) || "").slice("--case=".length);

// ---------------------------------------------------------------------------
// 从 termind config 读取真实 lark target / identities
// ---------------------------------------------------------------------------
const TERMIND_CONFIG_PATH = resolve(homedir(), ".config/termind/config.json");
let termindConfig = {};
try {
  termindConfig = JSON.parse(readFileSync(TERMIND_CONFIG_PATH, "utf8"));
} catch (e) {
  console.error(`[fatal] cannot read ${TERMIND_CONFIG_PATH}: ${e.message}`);
  process.exit(2);
}
const forwarding = termindConfig?.lark?.forwarding;
if (!forwarding?.routes?.length) {
  console.error(`[fatal] no lark.forwarding.routes in ${TERMIND_CONFIG_PATH}`);
  process.exit(2);
}

// ---------------------------------------------------------------------------
// 4 种 case 共用的 base event
// ---------------------------------------------------------------------------
const BASE_EVENT = {
  summary: "panic: runtime error: invalid memory address",
  command: "go run ./cmd/grade serve",
  cwd: "/Users/matterhorn/work/be-grade",
  project: "be-grade",
  user: "matterhorn",
  branch: "feat/rank-v2",
  branchKind: "feature",
  gitCommit: "8e4d21a",
  environment: "go1.22.3 macOS",
  tail: "panic: runtime error: invalid memory address\n  at be-grade/cron/rank.go:87 computeRank()",
  larkSender: "bot",
  larkForwardingIdentities: forwarding.identities,
  larkForwardingRoutes: forwarding.routes
};

// 模拟 OpenClaw memory.get 的回值 (registry record).
// 真实生产链路里这一步由 OpenClaw 执行 memory.get 提供, 这里直接 stub.
const STUB_RECORDS = {
  // new_case: registry 没记录
  new_case: { raw: null, missingCapability: false },

  // recurrence: 有记录,窗口内 2 次, 1 个用户, 未达升级阈值
  recurrence: {
    raw: {
      fingerprint: "WILL_BE_SET",
      reportUrl: "https://example.feishu.cn/docs/recurrence-stub",
      firstSeen: "2026-04-25T00:58:12+08:00",
      lastSeen: "2026-05-06T20:55:00+08:00",
      occurrences: 4,
      branchKind: "feature",
      status: "open",
      events: [
        // 窗口内 (近 120 分钟)
        { timestamp: new Date(Date.now() - 30 * 60 * 1000).toISOString(), user: "matterhorn" },
        { timestamp: new Date(Date.now() - 60 * 60 * 1000).toISOString(), user: "matterhorn" },
        // 窗口外 (旧的)
        { timestamp: "2026-04-25T00:58:12+08:00", user: "matterhorn" },
        { timestamp: "2026-04-30T10:00:00+08:00", user: "ancient-user" }
      ]
    },
    missingCapability: false
  },

  // escalation_candidate: 窗口内 5 次, 3 用户, 触发升级
  escalation_candidate: {
    raw: {
      fingerprint: "WILL_BE_SET",
      reportUrl: "https://example.feishu.cn/docs/escalation-stub",
      firstSeen: "2026-04-20T00:00:00+08:00",
      lastSeen: new Date().toISOString(),
      occurrences: 12,
      branchKind: "feature",
      status: "open",
      events: [
        { timestamp: new Date(Date.now() - 10 * 60 * 1000).toISOString(), user: "alice" },
        { timestamp: new Date(Date.now() - 20 * 60 * 1000).toISOString(), user: "bob" },
        { timestamp: new Date(Date.now() - 30 * 60 * 1000).toISOString(), user: "carol" },
        { timestamp: new Date(Date.now() - 40 * 60 * 1000).toISOString(), user: "alice" },
        { timestamp: new Date(Date.now() - 50 * 60 * 1000).toISOString(), user: "matterhorn" }
      ]
    },
    missingCapability: false
  },

  // missing_capability: registry 没能力, 链路应降级为 new_case 但保留标记
  missing_capability: { raw: null, missingCapability: true }
};

// ---------------------------------------------------------------------------
// 主链路
// ---------------------------------------------------------------------------
const cases = ONLY ? [ONLY] : Object.keys(STUB_RECORDS);
let exitCode = 0;
for (const caseName of cases) {
  if (!STUB_RECORDS[caseName]) {
    console.error(`[skip] unknown case: ${caseName}`);
    exitCode = 2;
    continue;
  }
  const ok = await runCase(caseName, STUB_RECORDS[caseName]);
  if (!ok) exitCode = 1;
}
process.exit(exitCode);

async function runCase(caseName, stub) {
  console.log(`\n========== case: ${caseName} ==========`);

  // 1. redact + normalize
  let event = redactFailureEvent(normalizeFailureEvent({ ...BASE_EVENT }, { requireFingerprint: false }));

  // 2. fingerprint
  const fp = computeFingerprint(event);
  event.fingerprint = fp.fingerprint;
  console.log(`[step 5] fingerprint = ${fp.fingerprint} (confidence: ${fp.confidence})`);

  // 3. registry plan (展示 OpenClaw 应该执行什么 capability)
  const plan = incidentRegistryTool({
    action: "plan",
    fingerprint: fp.fingerprint,
    windowMinutes: 120
  });
  console.log(`[step 6] registry plan key=${plan.key} hints=${plan.capabilityHints.map((h) => h.capability).join(",")}`);

  // 4. registry parse — 用 stub 模拟 capability 返回值
  let raw = stub.raw;
  if (raw && typeof raw === "object" && raw.fingerprint === "WILL_BE_SET") {
    raw = { ...raw, fingerprint: fp.fingerprint };
  }
  const registry = incidentRegistryTool({
    action: "parse",
    fingerprint: fp.fingerprint,
    raw,
    branchKind: event.branchKind,
    missingCapability: stub.missingCapability,
    windowMinutes: 120
  });
  console.log(`[step 6] registry branch=${registry.branch} found=${registry.found} reasons=${JSON.stringify(registry.reasons)}`);

  // 5. merge registry record into event (per skill flow)
  if (registry.record) {
    event.occurrences = registry.record.occurrences;
    event.affectedUsers = registry.record.affectedUsers.length;
    event.windowOccurrences = registry.record.windowOccurrences;
    event.windowMinutes = registry.record.windowMinutes;
    if (registry.record.firstSeen) event.firstSeen = registry.record.firstSeen;
    if (registry.record.lastSeen) event.lastSeen = registry.record.lastSeen;
    if (registry.record.reportUrl) event.reportUrl = registry.record.reportUrl;
  }
  event.registryBranch = registry.branch;

  // 6. classify
  const classification = classifyFailure(event);
  event.severity = classification.severity;
  console.log(`[step 7] severity=${classification.severity} route=${classification.route} reasons=${JSON.stringify(classification.reasons)}`);

  // 6b. owner resolve (step 12) — 用 sender=bot 模拟 "user OAuth 不可用"
  // 路径, plan 直接给 labelOnlyOwner; sender=user 路径需要真实 user profile,
  // 这里只演示 fallback. 真实链路里 termind-owner-resolve skill 会拿
  // user-OAuth profile 跑 lark-cli contact +search-user.
  const ownerPlan = ownerResolveTool({
    action: "plan",
    sender: "bot",
    name: "matterhorn",
    email: "matterhorn@example.com"
  });
  if (ownerPlan.labelOnlyOwner) {
    event.owner = ownerPlan.labelOnlyOwner;
  }
  console.log(`[step 12] owner.confidence=${event.owner?.confidence ?? "(none)"} label=${event.owner?.label ?? "-"}`);

  // 6c. knowledge RAG (step 9) — queries 派生 + 一次 user-only plan 演示.
  // 不真正调用 lark-cli docs +search (本机没有 user OAuth profile).
  // 真实链路里 termind-lark-knowledge-search skill 会跑 plan + exec + parse.
  if (classification.severity !== "info") {
    const queries = larkKnowledgeSearchTool({ action: "queries", event });
    console.log(`[step 9] knowledge queries: ${queries.queries.slice(0, 3).map(q => `[${q.source}] ${q.query}`).join(" | ")}`);
    const ragPlan = larkKnowledgeSearchTool({
      action: "plan",
      sender: "user",
      query: queries.queries[0]?.query || event.summary,
      pageSize: 5
    });
    console.log(`[step 9] rag plan ok=${ragPlan.ok} cmd=${ragPlan.commands[0]?.display?.slice(0, 80) ?? "(missing)"}`);
    // 模拟 stub knowledgeHits 让 card 渲染相关知识块
    if (caseName === "recurrence" || caseName === "escalation_candidate") {
      event.knowledgeHits = [
        {
          token: "doccnSTUB",
          type: "doc",
          title: "panic playbook (stub)",
          url: "https://example.feishu.cn/docs/doccnSTUB"
        }
      ];
    }
  }

  // 7. card build
  const card = buildIncidentCard(event);
  console.log(`[step 8] header.title="${card.header.title.content}" template=${card.header.template} elements=${card.elements.length}`);
  const historyEl = card.elements.find(
    (e) => e?.text?.content?.startsWith?.("历史\n") || e?.text?.content?.startsWith?.("升级原因\n")
  );
  if (historyEl) {
    console.log(`[step 8] history-block:\n  ${historyEl.text.content.replaceAll("\n", "\n  ")}`);
  }

  // 8. lark-cli command build
  const commands = buildLarkCliCommands(event, card);
  if (commands.length === 0) {
    console.error(`[fatal] no lark-cli command for case ${caseName}`);
    return false;
  }

  // 9. spawn lark-cli (dry-run by default)
  for (const cmd of commands) {
    const finalArgs = SEND
      ? [...cmd.args, "--idempotency-key", `termind-smoke-${caseName}-${fp.fingerprint}-${Date.now()}`]
      : [...cmd.args, "--dry-run"];
    console.log(`[step 10] spawn lark-cli ${SEND ? "(REAL SEND)" : "(--dry-run)"} args[0..3]=${finalArgs.slice(0, 4).join(" ")} ... target=${cmd.target.type}:${cmd.target.id}`);
    const result = await spawnAndCollect("lark-cli", finalArgs, { ...process.env, ...cmd.env });
    if (result.code !== 0) {
      console.error(`[fail] lark-cli exit=${result.code}\nstderr=${result.stderr}\nstdout=${result.stdout}`);
      return false;
    }
    console.log(`[ok] lark-cli exit=0`);
    if (result.stdout.trim()) {
      console.log(`[stdout]\n${indent(result.stdout)}`);
    }
  }

  // 10. registry write-back (step 13). plan 一遍, 不真的写 OpenClaw memory.
  // 真实链路里 termind-incident-recurrence / termind-incident-report skill 会
  // 选择 memory.set / kv.set capability 完成 ack.
  const upsertPlan = incidentRegistryUpsertTool({
    action: "plan",
    fingerprint: event.fingerprint,
    branchKind: event.branchKind,
    reportUrl: event.reportUrl,
    user: event.user,
    branch: event.branch,
    gitCommit: event.gitCommit,
    environment: event.environment,
    occurredAt: new Date().toISOString(),
    owner: event.owner,
    priorRaw: stub.raw ? JSON.stringify({ ...stub.raw, fingerprint: event.fingerprint }) : null
  });
  if (upsertPlan.ok) {
    const valueLen = upsertPlan.value.length;
    const occ = upsertPlan.record.occurrences;
    console.log(`[step 13] upsert plan ok key=${upsertPlan.key} occurrences=${occ} value-bytes=${valueLen} hints=${upsertPlan.capabilityHints.map(h => h.capability).join(",")}`);
    const ack = incidentRegistryUpsertTool({
      action: "parse",
      fingerprint: event.fingerprint,
      ack: { ok: true },
      record: upsertPlan.record
    });
    console.log(`[step 13] upsert ack written=${ack.written} ok=${ack.ok}`);
  } else {
    console.log(`[step 13] upsert plan failed: ${(upsertPlan.errors || []).join(" | ")}`);
  }
  return true;
}

function spawnAndCollect(cmd, args, env) {
  return new Promise((resolveP) => {
    const child = spawn(cmd, args, { env, stdio: ["ignore", "pipe", "pipe"] });
    let stdout = "";
    let stderr = "";
    child.stdout.on("data", (d) => (stdout += d.toString()));
    child.stderr.on("data", (d) => (stderr += d.toString()));
    child.on("close", (code) => resolveP({ code, stdout, stderr }));
  });
}

function indent(s) {
  return s.split("\n").map((l) => `  ${l}`).join("\n");
}
