package stdlib

import (
	"errors"
	"fmt"
	"math/big"
)

// ErrBigIntInvalid signals a malformed string passed to BigIntFromString.
var ErrBigIntInvalid = errors.New("invalid BigInt string")

// ErrBigIntRange signals that a BigInt value cannot fit the target Arca
// numeric type (e.g. ToInt on a value larger than Int range).
var ErrBigIntRange = errors.New("BigInt out of target range")

// BigInt is the arbitrary-precision third numeric layer alongside Int / UInt
// (fast + panic on overflow) and stdlib.CheckedAdd* (Result-returning checked
// arithmetic). Values are heap-allocated; operations always allocate a fresh
// result so the type behaves immutably from the user's perspective.
type BigInt struct {
	inner *big.Int
}

// NewBigInt constructs a BigInt from an Arca Int.
func NewBigInt(v int64) BigInt {
	return BigInt{inner: big.NewInt(v)}
}

// BigIntFromString parses a base-10 decimal string, supporting an optional
// leading `-`. Returns ErrBigIntInvalid for malformed input.
func BigIntFromString(s string) (BigInt, error) {
	z, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return BigInt{}, fmt.Errorf("%w: %q", ErrBigIntInvalid, s)
	}
	return BigInt{inner: z}, nil
}

// Add / Sub / Mul allocate a fresh BigInt for the result.
func (a BigInt) Add(b BigInt) BigInt {
	return BigInt{inner: new(big.Int).Add(a.inner, b.inner)}
}

func (a BigInt) Sub(b BigInt) BigInt {
	return BigInt{inner: new(big.Int).Sub(a.inner, b.inner)}
}

func (a BigInt) Mul(b BigInt) BigInt {
	return BigInt{inner: new(big.Int).Mul(a.inner, b.inner)}
}

// Div performs truncated division (Go's `/`); rejects divisor zero.
func (a BigInt) Div(b BigInt) (BigInt, error) {
	if b.inner.Sign() == 0 {
		return BigInt{}, fmt.Errorf("%w: BigInt division by zero", ErrDivByZero)
	}
	return BigInt{inner: new(big.Int).Quo(a.inner, b.inner)}, nil
}

// Mod returns the remainder of truncated division; rejects divisor zero.
func (a BigInt) Mod(b BigInt) (BigInt, error) {
	if b.inner.Sign() == 0 {
		return BigInt{}, fmt.Errorf("%w: BigInt mod by zero", ErrDivByZero)
	}
	return BigInt{inner: new(big.Int).Rem(a.inner, b.inner)}, nil
}

// Neg returns -a.
func (a BigInt) Neg() BigInt {
	return BigInt{inner: new(big.Int).Neg(a.inner)}
}

// Eq / Lt / Gt are the comparison primitives. Le / Ge are derivable as
// `!Gt` / `!Lt` so they're omitted from the surface.
func (a BigInt) Eq(b BigInt) bool { return a.inner.Cmp(b.inner) == 0 }
func (a BigInt) Lt(b BigInt) bool { return a.inner.Cmp(b.inner) < 0 }
func (a BigInt) Gt(b BigInt) bool { return a.inner.Cmp(b.inner) > 0 }

// String renders the value as a base-10 decimal.
func (a BigInt) String() string { return a.inner.String() }

// ToInt narrows the value into an Arca Int, returning ErrBigIntRange when
// the value does not fit int64. On a 64-bit Arca target int = int64, so the
// check covers the full Int range.
func (a BigInt) ToInt() (int, error) {
	if !a.inner.IsInt64() {
		return 0, fmt.Errorf("%w: %s", ErrBigIntRange, a.inner.String())
	}
	return int(a.inner.Int64()), nil
}
