// Package domain defines the core clipboard event model.
package domain

import (
	"crypto/rand"
	"fmt"
	"time"
)

// InstanceID uniquely identifies a daemon instance.
type InstanceID string

// NewInstanceID generates a random instance ID.
func NewInstanceID() InstanceID {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return InstanceID(fmt.Sprintf("%x", b))
}

// Sequence is a monotonically increasing event sequence number, starting at 1.
type Sequence uint64

// EventID uniquely identifies a clipboard event within a daemon instance.
// Format: "{instance-id}:{sequence}"
type EventID string

// NewEventID creates a new event ID from an instance ID and sequence number.
func NewEventID(instance InstanceID, seq Sequence) EventID {
	return EventID(fmt.Sprintf("%s:%d", instance, seq))
}

// ParseEventID parses an EventID into its instance ID and sequence components.
// Returns zero values if the format is invalid.
func ParseEventID(id EventID) (InstanceID, Sequence, bool) {
	s := string(id)
	colon := -1
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			colon = i
			break
		}
	}
	if colon < 0 {
		return "", 0, false
	}
	inst := InstanceID(s[:colon])
	var seq Sequence
	_, err := fmt.Sscanf(s[colon+1:], "%d", &seq)
	if err != nil {
		return "", 0, false
	}
	return inst, seq, true
}

// Cursor points to a specific event in history for replay purposes.
type Cursor struct {
	InstanceID InstanceID
	Sequence   Sequence
}

// NewCursor creates a cursor from an EventID.
func NewCursor(id EventID) (Cursor, bool) {
	inst, seq, ok := ParseEventID(id)
	if !ok {
		return Cursor{}, false
	}
	return Cursor{InstanceID: inst, Sequence: seq}, true
}

// CursorFromParts creates a cursor from an instance ID and sequence number.
func CursorFromParts(inst InstanceID, seq Sequence) Cursor {
	return Cursor{InstanceID: inst, Sequence: seq}
}

// ZeroCursor returns a cursor that represents "no history" (future events only).
func ZeroCursor() Cursor {
	return Cursor{}
}

// IsZero returns true if the cursor is the zero value.
func (c Cursor) IsZero() bool {
	return c == Cursor{}
}

// ClipboardEvent represents an immutable clipboard capture event.
type ClipboardEvent struct {
	ID        EventID    `json:"id"`
	InstanceID InstanceID `json:"instance_id"`
	Sequence  Sequence   `json:"sequence"`
	Timestamp time.Time  `json:"timestamp"`
	Session   string     `json:"session"`
	Text      string     `json:"text"`
}

// CursorAt returns the cursor pointing to this event.
func (e ClipboardEvent) CursorAt() Cursor {
	return Cursor{InstanceID: e.InstanceID, Sequence: e.Sequence}
}
