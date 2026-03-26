.PHONY: build build-all build-darwin build-linux build-linux-arm64 build-release clean test install

VERSION ?= dev

build:
	go build -ldflags "-X main.version=$(VERSION)" -o pty-mcp .

build-all: build
	go build -ldflags "-X main.version=$(VERSION)" -o ai-tmux ./cmd/ai-tmux/

build-darwin:
	GOOS=darwin GOARCH=arm64 go build -ldflags "-X main.version=$(VERSION)" -o pty-mcp-darwin-arm64 .
	GOOS=darwin GOARCH=arm64 go build -ldflags "-X main.version=$(VERSION)" -o ai-tmux-darwin-arm64 ./cmd/ai-tmux/
	GOOS=darwin GOARCH=amd64 go build -ldflags "-X main.version=$(VERSION)" -o pty-mcp-darwin-amd64 .
	GOOS=darwin GOARCH=amd64 go build -ldflags "-X main.version=$(VERSION)" -o ai-tmux-darwin-amd64 ./cmd/ai-tmux/

build-linux:
	GOOS=linux GOARCH=amd64 go build -ldflags "-X main.version=$(VERSION)" -o pty-mcp-linux-amd64 .
	GOOS=linux GOARCH=amd64 go build -ldflags "-X main.version=$(VERSION)" -o ai-tmux-linux-amd64 ./cmd/ai-tmux/

build-linux-arm64:
	GOOS=linux GOARCH=arm64 go build -ldflags "-X main.version=$(VERSION)" -o pty-mcp-linux-arm64 .
	GOOS=linux GOARCH=arm64 go build -ldflags "-X main.version=$(VERSION)" -o ai-tmux-linux-arm64 ./cmd/ai-tmux/

build-release: build-darwin build-linux build-linux-arm64

test:
	go test ./... -timeout 15s

clean:
	rm -f pty-mcp ai-tmux pty-mcp-darwin-arm64 ai-tmux-darwin-arm64 pty-mcp-darwin-amd64 ai-tmux-darwin-amd64 pty-mcp-linux-amd64 ai-tmux-linux-amd64 pty-mcp-linux-arm64 ai-tmux-linux-arm64 pty-mcp-linux ai-tmux-linux

install: build
	@echo "Register with Claude Code:"
	@echo "  claude mcp add pty-mcp -- $(shell pwd)/pty-mcp"
