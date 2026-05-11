//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
	"math/bits"
)

type Sample struct {
	Small int8
	Big   uint32
	Ratio float32
}

func safeAdd(a int, b int) int {
	return __addInt(a, b)
}

func safeSub(a uint, b uint) uint {
	return __subUInt(a, b)
}

func safeMul(a int, b int) int {
	return __mulInt(a, b)
}

func safeDiv(a int, b int) int {
	return __divInt(a, b)
}

func safeMod(a int, b int) int {
	return __modInt(a, b)
}

func safeDivU(a uint, b uint) uint {
	return __divUInt(a, b)
}

func safeModU(a uint, b uint) uint {
	return __modUInt(a, b)
}

func mixFloat(a float64, b float64) float64 {
	return a + b
}

func makeSample() Sample {
	return Sample{Small: 100, Big: 99999, Ratio: 3.14}
}

func __addInt(a, b int) int {
	s := a + b
	if (a >= 0) == (b >= 0) && (a >= 0) != (s >= 0) {
		panic(fmt.Sprintf("Int: addition overflow %d + %d", a, b))
	}
	return s
}

func __mulInt(a, b int) int {
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
		panic(fmt.Sprintf("Int: multiplication overflow %d * %d", a, b))
	}
	return a * b
}

func __subUInt(a, b uint) uint {
	if b > a {
		panic(fmt.Sprintf("UInt: subtraction underflow %d - %d", a, b))
	}
	return a - b
}

func __divInt(a, b int) int {
	if b == 0 {
		panic(fmt.Sprintf("Int: division by zero %d / 0", a))
	}
	if a == (-1<<63) && b == -1 {
		panic(fmt.Sprintf("Int: division overflow %d / %d", a, b))
	}
	return a / b
}

func __modInt(a, b int) int {
	if b == 0 {
		panic(fmt.Sprintf("Int: modulo by zero %d %% 0", a))
	}
	return a % b
}

func __divUInt(a, b uint) uint {
	if b == 0 {
		panic(fmt.Sprintf("UInt: division by zero %d / 0", a))
	}
	return a / b
}

func __modUInt(a, b uint) uint {
	if b == 0 {
		panic(fmt.Sprintf("UInt: modulo by zero %d %% 0", a))
	}
	return a % b
}
