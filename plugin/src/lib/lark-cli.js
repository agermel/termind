export function larkTargetsForEvent(event) {
  const targets = Array.isArray(event.larkTargets) ? event.larkTargets : [];
  const out = targets.filter(target => target?.enabled !== false && target?.id).map(target => ({
    type: target.type === "user" || target.type === "bot" ? target.type : "chat",
    id: String(target.id).trim(),
    label: target.label || ""
  })).filter(target => target.id);
  if (out.length > 0) return out;
  if (event.larkChatId) {
    return [{ type: "chat", id: event.larkChatId, label: "legacy chat" }];
  }
  if (event.larkUserOpenId) {
    return [{ type: "user", id: event.larkUserOpenId, label: "user" }];
  }
  return [];
}

export function buildLarkCliCommands(event, card) {
  const routeCommands = larkRouteCommandsForEvent(event, card);
  if (routeCommands.length > 0) return routeCommands;
  return larkTargetsForEvent(event).map(target => larkCliCommandForTarget(event, target, card));
}

function larkCliCommandForTarget(event, target, card) {
  const profile = String(event.larkCliProfile || target.profile || "").trim();
  const args = profileArgs(profile);
  args.push("im", "+messages-send", "--as", event.larkSender || "bot", "--content", JSON.stringify(card), "--msg-type", "interactive");
  if (target.type === "chat") {
    args.push("--chat-id", target.id);
  } else {
    args.push("--user-id", target.id);
  }
  return {
    target,
    command: "lark-cli",
    args,
    env: optionalEnv(event.larkCliConfigDir),
    display: ["lark-cli", ...args.map(shellQuote)].join(" ")
  };
}

function larkRouteCommandsForEvent(event, card) {
  const routes = Array.isArray(event.larkForwardingRoutes) ? event.larkForwardingRoutes : [];
  const identities = event.larkForwardingIdentities && typeof event.larkForwardingIdentities === "object" ? event.larkForwardingIdentities : {};
  const out = [];
  for (const route of routes) {
    if (!route || route.enabled === false) continue;
    const target = normalizeTarget(route.target);
    if (!target) continue;
    const identityId = String(route.identityId ?? "").trim();
    const identity = identities[identityId] && typeof identities[identityId] === "object" ? identities[identityId] : {};
    const sender = normalizeSender(identity.kind || route.sender || event.larkSender);
    const configDir = String(identity.larkCliConfigDir || route.larkCliConfigDir || "").trim();
    const profile = String(identity.profile || route.profile || "").trim();
    const args = profileArgs(profile);
    args.push("im", "+messages-send", "--as", sender, "--content", JSON.stringify(card), "--msg-type", "interactive");
    if (target.type === "chat") {
      args.push("--chat-id", target.id);
    } else {
      args.push("--user-id", target.id);
    }
    out.push({
      identityId,
      target,
      command: "lark-cli",
      args,
      env: optionalEnv(configDir),
      display: commandDisplay("lark-cli", args, configDir)
    });
  }
  return out;
}

function normalizeTarget(target) {
  if (!target || typeof target !== "object") return null;
  const id = String(target.id ?? "").trim();
  if (!id) return null;
  return {
    type: target.type === "user" || target.type === "bot" ? target.type : "chat",
    id,
    label: target.label || ""
  };
}

function normalizeSender(value) {
  return String(value ?? "").trim().toLowerCase() === "user" ? "user" : "bot";
}

function optionalEnv(configDir) {
  return configDir ? { LARKSUITE_CLI_CONFIG_DIR: configDir } : {};
}

function profileArgs(profile) {
  return profile ? ["--profile", profile] : [];
}

function commandDisplay(command, args, configDir) {
  const body = [command, ...args.map(shellQuote)].join(" ");
  return configDir ? "LARKSUITE_CLI_CONFIG_DIR=" + shellEnvValue(configDir) + " " + body : body;
}

function shellEnvValue(value) {
  const s = String(value ?? "").trim();
  if (s.startsWith("$HOME/")) {
    return "\"$HOME/" + s.slice("$HOME/".length).replaceAll("\\", "\\\\").replaceAll("\"", "\\\"").replaceAll("`", "\\`") + "\"";
  }
  return shellQuote(s);
}

function shellQuote(value) {
  const s = String(value);
  if (/^[A-Za-z0-9_@%+=:,./-]+$/.test(s)) return s;
  return `'${s.replace(/'/g, "'\\''")}'`;
}
