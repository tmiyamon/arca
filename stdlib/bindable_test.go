package stdlib

import (
	"testing"
	"time"
)

// Scan(nil) on a pointer-typed slot represents an SQL NULL for an
// Option-backed field — Set should flip to true so freeze reads the
// zero (typed-nil) Value as None, instead of erroring "unset".
func TestBindableSlot_Scan_NilOnPointerT(t *testing.T) {
	t.Parallel()
	var s BindableSlot[*time.Time]
	if err := s.Scan(nil); err != nil {
		t.Fatalf("Scan(nil) returned err: %v", err)
	}
	if !s.Set {
		t.Errorf("Set: want true (input was provided as null), got false")
	}
	if s.Value != nil {
		t.Errorf("Value: want nil, got %v", s.Value)
	}
}

// Scan(nil) on a non-pointer-typed slot must reject — a NULL column
// can't be represented in a non-nullable Arca type, and the Scanner
// error halts rows.Scan before freeze.
func TestBindableSlot_Scan_NilOnNonPointerT(t *testing.T) {
	t.Parallel()
	var s BindableSlot[int]
	if err := s.Scan(nil); err == nil {
		t.Fatalf("Scan(nil) on int slot: want error, got nil (Set=%v Value=%v)", s.Set, s.Value)
	}
	if s.Set {
		t.Errorf("Set: want false on rejected scan, got true")
	}
}

// Pointer-typed slots wrap incoming non-nil values into a freshly
// allocated pointer (Some semantics).
func TestBindableSlot_Scan_NonNilOnPointerT(t *testing.T) {
	t.Parallel()
	var s BindableSlot[*time.Time]
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	if err := s.Scan(now); err != nil {
		t.Fatalf("Scan(time.Time) returned err: %v", err)
	}
	if !s.Set {
		t.Errorf("Set: want true, got false")
	}
	if s.Value == nil || !s.Value.Equal(now) {
		t.Errorf("Value: want %v, got %v", now, s.Value)
	}
}

// Non-pointer slots accept directly-assignable / convertible values
// and reject others.
func TestBindableSlot_Scan_ConvertibleOnNonPointerT(t *testing.T) {
	t.Parallel()
	var s BindableSlot[int]
	// SQL drivers commonly hand back int64; we accept via Convert.
	if err := s.Scan(int64(42)); err != nil {
		t.Fatalf("Scan(int64) returned err: %v", err)
	}
	if !s.Set || s.Value != 42 {
		t.Errorf("want Set=true Value=42, got Set=%v Value=%v", s.Set, s.Value)
	}
}
