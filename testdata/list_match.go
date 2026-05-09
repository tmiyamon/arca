//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
)

func describe(items []string) string {
	if len(items) == 0 {
		return "empty"
	} else if len(items) >= 1 {
		first := items[0]
		return fmt.Sprintf("first: %v", first)
	}
	panic("unreachable")
}

func main() {
	fmt.Println(describe([]string{}))
	fmt.Println(describe([]string{"hello", "world"}))
	extended := append([]int{0}, nums()...)
	fmt.Println(extended)
}

func nums() []int {
	return []int{1, 2, 3}
}
