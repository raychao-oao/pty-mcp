#!/bin/bash
# pty-mcp binary installer
# Downloads the correct binary from GitHub releases into ${CLAUDE_PLUGIN_ROOT}/bin/

set -e

VERSION="v0.3.0"
REPO="raychao-oao/pty-mcp"
BIN_DIR="${CLAUDE_PLUGIN_ROOT}/bin"
BIN_PATH="${BIN_DIR}/pty-mcp"

# Skip if already installed
if [ -f "${BIN_PATH}" ]; then
    exit 0
fi

mkdir -p "${BIN_DIR}"

# Detect OS
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "${OS}" in
    darwin|linux) ;;
    *) echo "[pty-mcp] Unsupported OS: ${OS}" >&2; exit 1 ;;
esac

# Detect arch
ARCH=$(uname -m)
case "${ARCH}" in
    x86_64)          ARCH="amd64" ;;
    aarch64|arm64)   ARCH="arm64" ;;
    *) echo "[pty-mcp] Unsupported architecture: ${ARCH}" >&2; exit 1 ;;
esac

BINARY="pty-mcp-${OS}-${ARCH}"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${BINARY}"

echo "[pty-mcp] Downloading ${BINARY} ${VERSION}..."
curl -fsSL "${URL}" -o "${BIN_PATH}"
chmod +x "${BIN_PATH}"
echo "[pty-mcp] Ready: ${BIN_PATH}"
