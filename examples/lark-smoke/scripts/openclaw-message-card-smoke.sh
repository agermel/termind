#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${TERMIND_LARK_TARGET_ID:-}" ]]; then
  echo "TERMIND_LARK_TARGET_ID is required, for example: export TERMIND_LARK_TARGET_ID=oc_xxx" >&2
  exit 2
fi

target_type="${TERMIND_LARK_TARGET_TYPE:-chat}"
sender="${TERMIND_LARK_SENDER:-bot}"

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

if [[ "$target_type" == "chat" ]]; then
  lark-cli im +messages-send \
    --as "$sender" \
    --chat-id "$TERMIND_LARK_TARGET_ID" \
    --content "$card" \
    --msg-type interactive
else
  lark-cli im +messages-send \
    --as "$sender" \
    --user-id "$TERMIND_LARK_TARGET_ID" \
    --content "$card" \
    --msg-type interactive
fi
