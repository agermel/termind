export function larkCliDiscoverTool(params = {}) {
  const action = String(params.action ?? "plan").trim().toLowerCase();
  if (action === "parse") return parseLarkCliDiscover(params);
  return buildLarkCliDiscoverPlan(params);
}

export function buildLarkCliDiscoverPlan(params = {}) {
  const kind = normalizeKind(params.kind);
  const sender = normalizeSender(params.sender);
  const query = text(params.query);
  const memberOpenID = text(params.memberOpenID);
  const configDir = text(params.larkCliConfigDir ?? params.configDir);
  const commands = [];
  if (kind === "chat") {
    if (query || (sender === "user" && memberOpenID)) {
      commands.push({
        key: "chatSearch",
        command: "lark-cli",
        args: ["im", "+chat-search", "--as", sender, "--format", "json", ...optionalArg("--query", query), ...optionalArg("--member-ids", sender === "user" ? memberOpenID : "")],
        env: optionalEnv(configDir),
        display: commandDisplay("lark-cli", ["im", "+chat-search", "--as", sender, "--format", "json", ...optionalArg("--query", query), ...optionalArg("--member-ids", sender === "user" ? memberOpenID : "")], configDir),
        optional: false
      });
    } else {
      commands.push({
        key: "chatList",
        command: "lark-cli",
        args: ["im", "chats", "list", "--as", sender, "--format", "json", "--page-all", "--page-limit", "50"],
        env: optionalEnv(configDir),
        display: commandDisplay("lark-cli", ["im", "chats", "list", "--as", sender, "--format", "json", "--page-all", "--page-limit", "50"], configDir),
        optional: false
      });
    }
  } else {
    commands.push({
      key: "userSearch",
      command: "lark-cli",
      args: ["contact", "+search-user", "--as", sender, "--format", "json", ...optionalArg("--query", query)],
      env: optionalEnv(configDir),
      display: commandDisplay("lark-cli", ["contact", "+search-user", "--as", sender, "--format", "json", ...optionalArg("--query", query)], configDir),
      optional: false
    });
  }
  return {
    version: 1,
    sideEffects: false,
    kind,
    larkCliConfigDir: configDir,
    execTool: "exec",
    commands,
    parse: {
      tool: "termind_lark_cli_discover",
      action: "parse"
    }
  };
}

export function parseLarkCliDiscover(params = {}) {
  const kind = normalizeKind(params.kind);
  const errors = [];
  let choices = [];
  if (kind === "chat") {
    choices = collectChatChoicesFromValues(params.chatSearchOutput, params.chatListOutput, params.stdout, params.output, params.results, params.execResults);
    if (choices.length === 0) collectErrors(errors, params.chatSearchOutput, params.chatListOutput, params.stdout, params.output, params.results, params.execResults, params.chatSearchError, params.chatListError, params.stderr, params.error);
  } else {
    choices = collectUserChoicesFromValues(params.userSearchOutput, params.stdout, params.output, params.results, params.execResults);
    if (choices.length === 0) collectErrors(errors, params.userSearchOutput, params.stdout, params.output, params.results, params.execResults, params.userSearchError, params.stderr, params.error);
  }
  return {
    version: 1,
    kind,
    choices,
    errors
  };
}

function optionalArg(name, value) {
  return value ? [name, value] : [];
}

function optionalEnv(configDir) {
  return configDir ? { LARKSUITE_CLI_CONFIG_DIR: configDir } : {};
}

function commandDisplay(command, args, configDir) {
  const body = [command, ...args.map(shellQuote)].join(" ");
  return configDir ? "LARKSUITE_CLI_CONFIG_DIR=" + shellQuote(configDir) + " " + body : body;
}

function normalizeKind(value) {
  return String(value ?? "chat").trim().toLowerCase() === "user" ? "user" : "chat";
}

function normalizeSender(value) {
  return String(value ?? "").trim().toLowerCase() === "user" ? "user" : "bot";
}

function collectChatChoices(value) {
  const out = [];
  const seen = new Set();
  walk(value, item => {
    const id = text(item.chat_id ?? item.chatId);
    if (!id || seen.has(id)) return;
    seen.add(id);
    out.push({ type: "chat", id, label: text(item.name ?? item.description) });
  });
  return out;
}

function collectChatChoicesFromValues(...values) {
  return mergeChoices(values, collectChatChoices);
}

function collectUserChoices(value) {
  const out = [];
  const seen = new Set();
  walk(value, item => {
    const id = text(item.open_id ?? item.openId ?? item.user_id ?? item.userId);
    if (!id || seen.has(id)) return;
    seen.add(id);
    out.push({ type: "user", id, label: text(item.name ?? item.localized_name ?? item.localizedName) });
  });
  return out;
}

function collectUserChoicesFromValues(...values) {
  return mergeChoices(values, collectUserChoices);
}

function mergeChoices(values, collect) {
  const out = [];
  const seen = new Set();
  for (const value of values) {
    for (const candidate of valueCandidates(value)) {
      for (const choice of collect(candidate)) {
        const key = `${choice.type}:${choice.id}`;
        if (seen.has(key)) continue;
        seen.add(key);
        out.push(choice);
      }
    }
  }
  return out;
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

function collectErrors(errors, ...values) {
  const seen = new Set();
  for (const value of values) {
    const message = errorMessage(value);
    if (!message || seen.has(message)) continue;
    seen.add(message);
    errors.push(message);
  }
}

function errorMessage(value) {
  const direct = firstLine(value);
  for (const candidate of valueCandidates(value)) {
    if (!candidate || typeof candidate !== "object") continue;
    const nested = candidate.error;
    const message = text(candidate.message ?? candidate.msg ?? nested?.message ?? nested?.msg);
    const code = text(candidate.code ?? nested?.code);
    if (message && code && code !== "0") return `[${code}] ${message}`;
    if (message && (candidate.ok === false || nested || code)) return message;
  }
  if (/command exited with code|api call failed|invalid param|need_user_authorization/i.test(direct)) return direct;
  return "";
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
    const arrayStart = value.indexOf("[");
    const arrayEnd = value.lastIndexOf("]");
    if (arrayStart >= 0 && arrayEnd > arrayStart) {
      try {
        return JSON.parse(value.slice(arrayStart, arrayEnd + 1));
      } catch {}
    }
    return null;
  }
}

function firstLine(value) {
  return text(value).split(/\r?\n/).map(line => line.trim()).find(Boolean) ?? "";
}

function shellQuote(value) {
  value = String(value ?? "");
  if (/^[A-Za-z0-9_./:@%+=,-]+$/.test(value)) return value;
  return "'" + value.replaceAll("'", "'\\''") + "'";
}

function text(value) {
  return String(value ?? "").trim();
}
