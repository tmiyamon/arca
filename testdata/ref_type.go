//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

type User struct {
	Name string
}

func process(r *User) string {
	return r.Name
}

func main() {
	u := User{"Alice"}
	_ = process(&u)
}
