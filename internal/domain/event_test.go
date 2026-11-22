package domain

import (
	"testing"
)

func TestNewInstanceID(t *testing.T) {
	id1 := NewInstanceID()
	id2 := NewInstanceID()

	if id1 == "" {
		t.Fatal("expected non-empty instance ID")
	}
	if id1 == id2 {
		t.Fatal("expected unique instance IDs")
	}
}

func TestNewEventID(t *testing.T) {
	inst := InstanceID("abc123")
	seq := Sequence(42)
	id := NewEventID(inst, seq)

	if id != "abc123:42" {
		t.Fatalf("expected 'abc123:42', got '%s'", id)
	}
}

func TestParseEventID(t *testing.T) {
	tests := []struct {
		id       EventID
		wantInst InstanceID
		wantSeq  Sequence
		wantOK   bool
	}{
		{"abc123:42", "abc123", 42, true},
		{"abc123:0", "abc123", 0, true},
		{"x:1", "x", 1, true},
		{"", "", 0, false},
		{"no-colon", "", 0, false},
		{"a:b:c", "", 0, false},
		{"abc:xyz", "", 0, false},
	}

	for _, tt := range tests {
		inst, seq, ok := ParseEventID(tt.id)
		if ok != tt.wantOK {
			t.Errorf("ParseEventID(%q): got ok=%v, want %v", tt.id, ok, tt.wantOK)
		}
		if ok {
			if inst != tt.wantInst {
				t.Errorf("ParseEventID(%q): got inst=%q, want %q", tt.id, inst, tt.wantInst)
			}
			if seq != tt.wantSeq {
				t.Errorf("ParseEventID(%q): got seq=%d, want %d", tt.id, seq, tt.wantSeq)
			}
		}
	}
}

func TestEventIDRoundTrip(t *testing.T) {
	inst := NewInstanceID()
	for seq := Sequence(1); seq <= 100; seq++ {
		id := NewEventID(inst, seq)
		parsedInst, parsedSeq, ok := ParseEventID(id)
		if !ok {
			t.Fatalf("failed to parse EventID %q", id)
		}
		if parsedInst != inst {
			t.Errorf("sequence %d: parsed inst=%q, want %q", seq, parsedInst, inst)
		}
		if parsedSeq != seq {
			t.Errorf("sequence %d: parsed seq=%d, want %d", seq, parsedSeq, seq)
		}
	}
}

func TestNewCursor(t *testing.T) {
	id := NewEventID(InstanceID("inst1"), 10)
	cursor, ok := NewCursor(id)
	if !ok {
		t.Fatal("expected valid cursor")
	}
	if cursor.InstanceID != "inst1" {
		t.Errorf("got instance=%q, want 'inst1'", cursor.InstanceID)
	}
	if cursor.Sequence != 10 {
		t.Errorf("got sequence=%d, want 10", cursor.Sequence)
	}

	_, ok = NewCursor(EventID("invalid"))
	if ok {
		t.Error("expected invalid cursor to fail")
	}
}

func TestZeroCursor(t *testing.T) {
	c := ZeroCursor()
	if !c.IsZero() {
		t.Error("expected zero cursor")
	}

	c2 := CursorFromParts(InstanceID("x"), 1)
	if c2.IsZero() {
		t.Error("expected non-zero cursor")
	}
}

func TestClipboardEventCursorAt(t *testing.T) {
	e := ClipboardEvent{
		ID:         NewEventID(InstanceID("inst"), 5),
		InstanceID: "inst",
		Sequence:   5,
	}
	c := e.CursorAt()
	if c.InstanceID != "inst" || c.Sequence != 5 {
		t.Errorf("got cursor=%+v, want inst:5", c)
	}
}
