//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
)

type User struct {
	Id   int
	Name string
}

func main() {
	var users []User
	var count int = 42
	fmt.Println(users, count)
}
