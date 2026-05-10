//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
	"strconv"
)

func double(s string) (int, error) {
	__match1, __match1_err := strconv.Atoi(s)
	if __match1_err == nil {
		n := __match1
		return __addInt(n, n), nil
	} else {
		e := __goError{inner: __match1_err}
		return 0, e
	}
}

func main() {
	fmt.Println(double("21"))
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

func __addInt(a, b int) int {
	s := a + b
	if (a >= 0) == (b >= 0) && (a >= 0) != (s >= 0) {
		panic(fmt.Sprintf("Int: addition overflow %d + %d", a, b))
	}
	return s
}
