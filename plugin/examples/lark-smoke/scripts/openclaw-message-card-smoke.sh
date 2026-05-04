#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${TERMIND_LARK_CHAT_ID:-}" ]]; then
  echo "TERMIND_LARK_CHAT_ID is required, for example: export TERMIND_LARK_CHAT_ID=oc_xxx" >&2
  exit 2
fi

card='{
  "config": {
    "wide_screen_mode": true
  },
  "header": {
    "template": "orange",
    "title": {
      "tag": "plain_text",
      "content": "termind · 新错误已立案 · a3f9c2d1"
    }
  },
  "elements": [
    {
      "tag": "div",
      "text": {
        "tag": "lark_md",
        "content": "**报错摘要:** panic: runtime error: invalid memory address"
      }
    },
    {
      "tag": "div",
      "text": {
        "tag": "plain_text",
        "content": "触发命令\ngo run ./cmd/grade serve"
      }
    },
    {
      "tag": "div",
      "fields": [
        {
          "is_short": true,
          "text": {
            "tag": "lark_md",
            "content": "**服务/来源:** be-grade · matterhorn"
          }
        },
        {
          "is_short": true,
          "text": {
            "tag": "lark_md",
            "content": "**Git:** 8e4d21a · feat/rank-v2"
          }
        },
        {
          "is_short": true,
          "text": {
            "tag": "lark_md",
            "content": "**环境:** go1.22.3 macOS dev"
          }
        }
      ]
    },
    {
      "tag": "div",
      "text": {
        "tag": "plain_text",
        "content": "堆栈 Top 2\n1. be-grade/cron/rank.go:87 computeRank()\n2. be-grade/cron/rank.go:42 (*RankJob).Run()"
      }
    }
  ]
}'

openclaw message send \
  --channel feishu \
  --target "$TERMIND_LARK_CHAT_ID" \
  --card "$card" \
  --json
