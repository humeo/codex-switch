#!/usr/bin/env bash
set -euo pipefail

BINARY_NAME="codex-switch"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
DATA_DIR="${HOME}/.codex-switch"
BINARY_PATH="${INSTALL_DIR}/${BINARY_NAME}"
REMOVE_DATA="${REMOVE_DATA:-0}"

if [ -f "$BINARY_PATH" ]; then
  rm -f "$BINARY_PATH"
  echo "Removed ${BINARY_PATH}"
else
  echo "No installed binary found at ${BINARY_PATH}"
fi

if [ "$REMOVE_DATA" = "1" ]; then
  if [ -d "$DATA_DIR" ]; then
    rm -rf "$DATA_DIR"
    echo "Removed user data at ${DATA_DIR}"
  else
    echo "No user data directory found at ${DATA_DIR}"
  fi
else
  echo "Preserved user data at ${DATA_DIR}"
fi

echo "Left ~/.codex/auth.json untouched."
