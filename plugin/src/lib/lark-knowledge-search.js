// Termind Knowledge Search via lark-cli docs +search.
//
// 关键事实:
//   `lark-cli docs +search` (Search v2 doc_wiki/search) 是 **user-only**.
//   实测 `--as bot` 直接报错:
//     Error: --as bot is not supported, this command only supports: user
//   因此 plan 阶段强制 `--as user`. 如果 caller 把 sender=bot 推下来, 直接以
//   missingCapability=user_oauth 拒绝, 让 orchestrating skill 优雅降级,
//   不在 OpenClaw exec 上跑出一条注定失败的命令.
//
// Actions:
//   queries -> 从 failure event 推导出按特异性排序的检索词候选, 后续 plan
//              逐个尝试.
//   plan    -> 给定 query 构造一条 lark-cli docs +search --as user --format json
//              命令; 也支持 --filter / --page-size / --page-token / --profile /
//              LARKSUITE_CLI_CONFIG_DIR.
//   parse   -> 解析 stdout/stderr/exitCode, 抽出 hits[] (token/type/title/url
//              /ownerOpenId/ownerName/snippet/lastModified) + pageToken +
//              hasMore + needsUserAuthorization.
//
// 这个工具不访问 shell / 网络 / 文件 / env. 所有副作用都由 OpenClaw exec 触发.

const DEFAULT_PAGE_SIZE = 10;
const MAX_PAGE_SIZE = 20;
const ALLOWED_TYPES = new Set(["doc", "docx", "wiki", "sheet", "bitable", "mindnote", "file", "slide"]);

export function larkKnowledgeSearchTool(params = {}) {
  const action = String(params.action ?? "plan").trim().toLowerCase();
  if (action === "queries") return buildKnowledgeQueries(params);
  if (action === "parse") return parseLarkKnowledgeSearch(params);
  if (action === "plan") return buildLarkKnowledgeSearchPlan(params);
  return {
    version: 1,
    ok: false,
    errors: [`unsupported action: ${action || "(empty)"}`],
    supportedActions: ["queries", "plan", "parse"]
  };
}

// ---------------------------------------------------------------------------
// queries: 从 failure event 派生候选 query 列表.
// 顺序: 最特异 -> 最宽泛. orchestrating skill 按顺序 plan -> exec, 第一个
// 命中 hits>=1 就停止.
// ---------------------------------------------------------------------------
export function buildKnowledgeQueries(params = {}) {
  const event = params.event ?? params;
  const seen = new Set();
  const out = [];
  const push = (q, source) => {
    const cleaned = String(q ?? "").trim().slice(0, 200);
    if (!cleaned) return;
    const key = cleaned.toLowerCase();
    if (seen.has(key)) return;
    seen.add(key);
    out.push({ query: cleaned, source });
  };

  const summary = String(event.summary ?? "").trim();
  const command = String(event.command ?? "").trim();
  const project = String(event.project ?? "").trim();
  const stackTop = Array.isArray(event.stackTop) ? event.stackTop : [];

  // 最特异: 错误关键句 (前 2 行 / 第一行) + project
  const summaryHead = firstSentence(summary);
  if (project && summaryHead) push(`${project} ${summaryHead}`, "project+summary");
  if (summaryHead) push(summaryHead, "summary");

  // stack top frame -> 单独成 query, 通常是函数路径,精度最高
  for (const frame of stackTop.slice(0, 2)) {
    const f = stripStackFramePrefix(frame);
    if (f) push(f, "stack");
  }

  // command family (e.g. go test, npm run, docker compose) + project
  const family = commandFamily(command);
  if (family && project) push(`${project} ${family}`, "project+command");
  if (family) push(family, "command");

  // 错误关键词 (panic / fatal / cannot find / undefined) + summary 前缀
  const keyword = errorKeyword(summary);
  if (keyword && project) push(`${project} ${keyword}`, "project+keyword");
  if (keyword) push(keyword, "keyword");

  // fallback: 整段 summary (截断)
  if (summary) push(summary.slice(0, 120), "summary-full");

  return {
    version: 1,
    ok: true,
    queries: out.slice(0, 6),
    sender: "user",
    note: "lark-cli docs +search is user-only; orchestrator must hold a user OAuth profile."
  };
}

// ---------------------------------------------------------------------------
// plan
// ---------------------------------------------------------------------------
export function buildLarkKnowledgeSearchPlan(params = {}) {
  const sender = normalizeSender(params.sender);
  const query = text(params.query);
  const profile = text(params.profile);
  const configDir = text(params.larkCliConfigDir ?? params.configDir);
  const pageSize = clampPageSize(params.pageSize);
  const pageToken = text(params.pageToken);
  const filter = sanitizeFilter(params.filter);

  const errors = [];
  if (sender !== "user") {
    errors.push(
      "lark-cli docs +search only supports --as user. Bot identities cannot search the doc/wiki index. " +
        "Plan with sender=user using a logged-in user OAuth profile (LARKSUITE_CLI_CONFIG_DIR + lark-cli auth login)."
    );
  }
  if (!query) errors.push("query is required for knowledge search.");

  if (errors.length > 0) {
    return {
      version: 1,
      sideEffects: false,
      ok: false,
      sender,
      missingCapability: sender !== "user" ? "user_oauth" : undefined,
      execTool: null,
      commands: [],
      errors,
      parse: parseHandle()
    };
  }

  const args = larkCliArgs(profile, [
    "docs",
    "+search",
    "--as",
    "user",
    "--query",
    query,
    "--page-size",
    String(pageSize),
    "--format",
    "json"
  ]);
  if (pageToken) args.push("--page-token", pageToken);
  if (filter) args.push("--filter", filter);

  const env = configDir ? { LARKSUITE_CLI_CONFIG_DIR: configDir } : {};
  return {
    version: 1,
    sideEffects: false,
    ok: true,
    sender: "user",
    profile,
    larkCliConfigDir: configDir,
    pageSize,
    execTool: "exec",
    commands: [
      {
        key: "docsSearch",
        command: "lark-cli",
        args,
        env,
        display: commandDisplay("lark-cli", args, configDir),
        optional: false
      }
    ],
    errors: [],
    parse: parseHandle()
  };
}

// ---------------------------------------------------------------------------
// parse
// ---------------------------------------------------------------------------
export function parseLarkKnowledgeSearch(params = {}) {
  const errors = [];
  const stdout = text(params.stdout ?? params.output);
  const stderr = text(params.stderr ?? params.error);
  const exitCode = numberOrNull(params.exitCode);

  let needsUserAuthorization = false;
  let missingCapability = "";

  const candidates = valueCandidates(stdout);
  if (candidates.length === 0 && stdout) errors.push(firstLine(stdout));

  // 早期识别 lark-cli 包装的失败 envelope, 避免后面 hits=0 造成误以为没结果.
  for (const candidate of candidates) {
    if (!candidate || typeof candidate !== "object" || Array.isArray(candidate)) continue;
    if (candidate.ok === false) {
      const message = text(candidate.error?.message ?? candidate.message ?? candidate.msg);
      if (/need_user_authorization/i.test(message)) {
        needsUserAuthorization = true;
        missingCapability = "user_oauth";
      }
      if (message) errors.push(message);
    }
  }

  if (!needsUserAuthorization && stderr) {
    const line = firstLine(stderr);
    if (/need_user_authorization|--as bot is not supported/i.test(line)) {
      needsUserAuthorization = true;
      missingCapability = "user_oauth";
    }
    if (line) errors.push(line);
  }

  const hits = collectHits(candidates);
  const pagination = collectPagination(candidates);

  return {
    version: 1,
    ok: !needsUserAuthorization && exitCodeOk(exitCode) && (hits.length > 0 || errors.length === 0),
    needsUserAuthorization,
    missingCapability: missingCapability || undefined,
    hits,
    pageToken: pagination.pageToken,
    hasMore: pagination.hasMore,
    errors: dedupe(errors).filter(Boolean)
  };
}

function parseHandle() {
  return { tool: "termind_lark_knowledge_search", action: "parse" };
}

// ---------------------------------------------------------------------------
// hits / pagination extraction
// ---------------------------------------------------------------------------
function collectHits(candidates) {
  const out = [];
  const seen = new Set();
  for (const candidate of candidates) {
    walk(candidate, item => {
      const hit = normalizeHit(item);
      if (!hit) return;
      const key = hit.token + "|" + hit.type;
      if (seen.has(key)) return;
      seen.add(key);
      out.push(hit);
    });
  }
  return out;
}

function normalizeHit(item) {
  if (!item || typeof item !== "object" || Array.isArray(item)) return null;
  const token = text(
    item.token ??
      item.docs_token ??
      item.doc_token ??
      item.wiki_token ??
      item.obj_token ??
      item.objToken
  );
  if (!token) return null;
  const rawType = text(
    item.type ??
      item.docs_type ??
      item.doc_type ??
      item.wiki_obj_type ??
      item.obj_type ??
      item.objType
  ).toLowerCase();
  const type = mapDocType(rawType);
  if (!type) return null;
  const title = text(item.title ?? item.docs_title ?? item.doc_title ?? item.name);
  const url = text(item.url ?? item.docs_url ?? item.doc_url);
  const ownerOpenId = text(item.owner_id ?? item.ownerId ?? item.owner_open_id ?? item.ownerOpenId);
  const ownerName = text(item.owner_name ?? item.ownerName);
  const snippet = text(item.snippet ?? item.preview ?? item.description);
  const lastModified = text(item.update_time ?? item.updateTime ?? item.last_modified ?? item.lastModified);
  const score = Number(item.score ?? item.relevance ?? 0);
  return {
    token,
    type,
    title,
    url,
    ownerOpenId,
    ownerName,
    snippet,
    lastModified,
    score: Number.isFinite(score) ? score : 0
  };
}

function collectPagination(candidates) {
  let pageToken = "";
  let hasMore = false;
  for (const candidate of candidates) {
    walk(candidate, item => {
      if (!item || typeof item !== "object" || Array.isArray(item)) return;
      const token = text(item.page_token ?? item.pageToken ?? item.next_page_token ?? item.nextPageToken);
      if (token && !pageToken) pageToken = token;
      if (item.has_more === true || item.hasMore === true) hasMore = true;
    });
  }
  return { pageToken, hasMore };
}

function mapDocType(raw) {
  const t = raw.toLowerCase();
  if (!t) return "doc";
  if (t === "doc" || t === "docs") return "doc";
  if (t === "docx") return "docx";
  if (t === "wiki" || t === "wikinode") return "wiki";
  if (t === "sheet" || t === "sheets" || t === "spreadsheet") return "sheet";
  if (t === "bitable" || t === "base") return "bitable";
  if (t === "mindnote") return "mindnote";
  if (t === "file") return "file";
  if (t === "slide" || t === "slides") return "slide";
  return ALLOWED_TYPES.has(t) ? t : "doc";
}

// ---------------------------------------------------------------------------
// query helpers
// ---------------------------------------------------------------------------
function firstSentence(value) {
  const cleaned = String(value ?? "").trim();
  if (!cleaned) return "";
  // 取第一行 + 第一句, 截断到 120 char
  const firstLine = cleaned.split(/\r?\n/)[0] ?? cleaned;
  const sentence = firstLine.split(/[.!?。！？]/)[0] ?? firstLine;
  return sentence.trim().slice(0, 120);
}

function stripStackFramePrefix(frame) {
  const cleaned = String(frame ?? "").trim();
  if (!cleaned) return "";
  // 去掉常见前缀 "  at " / "  File ..." / "Traceback (most recent call last)"
  return cleaned
    .replace(/^\s*at\s+/i, "")
    .replace(/^\s*File\s+/i, "")
    .replace(/^\s*Traceback.*/i, "")
    .slice(0, 200);
}

function commandFamily(command) {
  const head = String(command ?? "").trim().split(/\s+/).slice(0, 2).join(" ").toLowerCase();
  if (!head) return "";
  // 一些常见命令族归一化
  const families = [
    "go test", "go run", "go build", "go mod",
    "npm run", "npm install", "npm ci",
    "yarn", "pnpm",
    "docker compose", "docker run", "docker build",
    "kubectl apply", "kubectl get",
    "make ", "cargo ", "rustc ",
    "pytest", "pip install"
  ];
  for (const f of families) {
    if (head.startsWith(f.trim())) return f.trim();
  }
  return head.split(/\s+/)[0] ?? "";
}

function errorKeyword(summary) {
  const m = String(summary ?? "").match(/(panic|fatal|error|exception|undefined|cannot find|not found|timeout|denied|refused)[^\n]{0,80}/i);
  return m ? m[0].trim() : "";
}

// ---------------------------------------------------------------------------
// shared helpers (subset of lark-cli-discover.js style)
// ---------------------------------------------------------------------------
function normalizeSender(value) {
  return String(value ?? "user").trim().toLowerCase() === "user" ? "user" : "bot";
}

function clampPageSize(value) {
  const n = Number(value);
  if (!Number.isFinite(n) || n <= 0) return DEFAULT_PAGE_SIZE;
  return Math.min(Math.max(Math.floor(n), 1), MAX_PAGE_SIZE);
}

function sanitizeFilter(value) {
  if (!value) return "";
  if (typeof value === "string") {
    try {
      JSON.parse(value);
      return value;
    } catch {
      return "";
    }
  }
  if (typeof value === "object") {
    try {
      return JSON.stringify(value);
    } catch {
      return "";
    }
  }
  return "";
}

function larkCliArgs(profile, args) {
  return profile ? ["--profile", profile, ...args] : args;
}

function commandDisplay(command, args, configDir) {
  const body = [command, ...args.map(shellQuote)].join(" ");
  return configDir ? "LARKSUITE_CLI_CONFIG_DIR=" + shellEnvValue(configDir) + " " + body : body;
}

function shellEnvValue(value) {
  value = text(value);
  if (value.startsWith("$HOME/")) {
    return "\"$HOME/" + value.slice("$HOME/".length).replaceAll("\\", "\\\\").replaceAll("\"", "\\\"").replaceAll("`", "\\`") + "\"";
  }
  return shellQuote(value);
}

function shellQuote(value) {
  value = String(value ?? "");
  if (/^[A-Za-z0-9_./:@%+=,-]+$/.test(value)) return value;
  return "'" + value.replaceAll("'", "'\\''") + "'";
}

function valueCandidates(value) {
  const out = [];
  const visit = item => {
    if (item == null || item === "") return;
    if (typeof item === "string") {
      const parsed = parseJSON(item);
      if (parsed != null) visit(parsed);
      return;
    }
    out.push(item);
    if (Array.isArray(item)) {
      for (const child of item) visit(child);
      return;
    }
    if (typeof item === "object") {
      for (const child of Object.values(item)) visit(child);
    }
  };
  visit(value);
  return out;
}

function parseJSON(value) {
  if (value == null || value === "") return null;
  if (typeof value !== "string") return value;
  try {
    return JSON.parse(value);
  } catch {
    const objectStart = value.indexOf("{");
    const objectEnd = value.lastIndexOf("}");
    if (objectStart >= 0 && objectEnd > objectStart) {
      try {
        return JSON.parse(value.slice(objectStart, objectEnd + 1));
      } catch {}
    }
    return null;
  }
}

function walk(value, visit) {
  if (Array.isArray(value)) {
    for (const item of value) walk(item, visit);
    return;
  }
  if (!value || typeof value !== "object") return;
  visit(value);
  for (const item of Object.values(value)) walk(item, visit);
}

function firstLine(value) {
  return String(value ?? "")
    .split(/\r?\n/)
    .map(line => line.trim())
    .find(Boolean) ?? "";
}

function dedupe(items) {
  const seen = new Set();
  const out = [];
  for (const item of items) {
    if (!item) continue;
    if (seen.has(item)) continue;
    seen.add(item);
    out.push(item);
  }
  return out;
}

function exitCodeOk(code) {
  if (code === null) return true;
  return code === 0;
}

function numberOrNull(value) {
  if (value === null || value === undefined || value === "") return null;
  const n = Number(value);
  return Number.isFinite(n) ? n : null;
}

function text(value) {
  return String(value ?? "").trim();
}
