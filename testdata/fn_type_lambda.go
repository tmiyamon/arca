//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
)

func apply(f func(int) int, x int) int {
	return f(x)
}

func main() {
	r1 := apply(func(n int) int {
		return n + 1
	}, 41)
	var double func(int) int = func(n int) int {
		return n * 2
	}
	fmt.Println(r1)
	fmt.Println(double(21))
}
