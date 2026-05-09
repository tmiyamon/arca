//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

func add(a int, b int) int {
	return a + b
}

func main() {
	if !(add(1, 2) == 3) {
		panic("assertion failed: add(1, 2) == 3")
	}
	if !(add(0, 0) == 0) {
		panic("assertion failed: add(0, 0) == 0")
	}
	if !(1+1 == 2) {
		panic("assertion failed: 1 + 1 == 2")
	}
}
