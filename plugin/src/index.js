import { definePluginEntry } from "openclaw/plugin-sdk/plugin-entry";

import { buildIncidentCard } from "./lib/card.js";
import { classifyFailure } from "./lib/classify.js";
import { computeFingerprint } from "./lib/fingerprint.js";
import { incidentRegistryTool, incidentRegistryUpsertTool } from "./lib/registry.js";
import { larkCliDiscoverTool } from "./lib/lark-cli-discover.js";
import { larkKnowledgeSearchTool } from "./lib/lark-knowledge-search.js";
import { ownerResolveTool } from "./lib/owner-resolve.js";
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
    // CLI 端通过 forwarding 字段把 lark-cli 多 identity 路由表传过来,
    // buildLarkCliCommands 优先消费这两个字段, normalize 不能把它们丢掉.
    larkForwardingIdentities: {
      type: "object",
      additionalProperties: {
        type: "object",
        additionalProperties: true,
        properties: {
          id: { type: "string" },
          kind: { type: "string", enum: ["bot", "user"] },
          label: { type: "string" },
          appId: { type: "string" },
          userOpenId: { type: "string" },
          profile: { type: "string" },
          larkCliConfigDir: { type: "string" },
          enabled: { type: "boolean" },
          source: { type: "string" },
          slot: { type: "string" }
        }
      }
    },
    larkForwardingRoutes: {
      type: "array",
      items: {
        type: "object",
        additionalProperties: true,
        properties: {
          identityId: { type: "string" },
          enabled: { type: "boolean" },
          target: {
            type: "object",
            additionalProperties: true,
            properties: {
              type: { type: "string", enum: ["chat", "user", "bot"] },
              id: { type: "string" },
              label: { type: "string" }
            }
          }
        }
      }
    },
    stackTop: { type: "array", items: { type: "string" } },
    reportUrl: { type: "string" },
    occurrences: { type: "number" },
    affectedUsers: { type: "number" },
    branchKind: { type: "string" },
    // Fields produced by termind_incident_registry_query and merged back
    // into the failure event before classify/card-build. The card reads
    // these directly to render history/escalation blocks.
    registryBranch: {
      type: "string",
      enum: ["new_case", "recurrence", "escalation_candidate"]
    },
    windowOccurrences: { type: "number" },
    windowMinutes: { type: "number" },
    firstSeen: { type: "string" },
    lastSeen: { type: "string" },
    // Owner produced by termind_owner_resolve (git author -> Lark open_id).
    // Card uses { openId, label, confidence } to decide whether to @-mention.
    owner: {
      type: "object",
      additionalProperties: true,
      properties: {
        kind: { type: "string" },
        openId: { type: "string" },
        label: { type: "string" },
        email: { type: "string" },
        source: { type: "string" },
        confidence: { type: "string" }
      }
    },
    // Knowledge hits produced by termind_lark_knowledge_search. Used by card
    // and report builders to render reference links.
    knowledgeHits: {
      type: "array",
      items: {
        type: "object",
        additionalProperties: true,
        properties: {
          token: { type: "string" },
          type: { type: "string" },
          title: { type: "string" },
          url: { type: "string" },
          ownerOpenId: { type: "string" },
          ownerName: { type: "string" },
          lastModified: { type: "string" },
          snippet: { type: "string" },
          score: { type: "number" }
        }
      }
    }
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
      name: "termind_incident_registry_query",
      description: "Look up a Termind failure fingerprint in the incident registry. Plan returns capability hints (memory.get / kv.get) and a missing-capability fallback; parse turns the raw record into branch (new_case | recurrence | escalation_candidate) plus windowed occurrence and affectedUsers counts. This tool does not access memory, network, shell, or files.",
      parameters: {
        type: "object",
        additionalProperties: true,
        properties: {
          action: { type: "string", enum: ["plan", "parse"] },
          fingerprint: { type: "string" },
          windowMinutes: { type: "number" },
          now: { type: "string", description: "ISO8601 timestamp; defaults to current time when omitted." },
          branchKind: { type: "string", enum: ["main", "release", "feature", "other", ""] },
          raw: {
            description: "Raw record returned from memory.get/kv.get. Accepts a JSON string or a pre-decoded object; null means lookup miss.",
            anyOf: [
              { type: "string" },
              { type: "object" },
              { type: "null" }
            ]
          },
          missingCapability: { type: "boolean" }
        }
      },
      async execute(_callId, params) {
        return jsonContent(incidentRegistryTool(params));
      }
    });

    api.registerTool({
      name: "termind_incident_registry_upsert",
      description: "Plan a registry write-back after a Termind alert is delivered. Plan computes the next record (occurrences+1, firstSeen preserved, lastSeen=now, events appended, owner/reportUrl merged) and emits memory.set/kv.set capability hints; parse acks the capability invocation result. This tool does not access memory, network, shell, or files.",
      parameters: {
        type: "object",
        additionalProperties: true,
        properties: {
          action: { type: "string", enum: ["plan", "parse"] },
          fingerprint: { type: "string" },
          branchKind: { type: "string" },
          reportUrl: { type: "string" },
          user: { type: "string" },
          branch: { type: "string" },
          gitCommit: { type: "string" },
          environment: { type: "string" },
          occurredAt: { type: "string" },
          windowMinutes: { type: "number" },
          status: { type: "string" },
          owner: {
            type: "object",
            additionalProperties: true
          },
          event: {
            type: "object",
            additionalProperties: true,
            properties: {
              user: { type: "string" },
              branch: { type: "string" },
              commit: { type: "string" },
              environment: { type: "string" },
              timestamp: { type: "string" }
            }
          },
          priorRaw: {
            description: "Prior registry record returned from memory.get/kv.get (string or pre-decoded object). null means new case.",
            anyOf: [
              { type: "string" },
              { type: "object" },
              { type: "null" }
            ]
          },
          // parse-time fields
          ack: {},
          result: {},
          output: { type: "string" },
          stdout: { type: "string" },
          stderr: { type: "string" },
          exitCode: { type: "number" },
          written: { type: "boolean" },
          record: {}
        }
      },
      async execute(_callId, params) {
        return jsonContent(incidentRegistryUpsertTool(params));
      }
    });

    api.registerTool({
      name: "termind_lark_knowledge_search",
      description: "Plan/parse Lark/Feishu doc & wiki knowledge search via lark-cli docs +search. lark-cli docs +search is **user-only** (--as bot is rejected by lark-cli); plan refuses sender=bot and returns missingCapability=user_oauth so the orchestrator can degrade gracefully. action=queries derives ordered candidate queries from a failure event. action=plan emits one lark-cli command. action=parse extracts {token,type,title,url,ownerOpenId,snippet,score,lastModified}. This tool does not execute shell commands.",
      parameters: {
        type: "object",
        additionalProperties: true,
        properties: {
          action: { type: "string", enum: ["queries", "plan", "parse"] },
          // queries
          event: failureEventSchema,
          // plan
          sender: { type: "string", enum: ["user", "bot"] },
          query: { type: "string" },
          pageSize: { type: "number" },
          pageToken: { type: "string" },
          profile: { type: "string" },
          larkCliConfigDir: { type: "string" },
          configDir: { type: "string" },
          filter: {
            anyOf: [
              { type: "string" },
              { type: "object" }
            ]
          },
          // parse
          stdout: { type: "string" },
          stderr: { type: "string" },
          output: { type: "string" },
          error: { type: "string" },
          exitCode: { type: "number" }
        }
      },
      async execute(_callId, params) {
        return jsonContent(larkKnowledgeSearchTool(params));
      }
    });

    api.registerTool({
      name: "termind_owner_resolve",
      description: "Plan/parse a git author -> Lark open_id resolution via lark-cli contact +search-user. +search-user is user-only; plan refuses sender=bot and returns missingCapability=user_oauth + a label-only owner fallback. parse picks the highest-confidence candidate (high=email match, medium=name match, label_only=fallback). This tool does not execute shell commands.",
      parameters: {
        type: "object",
        additionalProperties: true,
        properties: {
          action: { type: "string", enum: ["plan", "parse"] },
          sender: { type: "string", enum: ["user", "bot"] },
          email: { type: "string" },
          name: { type: "string" },
          authorEmail: { type: "string" },
          authorName: { type: "string" },
          queryKind: { type: "string", enum: ["email", "name"] },
          profile: { type: "string" },
          larkCliConfigDir: { type: "string" },
          configDir: { type: "string" },
          stdout: { type: "string" },
          stderr: { type: "string" },
          output: { type: "string" },
          error: { type: "string" },
          exitCode: { type: "number" }
        }
      },
      async execute(_callId, params) {
        return jsonContent(ownerResolveTool(params));
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
      description: "Build a manual OpenClaw-side lark-cli bot config init command using --app-secret-stdin. This tool never receives app_secret and does not execute shell commands.",
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
          profile: { type: "string" },
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
