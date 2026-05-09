//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
)

func main() {
	a := []int{1, 2, 3}
	b := append([]int{0}, a...)
	fmt.Println(b)
}
