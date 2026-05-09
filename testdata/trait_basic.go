//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
)

type ArcaDisplay interface {
	Show() string
}

type User struct {
	Name string
}

func (u User) Show() string {
	return u.Name
}

func render(d ArcaDisplay) string {
	return d.Show()
}

func main() {
	u := User{Name: "Alice"}
	fmt.Println(render(u))
}
