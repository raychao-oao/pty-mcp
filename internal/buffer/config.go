package buffer

import (
	"log"
	"os"
	"strconv"
)

const (
	DefaultBufferSize = 1024 * 1024      // 1MB
	MinBufferSize     = 64 * 1024        // 64KB
	MaxBufferSize     = 32 * 1024 * 1024 // 32MB
)

func BufferSizeFromEnv() int {
	val := os.Getenv("PTY_MCP_BUFFER_SIZE")
	if val == "" {
		return DefaultBufferSize
	}
	size, err := strconv.Atoi(val)
	if err != nil {
		log.Printf("[pty-mcp] invalid PTY_MCP_BUFFER_SIZE %q, using default %d", val, DefaultBufferSize)
		return DefaultBufferSize
	}
	if size < MinBufferSize {
		log.Printf("[pty-mcp] PTY_MCP_BUFFER_SIZE %d below minimum, clamping to %d", size, MinBufferSize)
		return MinBufferSize
	}
	if size > MaxBufferSize {
		log.Printf("[pty-mcp] PTY_MCP_BUFFER_SIZE %d above maximum, clamping to %d", size, MaxBufferSize)
		return MaxBufferSize
	}
	return size
}
