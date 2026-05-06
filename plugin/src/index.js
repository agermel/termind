import { definePluginEntry } from "openclaw/plugin-sdk/plugin-entry";

import { buildIncidentCard } from "./lib/card.js";
import { classifyFailure } from "./lib/classify.js";
import { computeFingerprint } from "./lib/fingerprint.js";
import { larkCliDiscoverTool } from "./lib/lark-cli-discover.js";
import { buildLarkCliCommands, larkTargetsForEvent } from "./lib/lark-cli.js";
import {
  larkCliAuthLoginTool,
  larkCliConfigBindTool,
  larkCliConfigInitTool,
  larkCliDoctorStatusTool,
  larkCliExistsTool,
  larkCliIdentityStatusTool,
  larkCliLoginStatusTool,
  larkCliProfileUseTool,
  larkCliStatusTool
} from "./lib/lark-cli-status.js";
import { buildReportTemplate } from "./lib/report.js";
import { normalizeFailureEvent, redactFailureEvent } from "./lib/redact.js";

const failureEventSchema = {
  type: "object",
  additionalProperties: true,
  required: ["summary", "command"],
  properties: {
    fingerprint: { type: "string" },
    summary: { type: "string" },
    command: { type: "string" },
    severity: { type: "string", enum: ["info", "warning", "incident"] },
    cwd: { type: "string" },
    project: { type: "string" },
    user: { type: "string" },
    branch: { type: "string" },
    gitCommit: { type: "string" },
    environment: { type: "string" },
    tail: { type: "string" },
    larkChatId: {
      type: "string",
      description: "Legacy Feishu/Lark chat id. Prefer larkTargets."
    },
    larkUserOpenId: {
      type: "string",
      description: "Feishu/Lark user open_id for personal delivery."
    },
    larkSender: {
      type: "string",
      enum: ["bot", "user"]
    },
    larkTargets: {
      type: "array",
      items: {
        type: "object",
        additionalProperties: true,
        properties: {
          type: { type: "string", enum: ["chat", "user", "bot"] },
          id: { type: "string" },
          label: { type: "string" },
          enabled: { type: "boolean" }
        }
      }
    },
    stackTop: { type: "array", items: { type: "string" } },
    reportUrl: { type: "string" },
    occurrences: { type: "number" },
    affectedUsers: { type: "number" },
    branchKind: { type: "string" }
  }
};

const larkCliCheckSchema = {
  type: "object",
  additionalProperties: true,
  properties: {
    action: { type: "string", enum: ["plan", "parse"] },
    stdout: { type: "string" },
    stderr: { type: "string" },
    output: { type: "string" },
    error: { type: "string" },
    exitCode: { type: "number" },
    appId: { type: "string" },
    identity: { type: "string" },
    phase: { type: "string" },
    deviceCode: { type: "string" },
    brand: { type: "string" },
    name: { type: "string" },
    larkCliConfigDir: { type: "string" },
    configDir: { type: "string" },
    profiles: {},
    profile: { type: "string" },
    versionOutput: { type: "string" },
    versionError: { type: "string" },
    profileListOutput: { type: "string" },
    profileListError: { type: "string" },
    selfUserOutput: { type: "string" },
    selfUserError: { type: "string" },
    doctorOutput: { type: "string" },
    doctorError: { type: "string" }
  }
};

export default definePluginEntry({
  id: "termind",
  name: "Termind",
  description: "Safe Termind terminal intelligence tools for OpenClaw orchestration.",
  register(api) {
    api.registerTool({
      name: "termind_event_redact",
      description: "Normalize and redact a Termind terminal failure event. Does not access network, shell, files, or environment.",
      parameters: failureEventSchema,
      async execute(_callId, params) {
        const event = redactFailureEvent(normalizeFailureEvent(params, { requireFingerprint: false }));
        return jsonContent({ event });
      }
    });

    api.registerTool({
      name: "termind_fingerprint_compute",
      description: "Compute a stable deterministic fingerprint for a normalized Termind failure event.",
      parameters: failureEventSchema,
      async execute(_callId, params) {
        const event = redactFailureEvent(normalizeFailureEvent(params, { requireFingerprint: false }));
        return jsonContent(computeFingerprint(event));
      }
    });

    api.registerTool({
      name: "termind_failure_classify",
      description: "Classify a Termind failure event severity and routing hints without side effects.",
      parameters: failureEventSchema,
      async execute(_callId, params) {
        const event = redactFailureEvent(normalizeFailureEvent(params, { requireFingerprint: false }));
        const fingerprint = event.fingerprint ? null : computeFingerprint(event);
        if (fingerprint) event.fingerprint = fingerprint.fingerprint;
        return jsonContent(classifyFailure(event));
      }
    });

    api.registerTool({
      name: "termind_lark_card_build",
      description: "Build Lark/Feishu interactive card JSON from a redacted Termind failure event. This tool does not send messages; use termind_lark_cli_send_command_build and OpenClaw exec to send with lark-cli.",
      parameters: failureEventSchema,
      async execute(_callId, params) {
        const event = redactFailureEvent(normalizeFailureEvent(params, { requireFingerprint: false }));
        if (!event.fingerprint) {
          event.fingerprint = computeFingerprint(event).fingerprint;
        }
        const classification = classifyFailure(event);
        event.severity = classification.severity;
        return jsonContent({
          event,
          classification,
          card: buildIncidentCard(event),
          sendHint: {
            preferred: "lark-cli",
            tool: "termind_lark_cli_send_command_build",
            execTool: "exec",
            targets: larkTargetsForEvent(event)
          }
        });
      }
    });

    api.registerTool({
      name: "termind_lark_cli_send_command_build",
      description: "Build safe lark-cli commands for sending a Termind Lark/Feishu interactive card. This tool returns command argv only and does not execute shell commands.",
      parameters: {
        type: "object",
        additionalProperties: true,
        required: ["event", "card"],
        properties: {
          event: failureEventSchema,
          card: { type: "object" }
        }
      },
      async execute(_callId, params) {
        const event = redactFailureEvent(normalizeFailureEvent(params.event ?? {}, { requireFingerprint: false }));
        const card = params.card;
        const commands = buildLarkCliCommands(event, card);
        return jsonContent({
          preferred: "lark-cli",
          commands,
          missingTarget: commands.length === 0,
          exec: {
            tool: "exec",
            requiresAllowlist: "lark-cli"
          }
        });
      }
    });

    api.registerTool({
      name: "termind_lark_cli_exists",
      description: "Fast lark-cli existence check for Termind init. Step 1 only: plan returns exactly one OpenClaw exec command, `lark-cli --version`; parse returns installed=true/false and a ready-shaped status JSON. Stop immediately when installed=false. This tool does not execute shell commands itself.",
      parameters: larkCliCheckSchema,
      async execute(_callId, params) {
        return jsonContent(larkCliExistsTool(params));
      }
    });

    api.registerTool({
      name: "termind_lark_cli_login_status",
      description: "Fast lark-cli login/profile check for Termind init. Use only after termind_lark_cli_exists installed=true. Plan returns exactly one OpenClaw exec command, `lark-cli profile list`; parse returns loggedIn, OpenClaw-side selected profile, profiles, stop/next, and a ready-shaped status JSON. Bot app profiles are valid for readiness. Stop immediately when loggedIn=false. This tool does not execute shell commands itself.",
      parameters: larkCliCheckSchema,
      async execute(_callId, params) {
        return jsonContent(larkCliLoginStatusTool(params));
      }
    });

    api.registerTool({
      name: "termind_lark_cli_identity_status",
      description: "Optional lark-cli current-user API capability check for Termind init. Plan returns exactly one OpenClaw exec command, `lark-cli contact +get-user --as user --format json`; parse extracts userOpenID when available. Failure does not make a bot app profile unready. This tool does not execute shell commands itself.",
      parameters: larkCliCheckSchema,
      async execute(_callId, params) {
        return jsonContent(larkCliIdentityStatusTool(params));
      }
    });

    api.registerTool({
      name: "termind_lark_cli_doctor_status",
      description: "Optional lark-cli doctor check for Termind init. Use only after exists and login/profile checks pass. Plan returns exactly one OpenClaw exec command, `lark-cli doctor --offline`; parse returns doctor ok/exitCode/output and statusPatch. This tool is optional for speed and does not execute shell commands itself.",
      parameters: larkCliCheckSchema,
      async execute(_callId, params) {
        return jsonContent(larkCliDoctorStatusTool(params));
      }
    });

    api.registerTool({
      name: "termind_lark_cli_status",
      description: "Parse direct OpenClaw-side lark-cli status check results into structured readiness JSON. This tool does not execute shell commands.",
      parameters: {
        type: "object",
        additionalProperties: true,
        properties: {
          action: { type: "string", enum: ["parse"] },
          profiles: {},
          profileListOutput: { type: "string" },
          profileListError: { type: "string" },
          versionError: { type: "string" },
          doctorExitCode: { type: "number" },
          doctorOutput: { type: "string" },
          selfUserOutput: { type: "string" }
        }
      },
      async execute(_callId, params) {
        return jsonContent(larkCliStatusTool(params));
      }
    });

    api.registerTool({
      name: "termind_lark_cli_profile_use",
      description: "Build and parse an OpenClaw-side lark-cli active profile switch. Plan returns exactly one OpenClaw exec command, `lark-cli profile use <profile>`. This mutates only OpenClaw-side lark-cli active profile state and does not write Termind config.",
      parameters: larkCliCheckSchema,
      async execute(_callId, params) {
        return jsonContent(larkCliProfileUseTool(params));
      }
    });

    api.registerTool({
      name: "termind_lark_cli_config_bind",
      description: "Build and parse an OpenClaw-side existing bot binding command. Plan returns exactly one OpenClaw exec command, `lark-cli config bind --source openclaw --app-id <appId> --identity bot-only`. This binds OpenClaw Feishu/Lark channel credentials into the OpenClaw-side lark-cli workspace and never accepts app secrets.",
      parameters: larkCliCheckSchema,
      async execute(_callId, params) {
        return jsonContent(larkCliConfigBindTool(params));
      }
    });

    api.registerTool({
      name: "termind_lark_cli_config_init",
      description: "Report that Direct app_secret lark-cli config init is unsupported in the current OpenClaw Gateway because no secure stdin exec primitive is exposed.",
      parameters: larkCliCheckSchema,
      async execute(_callId, params) {
        return jsonContent(larkCliConfigInitTool(params));
      }
    });

    api.registerTool({
      name: "termind_lark_cli_auth_login",
      description: "Build and parse OpenClaw-side lark-cli auth login device-flow commands. Plan phase=start returns `lark-cli auth login --recommend --no-wait --json`; plan phase=complete returns `lark-cli auth login --device-code <code> --json`. This tool never executes local shell commands.",
      parameters: larkCliCheckSchema,
      async execute(_callId, params) {
        return jsonContent(larkCliAuthLoginTool(params));
      }
    });

    api.registerTool({
      name: "termind_lark_cli_discover",
      description: "Build and parse OpenClaw-side lark-cli target discovery checks for chats or users. This tool does not execute shell commands; use OpenClaw exec for returned commands, then call this tool with action=parse.",
      parameters: {
        type: "object",
        additionalProperties: true,
        properties: {
          action: { type: "string", enum: ["plan", "parse"] },
          kind: { type: "string", enum: ["chat", "user"] },
          sender: { type: "string", enum: ["bot", "user"] },
          query: { type: "string" },
          memberOpenID: { type: "string" },
          larkCliConfigDir: { type: "string" },
          configDir: { type: "string" },
          chatSearchOutput: { type: "string" },
          chatSearchError: { type: "string" },
          chatListOutput: { type: "string" },
          chatListError: { type: "string" },
          userSearchOutput: { type: "string" },
          userSearchError: { type: "string" }
        }
      },
      async execute(_callId, params) {
        return jsonContent(larkCliDiscoverTool(params));
      }
    });

    api.registerTool({
      name: "termind_report_template_build",
      description: "Build a Markdown incident report template from a redacted Termind failure event.",
      parameters: failureEventSchema,
      async execute(_callId, params) {
        const event = redactFailureEvent(normalizeFailureEvent(params, { requireFingerprint: false }));
        if (!event.fingerprint) {
          event.fingerprint = computeFingerprint(event).fingerprint;
        }
        return jsonContent({
          event,
          markdown: buildReportTemplate(event)
        });
      }
    });

    api.log?.info?.("registered safe Termind tools");
  }
});

function jsonContent(value) {
  return {
    content: [
      {
        type: "text",
        text: JSON.stringify(value, null, 2)
      }
    ]
  };
}
