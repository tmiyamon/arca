//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
)

func main() {
	ages := map[string]int{"alice": 30, "bob": 25}
	var empty map[string]int = map[string]int{}
	n := ages["alice"]
	fmt.Println(ages)
	fmt.Println(empty)
	fmt.Println(n)
}
