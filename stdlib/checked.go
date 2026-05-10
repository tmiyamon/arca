package stdlib

import (
	"errors"
	"fmt"
	"math/bits"
)

// ErrOverflow signals an arithmetic overflow detected by a checked op.
// Stays a stable sentinel so callers can `errors.Is(err, ErrOverflow)`.
var ErrOverflow = errors.New("arithmetic overflow")

// ErrDivByZero signals integer division by zero. Float div by zero yields
// Inf in Go's IEEE 754 semantics, so the float helpers don't return this.
var ErrDivByZero = errors.New("division by zero")

// CheckedAddInt / CheckedSubInt / CheckedMulInt / CheckedDivInt are the
// Result-returning counterparts of the panic-checked `+ - * /` emit
// (`__addInt` etc.). Use them when a runtime panic isn't acceptable —
// e.g. user-driven arithmetic where overflow should bubble up as a
// recoverable error.
func CheckedAddInt(a, b int) (int, error) {
	s := a + b
	if (a >= 0) == (b >= 0) && (a >= 0) != (s >= 0) {
		return 0, fmt.Errorf("%w: Int addition %d + %d", ErrOverflow, a, b)
	}
	return s, nil
}

func CheckedSubInt(a, b int) (int, error) {
	d := a - b
	if (a >= 0) != (b >= 0) && (a >= 0) != (d >= 0) {
		return 0, fmt.Errorf("%w: Int subtraction %d - %d", ErrOverflow, a, b)
	}
	return d, nil
}

func CheckedMulInt(a, b int) (int, error) {
	p := a * b
	if a != 0 && p/a != b {
		return 0, fmt.Errorf("%w: Int multiplication %d * %d", ErrOverflow, a, b)
	}
	return p, nil
}

// CheckedDivInt rejects b == 0 and the special MinInt / -1 overflow.
func CheckedDivInt(a, b int) (int, error) {
	if b == 0 {
		return 0, fmt.Errorf("%w: Int %d / 0", ErrDivByZero, a)
	}
	const minInt = -1 << 63
	if a == minInt && b == -1 {
		return 0, fmt.Errorf("%w: Int division %d / %d", ErrOverflow, a, b)
	}
	return a / b, nil
}

func CheckedAddUInt(a, b uint) (uint, error) {
	s, carry := bits.Add64(uint64(a), uint64(b), 0)
	if carry != 0 {
		return 0, fmt.Errorf("%w: UInt addition %d + %d", ErrOverflow, a, b)
	}
	return uint(s), nil
}

func CheckedSubUInt(a, b uint) (uint, error) {
	if b > a {
		return 0, fmt.Errorf("%w: UInt subtraction %d - %d", ErrOverflow, a, b)
	}
	return a - b, nil
}

func CheckedMulUInt(a, b uint) (uint, error) {
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	if hi != 0 {
		return 0, fmt.Errorf("%w: UInt multiplication %d * %d", ErrOverflow, a, b)
	}
	return uint(lo), nil
}

func CheckedDivUInt(a, b uint) (uint, error) {
	if b == 0 {
		return 0, fmt.Errorf("%w: UInt %d / 0", ErrDivByZero, a)
	}
	return a / b, nil
}
