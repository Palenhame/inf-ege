package ws

import (
	"sync"
	"testing"
	"time"
)

func TestRingBuffer_BasicPushPop(t *testing.T) {
	rb := NewRingBuffer(4)
	if rb.Capacity() != 4 {
		t.Fatalf("expected capacity 4, got %d", rb.Capacity())
	}

	rb.Push([]byte("item1"))
	rb.Push([]byte("item2"))

	if rb.Len() != 2 {
		t.Fatalf("expected length 2, got %d", rb.Len())
	}

	item1, ok := rb.Pop()
	if !ok || string(item1) != "item1" {
		t.Fatalf("expected item1, got %s (ok=%v)", string(item1), ok)
	}

	item2, ok := rb.TryPop()
	if !ok || string(item2) != "item2" {
		t.Fatalf("expected item2, got %s (ok=%v)", string(item2), ok)
	}

	if rb.Len() != 0 {
		t.Fatalf("expected length 0, got %d", rb.Len())
	}

	_, ok = rb.TryPop()
	if ok {
		t.Fatal("expected TryPop to fail on empty buffer")
	}
}

func TestRingBuffer_OverflowDropsOldest(t *testing.T) {
	rb := NewRingBuffer(3)

	rb.Push([]byte("msg1"))
	rb.Push([]byte("msg2"))
	rb.Push([]byte("msg3"))

	if rb.Dropped() != 0 {
		t.Fatalf("expected 0 dropped, got %d", rb.Dropped())
	}

	// This 4th push should drop "msg1"
	rb.Push([]byte("msg4"))
	if rb.Dropped() != 1 {
		t.Fatalf("expected 1 dropped, got %d", rb.Dropped())
	}

	// This 5th push should drop "msg2"
	rb.Push([]byte("msg5"))
	if rb.Dropped() != 2 {
		t.Fatalf("expected 2 dropped, got %d", rb.Dropped())
	}

	if rb.Len() != 3 {
		t.Fatalf("expected len 3, got %d", rb.Len())
	}

	// Remaining items must be msg3, msg4, msg5
	expected := []string{"msg3", "msg4", "msg5"}
	for _, exp := range expected {
		val, ok := rb.Pop()
		if !ok || string(val) != exp {
			t.Fatalf("expected %s, got %s", exp, string(val))
		}
	}
}

func TestRingBuffer_CloseWakesPop(t *testing.T) {
	rb := NewRingBuffer(4)

	var wg sync.WaitGroup
	wg.Add(1)

	var popped []byte
	var ok bool
	go func() {
		defer wg.Done()
		popped, ok = rb.Pop()
	}()

	time.Sleep(20 * time.Millisecond)
	rb.Close()
	wg.Wait()

	if ok || popped != nil {
		t.Fatalf("expected Pop to return nil, false on close, got %v, %v", popped, ok)
	}

	if !rb.IsClosed() {
		t.Fatal("expected IsClosed to be true")
	}

	if rb.Push([]byte("fail")) {
		t.Fatal("expected Push to fail on closed buffer")
	}
}

func TestRingBuffer_ConcurrentProducersAndConsumers(t *testing.T) {
	rb := NewRingBuffer(100)
	totalMsgs := 1000
	numProducers := 5

	var prodWg sync.WaitGroup
	for p := 0; p < numProducers; p++ {
		prodWg.Add(1)
		go func() {
			defer prodWg.Done()
			for i := 0; i < totalMsgs/numProducers; i++ {
				rb.Push([]byte("data"))
			}
		}()
	}

	var consumed int
	doneCh := make(chan struct{})
	go func() {
		for {
			_, ok := rb.Pop()
			if !ok {
				break
			}
			consumed++
		}
		close(doneCh)
	}()

	prodWg.Wait()
	rb.Close()
	<-doneCh

	totalProcessed := uint64(consumed) + rb.Dropped()
	if totalProcessed != uint64(totalMsgs) {
		t.Fatalf("expected consumed + dropped == %d, got %d (consumed=%d, dropped=%d)",
			totalMsgs, totalProcessed, consumed, rb.Dropped())
	}
}
