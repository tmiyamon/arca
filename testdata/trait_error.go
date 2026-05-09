//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
)

type NotFound struct {
	Id int
}

func (n NotFound) Message() string {
	return fmt.Sprintf("not found: %v", n.Id)
}

func (n NotFound) Error() string {
	return n.Message()
}

func main() {
	e := NotFound{Id: 42}
	fmt.Println(e.Message())
}
