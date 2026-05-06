---
name: termind-incident-registry
description: Look up a Termind failure fingerprint in the incident registry and decide which alert branch (new_case / recurrence / escalation_candidate) downstream skills should take.
---

# Termind Incident Registry

Use this skill after a fingerprint has been computed (step 5) and before the
alert card is built. It is a stateless query layer: it never writes to the
registry — writes happen in the report-lifecycle skill after delivery succeeds.

## When to use

- Called by `termind-lark-alert` between `termind_fingerprint_compute` and
  `termind_failure_classify`.
- May also be called by `termind-incident-report` to enrich a report draft with
  prior history, or by other diagnosis flows that need recurrence context.

## Flow

1. Plan the lookup:
   ```
   termind_incident_registry_query { action: "plan", fingerprint, windowMinutes? }
   ```
   Returns `{ key, capabilityHints[], missingCapabilityFallback, parse }`.

2. Execute the lookup against any available capability:
   - Prefer `memory.get` with the returned `key`.
   - If memory is unavailable but a generic key/value capability exists, use
     `kv.get` with the namespace/key returned in `capabilityHints[1]`.
   - If no capability is available, do **not** fabricate a lookup. Pass
     `missingCapabilityFallback` directly into `parse`. This is a valid,
     graceful degradation.

3. Parse:
   ```
   termind_incident_registry_query {
     action: "parse",
     fingerprint,
     raw: <string from capability or null>,
     windowMinutes?,           // default 120
     now?,                     // ISO8601, default current time
     branchKind?,              // from event.branchKind, helps escalation logic
     missingCapability?        // true when the fallback path was used
   }
   ```
   Returns `{ found, branch, record, reasons, decoded, missingCapability }`.

4. Merge `record.occurrences`, `record.affectedUsers.length`, `record.reportUrl`,
   `record.firstSeen`, `record.lastSeen` into the failure event before invoking
   `termind_failure_classify` and `termind_lark_card_build`. Classify already
   reads `event.occurrences` and `event.affectedUsers` to upgrade severity.

## Branch semantics

- `new_case` — fingerprint is unseen, the registry has no capability, the JSON
  payload was malformed, or the previous record was already `resolved` /
  `false_positive`. Treat as a fresh incident; the report lifecycle should
  create a new template and seed the registry on successful delivery.
- `recurrence` — fingerprint exists, status is `open`, but the recent activity
  is below the escalation thresholds. Card should show "🔁 历史同款" with
  `record.occurrences` and a link to `record.reportUrl`.
- `escalation_candidate` — fingerprint exists and at least one of:
  - `branchKind === "main"` and the window contains ≥1 recurrence,
  - window occurrences ≥ 5,
  - distinct affected users ≥ 3.
  Card should be promoted to incident severity and routed to the team channel.

## Rules

- This skill must not write to the registry. Writes are step 13 work and live
  in a future `termind_incident_registry_upsert_plan` tool.
- Never invent a `reportUrl`; if `record.reportUrl` is empty, leave the card's
  history link empty rather than linking to a nonexistent doc.
- The decoded record is best-effort. When `decoded === false`, downstream skills
  should still send the alert as `new_case` so users are not silenced by a
  registry deserialization bug.
- Time math is windowed: `windowOccurrences` and `affectedUsers` count only
  events whose timestamp falls within `windowMinutes` of `now`.
