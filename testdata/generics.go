//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
)

type Pair[A any, B any] struct {
	First  A
	Second B
}

type Foo[A any, C any] struct {
	X A
	Y C
}

func main() {
	p1 := Pair[int, string]{First: 1, Second: "hello"}
	p2 := Pair[float64, int]{First: 2.5, Second: 42}
	f := Foo[string, float64]{X: "world", Y: 3.14}
	fmt.Println(p1.First)
	fmt.Println(p2.Second)
	fmt.Println(f.X)
}
