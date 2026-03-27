#!/bin/bash
# pty-mcp binary installer
# Downloads the correct binary from GitHub releases into ${CLAUDE_PLUGIN_ROOT}/bin/

set -e

VERSION="v0.3.1"
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
BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"
URL="${BASE_URL}/${BINARY}"
TMP_BIN="${BIN_PATH}.tmp"

echo "[pty-mcp] Downloading ${BINARY} ${VERSION}..."
curl -fsSL "${URL}" -o "${TMP_BIN}"

# Verify SHA256 checksum if SHA256SUMS is available
SUMS_TMP=$(mktemp)
if curl -fsSL "${BASE_URL}/SHA256SUMS" -o "${SUMS_TMP}" 2>/dev/null; then
    EXPECTED=$(grep " ${BINARY}$" "${SUMS_TMP}" | awk '{print $1}')
    if [ -n "${EXPECTED}" ]; then
        if command -v sha256sum >/dev/null 2>&1; then
            ACTUAL=$(sha256sum "${TMP_BIN}" | awk '{print $1}')
        elif command -v shasum >/dev/null 2>&1; then
            ACTUAL=$(shasum -a 256 "${TMP_BIN}" | awk '{print $1}')
        else
            ACTUAL=""
        fi
        if [ -n "${ACTUAL}" ]; then
            if [ "${EXPECTED}" = "${ACTUAL}" ]; then
                echo "[pty-mcp] Checksum verified"
            else
                rm -f "${TMP_BIN}" "${SUMS_TMP}"
                echo "[pty-mcp] Checksum mismatch! Expected: ${EXPECTED}, Got: ${ACTUAL}" >&2
                exit 1
            fi
        else
            rm -f "${TMP_BIN}" "${SUMS_TMP}"
            echo "[pty-mcp] Error: SHA256SUMS present but no sha256sum/shasum tool found; cannot verify integrity" >&2
            exit 1
        fi
    fi
fi
rm -f "${SUMS_TMP}"

mv "${TMP_BIN}" "${BIN_PATH}"
chmod +x "${BIN_PATH}"
echo "[pty-mcp] Ready: ${BIN_PATH}"
