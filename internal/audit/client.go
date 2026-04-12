package audit

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Config holds audit client configuration.
type Config struct {
	URL   string // collector base URL, e.g. http://localhost:8080
	User  string // operator identity label
	Token string // shared secret (Bearer token)
	Mode  string // "best-effort" (default) or "strict"
}

// CmdEntry is the phase-1 audit record, sent before command execution.
type CmdEntry struct {
	CmdID     string `json:"cmd_id"`
	TS        string `json:"ts"`
	User      string `json:"user"`
	SessionID string `json:"session_id"`
	Type      string `json:"type"`
	Target    string `json:"target"`
	Cmd       string `json:"cmd"`
	Raw       bool   `json:"raw,omitempty"`
}

// OutputEntry is the phase-2 audit record, sent after output is available.
type OutputEntry struct {
	CmdID         string `json:"cmd_id"`
	TS            string `json:"ts"`
	OutputSnippet string `json:"output_snippet,omitempty"`
	OutputBytes   int    `json:"output_bytes"`
}

// Client pushes audit entries to a collector over HTTP.
type Client struct {
	cfg        Config
	httpClient *http.Client
	queue      chan []byte
	dropped    atomic.Int64
	done       chan struct{}
	closeOnce  sync.Once
}

// NewClient creates a Client and starts the background worker.
// The worker handles phase-2 (output) entries in all modes, since phase-2 is always best-effort.
func NewClient(cfg Config) *Client {
	c := &Client{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 5 * time.Second},
		queue:      make(chan []byte, 256),
		done:       make(chan struct{}),
	}
	if cfg.URL != "" {
		go c.worker()
	}
	return c
}

// Close shuts down the background worker. Safe to call multiple times.
func (c *Client) Close() {
	c.closeOnce.Do(func() { close(c.done) })
}

// User returns the operator identity label.
func (c *Client) User() string { return c.cfg.User }

// NewCmdID generates a random 16-hex command ID.
func NewCmdID() string {
	var b [8]byte
	rand.Read(b[:]) //nolint:errcheck
	return hex.EncodeToString(b[:])
}

// SendCmd sends phase-1 (command entry).
// In strict mode it blocks and returns an error on failure.
// In best-effort mode it enqueues and never fails.
func (c *Client) SendCmd(entry CmdEntry) error {
	if c.cfg.URL == "" {
		return nil
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return nil
	}
	if c.cfg.Mode == "strict" {
		return c.postWithRetry(data)
	}
	c.enqueue(data)
	return nil
}

// SendOutput sends phase-2 (output entry). Always best-effort.
func (c *Client) SendOutput(entry OutputEntry) {
	if c.cfg.URL == "" {
		return
	}
	data, _ := json.Marshal(entry)
	c.enqueue(data)
}

func (c *Client) enqueue(data []byte) {
	select {
	case c.queue <- data:
		return
	default:
	}
	// queue full: drop oldest to make room for newest
	select {
	case <-c.queue:
		c.dropped.Add(1)
	default:
	}
	select {
	case c.queue <- data:
	default:
	}
}

func (c *Client) worker() {
	for {
		select {
		case <-c.done:
			return
		case data := <-c.queue:
			c.postWithRetry(data) //nolint:errcheck // best-effort: ignore error
		}
	}
}

var retryDelays = []time.Duration{0, time.Second, 2 * time.Second, 4 * time.Second}

func (c *Client) postWithRetry(data []byte) error {
	var lastErr error
	for i, delay := range retryDelays {
		if i > 0 {
			time.Sleep(delay)
		}
		err := c.post(data)
		if err == nil {
			return nil
		}
		lastErr = err
		if isClientError(err) {
			return err // don't retry 4xx
		}
	}
	return lastErr
}

func (c *Client) post(data []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.URL+"/audit", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	if resp.StatusCode >= 400 {
		return &httpStatusError{code: resp.StatusCode}
	}
	return nil
}

type httpStatusError struct{ code int }

func (e *httpStatusError) Error() string { return fmt.Sprintf("HTTP %d", e.code) }

func isClientError(err error) bool {
	var he *httpStatusError
	return errors.As(err, &he) && he.code >= 400 && he.code < 500
}

// Snippet returns at most maxBytes bytes of s, truncated at a UTF-8 character boundary.
func Snippet(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	// Walk rune boundaries to avoid splitting a multi-byte character.
	n := 0
	for i := range s {
		if i > maxBytes {
			break
		}
		n = i
	}
	return s[:n]
}
