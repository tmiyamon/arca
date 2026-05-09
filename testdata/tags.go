//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

type User struct {
	Id       int    `json:"id" db:"id"`
	UserName string `json:"userName" db:"user_name"`
}
