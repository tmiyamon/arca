package main

// Benchmark the Slice E4 arithmetic helpers against native ops, to gauge
// whether the Layer 1 panic emit needs an `unsafe` opt-out for hot loops.
//
// Run: go test -bench=BenchmarkArith -benchmem -run=^$ -count=3
//
// Helpers below are byte-identical copies of the bodies emitted by
// emit.go:980-1039. Keep in sync if those bodies change.

import (
	"math/bits"
	"testing"
)

func benchAddInt(a, b int) int {
	s := a + b
	if (a >= 0) == (b >= 0) && (a >= 0) != (s >= 0) {
		panic("Int: addition overflow")
	}
	return s
}

func benchSubInt(a, b int) int {
	d := a - b
	if (a >= 0) != (b >= 0) && (a >= 0) != (d >= 0) {
		panic("Int: subtraction overflow")
	}
	return d
}

func benchMulInt(a, b int) int {
	var ua, ub uint64
	if a < 0 {
		ua = uint64(-a)
	} else {
		ua = uint64(a)
	}
	if b < 0 {
		ub = uint64(-b)
	} else {
		ub = uint64(b)
	}
	hi, lo := bits.Mul64(ua, ub)
	limit := uint64(1<<63 - 1)
	if (a < 0) != (b < 0) {
		limit = 1 << 63
	}
	if hi != 0 || lo > limit {
		panic("Int: multiplication overflow")
	}
	return a * b
}

func benchAddUInt(a, b uint) uint {
	s, carry := bits.Add64(uint64(a), uint64(b), 0)
	if carry != 0 {
		panic("UInt: addition overflow")
	}
	return uint(s)
}

func benchMulUInt(a, b uint) uint {
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	if hi != 0 {
		panic("UInt: multiplication overflow")
	}
	return uint(lo)
}

var (
	sinkInt  int
	sinkUInt uint
)

const benchN = 1000

// Per-iteration inputs in pre-built slices to defeat constant-folding /
// loop fusion in the native baseline. Without this Go folds e.g.
// `s += j` over 1..1000 to a closed-form N*(N-1)/2 and the native
// timings reflect only loop overhead.
var (
	benchSrcInt  = func() []int { s := make([]int, benchN); for i := range s { s[i] = i + 1 }; return s }()
	benchSrcUInt = func() []uint { s := make([]uint, benchN); for i := range s { s[i] = uint(i + 1) }; return s }()
)

func BenchmarkArithNativeAddInt(b *testing.B) {
	for i := 0; i < b.N; i++ {
		s := 0
		for _, v := range benchSrcInt {
			s = s + v
		}
		sinkInt = s
	}
}

func BenchmarkArithCheckedAddInt(b *testing.B) {
	for i := 0; i < b.N; i++ {
		s := 0
		for _, v := range benchSrcInt {
			s = benchAddInt(s, v)
		}
		sinkInt = s
	}
}

func BenchmarkArithNativeSubInt(b *testing.B) {
	for i := 0; i < b.N; i++ {
		s := 1 << 30
		for _, v := range benchSrcInt {
			s = s - v
		}
		sinkInt = s
	}
}

func BenchmarkArithCheckedSubInt(b *testing.B) {
	for i := 0; i < b.N; i++ {
		s := 1 << 30
		for _, v := range benchSrcInt {
			s = benchSubInt(s, v)
		}
		sinkInt = s
	}
}

// Mul is benchmarked as element-wise product into a sink slice (no
// cumulative accumulation) so we stay inside int range — `1*2*...*1000`
// overflows int64 well before reaching 1000.
var benchSinkInt = make([]int, benchN)

func BenchmarkArithNativeMulInt(b *testing.B) {
	for i := 0; i < b.N; i++ {
		for j, v := range benchSrcInt {
			benchSinkInt[j] = v * v
		}
	}
}

func BenchmarkArithCheckedMulInt(b *testing.B) {
	for i := 0; i < b.N; i++ {
		for j, v := range benchSrcInt {
			benchSinkInt[j] = benchMulInt(v, v)
		}
	}
}

func BenchmarkArithNativeAddUInt(b *testing.B) {
	for i := 0; i < b.N; i++ {
		var s uint
		for _, v := range benchSrcUInt {
			s = s + v
		}
		sinkUInt = s
	}
}

func BenchmarkArithCheckedAddUInt(b *testing.B) {
	for i := 0; i < b.N; i++ {
		var s uint
		for _, v := range benchSrcUInt {
			s = benchAddUInt(s, v)
		}
		sinkUInt = s
	}
}

var benchSinkUInt = make([]uint, benchN)

func BenchmarkArithNativeMulUInt(b *testing.B) {
	for i := 0; i < b.N; i++ {
		for j, v := range benchSrcUInt {
			benchSinkUInt[j] = v * v
		}
	}
}

func BenchmarkArithCheckedMulUInt(b *testing.B) {
	for i := 0; i < b.N; i++ {
		for j, v := range benchSrcUInt {
			benchSinkUInt[j] = benchMulUInt(v, v)
		}
	}
}

// Mixed kernel: dot-product over benchSrcInt × benchSrcInt[reversed].
// Represents typical numerical workload mixing add and mul.
func BenchmarkArithNativeMixedKernel(b *testing.B) {
	for i := 0; i < b.N; i++ {
		s := 0
		for j, v := range benchSrcInt {
			w := benchSrcInt[len(benchSrcInt)-1-j]
			s = s + v*w
		}
		sinkInt = s
	}
}

func BenchmarkArithCheckedMixedKernel(b *testing.B) {
	for i := 0; i < b.N; i++ {
		s := 0
		for j, v := range benchSrcInt {
			w := benchSrcInt[len(benchSrcInt)-1-j]
			s = benchAddInt(s, benchMulInt(v, w))
		}
		sinkInt = s
	}
}
