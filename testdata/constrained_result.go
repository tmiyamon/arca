//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
)

type Email string

func NewEmail(v string) (Email, error) {
	if len(v) < 5 {
		return "", fmt.Errorf("min length 5")
	}
	if len(v) > 255 {
		return "", fmt.Errorf("max length 255")
	}
	return Email(v), nil
}

func main() {
	result, result_err := NewEmail("test@example.com")
	if result_err == nil {
		email := result
		fmt.Println(email)
	} else {
		err := result_err
		fmt.Println(err)
	}
}
