package mcp

// ToolError is a structured error returned by MCP tool handlers.
// Clients can use Code for programmatic handling and Retryable to decide
// whether to retry the operation.
type ToolError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func (e *ToolError) Error() string { return e.Message }

// Error code constants.
const (
	ErrSessionNotFound = "SESSION_NOT_FOUND"
	ErrSessionClosed   = "SESSION_CLOSED"
	ErrSessionLimit    = "SESSION_LIMIT_REACHED"
	ErrInvalidParams   = "INVALID_PARAMS"
	ErrPTYFailed       = "PTY_OPEN_FAILED"
	ErrSSHAuthFailed   = "SSH_AUTH_FAILED"
	ErrSSHConnFailed   = "SSH_CONNECT_FAILED"
	ErrSerialFailed    = "SERIAL_OPEN_FAILED"
	ErrWriteFailed     = "WRITE_FAILED"
	ErrTimeout         = "TIMEOUT"
	ErrUnknownTool     = "UNKNOWN_TOOL"
)

func newToolError(code, message string, retryable bool) *ToolError {
	return &ToolError{Code: code, Message: message, Retryable: retryable}
}
