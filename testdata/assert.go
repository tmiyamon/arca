//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
)

func add(a int, b int) int {
	return __addInt(a, b)
}

func main() {
	if !(add(1, 2) == 3) {
		panic("assertion failed: add(1, 2) == 3")
	}
	if !(add(0, 0) == 0) {
		panic("assertion failed: add(0, 0) == 0")
	}
	if !(__addInt(1, 1) == 2) {
		panic("assertion failed: 1 + 1 == 2")
	}
}

func __addInt(a, b int) int {
	s := a + b
	if (a >= 0) == (b >= 0) && (a >= 0) != (s >= 0) {
		panic(fmt.Sprintf("Int: addition overflow %d + %d", a, b))
	}
	return s
}
