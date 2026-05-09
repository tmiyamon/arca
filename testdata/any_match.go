//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
)

func describe(v interface{}) string {
	switch __tv := v.(type) {
	case int:
		n := __tv
		return fmt.Sprintf("int: %v", n)
	case string:
		s := __tv
		return fmt.Sprintf("string: %v", s)
	case bool:
		b := __tv
		return fmt.Sprintf("bool: %v", b)
	default:
		return "unknown"
	}
}

func main() {
	fmt.Println(describe(42))
	fmt.Println(describe("hello"))
	fmt.Println(describe(true))
	fmt.Println(describe(3.14))
}
