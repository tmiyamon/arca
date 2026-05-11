//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
	"math/bits"
)

func main() {
	double := func(x int) int {
		return __mulInt(x, 2)
	}
	nums := []int{10, 20, 30}
	doubled := Map_(nums, double)
	fmt.Println(doubled)
	fmt.Println(Map_([]int{1, 2, 3}, func(x int) int {
		return __addInt(x, 1)
	}))
}

func Map_[T any, U any](list []T, f func(T) U) []U {
	result := make([]U, len(list))
	for i, v := range list {
		result[i] = f(v)
	}
	return result
}

func __addInt(a, b int) int {
	s := a + b
	if (a >= 0) == (b >= 0) && (a >= 0) != (s >= 0) {
		panic(fmt.Sprintf("Int: addition overflow %d + %d", a, b))
	}
	return s
}

func __mulInt(a, b int) int {
	var ua, ub uint64
	if a < 0 {
		ua = uint64(-a)
	} else {
		ua = uint64(a)
	}
	if b < 0 {
		ub = uint64(-b)
	} else {
		ub = uint64(b)
	}
	hi, lo := bits.Mul64(ua, ub)
	limit := uint64(1<<63 - 1)
	if (a < 0) != (b < 0) {
		limit = 1 << 63
	}
	if hi != 0 || lo > limit {
		panic(fmt.Sprintf("Int: multiplication overflow %d * %d", a, b))
	}
	return a * b
}
