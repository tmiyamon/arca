//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
	"math/bits"
	"strconv"
)

func parseAndDouble(s string) (int, error) {
	__val1, __err1 := strconv.Atoi(s)
	if __err1 != nil {
		return 0, __err1
	}
	n := __val1
	return __mulInt(n, 2), nil
}

func main() {
	result, result_err := parseAndDouble("21")
	if result_err == nil {
		n := result
		fmt.Println(n)
	} else {
		err := __goError{inner: result_err}
		fmt.Println(err)
	}
}

type __goError struct{ inner error }

func (e __goError) Message() string {
	return e.inner.Error()
}

func (e __goError) Error() string {
	return e.inner.Error()
}

func (e __goError) Unwrap() error {
	return e.inner
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
