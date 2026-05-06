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
3. If Feishu/Lark docs/wiki tools are available, **delegate to skill
   `termind-lark-knowledge-search`**. That skill plans
   `lark-cli docs +search --as user` (the Search v2 API is user-only) and
   parses the result into `hits[]`.
4. If no knowledge capability is available, return a graceful
   `missing_capability` result and continue with local diagnosis only.

## Query Hints

Use `termind_lark_knowledge_search { action: "queries", event }` to derive
queries automatically. Internally it ranks queries by specificity:

1. project + first-sentence-of-summary
2. first-sentence-of-summary alone
3. top stack frame (path + symbol)
4. project + command family (e.g. `be-grade go test`)
5. command family alone
6. project + error keyword (e.g. `panic`, `cannot find`, `timeout`)
7. error keyword alone
8. raw summary (truncated)

Do not fetch large documents eagerly. Prefer index-first search and only fetch
the top 1-3 relevant candidates if a snippet is missing.

## Output

Return `{ hits: KnowledgeHit[], missingCapability?, errors[] }`. The caller
(`termind-lark-alert`) merges `hits` onto `event.knowledgeHits` so the card
and report builders can render the related-knowledge block.

## Rules

- Never plan with `sender: "bot"` for `docs +search` — the underlying
  command is user-only and lark-cli rejects bot identities.
- One Lark search per alert. Do not loop until hits appear; the
  delegated skill stops after at most 3 queries.
- Never include redacted secrets in a query.
