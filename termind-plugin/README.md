# Termind OpenClaw Plugin

Termind 是一个 OpenClaw **工具型插件**（Tool Plugin），为 OpenClaw Agent 提供
终端失败事件的纯数据转换工具。

## 核心原则

- **零副作用** — 所有工具不执行 shell 命令、不读取环境变量、不发送网络请求
- **纯数据管道** — 输入 JSON → 转换 → 输出 JSON
- **发送由技能编排** — 插件只构建数据，飞书卡片由技能指示 `lark-cli` 发送

## 架构

```
Termind CLI（终端工具）
    │  OpenClaw Gateway RPC (agent / agent.wait / sessions.get)
    ▼
OpenClaw Gateway
    │
    ├── Termind 插件（本插件）
    │     ┌─ termind_event_redact          — 规范化 + 脱敏
    │     ├─ termind_fingerprint_compute   — 指纹计算
    │     ├─ termind_failure_classify      — 严重级别分类
    │     ├─ termind_lark_card_build       — 构建飞书交互式卡片
    │     └─ termind_report_template_build — 构建事件报告模板
    │
    └── OpenClaw Agent
           ├─ 加载技能 (termind-lark-alert / knowledge-rag / incident-report)
           └─ 调用 lark-cli 发送飞书卡片
```

## 目录结构

```text
termind-pulgin/
  index.ts                    # 插件入口 definePluginEntry
  openclaw.plugin.json        # 插件清单
  package.json                # npm 元数据
  tsconfig.json               # TypeScript 配置
  README.md                   # 本文件
  COMPARISON.md               # 新旧版本对比
  INSTALL.md                  # 安装指南
  src/
    schemas/
      failure-event.ts        # TypeBox 类型定义（工具参数 schema）
    lib/
      redact.ts               # 脱敏工具
      fingerprint.ts          # 指纹计算
      classify.ts             # 严重级别分类
      card.ts                 # 飞书卡片构建
      report.ts               # 报告模板构建
  skills/
    termind-lark-alert/       # 飞书告警技能
    termind-knowledge-rag/    # 知识检索技能
    termind-incident-report/  # 事件报告技能
  test/
    redact.test.ts            # 脱敏测试 (11 个用例)
    fingerprint.test.ts       # 指纹测试 (5 个用例)
    classify.test.ts          # 分类测试 (7 个用例)
    card.test.ts              # 卡片测试 (8 个用例)
    report.test.ts            # 报告测试 (5 个用例)
```

## 配置

用户在 `openclaw.json` 中启用本插件的工具：

```json5
{
  plugins: {
    entries: {
      termind: { enabled: true }
    }
  },
  tools: {
    alsoAllow: [
      "termind_event_redact",
      "termind_fingerprint_compute",
      "termind_failure_classify",
      "termind_lark_card_build",
      "termind_report_template_build"
    ]
  }
}
```

所有 5 个工具注册为 `optional: true`，需要用户在 `tools.alsoAllow` 中显式启用。

## 工具清单

| 工具 | 功能 | 原理 |
| ---- | ---- | ---- |
| `termind_event_redact` | 规范化 + 脱敏 | 3 组正则清除 Bearer / key=value / PEM 密钥 |
| `termind_fingerprint_compute` | 指纹计算 | 5 维 basis → SHA256 → 8 位 hex |
| `termind_failure_classify` | 严重级别分类 | 4 条规则：频率/崩溃/主分支/低证据 |
| `termind_lark_card_build` | 飞书卡片构建 | header + 6 elements + action 按钮 |
| `termind_report_template_build` | 报告模板 | 6 段 Markdown（元数据/快照/根因/方案/预防/历史） |

## 开发

```bash
# 安装依赖
npm install

# 类型检查
npx tsc --noEmit

# 运行测试（36 个用例）
npm test
```

## 安装

详见 [INSTALL.md](INSTALL.md)

## 新旧版本对比

详见 [COMPARISON.md](COMPARISON.md)

## 发布

```bash
clawhub package publish termind/termind --dry-run
clawhub package publish termind/termind
openclaw plugins install clawhub:termind
```
