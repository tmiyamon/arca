//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
)

func main() {
	fmt.Println(__addInt(1, __mulInt(2, 3)))
	fmt.Println(__subInt(10, __mulInt(2, 3)))
	fmt.Println(__addInt(__mulInt(2, 3), __mulInt(4, 5)))
	fmt.Println(-5)
	fmt.Println(__addInt(-2, 3))
}

func __addInt(a, b int) int {
	s := a + b
	if (a >= 0) == (b >= 0) && (a >= 0) != (s >= 0) {
		panic(fmt.Sprintf("Int: addition overflow %d + %d", a, b))
	}
	return s
}

func __subInt(a, b int) int {
	d := a - b
	if (a >= 0) != (b >= 0) && (a >= 0) != (d >= 0) {
		panic(fmt.Sprintf("Int: subtraction overflow %d - %d", a, b))
	}
	return d
}

func __mulInt(a, b int) int {
	p := a * b
	if a != 0 && p/a != b {
		panic(fmt.Sprintf("Int: multiplication overflow %d * %d", a, b))
	}
	return p
}
