//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
)

func main() {
	nums := []int{1, 2, 3, 4, 5}
	names := []string{"alice", "bob", "charlie"}
	empty := []interface{}{}
	fmt.Println(nums)
	fmt.Println(names)
	fmt.Println(empty)
}
