// Package broker distributes clipboard events to WebSocket subscribers.
package broker

import (
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/clipwrap/clipwrapd/internal/domain"
)

// Subscriber receives clipboard events from the broker.
type Subscriber struct {
	ID     string
	Events chan domain.ClipboardEvent
	Closed chan struct{}
	mu     sync.Mutex
	closed bool
}

// Close closes the subscriber's event channel.
func (s *Subscriber) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.Events)
	close(s.Closed)
}

// IsClosed reports whether the subscriber has been closed.
func (s *Subscriber) IsClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// TrySend attempts to send an event without blocking.
// Returns false if the subscriber is closed or the queue is full.
func (s *Subscriber) TrySend(event domain.ClipboardEvent) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	select {
	case s.Events <- event:
		return true
	default:
		return false
	}
}

// Broker distributes events to subscribers.
type Broker struct {
	mu          sync.RWMutex
	subscribers []*Subscriber
	quit        chan struct{}
	wg          sync.WaitGroup
	queueSize   int
	nextID      atomic.Int64
}

// NewBroker creates a new broker with the given per-subscriber queue size.
func NewBroker(queueSize int) *Broker {
	return &Broker{
		subscribers: make([]*Subscriber, 0, 4),
		quit:        make(chan struct{}),
		queueSize:   queueSize,
	}
}

// Subscribe creates a new subscriber that receives events published after subscription.
func (b *Broker) Subscribe() *Subscriber {
	id := b.nextID.Add(1)
	sub := &Subscriber{
		ID:     strconv.Itoa(int(id)),
		Events: make(chan domain.ClipboardEvent, b.queueSize),
		Closed: make(chan struct{}),
	}

	b.mu.Lock()
	b.subscribers = append(b.subscribers, sub)
	b.mu.Unlock()

	b.wg.Add(1)
	go b.monitorSubscriber(sub)

	return sub
}

// monitorSubscriber waits for the subscriber to close and removes it.
func (b *Broker) monitorSubscriber(sub *Subscriber) {
	defer b.wg.Done()
	select {
	case <-sub.Closed:
	case <-b.quit:
		sub.Close()
	}
	b.mu.Lock()
	b.removeSubscriber(sub)
	b.mu.Unlock()
}

func (b *Broker) removeSubscriber(sub *Subscriber) {
	for i, s := range b.subscribers {
		if s == sub {
			b.subscribers = append(b.subscribers[:i], b.subscribers[i+1:]...)
			return
		}
	}
}

// Publish sends an event to all active subscribers.
// Slow subscribers whose queue is full are closed.
func (b *Broker) Publish(event domain.ClipboardEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, sub := range b.subscribers {
		if !sub.TrySend(event) {
			sub.Close()
		}
	}
}

// Count returns the number of active subscribers.
func (b *Broker) Count() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers)
}

// Shutdown stops the broker, closing all subscribers and waiting for cleanup.
func (b *Broker) Shutdown() {
	select {
	case <-b.quit:
		return
	default:
		close(b.quit)
	}

	b.mu.Lock()
	for _, sub := range b.subscribers {
		sub.Close()
	}
	b.subscribers = nil
	b.mu.Unlock()

	b.wg.Wait()
}
