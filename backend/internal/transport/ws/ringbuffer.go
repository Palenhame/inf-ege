package ws

import (
	"sync"
)

// RingBuffer is a thread-safe, non-blocking FIFO ring buffer for message byte slices.
// When the buffer reaches its maximum capacity, pushing a new message drops the oldest
// message to guarantee that slow consumers do not block broadcasters.
type RingBuffer struct {
	mu       sync.Mutex
	notEmpty *sync.Cond
	notifyCh chan struct{}
	buf      [][]byte
	head     int
	tail     int
	size     int
	capacity int
	closed   bool
	dropped  uint64
}

// NewRingBuffer allocates a new RingBuffer with the given capacity.
// Capacity defaults to 512 if non-positive.
func NewRingBuffer(capacity int) *RingBuffer {
	if capacity <= 0 {
		capacity = 512
	}
	rb := &RingBuffer{
		buf:      make([][]byte, capacity),
		capacity: capacity,
		notifyCh: make(chan struct{}, 1),
	}
	rb.notEmpty = sync.NewCond(&rb.mu)
	return rb
}

// Push appends a message to the buffer without blocking.
// If the buffer is full, it silently drops the oldest message.
// Returns false if the buffer has been closed.
func (rb *RingBuffer) Push(msg []byte) bool {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if rb.closed {
		return false
	}

	if rb.size == rb.capacity {
		// Drop oldest element
		rb.buf[rb.head] = nil
		rb.head = (rb.head + 1) % rb.capacity
		rb.size--
		rb.dropped++
	}

	rb.buf[rb.tail] = msg
	rb.tail = (rb.tail + 1) % rb.capacity
	rb.size++

	rb.notEmpty.Signal()
	select {
	case rb.notifyCh <- struct{}{}:
	default:
	}

	return true
}

// Pop blocks until a message is available or the buffer is closed.
// Returns (nil, false) when closed and empty.
func (rb *RingBuffer) Pop() ([]byte, bool) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	for rb.size == 0 && !rb.closed {
		rb.notEmpty.Wait()
	}

	if rb.size == 0 && rb.closed {
		return nil, false
	}

	msg := rb.buf[rb.head]
	rb.buf[rb.head] = nil // Avoid memory retention
	rb.head = (rb.head + 1) % rb.capacity
	rb.size--

	return msg, true
}

// TryPop attempts to pop an item without blocking.
// Returns (nil, false) if empty or closed.
func (rb *RingBuffer) TryPop() ([]byte, bool) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if rb.size == 0 {
		return nil, false
	}

	msg := rb.buf[rb.head]
	rb.buf[rb.head] = nil
	rb.head = (rb.head + 1) % rb.capacity
	rb.size--

	return msg, true
}

// Notify returns a receive-only notification channel that receives a signal
// whenever new items are pushed or the buffer is closed.
func (rb *RingBuffer) Notify() <-chan struct{} {
	return rb.notifyCh
}

// Close marks the buffer as closed and wakes all waiting consumers.
func (rb *RingBuffer) Close() {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if !rb.closed {
		rb.closed = true
		rb.notEmpty.Broadcast()
		select {
		case rb.notifyCh <- struct{}{}:
		default:
		}
	}
}

// Len returns current number of queued items.
func (rb *RingBuffer) Len() int {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.size
}

// Capacity returns the maximum capacity of the buffer.
func (rb *RingBuffer) Capacity() int {
	return rb.capacity
}

// Dropped returns the number of messages dropped due to buffer overflow.
func (rb *RingBuffer) Dropped() uint64 {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.dropped
}

// IsClosed reports whether the buffer has been closed.
func (rb *RingBuffer) IsClosed() bool {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.closed
}
