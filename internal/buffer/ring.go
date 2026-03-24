// Package buffer provides a thread-safe circular buffer with channel-based
// signaling for context-aware blocking reads.
package buffer

import (
	"context"
	"strings"
	"sync"
)

// RingBuffer is a fixed-size circular buffer. It overwrites the oldest data
// when full. Readers can block until new data is written using Wait, which
// respects context cancellation.
type RingBuffer struct {
	mu           sync.Mutex
	buf          []byte
	size         int
	head         int   // next write position (circular)
	written      int64 // total bytes written (monotonic, never resets)
	markSnapshot int64 // for Since/Mark compat with WaitForSettle
	notify       chan struct{} // buffered channel size 1
}

// NewRingBuffer creates a new RingBuffer with the given capacity.
func NewRingBuffer(size int) *RingBuffer {
	return &RingBuffer{
		buf:    make([]byte, size),
		size:   size,
		notify: make(chan struct{}, 1),
	}
}

// Write implements io.Writer. It writes p into the circular buffer, overwriting
// the oldest data if the buffer is full. It signals waiting readers after the
// write completes (releasing the mutex first).
func (rb *RingBuffer) Write(p []byte) (int, error) {
	rb.mu.Lock()

	n := len(p)
	for i, b := range p {
		_ = i
		rb.buf[rb.head] = b
		rb.head = (rb.head + 1) % rb.size
		rb.written++
	}

	rb.mu.Unlock()

	// Non-blocking send on notify; release mutex before channel op
	select {
	case rb.notify <- struct{}{}:
	default:
	}

	return n, nil
}

// Snapshot returns the current total bytes written count. Use this as a
// cursor for ReadSince to read only newly written data.
func (rb *RingBuffer) Snapshot() int64 {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.written
}

// IsTruncated reports whether the data at snapshot has been overwritten by
// subsequent writes (i.e., the snapshot is too old to read reliably).
func (rb *RingBuffer) IsTruncated(snapshot int64) bool {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	// Oldest data still in buffer starts at written - size.
	// If snapshot < that, the data at snapshot has been overwritten.
	oldest := rb.written - int64(rb.size)
	return snapshot < oldest
}

// ReadSince returns the content written after snapshot. If the snapshot is
// too old (data already overwritten), it returns all available content.
func (rb *RingBuffer) ReadSince(snapshot int64) string {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	// How many bytes are currently stored in the buffer
	available := rb.written
	if available > int64(rb.size) {
		available = int64(rb.size)
	}

	// Oldest readable position
	oldest := rb.written - available

	// Clamp snapshot to oldest readable
	if snapshot < oldest {
		snapshot = oldest
	}

	// Number of bytes to read
	count := rb.written - snapshot
	if count <= 0 {
		return ""
	}
	if count > int64(rb.size) {
		count = int64(rb.size)
	}

	// Starting position in circular buffer
	// head points to the next write position; going back `count` bytes
	// gives us the start of the data we want to read.
	start := (rb.head - int(count) + rb.size*2) % rb.size

	out := make([]byte, count)
	for i := int64(0); i < count; i++ {
		out[i] = rb.buf[(start+int(i))%rb.size]
	}
	return string(out)
}

// Wait blocks until new data is written to the buffer or the context is
// canceled. Returns true if unblocked by a write, false if the context was
// canceled.
func (rb *RingBuffer) Wait(ctx context.Context) bool {
	select {
	case <-rb.notify:
		return true
	case <-ctx.Done():
		return false
	}
}

// Tail returns the last n lines from the buffer content. Lines are split on
// newline characters; trailing empty lines are ignored.
func (rb *RingBuffer) Tail(n int) []string {
	content := rb.String()

	// Split and remove trailing empty element from terminal newline
	parts := strings.Split(content, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}

	if len(parts) <= n {
		return parts
	}
	return parts[len(parts)-n:]
}

// String returns all currently available content in the buffer. It satisfies
// the WaitForSettle compatibility requirement.
func (rb *RingBuffer) String() string {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	available := rb.written
	if available > int64(rb.size) {
		available = int64(rb.size)
	}

	if available == 0 {
		return ""
	}

	// Start reading from the oldest byte still in the buffer
	start := (rb.head - int(available) + rb.size*2) % rb.size

	out := make([]byte, available)
	for i := int64(0); i < available; i++ {
		out[i] = rb.buf[(start+int(i))%rb.size]
	}
	return string(out)
}

// Since returns content written since the last Mark call. Used for
// WaitForSettle compatibility.
func (rb *RingBuffer) Since() string {
	rb.mu.Lock()
	snap := rb.markSnapshot
	rb.mu.Unlock()
	return rb.ReadSince(snap)
}

// MarkSnapshot returns the current mark position. Used by waitForPattern to
// start reading from where ReadScreen last left off (unread data only).
func (rb *RingBuffer) MarkSnapshot() int64 {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.markSnapshot
}

// Mark advances the markSnapshot to the current written position. Subsequent
// calls to Since will return only data written after this point. Used for
// WaitForSettle compatibility.
func (rb *RingBuffer) Mark() {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.markSnapshot = rb.written
}
