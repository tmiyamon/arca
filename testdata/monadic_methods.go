//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
	"math/bits"
	"os"
	"strconv"
)

func double(x int) int {
	return __mulInt(x, 2)
}

func isPositive(x int) (int, error) {
	if x > 0 {
		return x, nil
	} else {
		return 0, strconv.ErrRange
	}
}

func main() {
	parsed, parsed_err := strconv.Atoi("21")
	var mapped int
	var mapped_err error
	if parsed_err == nil {
		__monadic_v := parsed
		mapped, mapped_err = double(__monadic_v), nil
	} else {
		__monadic_e := __goError{inner: parsed_err}
		mapped, mapped_err = 0, __monadic_e
	}
	if mapped_err == nil {
		n := mapped
		fmt.Println(n)
	} else {
		e := mapped_err
		fmt.Println(e)
	}
	var chained int
	var chained_err error
	if parsed_err == nil {
		__monadic_v := parsed
		chained, chained_err = isPositive(__monadic_v)
	} else {
		__monadic_e := __goError{inner: parsed_err}
		chained, chained_err = 0, __monadic_e
	}
	if chained_err == nil {
		n := chained
		fmt.Println(n)
	} else {
		e := __goError{inner: chained_err}
		fmt.Println(e)
	}
	home := __optFrom(os.LookupEnv("HOME"))
	var required string
	var required_err error
	if home != nil {
		__monadic_v := *home
		required, required_err = __monadic_v, nil
	} else {
		required, required_err = "", strconv.ErrRange
	}
	if required_err == nil {
		s := required
		fmt.Println(s)
	} else {
		e := required_err
		fmt.Println(e)
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

func __ptrOf[T any](v T) *T {
	return &v
}

func __optFrom[T any](v T, ok bool) *T {
	if ok {
		return &v
	}
	return nil
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
