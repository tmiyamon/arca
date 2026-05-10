package stdlib

import (
	"errors"
	"math"
	"testing"
)

func TestCheckedAddInt_Overflow(t *testing.T) {
	t.Parallel()
	if _, err := CheckedAddInt(math.MaxInt, 1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("MaxInt + 1: want ErrOverflow, got %v", err)
	}
	if _, err := CheckedAddInt(math.MinInt, -1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("MinInt + -1: want ErrOverflow, got %v", err)
	}
	v, err := CheckedAddInt(100, 200)
	if err != nil || v != 300 {
		t.Fatalf("100 + 200: want 300, got %d (err=%v)", v, err)
	}
}

func TestCheckedSubInt_Overflow(t *testing.T) {
	t.Parallel()
	if _, err := CheckedSubInt(math.MinInt, 1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("MinInt - 1: want ErrOverflow, got %v", err)
	}
	v, err := CheckedSubInt(10, 3)
	if err != nil || v != 7 {
		t.Fatalf("10 - 3: want 7, got %d (err=%v)", v, err)
	}
}

func TestCheckedMulInt_Overflow(t *testing.T) {
	t.Parallel()
	if _, err := CheckedMulInt(math.MaxInt, 2); !errors.Is(err, ErrOverflow) {
		t.Fatalf("MaxInt * 2: want ErrOverflow, got %v", err)
	}
	v, err := CheckedMulInt(7, 6)
	if err != nil || v != 42 {
		t.Fatalf("7 * 6: want 42, got %d (err=%v)", v, err)
	}
}

func TestCheckedDivInt_DivByZero(t *testing.T) {
	t.Parallel()
	if _, err := CheckedDivInt(10, 0); !errors.Is(err, ErrDivByZero) {
		t.Fatalf("10 / 0: want ErrDivByZero, got %v", err)
	}
}

func TestCheckedDivInt_MinIntNegOne(t *testing.T) {
	t.Parallel()
	if _, err := CheckedDivInt(math.MinInt, -1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("MinInt / -1: want ErrOverflow, got %v", err)
	}
}

func TestCheckedAddUInt_Overflow(t *testing.T) {
	t.Parallel()
	if _, err := CheckedAddUInt(math.MaxUint, 1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("MaxUint + 1: want ErrOverflow, got %v", err)
	}
}

func TestCheckedSubUInt_Underflow(t *testing.T) {
	t.Parallel()
	if _, err := CheckedSubUInt(3, 5); !errors.Is(err, ErrOverflow) {
		t.Fatalf("3 - 5 (uint): want ErrOverflow, got %v", err)
	}
}

func TestCheckedMulUInt_Overflow(t *testing.T) {
	t.Parallel()
	if _, err := CheckedMulUInt(math.MaxUint, 2); !errors.Is(err, ErrOverflow) {
		t.Fatalf("MaxUint * 2: want ErrOverflow, got %v", err)
	}
}

func TestCheckedDivUInt_DivByZero(t *testing.T) {
	t.Parallel()
	if _, err := CheckedDivUInt(10, 0); !errors.Is(err, ErrDivByZero) {
		t.Fatalf("10 / 0 (uint): want ErrDivByZero, got %v", err)
	}
}
