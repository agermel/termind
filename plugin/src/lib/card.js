// severity 决定卡片头颜色 + 路由 (info -> 本地 / warning -> 私聊 / incident -> 团队群).
// branch 决定标题文案 + emoji + 按钮文案. 两个维度正交, 不互相覆盖:
// 比如 "warning + new_case" 是 "🆕 新错误已立案" + 橙色头;
//      "incident + escalation_candidate" 是 "⛔️ 重复发生升级" + 红色头.
const severityMeta = {
  info: { tag: "green", fallbackTitle: "termind · 历史同款" },
  warning: { tag: "orange", fallbackTitle: "termind · 新错误已立案" },
  incident: { tag: "red", fallbackTitle: "termind · 指纹异常扩散" }
};

const branchMeta = {
  new_case: {
    emoji: "🆕",
    title: "termind · 新错误已立案",
    button: "打开报告"
  },
  recurrence: {
    emoji: "🔁",
    title: "termind · 历史同款",
    button: "打开历史报告"
  },
  escalation_candidate: {
    emoji: "⛔️",
    title: "termind · 重复发生升级",
    button: "查看升级报告"
  }
};

export function buildIncidentCard(event) {
  const sev = severityMeta[event.severity] || severityMeta.warning;
  const branch = pickBranch(event);
  const headerTitle = branch
    ? `${branchMeta[branch].emoji} ${branchMeta[branch].title}`
    : sev.fallbackTitle;

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
      template: sev.tag,
      title: {
        tag: "plain_text",
        content: `${headerTitle} · ${event.fingerprint}`
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
      ownerBlock(event),
      historyBlock(event, branch),
      stackBlock(event),
      tailBlock(event),
      knowledgeBlock(event),
      actionBlock(event, branch)
    ].filter(Boolean)
  };
}

// ownerBlock: 渲染责任人. 优先 @ open_id (lark_md 的 <at> 语法), 否则只显示
// label. 没有任何识别信息就不渲染.
function ownerBlock(event) {
  const owner = event.owner;
  if (!owner) return null;
  const openId = String(owner.openId || "").trim();
  const label = String(owner.label || "").trim();
  if (!openId && !label) return null;
  const confidence = String(owner.confidence || "").trim();
  let content;
  if (openId && confidence !== "label_only") {
    // Lark interactive card 的 @ 语法: <at id=ou_xxx></at>
    content = `**责任人:** <at id=${openId}></at>${label ? ` (${label})` : ""}`;
  } else if (label) {
    content = `**责任人:** ${label}${owner.email ? ` (${owner.email})` : ""}`;
  } else {
    return null;
  }
  return markdown(content);
}

// knowledgeBlock: 渲染 RAG 命中的参考文档列表 (最多 3 条).
function knowledgeBlock(event) {
  const hits = Array.isArray(event.knowledgeHits) ? event.knowledgeHits.filter(Boolean) : [];
  if (hits.length === 0) return null;
  const lines = hits.slice(0, 3).map((hit) => {
    const title = String(hit.title || hit.token || "(untitled)").slice(0, 100);
    const type = String(hit.type || "doc");
    if (hit.url) return `- [${title}](${hit.url}) · ${type}`;
    return `- ${title} · ${type}`;
  });
  return markdown(`**相关知识:**\n${lines.join("\n")}`);
}

function pickBranch(event) {
  const b = String(event.registryBranch || "").trim();
  return Object.prototype.hasOwnProperty.call(branchMeta, b) ? b : null;
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

// historyBlock 渲染指纹的历史信息块. 显示规则:
//   - new_case 且 occurrences <= 0: 不显示 (本次就是首次, 没历史可讲).
//   - 其他场景: 只要有 occurrences > 0 就显示, 文案依据可用字段降级.
//
// 字段不全时, 缺什么就少显示什么, 不报错. 比如只有 occurrences 没有
// firstSeen/lastSeen, 只显示第一行.
function historyBlock(event, branch) {
  const occurrences = numberOrZero(event.occurrences);
  if (occurrences <= 0) return null;
  if (branch === "new_case") return null;

  const lines = [];
  const headline = buildHistoryHeadline(event, occurrences);
  if (headline) lines.push(headline);
  const seenLine = buildHistorySeenLine(event);
  if (seenLine) lines.push(seenLine);
  if (lines.length === 0) return null;

  const title = branch === "escalation_candidate" ? "升级原因" : "历史";
  return textBlock(title, lines.join("\n"));
}

function buildHistoryHeadline(event, occurrences) {
  const parts = [`此前发生 ${occurrences} 次`];
  const affected = numberOrZero(event.affectedUsers);
  if (affected > 0) parts.push(`受影响 ${affected} 人`);

  const windowOccurrences = numberOrZero(event.windowOccurrences);
  const windowMinutes = numberOrZero(event.windowMinutes);
  if (windowOccurrences > 0 && windowMinutes > 0) {
    parts.push(`近 ${windowMinutes} 分钟内 ${windowOccurrences} 次`);
  }
  return parts.join(" · ");
}

function buildHistorySeenLine(event) {
  const segments = [];
  const first = String(event.firstSeen || "").trim();
  const last = String(event.lastSeen || "").trim();
  if (first) segments.push(`首次发现 ${first}`);
  if (last) segments.push(`最近发现 ${last}`);
  return segments.join(" · ");
}

function numberOrZero(value) {
  const n = Number(value);
  return Number.isFinite(n) && n > 0 ? n : 0;
}

function actionBlock(event, branch) {
  if (!event.reportUrl) return null;
  const buttonLabel = branch && branchMeta[branch] ? branchMeta[branch].button : "打开报告";
  return {
    tag: "action",
    actions: [{
    tag: "button",
    text: {
      tag: "plain_text",
      content: buttonLabel
    },
    type: "primary",
    url: event.reportUrl
    }]
  };
}
