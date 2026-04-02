package buffer

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestRingBuffer_WriteAndReadSince — basic write + read
func TestRingBuffer_WriteAndReadSince(t *testing.T) {
	rb := NewRingBuffer(256)

	snap := rb.Snapshot()
	rb.Write([]byte("hello world"))

	result := rb.ReadSince(snap)
	if result != "hello world" {
		t.Errorf("expected %q, got %q", "hello world", result)
	}
}

// TestRingBuffer_Overwrite — write more than buffer size, verify circular overwrite
func TestRingBuffer_Overwrite(t *testing.T) {
	rb := NewRingBuffer(8) // tiny buffer

	snap := rb.Snapshot() // snapshot at 0, before any writes
	rb.Write([]byte("12345678")) // fill exactly (written=8)
	rb.Write([]byte("ABCD"))    // overwrite first 4 bytes (written=12)

	// snapshot=0 is now too old: oldest readable = written-size = 12-8 = 4 > 0
	if !rb.IsTruncated(snap) {
		t.Error("expected snapshot to be truncated after overwrite")
	}

	// ReadSince with old snapshot should return all available content (clamped to oldest)
	content := rb.ReadSince(snap)
	if content == "" {
		t.Error("expected non-empty content from ReadSince with old snapshot")
	}
	// Buffer should now contain "5678ABCD"
	all := rb.String()
	if !strings.Contains(all, "ABCD") {
		t.Errorf("expected buffer to contain ABCD, got %q", all)
	}
}

// TestRingBuffer_SnapshotTooOld — verify IsTruncated returns true for old snapshots
func TestRingBuffer_SnapshotTooOld(t *testing.T) {
	rb := NewRingBuffer(8)

	snap := rb.Snapshot() // snapshot at 0
	rb.Write([]byte("123456789")) // write 9 bytes > size 8, so snap=0 is overwritten

	if !rb.IsTruncated(snap) {
		t.Error("expected IsTruncated to return true for snapshot before overwrite")
	}

	// Fresh snapshot should not be truncated
	freshSnap := rb.Snapshot()
	if rb.IsTruncated(freshSnap) {
		t.Error("expected fresh snapshot to not be truncated")
	}
}

// TestRingBuffer_WaitUnblocksOnWrite — goroutine writes after 50ms, Wait returns true
func TestRingBuffer_WaitUnblocksOnWrite(t *testing.T) {
	rb := NewRingBuffer(256)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		time.Sleep(50 * time.Millisecond)
		rb.Write([]byte("signal"))
	}()

	result := rb.Wait(ctx)
	if !result {
		t.Error("expected Wait to return true when write unblocks it")
	}
}

// TestRingBuffer_WaitReturnsFalseOnCancel — 50ms timeout context, Wait returns false
func TestRingBuffer_WaitReturnsFalseOnCancel(t *testing.T) {
	rb := NewRingBuffer(256)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	result := rb.Wait(ctx)
	if result {
		t.Error("expected Wait to return false when context is canceled")
	}
}

// TestRingBuffer_Tail — write multi-line content, verify Tail(2) returns last 2 lines
func TestRingBuffer_Tail(t *testing.T) {
	rb := NewRingBuffer(256)

	rb.Write([]byte("line1\nline2\nline3\nline4\n"))

	lines := rb.Tail(2)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "line3" {
		t.Errorf("expected lines[0] = %q, got %q", "line3", lines[0])
	}
	if lines[1] != "line4" {
		t.Errorf("expected lines[1] = %q, got %q", "line4", lines[1])
	}
}

// TestRingBuffer_SinceAndMark — test Since/Mark compatibility with WaitForSettle pattern
func TestRingBuffer_SinceAndMark(t *testing.T) {
	rb := NewRingBuffer(256)

	rb.Write([]byte("before mark\n"))
	rb.Mark()
	rb.Write([]byte("after mark\n"))

	since := rb.Since()
	if !strings.Contains(since, "after mark") {
		t.Errorf("Since() should contain 'after mark', got %q", since)
	}
	if strings.Contains(since, "before mark") {
		t.Errorf("Since() should not contain 'before mark', got %q", since)
	}

	// After another Mark, Since should be empty or only contain newly written data
	rb.Mark()
	since2 := rb.Since()
	if since2 != "" {
		t.Errorf("Since() after Mark with no new writes should be empty, got %q", since2)
	}

	rb.Write([]byte("new data\n"))
	since3 := rb.Since()
	if !strings.Contains(since3, "new data") {
		t.Errorf("Since() should contain 'new data', got %q", since3)
	}
}

// TestRingBuffer_ReadSinceMax — chunked reads with max_bytes
func TestRingBuffer_ReadSinceMax(t *testing.T) {
	rb := NewRingBuffer(1024)
	snap := rb.Snapshot()

	rb.Write([]byte("abcdefghij")) // 10 bytes

	// Read with max_bytes=4: should get first 4 bytes and has_more=true
	out, cur, hasMore := rb.ReadSinceMax(snap, 4)
	if out != "abcd" {
		t.Errorf("expected %q, got %q", "abcd", out)
	}
	if !hasMore {
		t.Errorf("expected has_more=true")
	}

	// Continue reading from new cursor
	out2, cur2, hasMore2 := rb.ReadSinceMax(cur, 4)
	if out2 != "efgh" {
		t.Errorf("expected %q, got %q", "efgh", out2)
	}
	if !hasMore2 {
		t.Errorf("expected has_more=true")
	}

	// Read remaining 2 bytes
	out3, _, hasMore3 := rb.ReadSinceMax(cur2, 4)
	if out3 != "ij" {
		t.Errorf("expected %q, got %q", "ij", out3)
	}
	if hasMore3 {
		t.Errorf("expected has_more=false")
	}

	// No max_bytes (0) = read all
	out4, _, hasMore4 := rb.ReadSinceMax(snap, 0)
	if out4 != "abcdefghij" {
		t.Errorf("expected full output, got %q", out4)
	}
	if hasMore4 {
		t.Errorf("expected has_more=false with no limit")
	}
}
