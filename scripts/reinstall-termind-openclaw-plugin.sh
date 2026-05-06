#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PLUGIN_DIR="${ROOT_DIR}/plugin"
PLUGIN_ID="termind"
EXT_DIR="${HOME}/.openclaw/extensions/${PLUGIN_ID}"
NPM_PLUGIN_DIR="${HOME}/.openclaw/npm/node_modules/termind-openclaw-plugin"

echo "==> Reinstalling Termind OpenClaw plugin from ${PLUGIN_DIR}"

if openclaw plugins uninstall "${PLUGIN_ID}" --force; then
  echo "==> Uninstalled existing ${PLUGIN_ID} plugin"
else
  echo "==> Existing ${PLUGIN_ID} plugin was not fully uninstalled; continuing"
fi

if [[ -d "${EXT_DIR}" ]]; then
  echo "==> Removing stale extension directory: ${EXT_DIR}"
  rm -rf "${EXT_DIR}"
fi

if [[ -d "${NPM_PLUGIN_DIR}" ]]; then
  echo "==> Removing stale npm plugin directory: ${NPM_PLUGIN_DIR}"
  rm -rf "${NPM_PLUGIN_DIR}"
fi

openclaw plugins install "${PLUGIN_DIR}" --force
openclaw plugins enable "${PLUGIN_ID}"

echo "==> Done. Restart OpenClaw gateway to apply the updated plugin."
