//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
)

func printNumbers() {
	for i := 0; i < 5; i++ {
		fmt.Println(i)
	}
}
