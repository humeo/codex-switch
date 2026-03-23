#!/usr/bin/env bash
set -euo pipefail

BINARY_NAME="codex-switch"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
DATA_DIR="${HOME}/.codex-switch"
BINARY_PATH="${INSTALL_DIR}/${BINARY_NAME}"
REMOVE_DATA="${REMOVE_DATA:-0}"
ZSH_COMPLETION_PATH="${ZSH_COMPLETION_PATH:-$HOME/.zsh/completions/_${BINARY_NAME}}"
BASH_COMPLETION_PATH="${BASH_COMPLETION_PATH:-$HOME/.local/share/bash-completion/completions/${BINARY_NAME}}"
FISH_COMPLETION_PATH="${FISH_COMPLETION_PATH:-$HOME/.config/fish/completions/${BINARY_NAME}.fish}"

remove_file() {
  path="$1"

  if [ -f "$path" ]; then
    rm -f "$path"
    echo "Removed ${path}"
  else
    echo "No installed file found at ${path}"
  fi
}

remove_file "$BINARY_PATH"
remove_file "$ZSH_COMPLETION_PATH"
remove_file "$BASH_COMPLETION_PATH"
remove_file "$FISH_COMPLETION_PATH"

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
