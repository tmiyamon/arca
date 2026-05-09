//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
)

func greet(name *string) string {
	if name != nil {
		n := *name
		return fmt.Sprintf("Hello %v!", n)
	} else {
		return "Hello stranger!"
	}
}

func main() {
	fmt.Println(greet(__ptrOf("Alice")))
	fmt.Println(greet((*string)(nil)))
}

func __ptrOf[T any](v T) *T {
	return &v
}

func __optFrom[T any](v T, ok bool) *T {
	if ok {
		return &v
	}
	return nil
}
