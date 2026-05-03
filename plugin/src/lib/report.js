export function buildReportTemplate(event) {
  const stack = event.stackTop?.length ? event.stackTop.map(line => `  ${line}`).join("\n") : "  (none captured)";
  const tail = event.tail || "(none captured)";
  return `# [错误报告] ${event.fingerprint}

## 元数据（Agent 自动填充）
- 指纹: ${event.fingerprint}
- 首次发生: ${new Date().toISOString()}
- 发生次数: ${event.occurrences || 1}
- 影响面: ${event.affectedUsers || 1} 人 / 1 环境
- 相关 commit: ${event.gitCommit || "(unknown)"}
- 分支: ${event.branch || "(unknown)"}
- 服务/来源: ${event.project || "(unknown)"}

## 现场快照（Agent 自动填充）
- 触发命令:

\`\`\`bash
${event.command}
\`\`\`

- 堆栈 Top:

\`\`\`text
${stack}
\`\`\`

- 终端最后输出:

\`\`\`text
${tail}
\`\`\`

## 根本原因（待负责人填写）
> ...

## 解决方案（待负责人填写）
> ...

## 预防措施（待负责人填写）
> ...

## 历史共现（Agent 持续更新）
| 时间 | 发生者 | 环境 | commit |
|------|--------|------|--------|
| ${new Date().toISOString()} | ${event.user || "(unknown)"} | ${event.environment || "(unknown)"} | ${event.gitCommit || "(unknown)"} |
`;
}
