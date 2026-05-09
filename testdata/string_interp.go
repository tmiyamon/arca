//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
)

func main() {
	name := "World"
	age := 30
	fmt.Println(fmt.Sprintf("Hello %v, you are %v!", name, age))
}
