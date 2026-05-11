//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
	"math/bits"
)

func nums() []int {
	return []int{1, 2, 3, 4, 5}
}

func main() {
	doubled := Map_(nums(), func(x int) int {
		return __mulInt(x, 2)
	})
	positives := Filter_(nums(), func(x int) bool {
		return x > 0
	})
	sum := Fold_(nums(), 0, func(acc int, x int) int {
		return __addInt(acc, x)
	})
	fmt.Println(doubled)
	fmt.Println(positives)
	fmt.Println(sum)
}

func Map_[T any, U any](list []T, f func(T) U) []U {
	result := make([]U, len(list))
	for i, v := range list {
		result[i] = f(v)
	}
	return result
}

func Filter_[T any](list []T, f func(T) bool) []T {
	var result []T
	for _, v := range list {
		if f(v) {
			result = append(result, v)
		}
	}
	return result
}

func Fold_[T any, U any](list []T, init U, f func(U, T) U) U {
	acc := init
	for _, v := range list {
		acc = f(acc, v)
	}
	return acc
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
