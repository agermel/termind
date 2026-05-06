---
name: termind-incident-recurrence
description: Append a recurrence row to a prior Termind incident report and bump occurrences via termind_incident_registry_upsert.
---

# Termind Incident Recurrence

Use this skill when `termind-incident-registry` returns
`branch: "recurrence"` or `branch: "escalation_candidate"` and a prior
report exists (`record.reportUrl` is non-empty).

This skill is the recurring-case sibling of `termind-incident-report`; the
two together cover the report lifecycle in step 11.

## Inputs

- The merged failure event (already enriched with `registryBranch`,
  `occurrences`, `firstSeen`, `lastSeen`, `reportUrl`, `owner`).
- The prior `record` returned by `termind_incident_registry_query`.

## Flow

1. Append a recurrence row to the prior report (best-effort):
   1. If a Lark/Feishu doc append capability is available (`docs +update`
      under a user identity that owns the report), append a row to the
      `## 历史共现` table:
      ```
      | <ISO8601 now> | <event.user> | <event.environment> | <event.gitCommit> |
      ```
   2. If no append capability is available, skip silently. The card will
      still link to `record.reportUrl`; the registry write in step 2 keeps
      occurrence counts honest.
2. Plan the registry write-back:
   ```
   termind_incident_registry_upsert {
     action: "plan",
     fingerprint: event.fingerprint,
     branchKind: event.branchKind,
     reportUrl: event.reportUrl,
     user: event.user,
     branch: event.branch,
     gitCommit: event.gitCommit,
     environment: event.environment,
     occurredAt: <ISO8601 now>,
     owner: event.owner,
     priorRaw: <raw record string returned by capability earlier>
   }
   ```
3. Execute the first available capability hint:
   - `memory.set` with the returned `key` / `value`.
   - Fall back to `kv.set` with `namespace` / `key` / `value`.
   - If neither is available, pass `missingCapabilityFallback` directly to
     `parse`. Do not retry forever; one missing-capability path is fine.
4. Parse:
   ```
   termind_incident_registry_upsert {
     action: "parse",
     fingerprint: event.fingerprint,
     ack: <capability ack object or stdout>,
     exitCode: <number>,
     stderr: "<…>",
     record: <plan.record>
   }
   ```
5. Return the parse result. Downstream (`termind-lark-alert`) uses it only
   for logging; delivery success was already confirmed before this skill
   ran.

## Rules

- This skill must run **after** `lark-cli` reported a successful send. A
  failed send must not increment `occurrences`.
- Never create a new report doc here. Doc creation belongs to the
  `termind-incident-report` skill (step 11 new-case path).
- Never silently degrade `escalation_candidate` to `recurrence` because the
  doc append failed; the card and registry decisions are independent of
  the doc-append outcome.
- Never write secrets, env dumps, or credentials into the registry record
  or the appended history row.
