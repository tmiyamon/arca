//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
)

func inc(n int) int {
	return __addInt(n, 1)
}

func apply(f func(int) int, x int) int {
	return f(x)
}

func main() {
	result := apply(inc, 41)
	fmt.Println(result)
}

func __addInt(a, b int) int {
	s := a + b
	if (a >= 0) == (b >= 0) && (a >= 0) != (s >= 0) {
		panic(fmt.Sprintf("Int: addition overflow %d + %d", a, b))
	}
	return s
}
