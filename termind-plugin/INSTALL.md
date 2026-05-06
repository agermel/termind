# Termind 插件安装指南

## 前置条件

- OpenClaw >= 2026.4.25
- Node.js >= 22
- `lark-cli` 已安装并登录（用于飞书卡片发送）

## 安装步骤

### 1. 确认 OpenClaw Gateway 运行

```bash
openclaw gateway status
```

如果未运行：

```bash
openclaw gateway start
```

### 2. 安装插件

```bash
# 从本地路径安装
openclaw plugins install /path/to/termind-pulgin --force

# 如果已安装旧版，先卸载
openclaw plugins uninstall termind
openclaw plugins install /path/to/termind-pulgin
```

### 3. 启用插件

```bash
openclaw plugins enable termind
```

### 4. 配置工具白名单

```bash
openclaw config set tools.alsoAllow '[
  "termind_event_redact",
  "termind_fingerprint_compute",
  "termind_failure_classify",
  "termind_lark_card_build",
  "termind_report_template_build"
]' --strict-json
```

### 5. 重启 Gateway

```bash
openclaw gateway restart
```

### 6. 验证安装

```bash
# 检查插件状态
openclaw plugins inspect termind

# 确认工具已注册（重启后日志中应有此行）
openclaw gateway logs --lines 50 2>&1 | grep "registered 5 safe Termind tools"
```

预期输出：

```
registered 5 safe Termind tools (all optional): event_redact, fingerprint_compute, failure_classify, lark_card_build, report_template_build
```

## 插件工具清单

| 工具名 | 功能 | 说明 |
|--------|------|------|
| `termind_event_redact` | 规范化 + 脱敏 | 清除密钥/token/私钥，截断超长字段 |
| `termind_fingerprint_compute` | 指纹计算 | SHA256(basis) 前 8 位 hex，用于去重关联 |
| `termind_failure_classify` | 严重级别分类 | 输出 info/warning/incident + 路由策略 |
| `termind_lark_card_build` | 飞书卡片构建 | 构建交互式卡片 JSON（不发送） |
| `termind_report_template_build` | 报告模板 | 生成 Markdown 事件报告模板 |

## 技能清单

| 技能 | 用途 |
|------|------|
| `termind-lark-alert` | 编排卡片发送流程（脱敏→指纹→分类→卡片→lark-cli 发送） |
| `termind-knowledge-rag` | 知识检索（按指纹/摘要/堆栈搜索历史） |
| `termind-incident-report` | 事件报告生成（脱敏→指纹→报告模板） |

## 测试

### 单元测试

```bash
cd /path/to/termind-pulgin
npm install
npm test
```

### 集成测试（模拟 CLI 诊断路径）

```bash
openclaw agent --local --agent main \
  --session-id termind-test-diagnose \
  --message '你是 termind 的 shell 错误诊断助手...' \
  --timeout 60
```

### 集成测试（模拟 CLI 飞书告警路径）

```bash
openclaw agent --local --agent main \
  --session-id termind-test-alert \
  --message 'Use the termind-lark-alert skill.

```json
{
  "summary": "panic: runtime error",
  "command": "go run ./cmd/server",
  "severity": "warning",
  "project": "test",
  "larkChatId": "oc_xxx",
  "stackTop": ["main.go:42 main()"],
  "tail": "panic: runtime error"
}
```' \
  --timeout 120
```

## 卸载

```bash
openclaw plugins uninstall termind
openclaw gateway restart
```

## 故障排查

| 问题 | 解决 |
|------|------|
| 插件未加载 | 检查 `openclaw plugins list` 中 termind 是否为 enabled |
| 工具不可用 | 检查 `openclaw config get tools` 确认 alsoAllow 含 termind 工具 |
| 飞书卡片未发送 | 检查 `lark-cli` 是否已登录，chatId 是否正确 |
| 文件权限问题（WSL） | `chmod -R 755 /path/to/plugin` |
| `runtime extension entry not found` | 本地源码安装不应声明 `runtimeExtensions`，移除该字段 |
