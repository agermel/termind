export function buildLarkCliStatus(params = {}) {
  const profiles = collectProfiles(params.profiles ?? parseJSON(params.profileListOutput));
  const selected = selectLarkProfile(profiles);
  const configDir = text(params.larkCliConfigDir ?? params.configDir);
  const doctorExitCode = numberOrNull(params.doctorExitCode);
  const doctorOutput = text(params.doctorOutput);
  const selfUser = parseJSON(params.selfUserOutput);
  const userOpenID = firstStringForKey(selfUser, "open_id");
  const installed = !looksLikeMissingCommand(params.versionError) && !looksLikeMissingCommand(params.profileListError);
  const doctorOK = doctorExitCode === 0 || /\b(ok|pass|success|healthy)\b/i.test(doctorOutput);
  const identityChecked = Object.prototype.hasOwnProperty.call(params, "selfUserOutput");
  const errors = [];
  if (!installed) errors.push("lark-cli not found in OpenClaw exec environment");
  if (profiles.length === 0) errors.push("no lark-cli profile found");
  if (identityChecked && !userOpenID) errors.push("lark-cli user contact API is not authorized; bot profile can still be ready");
  if (doctorExitCode !== null && !doctorOK) errors.push("lark-cli doctor --offline failed");
  const profileReady = installed && Boolean(selected?.name);
  return {
    version: 1,
    installed,
    ready: profileReady && (doctorExitCode === null || doctorOK),
    profile: selected?.name ?? "",
    larkCliConfigDir: configDir,
    profiles,
    doctor: {
      ok: doctorExitCode === null ? null : doctorOK,
      exitCode: doctorExitCode,
      output: firstLine(doctorOutput)
    },
    auth: {
      userOpenID
    },
    errors
  };
}

export function larkCliStatusTool(params = {}) {
  const action = String(params.action ?? "parse").trim().toLowerCase();
  if (action && action !== "parse") {
    return {
      version: 1,
      installed: false,
      ready: false,
      profile: "",
      profiles: [],
      doctor: {},
      auth: {},
      errors: [`unsupported action: ${action}`]
    };
  }
  return buildLarkCliStatus(params);
}

export function larkCliExistsTool(params = {}) {
  const action = normalizedAction(params);
  const configDir = text(params.larkCliConfigDir ?? params.configDir);
  if (action === "plan") {
    return execPlan("exists", "termind_lark_cli_exists", prefixLarkCliCommand(configDir, "lark-cli --version"), { larkCliConfigDir: configDir });
  }
  if (action !== "parse") return unsupportedStep("exists", action);
  const stdout = execStdout(params);
  const stderr = execStderr(params);
  const exitCode = numberOrNull(params.exitCode);
  const installed = exitCode !== 127 && !looksLikeMissingCommand(stdout) && !looksLikeMissingCommand(stderr);
  const errors = installed ? [] : ["lark-cli not found in OpenClaw exec environment"];
  return {
    version: 1,
    check: "exists",
    installed,
    larkCliConfigDir: configDir,
    versionText: firstLine(stdout),
    stop: !installed,
    next: installed ? "termind_lark_cli_login_status" : "",
    status: {
      version: 1,
      installed,
      ready: false,
      profile: "",
      larkCliConfigDir: configDir,
      profiles: [],
      doctor: {},
      auth: {},
      errors
    }
  };
}

export function larkCliLoginStatusTool(params = {}) {
  const action = normalizedAction(params);
  const configDir = text(params.larkCliConfigDir ?? params.configDir);
  if (action === "plan") {
    return execPlan("login_status", "termind_lark_cli_login_status", prefixLarkCliCommand(configDir, "lark-cli profile list"), { larkCliConfigDir: configDir });
  }
  if (action !== "parse") return unsupportedStep("login_status", action);
  const stdout = execStdout(params);
  const stderr = execStderr(params);
  const profiles = collectProfiles(params.profiles ?? parseJSON(stdout));
  const selected = selectLarkProfile(profiles);
  const loggedIn = Boolean(selected?.name) && !looksLikeAuthProblem(stderr) && !looksLikeAuthProblem(stdout);
  const errors = [];
  if (profiles.length === 0) errors.push("no lark-cli profile found");
  if (profiles.length > 0 && !loggedIn) errors.push("lark-cli profile exists but login is not valid");
  const ready = Boolean(loggedIn);
  return {
    version: 1,
    check: "login_status",
    installed: true,
    loggedIn,
    profile: selected?.name ?? "",
    larkCliConfigDir: configDir,
    profiles,
    stop: !loggedIn,
    next: loggedIn ? "termind_lark_cli_doctor_status" : "",
    status: {
      version: 1,
      installed: true,
      ready,
      profile: selected?.name ?? "",
      larkCliConfigDir: configDir,
      profiles,
      doctor: {},
      auth: {},
      errors
    }
  };
}

export function larkCliIdentityStatusTool(params = {}) {
  const action = normalizedAction(params);
  const configDir = text(params.larkCliConfigDir ?? params.configDir);
  if (action === "plan") {
    return execPlan("identity_status", "termind_lark_cli_identity_status", prefixLarkCliCommand(configDir, "lark-cli contact +get-user --as user --format json"), { larkCliConfigDir: configDir });
  }
  if (action !== "parse") return unsupportedStep("identity_status", action);
  const stdout = execStdout(params);
  const stderr = execStderr(params);
  const selfUser = parseJSON(stdout);
  const userOpenID = firstStringForKey(selfUser, "open_id");
  const profiles = collectProfiles(params.profiles ?? []);
  const profile = selectLarkProfile(profiles)?.name || "";
  const errors = [];
  if (!userOpenID) errors.push("lark-cli user contact API is not authorized; bot profile can still be ready");
  if (stderr && !userOpenID) errors.push(firstLine(stderr));
  const profileReady = Boolean(profile) || Boolean(params.profileReady);
  return {
    version: 1,
    check: "identity_status",
    installed: true,
    loggedIn: Boolean(userOpenID),
    userOpenID,
    larkCliConfigDir: configDir,
    stop: false,
    next: "termind_lark_cli_doctor_status",
    status: {
      version: 1,
      installed: true,
      ready: profileReady || Boolean(userOpenID),
      profile,
      larkCliConfigDir: configDir,
      profiles,
      doctor: {},
      auth: { userOpenID },
      errors
    }
  };
}

export function larkCliDoctorStatusTool(params = {}) {
  const action = normalizedAction(params);
  const configDir = text(params.larkCliConfigDir ?? params.configDir);
  if (action === "plan") {
    return execPlan("doctor_status", "termind_lark_cli_doctor_status", prefixLarkCliCommand(configDir, "lark-cli doctor --offline"), { larkCliConfigDir: configDir });
  }
  if (action !== "parse") return unsupportedStep("doctor_status", action);
  const stdout = execStdout(params);
  const stderr = execStderr(params);
  const output = text(stdout + "\n" + stderr);
  const exitCode = numberOrNull(params.exitCode);
  const ok = exitCode === 0 || /\b(ok|pass|success|healthy)\b/i.test(output);
  const errors = ok ? [] : ["lark-cli doctor --offline failed"];
  return {
    version: 1,
    check: "doctor_status",
    ok,
    larkCliConfigDir: configDir,
    exitCode,
    stop: !ok,
    next: "",
    statusPatch: {
      doctor: {
        ok,
        exitCode,
        output: firstLine(output)
      },
      errors
    }
  };
}

export function larkCliProfileUseTool(params = {}) {
  const action = normalizedAction(params);
  const profile = text(params.profile);
  const configDir = text(params.larkCliConfigDir ?? params.configDir);
  if (action === "plan") {
    if (!profile) {
      return {
        version: 1,
        ok: false,
        profile: "",
        command: "",
        commands: [],
        errors: ["profile is required"]
      };
    }
    return execPlan("profile_use", "termind_lark_cli_profile_use", prefixLarkCliCommand(configDir, "lark-cli profile use " + shellQuote(profile)), { larkCliConfigDir: configDir });
  }
  if (action !== "parse") return unsupportedStep("profile_use", action);
  const stdout = execStdout(params);
  const stderr = execStderr(params);
  const exitCode = numberOrNull(params.exitCode);
  const output = text(stdout + "\n" + stderr);
  const ok = exitCode === null ? !looksLikeFailure(output) : exitCode === 0;
  return {
    version: 1,
    ok,
    profile,
    output: firstLine(output),
    errors: ok ? [] : [firstLine(output) || "lark-cli profile use failed"]
  };
}

export function larkCliConfigBindTool(params = {}) {
  const action = normalizedAction(params);
  const appId = text(params.appId ?? params.appID ?? params.app_id);
  const identity = normalizeBindIdentity(params.identity);
  const configDir = text(params.larkCliConfigDir ?? params.configDir);
  if (action === "plan") {
    if (!appId) {
      return {
        version: 1,
        check: "config_bind",
        ok: false,
        appId: "",
        identity,
        command: "",
        commands: [],
        errors: ["appId is required"]
      };
    }
    return execPlan("config_bind", "termind_lark_cli_config_bind", prefixLarkCliCommand(configDir, "lark-cli config bind --source openclaw --app-id " + shellQuote(appId) + " --identity " + shellQuote(identity)), { larkCliConfigDir: configDir });
  }
  if (action !== "parse") return unsupportedStep("config_bind", action);
  const stdout = execStdout(params);
  const stderr = execStderr(params);
  const exitCode = numberOrNull(params.exitCode);
  const output = text(stdout + "\n" + stderr);
  const ok = exitCode === null ? !looksLikeFailure(output) : exitCode === 0;
  return {
    version: 1,
    check: "config_bind",
    ok,
    appId,
    identity,
    larkCliConfigDir: configDir,
    profile: text(params.profile) || appId,
    output: firstLine(output),
    errors: ok ? [] : [firstLine(output) || "lark-cli config bind failed"]
  };
}

export function larkCliConfigInitTool(params = {}) {
  const action = normalizedAction(params);
  const appId = text(params.appId ?? params.appID ?? params.app_id);
  const profile = text(params.profile ?? params.name);
  const brand = normalizeBrand(params.brand);
  const configDir = text(params.larkCliConfigDir ?? params.configDir);
  if (action === "plan") {
    const command = appId ? prefixLarkCliCommand(configDir, "lark-cli config init --app-id " + shellQuote(appId) + " --brand " + shellQuote(brand) + " --app-secret-stdin") : "";
    return {
      version: 1,
      check: "config_init",
      ok: Boolean(appId),
      appId,
      profile,
      brand,
      larkCliConfigDir: configDir,
      command,
      commands: [],
      manual: true,
      sideEffects: true,
      stop: true,
      requiresSecretInput: Boolean(appId),
      errors: appId ? [] : ["appId is required"]
    };
  }
  if (action === "parse") {
    const stdout = execStdout(params);
    const stderr = execStderr(params);
    const exitCode = numberOrNull(params.exitCode);
    const output = text(stdout + "\n" + stderr);
    const ok = exitCode === null ? !looksLikeFailure(output) : exitCode === 0;
    return {
      version: 1,
      check: "config_init",
      ok,
      appId,
      profile,
      brand,
      larkCliConfigDir: configDir,
      output: firstLine(output),
      errors: ok ? [] : [firstLine(output) || "lark-cli config init failed"]
    };
  }
  return {
    version: 1,
    check: "config_init",
    ok: false,
    appId,
    profile,
    brand,
    larkCliConfigDir: configDir,
    output: "",
    errors: [`unsupported action: ${action}`]
  };
}

export function larkCliAuthLoginTool(params = {}) {
  const action = normalizedAction(params);
  const phase = normalizeAuthPhase(params.phase ?? params.step ?? (params.deviceCode ? "complete" : "start"));
  const deviceCode = text(params.deviceCode);
  const configDir = text(params.larkCliConfigDir ?? params.configDir);
  if (action === "plan") {
    if (phase === "complete") {
      if (!deviceCode) {
        return {
          version: 1,
          check: "auth_login_complete",
          ok: false,
          command: "",
          commands: [],
          errors: ["deviceCode is required"]
        };
      }
      return execPlan("auth_login_complete", "termind_lark_cli_auth_login", prefixLarkCliCommand(configDir, "lark-cli auth login --device-code " + shellQuote(deviceCode) + " --json"), { larkCliConfigDir: configDir });
    }
    return execPlan("auth_login_start", "termind_lark_cli_auth_login", prefixLarkCliCommand(configDir, "lark-cli auth login --recommend --no-wait --json"), { larkCliConfigDir: configDir });
  }
  if (action !== "parse") return unsupportedStep("auth_login", action);
  if (phase === "complete") return parseAuthLoginComplete(params);
  return parseAuthLoginStart(params);
}

export function selectLarkProfile(profiles) {
  if (!Array.isArray(profiles) || profiles.length === 0) return null;
  return profiles.find(profile => profile.active && profile.tokenValid) ??
    profiles.find(profile => profile.active) ??
    profiles.find(profile => profile.tokenValid) ??
    profiles[0];
}

function parseAuthLoginStart(params) {
  const stdout = execStdout(params);
  const stderr = execStderr(params);
  const exitCode = numberOrNull(params.exitCode);
  const output = text(stdout + "\n" + stderr);
  const raw = parseLooseJSON(stdout) ?? parseLooseJSON(stderr) ?? {};
  const deviceCode = firstStringForKeys(raw, "device_code", "deviceCode");
  const userCode = firstStringForKeys(raw, "user_code", "userCode");
  const verificationURL = firstStringForKeys(raw, "verification_uri_complete", "verificationURIComplete", "verificationUriComplete", "verification_url", "verificationURL", "verificationUrl", "verification_uri", "verificationURI", "verificationUri") || firstURL(output);
  const expiresIn = firstNumberForKeys(raw, "expires_in", "expiresIn");
  const interval = firstNumberForKeys(raw, "interval");
  const message = firstStringForKeys(raw, "message");
  const ok = Boolean(deviceCode) && (exitCode === null || exitCode === 0);
  const errors = [];
  if (!ok) errors.push(firstLine(output) || "lark-cli auth login --no-wait failed");
  return {
    version: 1,
    check: "auth_login_start",
    ok,
    deviceCode,
    userCode,
    verificationURL,
    expiresIn,
    interval,
    message,
    output: firstLine(output),
    errors
  };
}

function parseAuthLoginComplete(params) {
  const stdout = execStdout(params);
  const stderr = execStderr(params);
  const exitCode = numberOrNull(params.exitCode);
  const output = text(stdout + "\n" + stderr);
  const ok = exitCode === null ? !looksLikeFailure(output) : exitCode === 0;
  return {
    version: 1,
    check: "auth_login_complete",
    ok,
    output: firstLine(output),
    errors: ok ? [] : [firstLine(output) || "lark-cli auth login --device-code failed"]
  };
}

function collectProfiles(value) {
  const out = [];
  const seen = new Set();
  walk(value, candidate => {
    const name = text(candidate.name ?? candidate.profile ?? candidate.profileName);
    if (!name || seen.has(name)) return;
    seen.add(name);
    const tokenStatus = text(candidate.tokenStatus ?? candidate.authStatus ?? candidate.status);
    const user = text(candidate.user ?? candidate.userName ?? candidate.username ?? candidate.userOpenID ?? candidate.userOpenId);
    const identity = normalizeProfileIdentity(candidate.identity ?? candidate.as ?? candidate.sender);
    out.push({
      name,
      appId: text(candidate.appId ?? candidate.appID ?? candidate.app_id),
      brand: text(candidate.brand),
      user,
      identity,
      active: Boolean(candidate.active ?? candidate.isActive ?? candidate.current),
      tokenStatus,
      tokenValid: isValidTokenStatus(tokenStatus)
    });
  });
  return out;
}

function walk(value, visit) {
  if (Array.isArray(value)) {
    for (const item of value) walk(item, visit);
    return;
  }
  if (!value || typeof value !== "object") return;
  if (value.name || value.profile || value.profileName) visit(value);
  for (const item of Object.values(value)) walk(item, visit);
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

function parseLooseJSON(value) {
  if (value == null || value === "") return null;
  if (typeof value !== "string") return value;
  const parsed = parseJSON(value);
  if (parsed != null) return parsed;
  const objectStart = value.indexOf("{");
  const objectEnd = value.lastIndexOf("}");
  if (objectStart >= 0 && objectEnd > objectStart) {
    const object = parseJSON(value.slice(objectStart, objectEnd + 1));
    if (object != null) return object;
  }
  const arrayStart = value.indexOf("[");
  const arrayEnd = value.lastIndexOf("]");
  if (arrayStart >= 0 && arrayEnd > arrayStart) {
    const array = parseJSON(value.slice(arrayStart, arrayEnd + 1));
    if (array != null) return array;
  }
  return null;
}

function firstStringForKey(value, key) {
  if (Array.isArray(value)) {
    for (const item of value) {
      const found = firstStringForKey(item, key);
      if (found) return found;
    }
    return "";
  }
  if (!value || typeof value !== "object") return "";
  if (typeof value[key] === "string" && value[key].trim()) return value[key].trim();
  for (const item of Object.values(value)) {
    const found = firstStringForKey(item, key);
    if (found) return found;
  }
  return "";
}

function firstStringForKeys(value, ...keys) {
  const found = firstValueForKeys(value, keys);
  if (typeof found === "string") return found.trim();
  if (found == null) return "";
  return String(found).trim();
}

function firstNumberForKeys(value, ...keys) {
  const found = firstValueForKeys(value, keys);
  const n = Number(found);
  return Number.isFinite(n) && n > 0 ? n : 0;
}

function firstValueForKeys(value, keys) {
  const wanted = new Set(keys.map(normalizeLookupKey));
  const visit = item => {
    if (Array.isArray(item)) {
      for (const child of item) {
        const found = visit(child);
        if (found !== undefined) return found;
      }
      return undefined;
    }
    if (!item || typeof item !== "object") return undefined;
    for (const [key, child] of Object.entries(item)) {
      if (wanted.has(normalizeLookupKey(key))) return child;
    }
    for (const child of Object.values(item)) {
      const found = visit(child);
      if (found !== undefined) return found;
    }
    return undefined;
  };
  return visit(value);
}

function normalizeLookupKey(value) {
  return String(value ?? "").trim().toLowerCase().replaceAll(/[_\-. ]/g, "");
}

function firstURL(value) {
  return text(value).match(/https?:\/\/[^\s"'<>]+/)?.[0] ?? "";
}

function isValidTokenStatus(value) {
  return /^(valid|ok|ready|authenticated|logged[_ -]?in)$/i.test(text(value));
}

function normalizeAuthPhase(value) {
  const phase = text(value).toLowerCase();
  return phase === "complete" || phase === "poll" || phase === "finish" ? "complete" : "start";
}

function normalizeBindIdentity(value) {
  const identity = text(value).toLowerCase();
  return identity === "user" || identity === "user-default" ? "user-default" : "bot-only";
}

function normalizeBrand(value) {
  return text(value).toLowerCase() === "lark" ? "lark" : "feishu";
}

function normalizeProfileIdentity(value) {
  return text(value).toLowerCase() === "user" ? "user" : "bot";
}

function numberOrNull(value) {
  if (value === null || value === undefined || value === "") return null;
  const n = Number(value);
  return Number.isFinite(n) ? n : null;
}

function looksLikeMissingCommand(value) {
  return /command not found|not found|no such file|executable file not found/i.test(text(value));
}

function looksLikeAuthProblem(value) {
  return /need[_ -]?user[_ -]?authorization|unauthori[sz]ed|invalid[_ -]?token|expired|not[_ -]?login|login required/i.test(text(value));
}

function looksLikeFailure(value) {
  return /error|failed|not found|invalid|unauthori[sz]ed/i.test(text(value));
}

function shellQuote(value) {
  value = String(value ?? "");
  if (/^[A-Za-z0-9_./:@%+=,-]+$/.test(value)) return value;
  return "'" + value.replaceAll("'", "'\\''") + "'";
}

function prefixLarkCliCommand(configDir, command) {
  configDir = text(configDir);
  if (!configDir) return command;
  return "LARKSUITE_CLI_CONFIG_DIR=" + shellEnvValue(configDir) + " " + command;
}

function shellEnvValue(value) {
  value = text(value);
  if (value.startsWith("$HOME/")) {
    return "\"$HOME/" + value.slice("$HOME/".length).replaceAll("\\", "\\\\").replaceAll("\"", "\\\"").replaceAll("`", "\\`") + "\"";
  }
  return shellQuote(value);
}

function normalizedAction(params) {
  return String(params.action ?? "plan").trim().toLowerCase();
}

function execPlan(check, parseTool, command, extra = {}) {
  return {
    version: 1,
    check,
    command,
    commands: [{ key: check, command }],
    ...extra,
    exec: {
      tool: "exec",
      requiresAllowlist: "lark-cli"
    },
    parse: {
      tool: parseTool,
      action: "parse"
    }
  };
}

function unsupportedStep(check, action) {
  return {
    version: 1,
    check,
    stop: true,
    status: {
      version: 1,
      installed: false,
      ready: false,
      profile: "",
      profiles: [],
      doctor: {},
      auth: {},
      errors: [`unsupported action: ${action}`]
    }
  };
}

function execStdout(params) {
  return text(params.stdout ?? params.output ?? params.versionOutput ?? params.profileListOutput ?? params.selfUserOutput ?? params.doctorOutput);
}

function execStderr(params) {
  return text(params.stderr ?? params.error ?? params.versionError ?? params.profileListError ?? params.selfUserError ?? params.doctorError);
}

function firstLine(value) {
  return text(value).split(/\r?\n/).map(line => line.trim()).find(Boolean) ?? "";
}

function text(value) {
  return String(value ?? "").trim();
}
