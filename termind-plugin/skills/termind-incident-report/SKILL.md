---
name: termind-incident-report
description: Build or update Termind incident report drafts from terminal failure events.
---

# Termind Incident Report

Use this skill when a failure is new, serious, repeated, or should be preserved
as internal knowledge.

## Flow

1. Redact the event with `termind_event_redact`.
2. Compute a fingerprint with `termind_fingerprint_compute`.
3. Build a Markdown report with `termind_report_template_build`.
4. If a document creation capability exists, create or update a report document.
5. If no document capability exists, return the Markdown draft to the user.

## Rules

- Never create duplicate reports for the same fingerprint when a prior report is
  found.
- Preserve the auto-filled metadata and leave root cause, fix, and prevention
  sections for the owner to complete.
- Do not upload full terminal logs unless the user or workspace policy allows it.
