package broker

import (
	"sync"
	"testing"
	"time"

	"github.com/clipwrap/clipwrapd/internal/domain"
)

func makeEvent(seq domain.Sequence) domain.ClipboardEvent {
	return domain.ClipboardEvent{
		ID:         domain.NewEventID(domain.InstanceID("test"), seq),
		InstanceID: "test",
		Sequence:   seq,
	}
}

func TestSubscribeAndPublish(t *testing.T) {
	b := NewBroker(10)
	defer b.Shutdown()

	sub := b.Subscribe()
	if b.Count() != 1 {
		t.Errorf("got count %d, want 1", b.Count())
	}

	b.Publish(makeEvent(1))
	b.Publish(makeEvent(2))

	received := 0
	timeout := time.After(100 * time.Millisecond)
	for received < 2 {
		select {
		case ev, ok := <-sub.Events:
			if !ok {
				t.Fatal("channel closed unexpectedly")
			}
			received++
			if ev.Sequence != domain.Sequence(received) {
				t.Errorf("got sequence %d, want %d", ev.Sequence, received)
			}
		case <-timeout:
			t.Fatalf("received %d events, want 2", received)
		}
	}
}

func TestMultipleSubscribers(t *testing.T) {
	b := NewBroker(10)
	defer b.Shutdown()

	sub1 := b.Subscribe()
	sub2 := b.Subscribe()

	if b.Count() != 2 {
		t.Errorf("got count %d, want 2", b.Count())
	}

	b.Publish(makeEvent(1))

	for i, sub := range []*Subscriber{sub1, sub2} {
		select {
		case ev := <-sub.Events:
			if ev.Sequence != 1 {
				t.Errorf("sub %d: got sequence %d, want 1", i, ev.Sequence)
			}
		case <-time.After(100 * time.Millisecond):
			t.Errorf("sub %d: did not receive event", i)
		}
	}
}

func TestSubscriberClose(t *testing.T) {
	b := NewBroker(10)
	defer b.Shutdown()

	sub := b.Subscribe()
	sub.Close()

	select {
	case <-sub.Closed:
		// OK
	case <-time.After(100 * time.Millisecond):
		t.Error("expected Closed channel to be closed")
	}

	if !sub.IsClosed() {
		t.Error("expected subscriber to be closed")
	}

	// Double close should be safe
	sub.Close()
}

func TestSlowSubscriberClosed(t *testing.T) {
	b := NewBroker(2)
	defer b.Shutdown()

	sub := b.Subscribe()

	// Fill the queue
	b.Publish(makeEvent(1))
	b.Publish(makeEvent(2))

	// Next publish should close the slow subscriber
	b.Publish(makeEvent(3))

	select {
	case <-sub.Closed:
		// OK — slow subscriber was closed
	case <-time.After(100 * time.Millisecond):
		t.Error("expected slow subscriber to be closed")
	}

	// Allow monitor goroutine time to clean up
	time.Sleep(50 * time.Millisecond)

	if b.Count() != 0 {
		t.Errorf("got count %d, want 0", b.Count())
	}
}

func TestSubscriberCleanupOnClose(t *testing.T) {
	b := NewBroker(10)
	defer b.Shutdown()

	sub := b.Subscribe()
	if b.Count() != 1 {
		t.Errorf("got count %d, want 1", b.Count())
	}

	sub.Close()
	time.Sleep(50 * time.Millisecond) // allow monitor to clean up

	if b.Count() != 0 {
		t.Errorf("got count %d, want 0", b.Count())
	}
}

func TestShutdown(t *testing.T) {
	b := NewBroker(10)

	sub1 := b.Subscribe()
	sub2 := b.Subscribe()

	b.Shutdown()

	// Should be idempotent
	b.Shutdown()

	if b.Count() != 0 {
		t.Errorf("got count %d, want 0", b.Count())
	}

	// Subscribers should be closed
	for i, sub := range []*Subscriber{sub1, sub2} {
		select {
		case <-sub.Closed:
		case <-time.After(100 * time.Millisecond):
			t.Errorf("sub %d: not closed after shutdown", i)
		}
	}
}

func TestPublishAfterSubscriberClose(t *testing.T) {
	b := NewBroker(10)
	defer b.Shutdown()

	sub1 := b.Subscribe()
	sub2 := b.Subscribe()

	sub2.Close()
	time.Sleep(50 * time.Millisecond)

	// Publish should only go to sub1
	b.Publish(makeEvent(1))

	select {
	case ev := <-sub1.Events:
		if ev.Sequence != 1 {
			t.Errorf("got sequence %d, want 1", ev.Sequence)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("sub1 did not receive event")
	}
}

func TestConcurrentSubscribeAndPublish(t *testing.T) {
	b := NewBroker(100)
	defer b.Shutdown()

	var wg sync.WaitGroup

	// Subscribers
	wg.Add(5)
	for i := 0; i < 5; i++ {
		go func() {
			defer wg.Done()
			sub := b.Subscribe()
			defer sub.Close()
			for j := 0; j < 20; j++ {
				select {
				case <-sub.Events:
				case <-time.After(200 * time.Millisecond):
				}
			}
		}()
	}

	// Publisher
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			b.Publish(makeEvent(domain.Sequence(i + 1)))
		}
	}()

	wg.Wait()
}

func TestEventOrdering(t *testing.T) {
	b := NewBroker(100)
	defer b.Shutdown()

	sub := b.Subscribe()

	// Publish 20 events
	for i := 1; i <= 20; i++ {
		b.Publish(makeEvent(domain.Sequence(i)))
	}

	// Receive and check order
	for i := 1; i <= 20; i++ {
		select {
		case ev := <-sub.Events:
			if ev.Sequence != domain.Sequence(i) {
				t.Errorf("got sequence %d, want %d", ev.Sequence, i)
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("event %d not received", i)
		}
	}
}

func TestSubscriberIDUniqueness(t *testing.T) {
	b := NewBroker(10)
	defer b.Shutdown()

	ids := make(map[string]bool)
	for i := 0; i < 10; i++ {
		sub := b.Subscribe()
		if ids[sub.ID] {
			t.Errorf("duplicate subscriber ID: %s", sub.ID)
		}
		ids[sub.ID] = true
		sub.Close()
	}
}
