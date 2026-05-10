//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
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
	p := a * b
	if a != 0 && p/a != b {
		panic(fmt.Sprintf("Int: multiplication overflow %d * %d", a, b))
	}
	return p
}

func __subUInt(a, b uint) uint {
	if b > a {
		panic(fmt.Sprintf("UInt: subtraction underflow %d - %d", a, b))
	}
	return a - b
}
