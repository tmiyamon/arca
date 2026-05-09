//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
)

func main() {
	fmt.Println(1 + 2*3)
	fmt.Println(10 - 2*3)
	fmt.Println(2*3 + 4*5)
	fmt.Println(-5)
	fmt.Println(-2 + 3)
}
