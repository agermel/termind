import type { FailureEvent } from "../schemas/failure-event.js";

/** Severity → header color + title prefix. */
const severityMeta: Record<
  string,
  { tag: "green" | "orange" | "red"; title: string }
> = {
  info: { tag: "green", title: "历史同款" },
  warning: { tag: "orange", title: "新错误已立案" },
  incident: { tag: "red", title: "指纹异常扩散" },
};

/** Build a Lark / Feishu interactive card JSON payload from a redacted
 *  Termind failure event.
 *
 *  Card layout:
 *  ┌─ header (color tag + title + fingerprint)
 *  ├─ markdown: 报错摘要
 *  ├─ text: 触发命令
 *  ├─ div.fields: 指纹 | 服务/来源 | Git | 环境   (2-col)
 *  ├─ text: 堆栈 Top 3
 *  ├─ text: 终端尾部输出 (≤ 1200 chars)
 *  └─ action: [打开报告] [标记误报] */
export function buildIncidentCard(event: FailureEvent): Record<string, unknown> {
  const meta = severityMeta[event.severity ?? "warning"] ?? severityMeta.warning;

  const fields: string[][] = [
    ["指纹", event.fingerprint ?? ""],
    ["服务/来源", sourceLine(event)],
    ["Git", gitLine(event)],
    ["环境", event.environment ?? ""],
  ].filter(([, v]) => Boolean(v));

  const elements: unknown[] = [
    markdown(`**报错摘要:** ${event.summary}`),
    textBlock("触发命令", event.command),
    fields.length > 0
      ? {
          tag: "div",
          fields: fields.map(([label, value]) => ({
            is_short: true,
            text: { tag: "lark_md", content: `**${label}:** ${value}` },
          })),
        }
      : null,
    stackBlock(event.stackTop ?? []),
    tailBlock(event.tail ?? ""),
    actionBlock(event),
  ].filter(Boolean);

  return {
    config: { wide_screen_mode: true },
    header: {
      template: meta.tag,
      title: {
        tag: "plain_text",
        content: `${meta.title} · ${event.fingerprint ?? ""}`,
      },
    },
    elements,
  };
}

// ── element builders ──────────────────────────────────────────────────

function markdown(content: string) {
  return { tag: "div", text: { tag: "lark_md", content } };
}

function textBlock(title: string, content: string) {
  const value = String(content || "").trim();
  if (!value) return null;
  return {
    tag: "div",
    text: { tag: "plain_text", content: `${title}\n${value}` },
  };
}

function sourceLine(event: FailureEvent): string {
  return [event.project, event.user].filter(Boolean).join(" · ");
}

function gitLine(event: FailureEvent): string {
  return [event.gitCommit, event.branch].filter(Boolean).join(" · ");
}

function stackBlock(stackTop: string[]) {
  if (!stackTop.length) return null;
  const lines = stackTop
    .slice(0, 3)
    .map((line, i) => `${i + 1}. ${line}`)
    .join("\n");
  return textBlock("堆栈 Top 3", lines);
}

function tailBlock(tail: string) {
  if (!tail) return null;
  return textBlock("终端尾部输出", tail.slice(0, 1200));
}

function actionBlock(event: FailureEvent) {
  const actions: unknown[] = [];
  if (event.reportUrl) {
    actions.push({
      tag: "button",
      text: { tag: "plain_text", content: "打开报告" },
      type: "primary",
      url: event.reportUrl,
    });
  }
  actions.push({
    tag: "button",
    text: { tag: "plain_text", content: "标记误报" },
    type: "default",
    value: {
      action: "termind.false_positive",
      fingerprint: event.fingerprint,
    },
  });
  return { tag: "action", actions };
}
