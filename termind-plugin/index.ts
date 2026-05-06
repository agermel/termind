import { definePluginEntry } from "openclaw/plugin-sdk/plugin-entry";

import { buildIncidentCard } from "./src/lib/card.js";
import { classifyFailure } from "./src/lib/classify.js";
import { computeFingerprint } from "./src/lib/fingerprint.js";
import { buildReportTemplate } from "./src/lib/report.js";
import {
  normalizeFailureEvent,
  redactFailureEvent,
} from "./src/lib/redact.js";
import { FailureEventSchema } from "./src/schemas/failure-event.js";

export default definePluginEntry({
  id: "termind",
  name: "Termind",
  description:
    "Safe Termind terminal intelligence — pure data-transform tools for OpenClaw orchestration.",

  register(api) {
    // ── termind_event_redact ──────────────────────────────────────
    api.registerTool(
      {
        name: "termind_event_redact",
        description:
          "Normalize and redact a Termind terminal failure event. " +
          "Strips bearer tokens, passwords, API keys, private keys, " +
          "and other secrets. Does not access network, shell, files, " +
          "or environment variables.",
        parameters: FailureEventSchema,
        async execute(_callId, params) {
          try {
            const event = redactFailureEvent(
              normalizeFailureEvent(params as Record<string, unknown>),
            );
            return jsonLine({ event });
          } catch (err) {
            return toolError(err);
          }
        },
      },
      { optional: true },
    );

    // ── termind_fingerprint_compute ───────────────────────────────
    api.registerTool(
      {
        name: "termind_fingerprint_compute",
        description:
          "Compute a stable deterministic fingerprint for a normalized " +
          "Termind failure event. Used for deduplication and incident " +
          "correlation across users and occurrences.",
        parameters: FailureEventSchema,
        async execute(_callId, params) {
          try {
            const event = redactFailureEvent(
              normalizeFailureEvent(params as Record<string, unknown>),
            );
            return jsonLine(computeFingerprint(event));
          } catch (err) {
            return toolError(err);
          }
        },
      },
      { optional: true },
    );

    // ── termind_failure_classify ──────────────────────────────────
    api.registerTool(
      {
        name: "termind_failure_classify",
        description:
          "Classify a Termind failure event severity and routing hints. " +
          "Returns severity (info/warning/incident), recommended route, " +
          "and whether a report or knowledge search is warranted. " +
          "No side effects.",
        parameters: FailureEventSchema,
        async execute(_callId, params) {
          try {
            const event = redactFailureEvent(
              normalizeFailureEvent(params as Record<string, unknown>),
            );
            if (!event.fingerprint) {
              event.fingerprint = computeFingerprint(event).fingerprint;
            }
            return jsonLine(classifyFailure(event));
          } catch (err) {
            return toolError(err);
          }
        },
      },
      { optional: true },
    );

    // ── termind_lark_card_build ───────────────────────────────────
    api.registerTool(
      {
        name: "termind_lark_card_build",
        description:
          "Build Lark / Feishu interactive card JSON from a redacted " +
          "Termind failure event. This tool does NOT send messages; " +
          "use OpenClaw's built-in `message` tool (action=send, " +
          "channel=feishu) to deliver the returned card.",
        parameters: FailureEventSchema,
        async execute(_callId, params) {
          try {
            const event = redactFailureEvent(
              normalizeFailureEvent(params as Record<string, unknown>),
            );
            if (!event.fingerprint) {
              event.fingerprint = computeFingerprint(event).fingerprint;
            }
            const classification = classifyFailure(event);
            event.severity = classification.severity;
            const card = buildIncidentCard(event);
            return jsonLine({
              event,
              classification,
              card,
              sendHint: {
                preferred: "lark-cli",
                command: "lark-cli message send",
                flag_channel: "--channel feishu",
                flag_target: `--target ${event.larkChatId ?? "$TERMIND_LARK_CHAT_ID"}`,
                flag_card: "--card <card_json>",
              },
            });
          } catch (err) {
            return toolError(err);
          }
        },
      },
      { optional: true },
    );

    // ── termind_report_template_build ─────────────────────────────
    api.registerTool(
      {
        name: "termind_report_template_build",
        description:
          "Build a Markdown incident report template from a redacted " +
          "Termind failure event. The template includes auto-filled " +
          "metadata and live-snapshot sections, with placeholders for " +
          "root cause, fix, and prevention to be completed by the owner.",
        parameters: FailureEventSchema,
        async execute(_callId, params) {
          try {
            const event = redactFailureEvent(
              normalizeFailureEvent(params as Record<string, unknown>),
            );
            if (!event.fingerprint) {
              event.fingerprint = computeFingerprint(event).fingerprint;
            }
            const markdown = buildReportTemplate(event);
            return jsonLine({ event, markdown });
          } catch (err) {
            return toolError(err);
          }
        },
      },
      { optional: true },
    );

    api.logger.info(
      "registered 5 safe Termind tools (all optional): " +
        "event_redact, fingerprint_compute, failure_classify, " +
        "lark_card_build, report_template_build",
    );
  },
});

// ── return-value helpers ─────────────────────────────────────────────

function jsonLine(value: unknown) {
  return {
    content: [{ type: "text" as const, text: JSON.stringify(value, null, 2) }],
  };
}

function toolError(err: unknown) {
  const message = err instanceof Error ? err.message : String(err);
  return {
    content: [
      {
        type: "text" as const,
        text: JSON.stringify({ error: message }, null, 2),
      },
    ],
  };
}
