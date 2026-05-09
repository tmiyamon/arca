//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
	"strconv"
)

func parseAndDouble(s string) (int, error) {
	__val1, __err1 := strconv.Atoi(s)
	if __err1 != nil {
		return 0, __err1
	}
	n := __val1
	return n + n, nil
}

func main() {
	fmt.Println(parseAndDouble("21"))
	fmt.Println(parseAndDouble("abc"))
}
