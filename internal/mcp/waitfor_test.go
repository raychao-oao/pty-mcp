package mcp

import (
	"testing"
	"time"

	"github.com/raychao-oao/pty-mcp/internal/buffer"
)

func TestWaitForPattern_ImmediateMatch(t *testing.T) {
	rb := buffer.NewRingBuffer(1024)
	rb.Write([]byte("line1\nline2\nhello world\nline4\n"))

	result := waitForPattern(rb, func() bool { return true }, WaitForParams{
		WaitFor:      "hello",
		Timeout:      5 * time.Second,
		ContextLines: 1,
	})

	if !result.Matched {
		t.Fatalf("expected match, got error: %s", result.Error)
	}
	if result.MatchLine != "hello world" {
		t.Fatalf("got match_line %q, want %q", result.MatchLine, "hello world")
	}
	if result.Context == "" {
		t.Fatal("expected context with context_lines=1")
	}
}

func TestWaitForPattern_Timeout(t *testing.T) {
	rb := buffer.NewRingBuffer(1024)
	rb.Write([]byte("nothing here\n"))

	result := waitForPattern(rb, func() bool { return true }, WaitForParams{
		WaitFor:   "missing",
		Timeout:   300 * time.Millisecond,
		TailLines: 5,
	})

	if result.Matched {
		t.Fatal("expected no match")
	}
	if result.Error == "" {
		t.Fatal("expected timeout error")
	}
	if result.Tail == "" {
		t.Fatal("expected tail output with tail_lines=5")
	}
}

func TestWaitForPattern_AsyncMatch(t *testing.T) {
	rb := buffer.NewRingBuffer(1024)

	go func() {
		time.Sleep(100 * time.Millisecond)
		rb.Write([]byte("waiting...\nserver ready\n"))
	}()

	result := waitForPattern(rb, func() bool { return true }, WaitForParams{
		WaitFor: "ready",
		Timeout: 2 * time.Second,
	})

	if !result.Matched {
		t.Fatalf("expected match, got error: %s", result.Error)
	}
	if result.MatchLine != "server ready" {
		t.Fatalf("got %q, want %q", result.MatchLine, "server ready")
	}
}

func TestWaitForPattern_InvalidRegexFallback(t *testing.T) {
	rb := buffer.NewRingBuffer(1024)
	rb.Write([]byte("test [invalid regex\n"))

	result := waitForPattern(rb, func() bool { return true }, WaitForParams{
		WaitFor: "[invalid",
		Timeout: 1 * time.Second,
	})

	if !result.Matched {
		t.Fatal("expected match via plain text fallback")
	}
	if result.Warning == "" {
		t.Fatal("expected warning about invalid regex")
	}
}

func TestWaitForPattern_PartialLine(t *testing.T) {
	rb := buffer.NewRingBuffer(1024)

	go func() {
		rb.Write([]byte("pass"))
		time.Sleep(50 * time.Millisecond)
		rb.Write([]byte("word: \n"))
	}()

	result := waitForPattern(rb, func() bool { return true }, WaitForParams{
		WaitFor: "password:",
		Timeout: 2 * time.Second,
	})

	if !result.Matched {
		t.Fatalf("expected match on partial line reassembly, got error: %s", result.Error)
	}
}

func TestWaitForPattern_SessionDead(t *testing.T) {
	rb := buffer.NewRingBuffer(1024)
	alive := true

	go func() {
		time.Sleep(100 * time.Millisecond)
		rb.Write([]byte("some output\n"))
		alive = false
		// write again to trigger notify
		rb.Write([]byte("final\n"))
	}()

	result := waitForPattern(rb, func() bool { return alive }, WaitForParams{
		WaitFor:   "never_match",
		Timeout:   2 * time.Second,
		TailLines: 10,
	})

	if result.Matched {
		t.Fatal("expected no match")
	}
	if result.IsAlive {
		t.Fatal("expected is_alive=false")
	}
}
