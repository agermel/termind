// Termind Owner Resolve — git author -> Lark open_id 解析的纯数据 tool.
//
// 使用 lark-cli contact +search-user --as user --query <email|name>.
// 注意: +search-user 只支持 --as user, bot 不支持. 因此当没有 user OAuth
// profile 时, plan 阶段返回 missingCapability=user_oauth, orchestrating
// skill 应当 fallback 到 "label-only" owner (只显示 git author 名字而不 @).
//
// Actions:
//   plan  -> 给定 author email/name + sender, 输出一条 lark-cli 命令.
//            email 优先 (精度高), 没 email 才退回 name.
//   parse -> 解析 stdout, 返回 { owner: { kind, openId, label, email, source,
//            confidence }, errors[] }. 找到唯一精确命中: confidence="high";
//            多命中: confidence="medium"; 命中 0 但拿到了 git author label:
//            confidence="label_only".
//
// 不访问 shell / 网络 / 文件 / env.

const DEFAULT_PAGE_SIZE = 5;

export function ownerResolveTool(params = {}) {
  const action = String(params.action ?? "plan").trim().toLowerCase();
  if (action === "parse") return parseOwnerResolve(params);
  if (action === "plan") return buildOwnerResolvePlan(params);
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
export function buildOwnerResolvePlan(params = {}) {
  const sender = normalizeSender(params.sender);
  const email = text(params.email ?? params.authorEmail);
  const name = text(params.name ?? params.authorName);
  const profile = text(params.profile);
  const configDir = text(params.larkCliConfigDir ?? params.configDir);

  const errors = [];
  if (!email && !name) {
    errors.push("either email or name is required for owner resolve.");
  }
  if (sender !== "user") {
    // contact +search-user 是 user-only. fallback 路径(只用 git author 名字)
    // 不需要 OpenClaw exec, orchestrating skill 直接构造 owner.confidence=label_only.
    return {
      version: 1,
      sideEffects: false,
      ok: false,
      sender,
      missingCapability: "user_oauth",
      execTool: null,
      commands: [],
      authorEmail: email,
      authorName: name,
      labelOnlyOwner: buildLabelOnlyOwner(name, email),
      errors: errors.length > 0 ? errors : [
        "lark-cli contact +search-user is user-only; no user OAuth available, fall back to label-only owner."
      ],
      parse: parseHandle()
    };
  }
  if (errors.length > 0) {
    return {
      version: 1,
      sideEffects: false,
      ok: false,
      sender,
      execTool: null,
      commands: [],
      errors,
      parse: parseHandle()
    };
  }

  // email 优先, 没 email 才退回 name. 两段都用 contact +search-user.
  const query = email || name;
  const args = larkCliArgs(profile, [
    "contact",
    "+search-user",
    "--as",
    "user",
    "--query",
    query,
    "--page-size",
    String(DEFAULT_PAGE_SIZE),
    "--format",
    "json"
  ]);
  const env = configDir ? { LARKSUITE_CLI_CONFIG_DIR: configDir } : {};

  return {
    version: 1,
    sideEffects: false,
    ok: true,
    sender: "user",
    profile,
    larkCliConfigDir: configDir,
    authorEmail: email,
    authorName: name,
    queryUsed: query,
    queryKind: email ? "email" : "name",
    execTool: "exec",
    commands: [
      {
        key: "ownerSearch",
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
export function parseOwnerResolve(params = {}) {
  const errors = [];
  const stdout = text(params.stdout ?? params.output);
  const stderr = text(params.stderr ?? params.error);
  const exitCode = numberOrNull(params.exitCode);
  const email = text(params.email ?? params.authorEmail).toLowerCase();
  const name = text(params.name ?? params.authorName);
  const queryKind = text(params.queryKind);

  let needsUserAuthorization = false;
  let missingCapability = "";

  const candidates = valueCandidates(stdout);
  if (candidates.length === 0 && stdout) errors.push(firstLine(stdout));
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

  if (needsUserAuthorization) {
    return {
      version: 1,
      ok: false,
      needsUserAuthorization: true,
      missingCapability: "user_oauth",
      owner: buildLabelOnlyOwner(name, email),
      candidates: [],
      errors: dedupe(errors).filter(Boolean)
    };
  }

  const users = collectUsers(candidates);

  if (users.length === 0) {
    return {
      version: 1,
      ok: exitCodeOk(exitCode),
      needsUserAuthorization: false,
      owner: buildLabelOnlyOwner(name, email),
      candidates: [],
      errors: dedupe(errors).filter(Boolean)
    };
  }

  // 精确匹配优先: email 命中 (queryKind=email) 比 name 命中可信度更高.
  let exact = null;
  if (email && queryKind === "email") {
    exact = users.find(u => u.email && u.email.toLowerCase() === email) ?? null;
  }
  if (!exact && name) {
    exact = users.find(u => sameName(u.name, name)) ?? null;
  }

  if (exact) {
    const confidence = email && queryKind === "email" && exact.email && exact.email.toLowerCase() === email
      ? "high"
      : "medium";
    return {
      version: 1,
      ok: true,
      needsUserAuthorization: false,
      owner: {
        kind: "lark_user",
        openId: exact.openId,
        label: exact.name || name,
        email: exact.email || email,
        source: "lark_search",
        confidence
      },
      candidates: users.slice(0, DEFAULT_PAGE_SIZE),
      errors: dedupe(errors).filter(Boolean)
    };
  }

  // 没有精确命中, 但有候选: 返回 confidence=ambiguous 让 skill 决定 (通常
  // 不 @ 任何人, 只列候选给报告 reviewer 看).
  return {
    version: 1,
    ok: true,
    needsUserAuthorization: false,
    owner: buildLabelOnlyOwner(name, email),
    candidates: users.slice(0, DEFAULT_PAGE_SIZE),
    errors: dedupe(errors).filter(Boolean)
  };
}

function parseHandle() {
  return { tool: "termind_owner_resolve", action: "parse" };
}

function buildLabelOnlyOwner(name, email) {
  const cleanedName = text(name);
  const cleanedEmail = text(email);
  if (!cleanedName && !cleanedEmail) return null;
  return {
    kind: "git_author",
    openId: "",
    label: cleanedName || cleanedEmail,
    email: cleanedEmail,
    source: "git_author",
    confidence: "label_only"
  };
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------
function collectUsers(candidates) {
  const out = [];
  const seen = new Set();
  for (const candidate of candidates) {
    walk(candidate, item => {
      const u = normalizeUser(item);
      if (!u) return;
      if (seen.has(u.openId)) return;
      seen.add(u.openId);
      out.push(u);
    });
  }
  return out;
}

function normalizeUser(item) {
  if (!item || typeof item !== "object" || Array.isArray(item)) return null;
  const openId = text(item.open_id ?? item.openId);
  if (!openId) return null;
  const name = text(item.name ?? item.localized_name ?? item.localizedName);
  const email = text(item.email ?? item.enterprise_email ?? item.enterpriseEmail);
  const department = text(item.department_path ?? item.departmentPath);
  return { openId, name, email, department };
}

function sameName(a, b) {
  if (!a || !b) return false;
  return String(a).trim().toLowerCase() === String(b).trim().toLowerCase();
}

function normalizeSender(value) {
  return String(value ?? "user").trim().toLowerCase() === "user" ? "user" : "bot";
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
