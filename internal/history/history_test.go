package history

import (
	"sync"
	"testing"

	"github.com/clipwrap/clipwrapd/internal/domain"
)

func TestAppendSequential(t *testing.T) {
	inst := domain.InstanceID("test")
	store := NewStore(inst, 10)

	for i := 0; i < 5; i++ {
		e := store.Append("session", "text")
		if e.Sequence != domain.Sequence(i+1) {
			t.Errorf("event %d: got sequence %d, want %d", i, e.Sequence, i+1)
		}
		if e.InstanceID != inst {
			t.Errorf("event %d: got instance %q, want %q", i, e.InstanceID, inst)
		}
		if e.Session != "session" {
			t.Errorf("event %d: got session %q, want 'session'", i, e.Session)
		}
		if e.Text != "text" {
			t.Errorf("event %d: got text %q, want 'text'", i, e.Text)
		}
	}

	if store.Count() != 5 {
		t.Errorf("got count %d, want 5", store.Count())
	}
	if store.Sequence() != 5 {
		t.Errorf("got sequence %d, want 5", store.Sequence())
	}
}

func TestLatest(t *testing.T) {
	inst := domain.InstanceID("test")
	store := NewStore(inst, 10)

	// Empty store
	if _, ok := store.Latest(); ok {
		t.Error("expected no latest event on empty store")
	}

	_ = store.Append("s1", "first")
	e2 := store.Append("s2", "second")

	latest, ok := store.Latest()
	if !ok {
		t.Fatal("expected latest event")
	}
	if latest.ID != e2.ID {
		t.Errorf("got latest ID %q, want %q", latest.ID, e2.ID)
	}
}

func TestRingBufferEviction(t *testing.T) {
	inst := domain.InstanceID("test")
	capacity := 5
	store := NewStore(inst, capacity)

	// Fill beyond capacity
	for i := 0; i < 10; i++ {
		store.Append("session", "text")
	}

	if store.Count() != capacity {
		t.Errorf("got count %d, want %d", store.Count(), capacity)
	}
	if store.Sequence() != 10 {
		t.Errorf("got sequence %d, want 10", store.Sequence())
	}

	// Oldest retained should be sequence 6
	events := store.AllEvents()
	if len(events) != capacity {
		t.Fatalf("got %d events, want %d", len(events), capacity)
	}
	if events[0].Sequence != 6 {
		t.Errorf("oldest event sequence=%d, want 6", events[0].Sequence)
	}
	if events[4].Sequence != 10 {
		t.Errorf("newest event sequence=%d, want 10", events[4].Sequence)
	}
}

func TestAllEventsOrder(t *testing.T) {
	inst := domain.InstanceID("test")
	store := NewStore(inst, 10)

	for i := 0; i < 7; i++ {
		store.Append("session", "text")
	}

	events := store.AllEvents()
	for i, e := range events {
		if e.Sequence != domain.Sequence(i+1) {
			t.Errorf("event at index %d: got sequence %d, want %d", i, e.Sequence, i+1)
		}
	}
}

func TestSinceZeroCursor(t *testing.T) {
	inst := domain.InstanceID("test")
	store := NewStore(inst, 10)

	store.Append("s1", "first")
	store.Append("s2", "second")
	store.Append("s3", "third")

	result := store.Since(domain.ZeroCursor())
	if len(result.Events) != 3 {
		t.Errorf("got %d events, want 3", len(result.Events))
	}
	if result.Gap {
		t.Error("expected no gap for zero cursor")
	}
}

func TestSinceValidCursor(t *testing.T) {
	inst := domain.InstanceID("test")
	store := NewStore(inst, 10)

	for i := 0; i < 5; i++ {
		store.Append("session", "text")
	}

	// Replay from sequence 2
	cursor := domain.CursorFromParts(inst, 2)
	result := store.Since(cursor)

	if len(result.Events) != 3 {
		t.Errorf("got %d events, want 3", len(result.Events))
	}
	if result.Gap {
		t.Error("expected no gap for valid cursor")
	}
	if len(result.Events) > 0 && result.Events[0].Sequence != 3 {
		t.Errorf("first replayed event sequence=%d, want 3", result.Events[0].Sequence)
	}
}

func TestSinceLastEvent(t *testing.T) {
	inst := domain.InstanceID("test")
	store := NewStore(inst, 10)

	for i := 0; i < 5; i++ {
		store.Append("session", "text")
	}

	// Cursor at last event — nothing after
	cursor := domain.CursorFromParts(inst, 5)
	result := store.Since(cursor)

	if len(result.Events) != 0 {
		t.Errorf("got %d events, want 0", len(result.Events))
	}
}

func TestSinceEvictedCursor(t *testing.T) {
	inst := domain.InstanceID("test")
	capacity := 5
	store := NewStore(inst, capacity)

	// Fill beyond capacity so seq 1-5 are evicted
	for i := 0; i < 10; i++ {
		store.Append("session", "text")
	}

	// Cursor at evicted sequence 3
	cursor := domain.CursorFromParts(inst, 3)
	result := store.Since(cursor)

	if !result.Gap {
		t.Error("expected gap for evicted cursor")
	}
	if len(result.Events) != 5 {
		t.Errorf("got %d events, want 5", len(result.Events))
	}
	if len(result.Events) > 0 && result.Events[0].Sequence != 6 {
		t.Errorf("first event after gap sequence=%d, want 6", result.Events[0].Sequence)
	}
}

func TestSinceFutureCursor(t *testing.T) {
	inst := domain.InstanceID("test")
	store := NewStore(inst, 10)

	for i := 0; i < 5; i++ {
		store.Append("session", "text")
	}

	// Cursor at future sequence
	cursor := domain.CursorFromParts(inst, 100)
	result := store.Since(cursor)

	if len(result.Events) != 0 {
		t.Errorf("got %d events, want 0", len(result.Events))
	}
	if result.Gap {
		t.Error("expected no gap for future cursor")
	}
}

func TestSinceInstanceGap(t *testing.T) {
	inst := domain.InstanceID("current")
	store := NewStore(inst, 10)

	for i := 0; i < 5; i++ {
		store.Append("session", "text")
	}

	// Cursor from a different instance
	cursor := domain.CursorFromParts(domain.InstanceID("previous"), 3)
	result := store.Since(cursor)

	if !result.InstanceGap {
		t.Error("expected instance gap")
	}
	if len(result.Events) != 0 {
		t.Errorf("got %d events, want 0", len(result.Events))
	}
}

func TestSinceEmptyStore(t *testing.T) {
	inst := domain.InstanceID("test")
	store := NewStore(inst, 10)

	cursor := domain.CursorFromParts(inst, 5)
	result := store.Since(cursor)

	if len(result.Events) != 0 {
		t.Errorf("got %d events, want 0", len(result.Events))
	}
}

func TestConcurrentAppend(t *testing.T) {
	inst := domain.InstanceID("test")
	store := NewStore(inst, 1000)

	var wg sync.WaitGroup
	n := 100
	per := 10

	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < per; j++ {
				store.Append("session", "text")
			}
		}()
	}
	wg.Wait()

	if store.Sequence() != domain.Sequence(n*per) {
		t.Errorf("got sequence %d, want %d", store.Sequence(), n*per)
	}
	if store.Count() != n*per {
		t.Errorf("got count %d, want %d", store.Count(), n*per)
	}

	// Verify all sequences are unique
	events := store.AllEvents()
	seen := make(map[domain.Sequence]bool, len(events))
	for _, e := range events {
		if seen[e.Sequence] {
			t.Errorf("duplicate sequence %d", e.Sequence)
		}
		seen[e.Sequence] = true
	}
}

func TestConcurrentAppendAndRead(t *testing.T) {
	inst := domain.InstanceID("test")
	store := NewStore(inst, 1000)

	var wg sync.WaitGroup

	// Writers
	wg.Add(10)
	for i := 0; i < 10; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				store.Append("session", "text")
			}
		}()
	}

	// Readers
	wg.Add(5)
	for i := 0; i < 5; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = store.Latest()
				_ = store.Since(domain.ZeroCursor())
				_ = store.AllEvents()
			}
		}()
	}

	wg.Wait()
}

func TestReplayAfterEviction(t *testing.T) {
	inst := domain.InstanceID("test")
	capacity := 3
	store := NewStore(inst, capacity)

	// Fill and overflow
	for i := 0; i < 6; i++ {
		store.Append("session", "text")
	}

	// Replay from seq 4 (retained)
	cursor := domain.CursorFromParts(inst, 4)
	result := store.Since(cursor)

	if result.Gap {
		t.Error("expected no gap for retained cursor")
	}
	if len(result.Events) != 2 {
		t.Errorf("got %d events, want 2", len(result.Events))
	}

	// Replay from seq 3 (evicted)
	cursor = domain.CursorFromParts(inst, 3)
	result = store.Since(cursor)

	if !result.Gap {
		t.Error("expected gap for evicted cursor")
	}
	if len(result.Events) != 3 {
		t.Errorf("got %d events, want 3", len(result.Events))
	}
}

func TestCapacityBoundaries(t *testing.T) {
	inst := domain.InstanceID("test")

	// Capacity 1
	store := NewStore(inst, 1)
	_ = store.Append("s", "a")
	e2 := store.Append("s", "b")

	if store.Count() != 1 {
		t.Errorf("got count %d, want 1", store.Count())
	}
	latest, _ := store.Latest()
	if latest.ID != e2.ID {
		t.Error("expected latest to be second event")
	}

	// Capacity 2
	store = NewStore(inst, 2)
	store.Append("s", "a")
	store.Append("s", "b")
	store.Append("s", "c")

	events := store.AllEvents()
	if len(events) != 2 {
		t.Errorf("got %d events, want 2", len(events))
	}
	if events[0].Sequence != 2 {
		t.Errorf("oldest sequence=%d, want 2", events[0].Sequence)
	}
}

func TestClipboardTextPreservation(t *testing.T) {
	inst := domain.InstanceID("test")
	store := NewStore(inst, 10)

	// Text with newlines, spaces, special chars
	texts := []string{
		"hello\nworld",
		"  spaces  ",
		"tabs\there",
		"unicode: café",
		"",
		"\n\n\n",
	}

	for _, text := range texts {
		e := store.Append("session", text)
		if e.Text != text {
			t.Errorf("text mismatch: got %q, want %q", e.Text, text)
		}
	}
}
