// Package history implements a bounded in-memory ring buffer for clipboard events.
package history

import (
	"fmt"
	"sync"
	"time"

	"github.com/clipwrap/clipwrapd/internal/domain"
)

// Store is a bounded, concurrency-safe ring buffer for clipboard events.
type Store struct {
	mu       sync.RWMutex
	instance domain.InstanceID
	buf      []domain.ClipboardEvent
	capacity int
	head     int // next write position
	full     bool
	seq      domain.Sequence
}

// NewStore creates a new event store with the given capacity.
func NewStore(instance domain.InstanceID, capacity int) *Store {
	return &Store{
		instance: instance,
		buf:      make([]domain.ClipboardEvent, capacity),
		capacity: capacity,
	}
}

// Append adds a new clipboard event to the store.
// Returns the event with its assigned ID and sequence number.
func (s *Store) Append(session, text string) domain.ClipboardEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.seq++
	id := domain.NewEventID(s.instance, s.seq)
	event := domain.ClipboardEvent{
		ID:         id,
		InstanceID: s.instance,
		Sequence:   s.seq,
		Timestamp:  time.Now().UTC(),
		Session:    session,
		Text:       text,
	}

	s.buf[s.head] = event
	s.head = (s.head + 1) % s.capacity
	if s.head == 0 && !s.full {
		// First wrap-around
		s.full = true
	}

	return event
}

// Latest returns the most recently appended event, if any.
func (s *Store) Latest() (domain.ClipboardEvent, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.seq == 0 {
		var zero domain.ClipboardEvent
		return zero, false
	}

	// The last event is at head-1 (modulo capacity)
	idx := s.head - 1
	if idx < 0 {
		idx = s.capacity - 1
	}
	return s.buf[idx], true
}

// Count returns the number of events currently in the store.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.full {
		return s.capacity
	}
	return int(s.seq)
}

// Sequence returns the current sequence number (total events appended).
func (s *Store) Sequence() domain.Sequence {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.seq
}

// ReplayResult contains the events returned by Since, along with gap information.
type ReplayResult struct {
	Events      []domain.ClipboardEvent
	Gap         bool       // true if there's a gap between cursor and first returned event
	InstanceGap bool       // true if the cursor references a different daemon instance
	OldestSeq   domain.Sequence // the oldest available sequence in the store
}

// Since returns events after the given cursor position.
// If the cursor is zero, returns all retained events.
func (s *Store) Since(cursor domain.Cursor) ReplayResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := ReplayResult{}

	// Check for instance gap
	if !cursor.IsZero() && cursor.InstanceID != s.instance {
		result.InstanceGap = true
		result.OldestSeq = s.oldestSequence()
		return result
	}

	count := s.countLocked()
	if count == 0 {
		result.OldestSeq = 0
		return result
	}

	result.OldestSeq = s.oldestSequence()

	// If cursor is zero, return all events
	if cursor.IsZero() {
		result.Events = s.allEventsLocked()
		return result
	}

	// Find the cursor position and collect events after it
	startIdx := -1
	for i := 0; i < count; i++ {
		idx := s.readIndex(i)
		if s.buf[idx].Sequence == cursor.Sequence {
			startIdx = i
			break
		}
	}

	if startIdx == -1 {
		// Cursor not found — could be evicted or future
		if cursor.Sequence > s.seq {
			// Future sequence — no events to return
			return result
		}
		// Cursor was evicted — find the first available event after cursor
		result.Gap = true
		// Find first event with sequence > cursor.Sequence
		for i := 0; i < count; i++ {
			idx := s.readIndex(i)
			if s.buf[idx].Sequence > cursor.Sequence {
				startIdx = i
				break
			}
		}
		if startIdx == -1 {
			// All events are before the cursor (shouldn't happen if cursor.Sequence <= s.seq)
			return result
		}
	} else {
		// Start after the cursor position
		startIdx++
	}

	// Collect events from startIdx to end
	if startIdx >= count {
		return result
	}

	n := count - startIdx
	result.Events = make([]domain.ClipboardEvent, n)
	for i := 0; i < n; i++ {
		idx := s.readIndex(startIdx + i)
		result.Events[i] = s.buf[idx]
	}

	return result
}

// AllEvents returns all retained events in chronological order.
func (s *Store) AllEvents() []domain.ClipboardEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.allEventsLocked()
}

func (s *Store) allEventsLocked() []domain.ClipboardEvent {
	count := s.countLocked()
	if count == 0 {
		return nil
	}
	events := make([]domain.ClipboardEvent, count)
	for i := 0; i < count; i++ {
		idx := s.readIndex(i)
		events[i] = s.buf[idx]
	}
	return events
}

func (s *Store) countLocked() int {
	if s.full {
		return s.capacity
	}
	return int(s.seq)
}

func (s *Store) oldestSequence() domain.Sequence {
	if s.seq == 0 {
		return 0
	}
	if !s.full {
		return 1
	}
	return s.seq - domain.Sequence(s.capacity) + 1
}

func (s *Store) readIndex(logical int) int {
	// When not full, head points to next write, and events start at 0
	// When full, head points to oldest event
	var start int
	if s.full {
		start = s.head
	} else {
		start = 0
	}
	return (start + logical) % s.capacity
}

// ErrInvalidCursor is returned when a cursor is malformed.
var ErrInvalidCursor = fmt.Errorf("invalid cursor")
