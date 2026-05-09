//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

type Counter struct {
	N uint
}

func (c Counter) total() uint {
	return add(c.N, c.N)
}

func add(a uint, b uint) uint {
	return a + b
}
