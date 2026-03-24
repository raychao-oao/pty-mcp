.PHONY: build build-all clean test install

VERSION ?= dev

build:
	go build -ldflags "-X main.version=$(VERSION)" -o pty-mcp .

build-all: build
	go build -o ai-tmux ./cmd/ai-tmux/

build-linux:
	GOOS=linux GOARCH=amd64 go build -ldflags "-X main.version=$(VERSION)" -o pty-mcp-linux .
	GOOS=linux GOARCH=amd64 go build -o ai-tmux-linux ./cmd/ai-tmux/

build-linux-arm64:
	GOOS=linux GOARCH=arm64 go build -ldflags "-X main.version=$(VERSION)" -o pty-mcp-linux-arm64 .
	GOOS=linux GOARCH=arm64 go build -o ai-tmux-linux-arm64 ./cmd/ai-tmux/

test:
	go test ./... -timeout 15s

clean:
	rm -f pty-mcp ai-tmux pty-mcp-linux ai-tmux-linux pty-mcp-linux-arm64 ai-tmux-linux-arm64

install: build
	@echo "Register with Claude Code:"
	@echo "  claude mcp add pty-mcp -- $(shell pwd)/pty-mcp"
