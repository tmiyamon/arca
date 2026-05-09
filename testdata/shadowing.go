//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
)

func main() {
	x := "hello"
	fmt.Println(x)
	x_2 := 42
	fmt.Println(x_2)
}
