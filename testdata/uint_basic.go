//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
	"math/bits"
)

type Counter struct {
	N uint
}

func (c Counter) total() uint {
	return add(c.N, c.N)
}

func add(a uint, b uint) uint {
	return __addUInt(a, b)
}

func __addUInt(a, b uint) uint {
	s, carry := bits.Add64(uint64(a), uint64(b), 0)
	if carry != 0 {
		panic(fmt.Sprintf("UInt: addition overflow %d + %d", a, b))
	}
	return uint(s)
}
