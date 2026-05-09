//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
)

func main() {
	items := []string{"alice", "bob", "charlie"}
	__list1 := items
	first := __list1[0]
	rest := __list1[1:]
	fmt.Println(first)
	fmt.Println(rest)
	__list2 := items
	a := __list2[0]
	b := __list2[1]
	c := __list2[2]
	fmt.Println(a)
	fmt.Println(b)
	fmt.Println(c)
}
