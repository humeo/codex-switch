#!/usr/bin/env bash
set -euo pipefail

BINARY_NAME="codex-switch"
REPO="${REPO:-humeo/codex-switch}"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${VERSION:-latest}"

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Error: required command not found: $1" >&2
    exit 1
  fi
}

detect_os() {
  case "$(uname -s)" in
    Darwin) echo "darwin" ;;
    Linux) echo "linux" ;;
    *)
      echo "Error: unsupported operating system: $(uname -s)" >&2
      exit 1
      ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo "amd64" ;;
    arm64|aarch64) echo "arm64" ;;
    *)
      echo "Error: unsupported architecture: $(uname -m)" >&2
      exit 1
      ;;
  esac
}

path_contains() {
  case ":${PATH:-}:" in
    *":$1:"*) return 0 ;;
    *) return 1 ;;
  esac
}

need_cmd curl
need_cmd tar
need_cmd mktemp

OS="$(detect_os)"
ARCH="$(detect_arch)"
ASSET_NAME="${BINARY_NAME}_${OS}_${ARCH}.tar.gz"

if [ -n "${RELEASE_BASE_URL:-}" ]; then
  DOWNLOAD_URL="${RELEASE_BASE_URL%/}/${ASSET_NAME}"
elif [ "$VERSION" = "latest" ]; then
  DOWNLOAD_URL="https://github.com/${REPO}/releases/latest/download/${ASSET_NAME}"
else
  DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET_NAME}"
fi

TMPDIR="$(mktemp -d)"
cleanup() {
  rm -rf "$TMPDIR"
}
trap cleanup EXIT

ARCHIVE_PATH="${TMPDIR}/${ASSET_NAME}"
curl --fail --silent --show-error --location "$DOWNLOAD_URL" --output "$ARCHIVE_PATH"
tar -xzf "$ARCHIVE_PATH" -C "$TMPDIR"

if [ ! -f "${TMPDIR}/${BINARY_NAME}" ]; then
  echo "Error: archive did not contain ${BINARY_NAME}" >&2
  exit 1
fi

mkdir -p "$INSTALL_DIR"
cp "${TMPDIR}/${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}"
chmod 0755 "${INSTALL_DIR}/${BINARY_NAME}"

echo "Installed ${BINARY_NAME} to ${INSTALL_DIR}/${BINARY_NAME}"
if ! path_contains "$INSTALL_DIR"; then
  echo "Add ${INSTALL_DIR} to your PATH to run ${BINARY_NAME} without a full path."
fi
