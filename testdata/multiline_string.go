//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
)

func main() {
	sql := `SELECT *
FROM users
WHERE id = 1`
	fmt.Println(sql)
	name := "Alice"
	msg := fmt.Sprintf(`Hello %v!
Welcome to Arca.
`, name)
	fmt.Println(msg)
}
