package main

import (
	"fmt"
	"strconv"
)

func double(s string) (int, error) {
	__match1, __match1_err := strconv.Atoi(s)
	if __match1_err == nil {
		n := __match1
		return n + n, nil
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
