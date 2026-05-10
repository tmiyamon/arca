package stdlib

import (
	"errors"
	"math"
	"testing"
)

func TestBigInt_RoundTrip(t *testing.T) {
	t.Parallel()
	a := NewBigInt(42)
	if a.String() != "42" {
		t.Fatalf("NewBigInt(42).String(): want %q, got %q", "42", a.String())
	}
}

func TestBigInt_FromString_Valid(t *testing.T) {
	t.Parallel()
	v, err := BigIntFromString("123456789012345678901234567890")
	if err != nil {
		t.Fatalf("BigIntFromString: %v", err)
	}
	if v.String() != "123456789012345678901234567890" {
		t.Errorf("round-trip: got %q", v.String())
	}
}

func TestBigInt_FromString_Invalid(t *testing.T) {
	t.Parallel()
	if _, err := BigIntFromString("abc"); !errors.Is(err, ErrBigIntInvalid) {
		t.Fatalf("want ErrBigIntInvalid, got %v", err)
	}
}

func TestBigInt_AddOverflowsInt(t *testing.T) {
	t.Parallel()
	a := NewBigInt(math.MaxInt64)
	b := NewBigInt(math.MaxInt64)
	c := a.Add(b)
	// Sum exceeds int64 — String stays correct, ToInt errors.
	if c.String() != "18446744073709551614" {
		t.Errorf("MaxInt64 + MaxInt64: got %q", c.String())
	}
	if _, err := c.ToInt(); !errors.Is(err, ErrBigIntRange) {
		t.Errorf("ToInt on out-of-range value: want ErrBigIntRange, got %v", err)
	}
}

func TestBigInt_ToInt_InRange(t *testing.T) {
	t.Parallel()
	v, err := NewBigInt(42).ToInt()
	if err != nil || v != 42 {
		t.Errorf("ToInt(42): want 42, got %d (err=%v)", v, err)
	}
}

func TestBigInt_DivByZero(t *testing.T) {
	t.Parallel()
	a := NewBigInt(10)
	z := NewBigInt(0)
	if _, err := a.Div(z); !errors.Is(err, ErrDivByZero) {
		t.Errorf("Div by zero: want ErrDivByZero, got %v", err)
	}
	if _, err := a.Mod(z); !errors.Is(err, ErrDivByZero) {
		t.Errorf("Mod by zero: want ErrDivByZero, got %v", err)
	}
}

func TestBigInt_Comparison(t *testing.T) {
	t.Parallel()
	a := NewBigInt(5)
	b := NewBigInt(10)
	if !a.Lt(b) || a.Gt(b) || a.Eq(b) {
		t.Errorf("5 vs 10: Lt expected, Gt/Eq not")
	}
	if !a.Eq(NewBigInt(5)) {
		t.Errorf("5 == 5: Eq expected")
	}
}

func TestBigInt_NegMulRoundTrip(t *testing.T) {
	t.Parallel()
	a := NewBigInt(7)
	b := a.Mul(NewBigInt(-3)).Neg() // 7 * -3 = -21, negate → 21
	if v, _ := b.ToInt(); v != 21 {
		t.Errorf("(7 * -3).Neg(): want 21, got %d", v)
	}
}
