//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
	"math/bits"
	"os"
	"strconv"
)

type Box struct {
	Val int
}

func (b Box) double() int {
	return __mulInt(b.Val, 2)
}

func boxFor(n int) (Box, error) {
	if n > 0 {
		return Box{n}, nil
	} else {
		return Box{}, strconv.ErrRange
	}
}

func maybe(n int) (*int, error) {
	if n > 0 {
		return __ptrOf(n), nil
	} else {
		return nil, strconv.ErrRange
	}
}

func main() {
	if err := func() error {
		__try1, __try1_err := boxFor(3)
		if __try1_err != nil {
			return __try1_err
		}
		a := __try1.double()
		fmt.Println(a)
		__try2, __try2_err := boxFor(7)
		if __try2_err != nil {
			return __try2_err
		}
		b := __try2.Val
		fmt.Println(b)
		__try3, __try3_err := maybe(4)
		if __try3_err != nil {
			return __try3_err
		}
		var __val4 int
		var __err4 error
		if __try3 != nil {
			__monadic_v := *__try3
			__val4, __err4 = __monadic_v, nil
		} else {
			__val4, __err4 = 0, strconv.ErrRange
		}
		if __err4 != nil {
			return __err4
		}
		c := __val4
		fmt.Println(c)
		return nil
	}(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
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
