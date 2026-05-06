---
name: termind-wiki-bootstrap
description: Analyze a GitHub project and bootstrap structured documentation (overview, architecture, dev guide, coding standards, deployment) into Feishu wiki.
---

# Termind Wiki Bootstrap

Use this skill when a user runs `termind doc init` and provides a GitHub repository URL.
The goal is to analyze the project and generate 5 structured documents in a Feishu wiki space.

## Flow

### Phase 1 — Understand the project

1. Fetch the repository's README, directory structure, and key config files via `gh` CLI or `WebFetch`.
   - `gh repo view <owner/repo> --json name,description,url,createdAt,updatedAt`
   - `gh api repos/<owner>/repo/readme --jq '.content'` (base64, decode it)
   - `gh api repos/<owner>/repo/git/trees/main?recursive=1 --jq '.tree[].path'` for directory structure
   - Try `gh api repos/<owner>/repo/contents/package.json`, `tsconfig.json`, `Makefile`, `docker-compose.yml`, etc.
2. If `gh` is unavailable, fall back to `WebFetch` on the GitHub repo page and raw file URLs.
3. Identify from the collected data:
   - Project name, description, purpose
   - Tech stack (languages, frameworks, key dependencies)
   - Directory/module layout
   - Build, test, run commands
   - Linting / formatting config (.eslintrc, .prettierrc, etc.)
   - Git branching / contribution patterns
   - Deployment artifacts (Dockerfile, CI configs, etc.)

### Phase 2 — Create or locate the wiki space

1. Call `feishu_wiki` with action `spaces` to list all accessible wiki spaces.
2. Search for an existing space whose name matches the project name (case-insensitive).
3. If found, record its `space_id` and skip space creation.
4. If NOT found, create a new wiki space via the Feishu OpenAPI:
   - Use `lark-openapi-explorer` to call `POST /open-apis/wiki/v2/spaces` with body `{ "name": "<project-name>" }`.
   - Record the returned `space_id`.
5. If space creation fails (e.g. permission), stop and tell the user: "无法创建知识空间，请检查飞书机器人是否已加入目标空间或手动创建空间后重试。"

### Phase 3 — Generate and write documents

For each of the 5 documents below, repeat this write sub-flow:

1. **Generate the Markdown content** based on the analysis from Phase 1. Write the document in Chinese by default (headings and structure in Chinese, code and technical terms in English). Use the format conventions:
   - Use `#` level-1 title for the document title
   - Use Mermaid diagrams (flowchart / graph TD / sequenceDiagram) where structure needs visualization
   - Use tables for config references, dependency lists, or command summaries
   - Wrap file paths, commands, and code in fenced code blocks

2. **Create the wiki node**: call `feishu_wiki` with action `create` — `space_id`, `title`, `obj_type: "docx"`. Record the returned `obj_token` and `node_token`.

3. **Write the content**: call `feishu_doc` with action `write` — `doc_token: <obj_token>`, `content: <markdown>`.

4. **Record the document link**: construct the URL as `https://<feishu-host>/wiki/<node_token>` and save it.

#### Document 1 — 项目介绍 (Project Overview)

Title: `<项目名> - 项目介绍`

Cover:
- 项目概述 (what it does, why it exists)
- 核心功能 (main features)
- 技术栈 (languages, frameworks, key dependencies in a table)
- 快速开始 (minimal setup steps distilled from README)

#### Document 2 — 项目架构 (Architecture)

Title: `<项目名> - 项目架构`

Cover:
- 整体架构图 (Mermaid diagram showing component relationships)
- 目录结构 (annotated directory tree with module responsibilities)
- 核心模块说明 (one paragraph per major module)
- 数据流 (Mermaid sequence diagram if applicable, or data-flow description)
- 关键设计决策 (any notable patterns or trade-offs visible in the code)

#### Document 3 — 本地开发指南 (Development Guide)

Title: `<项目名> - 本地开发指南`

Cover:
- 环境要求 (Node version, system dependencies, tools)
- 项目初始化 (`git clone ...`, `npm install`, env setup)
- 常用命令 (dev server, build, test, lint — as a table)
- 项目约定 (directory conventions, file naming, config)
- 常见问题 (if README or issues reveal setup pitfalls)

#### Document 4 — 编码规范 (Coding Standards)

Title: `<项目名> - 编码规范`

Cover:
- 命名规范 (files, variables, functions, types)
- 代码风格 (indentation, quotes, semicolons — inferred from Prettier/ESLint config)
- TypeScript 规范 (if applicable: strict mode, type-only imports, enum vs union)
- Git 提交规范 (commit message format, branch naming)
- Code Review 要点 (what to look for based on the project's conventions)

If the project lacks explicit style config, note that conventions are inferred from existing code and suggest adopting explicit tooling (ESLint, Prettier).

#### Document 5 — 部署文档 (Deployment Guide)

Title: `<项目名> - 部署文档`

Cover:
- 构建产物 (`npm run build` output, Docker image, static files)
- 部署流程 (step-by-step deployment steps)
- 环境变量 (list of env vars discovered from code/config, with descriptions)
- 基础设施依赖 (databases, caches, external services)
- CI/CD (if GitHub Actions / CI configs exist, describe the pipeline)

If the project has no deployment artifacts, write a brief note instead: "该项目暂无自动化部署配置。建议添加 CI/CD 流水线和容器化部署方案。"

### Phase 4 — Report completion

1. Collect all 5 document links.
2. Assemble the summary message:

```
项目文档已生成 ✅

知识空间：<space-name>
├── 📄 项目介绍 —— <link>
├── 🏗️ 项目架构 —— <link>
├── 💻 本地开发指南 —— <link>
├── 📐 编码规范 —— <link>
└── 🚀 部署文档 —— <link>
```

3. **Send the summary to the user via Feishu**: use the OpenClaw `message` tool to notify the user who initiated the request.
   - `channel`: `feishu`
   - `action`: `send`
   - `target`: the user's `open_id` found in the triggering message (e.g. `用户 open_id: ou_xxx`)
   - `content`: the summary text from step 2
   - If no `open_id` is found in the triggering message, output the summary to the terminal and note: "无法发送飞书通知：请提供用户 open_id。"

4. Also output the summary to the terminal for the CLI caller.

## Rules

- **No confirmation**: do NOT ask the user for permission before creating wiki spaces or documents. Proceed directly — this skill is invoked by a CLI tool and the user has already made the decision to generate documentation.
- **Do not skip documents**: all 5 documents must be created. If a document has little content, write what is available and note gaps.
- **Idempotent**: if a wiki node with the same title already exists under the space, update it (use `feishu_doc write`) rather than creating a duplicate. Check via `feishu_wiki nodes`.
- **Chinese by default**: document headings and explanatory text should be in Chinese. Code snippets, technical terms, and CLI commands remain in English.
- **Live analysis only**: do NOT fabricate project details. Every claim in the documents must be traceable to actual files or configs found in the repository.
- **Permission errors**: if any wiki or doc operation fails with a permission error, collect the error details, report which step failed, and suggest the user check bot membership in the wiki space.
- **Large repos**: if the directory tree exceeds 200 entries, sample strategically — focus on top-level directories and key config files.
