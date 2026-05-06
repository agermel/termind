import type { FailureEvent } from "../schemas/failure-event.js";

/** Build a Markdown incident report template from a redacted failure event.
 *
 *  Sections:
 *  1. 元数据（Agent 自动填充）
 *  2. 现场快照（Agent 自动填充）— 命令 / 堆栈 / 输出
 *  3. 根本原因（待负责人填写）
 *  4. 解决方案（待负责人填写）
 *  5. 预防措施（待负责人填写）
 *  6. 历史共现（Agent 持续更新） */
export function buildReportTemplate(event: FailureEvent): string {
  const fingerprint = event.fingerprint ?? "unknown";
  const now = new Date().toISOString();
  const stack =
    event.stackTop?.length
      ? event.stackTop.map((line) => `  ${line}`).join("\n")
      : "  (none captured)";
  const tail = event.tail || "(none captured)";

  return [
    `# [错误报告] ${fingerprint}`,
    "",
    "## 元数据（Agent 自动填充）",
    `- 指纹: ${fingerprint}`,
    `- 首次发生: ${now}`,
    `- 发生次数: ${event.occurrences || 1}`,
    `- 影响面: ${event.affectedUsers || 1} 人 / 1 环境`,
    `- 相关 commit: ${event.gitCommit || "(unknown)"}`,
    `- 分支: ${event.branch || "(unknown)"}`,
    `- 服务/来源: ${event.project || "(unknown)"}`,
    "",
    "## 现场快照（Agent 自动填充）",
    "- 触发命令:",
    "",
    "```bash",
    event.command,
    "```",
    "",
    "- 堆栈 Top:",
    "",
    "```text",
    stack,
    "```",
    "",
    "- 终端最后输出:",
    "",
    "```text",
    tail,
    "```",
    "",
    "## 根本原因（待负责人填写）",
    "> ...",
    "",
    "## 解决方案（待负责人填写）",
    "> ...",
    "",
    "## 预防措施（待负责人填写）",
    "> ...",
    "",
    "## 历史共现（Agent 持续更新）",
    "| 时间 | 发生者 | 环境 | commit |",
    "|------|--------|------|--------|",
    `| ${now} | ${event.user || "(unknown)"} | ${event.environment || "(unknown)"} | ${event.gitCommit || "(unknown)"} |`,
    "",
  ].join("\n");
}
