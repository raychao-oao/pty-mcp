#!/bin/sh
set -e

REPO="raychao-oao/pty-mcp"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

# Detect OS and arch
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

case "$OS" in
  darwin|linux) ;;
  *) echo "Unsupported OS: $OS"; exit 1 ;;
esac

# Get latest version
VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name"' | sed 's/.*"tag_name": *"//;s/".*//')
if [ -z "$VERSION" ]; then
  echo "Failed to get latest version"
  exit 1
fi

echo "Installing pty-mcp $VERSION ($OS/$ARCH)..."

BASE_URL="https://github.com/$REPO/releases/download/$VERSION"

# Download pty-mcp
echo "Downloading pty-mcp..."
curl -fsSL "$BASE_URL/pty-mcp-$OS-$ARCH" -o /tmp/pty-mcp
chmod +x /tmp/pty-mcp

# Download ai-tmux
echo "Downloading ai-tmux..."
curl -fsSL "$BASE_URL/ai-tmux-$OS-$ARCH" -o /tmp/ai-tmux
chmod +x /tmp/ai-tmux

# Install
if [ -w "$INSTALL_DIR" ]; then
  mv /tmp/pty-mcp "$INSTALL_DIR/pty-mcp"
  mv /tmp/ai-tmux "$INSTALL_DIR/ai-tmux"
else
  echo "Installing to $INSTALL_DIR (requires sudo)..."
  sudo mv /tmp/pty-mcp "$INSTALL_DIR/pty-mcp"
  sudo mv /tmp/ai-tmux "$INSTALL_DIR/ai-tmux"
fi

echo ""
echo "Installed:"
echo "  pty-mcp  -> $INSTALL_DIR/pty-mcp"
echo "  ai-tmux  -> $INSTALL_DIR/ai-tmux"
echo ""
echo "Register with Claude Code:"
echo "  claude mcp add pty-mcp -- $INSTALL_DIR/pty-mcp"
echo ""
echo "Deploy ai-tmux to remote servers for persistent sessions:"
echo "  scp $INSTALL_DIR/ai-tmux server:/usr/local/bin/ai-tmux"
