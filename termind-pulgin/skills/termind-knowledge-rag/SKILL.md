---
name: termind-knowledge-rag
description: Search available OpenClaw knowledge sources for Termind terminal failure context with graceful fallback.
---

# Termind Knowledge RAG

Use this skill to find internal knowledge related to a Termind failure event.

## Capability Ladder

1. If OpenClaw memory search is available, search by fingerprint, summary,
   command family, project, and top stack frame.
2. If wiki tools are available, search incident reports and runbooks.
3. If Feishu / Lark docs / wiki tools are available, search internal docs.
4. If no knowledge capability is available, return a graceful
   `missing_capability` result and continue with local diagnosis only.

## Query Hints

Build queries from:

- fingerprint
- normalized error kind
- top stack frame
- command family
- project / service name
- important tail lines

Do not fetch large documents eagerly. Prefer index-first search and only fetch
the top relevant candidates.
