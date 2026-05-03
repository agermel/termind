import { definePluginEntry } from "openclaw/plugin-sdk/plugin-entry";

import { buildIncidentCard } from "./lib/card.js";
import { classifyFailure } from "./lib/classify.js";
import { computeFingerprint } from "./lib/fingerprint.js";
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
      description: "Target Feishu/Lark chat id for OpenClaw message tool delivery."
    },
    stackTop: { type: "array", items: { type: "string" } },
    reportUrl: { type: "string" },
    occurrences: { type: "number" },
    affectedUsers: { type: "number" },
    branchKind: { type: "string" }
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
      description: "Build Lark/Feishu interactive card JSON from a redacted Termind failure event. This tool does not send messages; use OpenClaw's message tool to send cards.",
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
            preferred: "openclaw-message-tool",
            tool: "message",
            action: "send",
            channel: "feishu",
            target: event.larkChatId || "$TERMIND_LARK_CHAT_ID",
            cardParam: "card"
          }
        });
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
