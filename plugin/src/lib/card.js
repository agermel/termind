const severityMeta = {
  info: {
    tag: "green",
    title: "termind · 历史同款"
  },
  warning: {
    tag: "orange",
    title: "termind · 新错误已立案"
  },
  incident: {
    tag: "red",
    title: "termind · 指纹异常扩散"
  }
};

export function buildIncidentCard(event) {
  const meta = severityMeta[event.severity] || severityMeta.warning;
  const fields = [
    ["指纹", event.fingerprint],
    ["服务/来源", sourceLine(event)],
    ["Git", gitLine(event)],
    ["环境", event.environment]
  ].filter(([, value]) => Boolean(value));

  return {
    config: {
      wide_screen_mode: true
    },
    header: {
      template: meta.tag,
      title: {
        tag: "plain_text",
        content: `${meta.title} · ${event.fingerprint}`
      }
    },
    elements: [
      markdown(`**报错摘要:** ${event.summary}`),
      textBlock("触发命令", event.command),
      fields.length > 0
        ? {
            tag: "div",
            fields: fields.map(([label, value]) => ({
              is_short: true,
              text: {
                tag: "lark_md",
                content: `**${label}:** ${value}`
              }
            }))
          }
        : null,
      stackBlock(event),
      tailBlock(event),
      actionBlock(event)
    ].filter(Boolean)
  };
}

function markdown(content) {
  return {
    tag: "div",
    text: {
      tag: "lark_md",
      content
    }
  };
}

function textBlock(title, content) {
  const value = String(content || "").trim();
  if (!value) return null;
  return {
    tag: "div",
    text: {
      tag: "plain_text",
      content: `${title}\n${value}`
    }
  };
}

function sourceLine(event) {
  const parts = [event.project, event.user].filter(Boolean);
  return parts.length > 0 ? parts.join(" · ") : "";
}

function gitLine(event) {
  const parts = [event.gitCommit, event.branch].filter(Boolean);
  return parts.length > 0 ? parts.join(" · ") : "";
}

function stackBlock(event) {
  if (!event.stackTop?.length) return null;
  const lines = event.stackTop.slice(0, 3).map((line, index) => `${index + 1}. ${line}`).join("\n");
  return textBlock("堆栈 Top 3", lines);
}

function tailBlock(event) {
  if (!event.tail) return null;
  return textBlock("终端尾部输出", event.tail.slice(0, 1200));
}

function actionBlock(event) {
  const actions = [];
  if (event.reportUrl) {
    actions.push({
      tag: "button",
      text: {
        tag: "plain_text",
        content: "打开报告"
      },
      type: "primary",
      url: event.reportUrl
    });
  }
  actions.push({
    tag: "button",
    text: {
      tag: "plain_text",
      content: "标记误报"
    },
    type: "default",
    value: {
      action: "termind.false_positive",
      fingerprint: event.fingerprint
    }
  });
  return {
    tag: "action",
    actions
  };
}
